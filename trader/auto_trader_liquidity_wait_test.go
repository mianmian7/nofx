package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

// Exercise the native OKX parser and the full decision cycle without network
// access. The default transport is restored before any other test can run.
type liquidityWaitTransport struct {
	failedSymbol string
	missingOI    bool
}

func (transport liquidityWaitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "www.okx.com" {
		return nil, fmt.Errorf("unexpected market host %s", request.URL.Host)
	}
	writer := httptest.NewRecorder()
	writer.Header().Set("Content-Type", "application/json")
	instrument := request.URL.Query().Get("instId")
	if strings.HasPrefix(instrument, transport.failedSymbol+"-") && transport.failedSymbol != "" {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.WriteString(`{"code":"50000","msg":"market unavailable","data":[]}`)
		return writer.Result(), nil
	}
	now := time.Now()
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	switch request.URL.Path {
	case "/api/v5/public/instruments":
		_, _ = fmt.Fprintf(writer, `{"code":"0","data":[{"instId":%q,"instType":"SWAP","settleCcy":"USDT","ctVal":"1","ctMult":"1","lotSz":"0.01","minSz":"0.01","tickSz":"0.01","state":"live"}]}`, instrument)
	case "/api/v5/market/candles":
		duration := 15 * time.Minute
		switch request.URL.Query().Get("bar") {
		case "1H":
			duration = time.Hour
		case "4H":
			duration = 4 * time.Hour
		}
		rows := make([][]string, 0, 200)
		latest := now.Truncate(duration)
		for i := 199; i >= 0; i-- {
			price := 100 + float64(199-i)/100
			rows = append(rows, []string{
				strconv.FormatInt(latest.Add(-time.Duration(i)*duration).UnixMilli(), 10),
				fmt.Sprint(price), fmt.Sprint(price + 1), fmt.Sprint(price - 1), fmt.Sprint(price), "10", "0", "1000", "0",
			})
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": "0", "data": rows})
	case "/api/v5/public/open-interest":
		if transport.missingOI {
			_, _ = writer.WriteString(`{"code":"0","data":[{}]}`)
		} else {
			_, _ = fmt.Fprintf(writer, `{"code":"0","data":[{"oi":"1000","oiCcy":"1000","oiUsd":"1000000","ts":%q}]}`, stamp)
		}
	case "/api/v5/public/funding-rate":
		_, _ = fmt.Fprintf(writer, `{"code":"0","data":[{"instId":%q,"fundingRate":"0.0001","nextFundingTime":%q,"ts":%q}]}`, instrument, stamp, stamp)
	case "/api/v5/public/mark-price":
		_, _ = fmt.Fprintf(writer, `{"code":"0","data":[{"instId":%q,"markPx":"101.99","ts":%q}]}`, instrument, stamp)
	default:
		return nil, fmt.Errorf("unexpected OKX endpoint %s", request.URL.Path)
	}
	return writer.Result(), nil
}

func TestCycleLiquidityFilteringWaitsWithoutAI(t *testing.T) {
	for _, test := range []struct {
		name            string
		transport       liquidityWaitTransport
		wantSuccess     bool
		wantWaits       int
		duplicate       bool
		emptyCandidates bool
		heldPosition    bool
		wantAI          bool
		storageFailure  bool
	}{
		{name: "all_fresh_but_low_liquidity", wantSuccess: true, wantWaits: 3},
		{name: "duplicate_candidates", wantSuccess: true, wantWaits: 3, duplicate: true},
		{name: "one_candidate_unavailable", transport: liquidityWaitTransport{failedSymbol: "GALA"}},
		{name: "missing_open_interest", transport: liquidityWaitTransport{missingOI: true}},
		{name: "empty_candidate_universe", emptyCandidates: true},
		{name: "decision_persistence_failure", storageFailure: true},
		{name: "held_position_still_managed", heldPosition: true, wantSuccess: true, wantAI: true, wantWaits: 1},
		{name: "held_position_data_unavailable", heldPosition: true, transport: liquidityWaitTransport{failedSymbol: "BTC"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NOFX_DISABLE_MARKET_STREAM", "true")
			t.Setenv("NOFX_MARKET_DATA_CACHE_TTL", "0s")
			previousTransport := http.DefaultTransport
			http.DefaultTransport = test.transport
			t.Cleanup(func() { http.DefaultTransport = previousTransport })
			market.ResetMarketDataCache()

			release := make(chan struct{})
			close(release)
			client := &controlledCycleAIClient{
				entered: make(chan struct{}), release: release,
				cycleIDs: make(chan string, 2), response: `[]`,
			}
			at, broker, mainStore := newFullCycleTimeoutTrader(t, client)
			if test.heldPosition {
				if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 100, StopLoss: 99, TakeProfit: 103}); err != nil {
					t.Fatal(err)
				}
				client.response = `[{"symbol":"BTCUSDT","action":"hold","reasoning":"manage existing exposure"}]`
			}
			initialFills, initialOrders := len(broker.RecentFills(100)), len(broker.PendingOrders())
			strategy := store.GetDefaultStrategyConfig("en")
			strategy.Indicators.Klines.SelectedTimeframes = []string{"15m", "1h", "4h"}
			strategy.Indicators.Klines.PrimaryTimeframe = "15m"
			at.strategyEngine = kernel.NewStrategyEngineForExchange(&strategy, "okx")
			at.exchange = "okx"
			at.config.StrategyConfig = &strategy
			at.config.ScanInterval = 15 * time.Minute
			at.config.CycleTimeout = 2 * time.Second
			at.consecutiveAIFailures = 3
			at.setSafeModeWithCause(true, SafeModeCauseAI, "prior AI failure")
			at.tradingContextBuilder = func(context.Context) (*kernel.Context, error) {
				ctx := &kernel.Context{
					TraderID:               at.id,
					Account:                kernel.AccountInfo{TotalEquity: 1_000, AvailableBalance: 1_000},
					CandidateCoins:         []kernel.CandidateCoin{{Symbol: "SANDUSDT"}, {Symbol: "NIGHTUSDT"}, {Symbol: "GALAUSDT"}},
					RequireFreshMarketData: true,
				}
				if test.duplicate {
					ctx.CandidateCoins = append(ctx.CandidateCoins, kernel.CandidateCoin{Symbol: "SANDUSDT"})
				}
				if test.emptyCandidates {
					ctx.CandidateCoins = nil
				}
				if test.heldPosition {
					ctx.Positions = []kernel.PositionInfo{{Symbol: "BTCUSDT", Side: "long", EntryPrice: 100, MarkPrice: 101.99, Quantity: 1, Leverage: 3}}
					ctx.Account.PositionCount = 1
				}
				return ctx, nil
			}

			if test.storageFailure {
				if err := mainStore.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				err := at.runAutomaticCycle()
				if test.storageFailure {
					if err == nil || !strings.Contains(err.Error(), "database is closed") {
						t.Fatalf("wait persistence error was hidden: %v", err)
					}
				} else if err != nil {
					t.Fatalf("cycle %d: %v", i, err)
				}
			}
			if test.storageFailure {
				if client.enterOnce.Load() || at.consecutiveAIFailures != 3 || !at.isSafeMode() || len(broker.RecentFills(100)) != initialFills || len(broker.PendingOrders()) != initialOrders {
					t.Fatal("failed wait persistence changed AI/order state")
				}
				return
			}
			records, err := mainStore.Decision().GetLatestRecords(at.id, 2)
			if err != nil || len(records) != 2 {
				t.Fatalf("decision records = %d, error = %v", len(records), err)
			}
			for _, record := range records {
				if record.Success != test.wantSuccess {
					t.Fatalf("success = %v, want %v: %s", record.Success, test.wantSuccess, record.ErrorMessage)
				}
				if len(record.Decisions) != test.wantWaits {
					t.Fatalf("recorded actions = %#v, want %d explicit waits", record.Decisions, test.wantWaits)
				}
				for _, action := range record.Decisions {
					if test.wantAI {
						if action.Action != "hold" || !action.Success {
							t.Fatalf("position management was lost: %#v", action)
						}
					} else if action.Action != "wait" || !action.Success || !strings.Contains(action.Reasoning, "liquidity") {
						t.Fatalf("unexpected wait: %#v", action)
					}
				}
				if !test.wantAI && (record.CallID != "" || record.AIRequestDurationMs != 0) {
					t.Fatalf("non-AI wait has AI metadata: %#v", record)
				}
			}
			if client.enterOnce.Load() != test.wantAI || len(broker.RecentFills(100)) != initialFills || len(broker.PendingOrders()) != initialOrders {
				t.Fatal("cycle lost required AI management or changed orders")
			}
			charges, _, err := mainStore.AICharge().GetCharges(at.id, "all")
			wantCharges := 0
			if test.wantAI {
				wantCharges = 2
			}
			if err != nil || len(charges) != wantCharges {
				t.Fatalf("charges = %d, want %d, error = %v", len(charges), wantCharges, err)
			}
			wantFailures := 3
			if test.wantAI {
				wantFailures = 0
			}
			if at.consecutiveAIFailures != wantFailures || at.isSafeMode() == test.wantAI {
				t.Fatalf("AI recovery state mismatch: failures=%d safe=%v", at.consecutiveAIFailures, at.isSafeMode())
			}
		})
	}
}
