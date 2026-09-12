package trader

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	tradertypes "nofx/trader/types"
)

// Use production strategy-to-broker construction, replacing only external
// market data. This catches missing policy wiring as well as wrong arithmetic.
func newPaperRRTrader(t *testing.T, minimum float64, maker bool, source *fixedPaperMakerSource) *AutoTrader {
	t.Helper()
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.RiskControl.MinRiskRewardRatio = minimum
	at, err := NewAutoTrader(AutoTraderConfig{
		ID: "paper-rr", AIModel: "custom", Exchange: "binance",
		ExecutionMode: ExecutionModePaper, InitialBalance: 10_000, StrategyConfig: &strategy,
	}, nil, "test-user")
	if err != nil {
		t.Fatal(err)
	}
	at.marketDataProvider = nil
	at.binanceMarketClient = nil
	at.paperBroker.prices = source
	at.paperBroker.fundingSource = nil
	at.paperBroker.config.MakerFirst = maker
	at.paperBroker.config.SlippageBPS = 0
	return at
}

func TestPaperEntryRRRejectsBelowStrategyMinimumWithoutMutation(t *testing.T) {
	at := newPaperRRTrader(t, 2, false, &fixedPaperMakerSource{price: 100})
	before := at.paperBroker.Snapshot()
	action := &store.DecisionAction{}
	// At 1x the -20% margin stop does not tighten SL=90. Actual RR is
	// (110-100)/(100-90) = 1, while the parser's inferred entry claims 4.
	err := at.executeDecisionWithRecord(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", Leverage: 1,
		PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 110,
	}, action)
	if err == nil || !strings.Contains(err.Error(), "risk/reward") {
		t.Fatalf("expected RR rejection, got err=%v action=%+v", err, action)
	}
	after := at.paperBroker.Snapshot()
	if after.Balance != before.Balance || after.Fees != before.Fees || after.OpenPositions != 0 || after.PendingOrders != 0 || len(at.paperBroker.RecentFills(0)) != 0 {
		t.Fatalf("rejected entry mutated Paper account: before=%+v after=%+v", before, after)
	}
}

func TestPaperEntryRRMakerUsesLimitNotTicker(t *testing.T) {
	for _, tc := range []struct {
		action       string
		bid, ask     string
		stop, target float64
	}{
		{"open_long", "109", "110", 90, 120},
		{"open_short", "90", "91", 110, 80},
	} {
		t.Run(tc.action, func(t *testing.T) {
			at := newPaperRRTrader(t, 2, true, &fixedPaperMakerSource{
				price: 100, bids: [][]string{{tc.bid, "10"}}, asks: [][]string{{tc.ask, "10"}},
			})
			// The ticker yields RR=2, but the resting limit yields only 11/19.
			err := at.executeDecisionWithRecord(&kernel.Decision{
				Symbol: "MUUSDT", Action: tc.action, Leverage: 1,
				PositionSizeUSD: 100, StopLoss: tc.stop, TakeProfit: tc.target,
			}, &store.DecisionAction{})
			if err == nil || !strings.Contains(err.Error(), "risk/reward") {
				t.Fatalf("expected resting-price RR rejection, got %v", err)
			}
			if s := at.paperBroker.Snapshot(); s.OpenPositions != 0 || s.PendingOrders != 0 || s.Balance != 10_000 || s.Fees != 0 {
				t.Fatalf("rejected maker entry mutated state: %+v", s)
			}
		})
	}
}

func TestPaperEntryRRRepriceCancelsInvalidRemainderAndPreservesPosition(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
			source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
			st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			config := PaperBrokerConfig{InitialBalance: 1000, MinRiskRewardRatio: 2, MakerFirst: true, MakerFeeBPS: 2, MakerTimeout: 15 * time.Second, MakerMaxReprices: 2, Clock: func() time.Time { return now }}
			broker, err := NewPersistentPaperBroker(config, source, st.Paper(), "rr-reprice", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 1, PositionSizeUSD: 300, StopLoss: 90, TakeProfit: 130}); err != nil {
				t.Fatal(err)
			}
			if partial {
				source.asks = [][]string{{"99", "1"}}
				now = now.Add(5 * time.Second)
				fills, err := broker.RefreshMakerOrders(now)
				if err != nil || len(fills) != 1 {
					t.Fatalf("partial fill: %v %v", fills, err)
				}
			}
			before := broker.Snapshot()
			// Replacement at 120 would have RR=(130-120)/(120-90)=1/3.
			source.price, source.bids, source.asks = 120, [][]string{{"120", "10"}}, [][]string{{"121", "10"}}
			now = now.Add(time.Minute)
			_, err = broker.RefreshMakerOrders(now)
			if err == nil || !strings.Contains(err.Error(), "risk/reward") {
				t.Fatalf("expected reprice rejection, got %v", err)
			}
			for _, order := range broker.PendingOrders() {
				if !order.ReduceOnly {
					t.Fatalf("invalid remainder remains executable: %+v", order)
				}
			}
			after := broker.Snapshot()
			if after.Balance != before.Balance || after.Fees != before.Fees || after.OpenPositions != before.OpenPositions {
				t.Fatalf("reprice rejection mutated filled account: before=%+v after=%+v", before, after)
			}
			reloaded, err := NewPersistentPaperBroker(config, source, st.Paper(), "rr-reprice", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, order := range reloaded.PendingOrders() {
				if !order.ReduceOnly {
					t.Fatalf("cancellation not durable: %+v", order)
				}
			}
			if partial {
				ps, err := reloaded.GetPositions()
				if err != nil || len(ps) != 1 {
					t.Fatalf("filled position lost: %v %v", ps, err)
				}
				assertPaperFloat(t, "retained quantity", ps[0]["quantity"].(float64), 1)
				// The retained partial position must still have its original stop.
				fills, err := reloaded.ProcessPrice("MUUSDT", 89, now.Add(time.Second))
				if err != nil || len(fills) != 1 || fills[0].Action != "stop_loss" {
					t.Fatalf("partial-position stop lost: %v %v", fills, err)
				}
			}
		})
	}
}

func TestPaperEntryRRRestoredPendingFillUsesCurrentPolicy(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
			source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
			st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			cfg := PaperBrokerConfig{InitialBalance: 1000, MinRiskRewardRatio: 1, MakerFirst: true, MakerFeeBPS: 2, Clock: func() time.Time { return now }}
			b, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "pending-policy", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 1, PositionSizeUSD: 300, StopLoss: 90, TakeProfit: 110}); err != nil {
				t.Fatal(err)
			}
			if partial {
				source.asks = [][]string{{"99", "1"}}
				if _, err := b.RefreshMakerOrders(now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			cfg.MinRiskRewardRatio = 2
			reloaded, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "pending-policy", nil)
			if err != nil {
				t.Fatal(err)
			}
			before := reloaded.Snapshot()
			source.asks = [][]string{{"99", "10"}}
			fills, err := reloaded.RefreshMakerOrders(now.Add(2 * time.Second))
			if err == nil || !strings.Contains(err.Error(), "risk/reward") || len(fills) != 0 {
				t.Fatalf("restored order filled below current RR minimum: fills=%+v err=%v", fills, err)
			}
			after := reloaded.Snapshot()
			if before.Balance != after.Balance || before.Fees != after.Fees || before.OpenPositions != after.OpenPositions {
				t.Fatalf("rejected fill changed account: before=%+v after=%+v", before, after)
			}
			for _, o := range reloaded.PendingOrders() {
				if !o.ReduceOnly {
					t.Fatalf("rejected entry still pending: %+v", o)
				}
			}
			again, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "pending-policy", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range again.PendingOrders() {
				if !o.ReduceOnly {
					t.Fatalf("rejected entry resurrected: %+v", o)
				}
			}
		})
	}
}

func TestPaperEntryRRStrategyReloadUpdatesPendingEntryPolicy(t *testing.T) {
	source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
	at := newPaperRRTrader(t, 2, true, source)
	if err := at.executeDecisionWithRecord(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 1, PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 130}, &store.DecisionAction{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "strategy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.RiskControl.MinRiskRewardRatio = 4
	cfg.RiskControl.PaperMaxTradeRiskRatio = .001
	cfg.RiskControl.PaperMaxPortfolioRiskRatio = .002
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().Create(&store.Strategy{ID: "rr-policy", UserID: "test-user", Name: "RR", Config: string(raw)}); err != nil {
		t.Fatal(err)
	}
	at.store, at.config.StrategyID = st, "rr-policy"
	if err := at.reloadStrategyConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	if at.paperBroker.config.MaxTradeRiskRatio != .001 || at.paperBroker.config.MaxPortfolioRiskRatio != .002 {
		t.Fatal("strategy reload did not update Paper account risk limits")
	}
	source.asks = [][]string{{"99", "10"}}
	fills, err := at.paperBroker.RefreshMakerOrders(time.Now())
	if err == nil || !strings.Contains(err.Error(), "risk/reward") || len(fills) != 0 {
		t.Fatalf("strategy reload did not reach pending entry gate: fills=%v err=%v", fills, err)
	}
}

func TestPaperEntryRRActualPriceAndNormalizedProtection(t *testing.T) {
	for _, tc := range []struct {
		name, action                  string
		price, stop, target, slippage float64
		leverage                      int
		wantError                     bool
	}{
		{"long_at_minimum", "open_long", 100, 90, 120, 0, 1, false},
		{"long_entry_shift", "open_long", 105, 90, 120, 0, 1, true},
		{"long_favorable_entry", "open_long", 95, 90, 120, 0, 1, false},
		{"short_at_minimum", "open_short", 100, 110, 80, 0, 1, false},
		{"short_entry_shift", "open_short", 95, 110, 80, 0, 1, true},
		{"short_favorable_entry", "open_short", 105, 110, 80, 0, 1, false},
		{"long_slippage_crosses_minimum", "open_long", 100, 90, 120, 2, 1, true},
		{"short_slippage_crosses_minimum", "open_short", 100, 110, 80, 2, 1, true},
		// At 10x the existing margin guard tightens the stop from 90 to 98,
		// making the actual RR 5. The RR gate must use that normalized price.
		{"post_normalization", "open_long", 100, 90, 110, 0, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := newPaperRRTrader(t, 2, false, &fixedPaperMakerSource{price: tc.price})
			at.paperBroker.config.SlippageBPS = tc.slippage
			err := at.executeDecisionWithRecord(&kernel.Decision{Symbol: "MUUSDT", Action: tc.action, Leverage: tc.leverage, PositionSizeUSD: 100, StopLoss: tc.stop, TakeProfit: tc.target}, &store.DecisionAction{})
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "risk/reward") {
					t.Fatalf("expected RR rejection: %v", err)
				}
				if s := at.paperBroker.Snapshot(); s.OpenPositions != 0 || s.PendingOrders != 0 || s.Fees != 0 || s.Balance != 10000 {
					t.Fatalf("rejected account mutated: %+v", s)
				}
			} else if err != nil {
				t.Fatalf("valid entry rejected: %v", err)
			}
		})
	}
}

type paperRiskFailingLedger struct {
	PaperLedger
	fail bool
}

func (l *paperRiskFailingLedger) SavePaperState(id string, raw []byte) error {
	if l.fail {
		return fmt.Errorf("injected ledger failure")
	}
	return l.PaperLedger.SavePaperState(id, raw)
}

func TestPaperEntryRRCancellationPersistenceFailureIsRetryable(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
	st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ledger := &paperRiskFailingLedger{PaperLedger: st.Paper()}
	cfg := PaperBrokerConfig{InitialBalance: 1000, MinRiskRewardRatio: 1, MakerFirst: true, Clock: func() time.Time { return now }}
	b, err := NewPersistentPaperBroker(cfg, source, ledger, "cancel-retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 1, PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 110}); err != nil {
		t.Fatal(err)
	}
	cfg.MinRiskRewardRatio = 2
	b, err = NewPersistentPaperBroker(cfg, source, ledger, "cancel-retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	source.asks = [][]string{{"99", "10"}}
	ledger.fail = true
	if _, err := b.RefreshMakerOrders(now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "persist entry cancellation") {
		t.Fatalf("missing persistence failure: %v", err)
	}
	if len(b.PendingOrders()) != 1 || len(b.RecentFills(0)) != 0 {
		t.Fatal("failed cancellation lost order or allowed a fill")
	}
	ledger.fail = false
	if _, err := b.RefreshMakerOrders(now.Add(2 * time.Second)); err == nil || !strings.Contains(err.Error(), "risk/reward") {
		t.Fatalf("retry should reject: %v", err)
	}
	if len(b.PendingOrders()) != 0 || b.Snapshot().OpenPositions != 0 {
		t.Fatal("retry did not durably cancel unfilled entry")
	}
	restored, err := NewPersistentPaperBroker(cfg, source, ledger, "cancel-retry", nil)
	if err != nil || len(restored.PendingOrders()) != 0 {
		t.Fatalf("retry cancellation lost on restart: %v", err)
	}
}

func TestPaperEntryRRLegacyAndGridCompatibility(t *testing.T) {
	source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
	legacy, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1000}, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 1, PositionSizeUSD: 100}); err != nil {
		t.Fatalf("legacy optional protection changed: %v", err)
	}
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.StrategyType = "grid_trading"
	strategy.GridConfig = &store.GridStrategyConfig{}
	at, err := NewAutoTrader(AutoTraderConfig{ID: "rr-grid", AIModel: "custom", Exchange: "binance", ExecutionMode: ExecutionModePaper, InitialBalance: 1000, StrategyConfig: &strategy}, nil, "test-user")
	if err != nil {
		t.Fatal(err)
	}
	at.paperBroker.prices, at.paperBroker.fundingSource = source, nil
	order, err := at.paperBroker.PlaceLimitOrder(&tradertypes.LimitOrderRequest{Symbol: "MUUSDT", Side: "BUY", Price: 100, Quantity: 1, Leverage: 1})
	if err != nil || order.Status != "FILLED" {
		t.Fatalf("grid optional protection changed: order=%+v err=%v", order, err)
	}
	// Even with an AI policy configured, the grid-specific entry marker keeps
	// raw grid limit fills out of the AI-only gate.
	grid, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1000, MinRiskRewardRatio: 2}, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grid.PlaceLimitOrder(&tradertypes.LimitOrderRequest{Symbol: "MUUSDT", Side: "BUY", Price: 100, Quantity: 1, Leverage: 1}); err != nil {
		t.Fatalf("grid fill reached AI gate: %v", err)
	}
}

func TestPaperEntryRRProtectionChangeCancelsRemainderWithoutRestoringOldTP(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, stage := range []string{"fill", "reprice", "fill_persist", "reprice_persist"} {
			t.Run(side+"/"+stage, func(t *testing.T) {
				now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
				source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
				st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				ledger := &paperRiskFailingLedger{PaperLedger: st.Paper()}
				cfg := PaperBrokerConfig{InitialBalance: 1000, MinRiskRewardRatio: 2, MakerFirst: true, MakerFeeBPS: 2, MakerMaxReprices: 2, Clock: func() time.Time { return now }}
				b, err := NewPersistentPaperBroker(cfg, source, ledger, "changed-protection", nil)
				if err != nil {
					t.Fatal(err)
				}
				sl, tp, changedTP := 90.0, 130.0, 105.0
				if side == "short" {
					sl, tp, changedTP = 110, 70, 95
				}
				if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_" + side, Leverage: 1, PositionSizeUSD: 300, StopLoss: sl, TakeProfit: tp}); err != nil {
					t.Fatal(err)
				}
				source.bids, source.asks = [][]string{{"100", "1"}}, [][]string{{"100", "1"}}
				if fills, err := b.RefreshMakerOrders(now.Add(time.Second)); err != nil || len(fills) != 1 {
					t.Fatalf("partial entry: %+v %v", fills, err)
				}
				if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "update_position", NewTakeProfit: changedTP}); err != nil {
					t.Fatalf("protection update fixture: %v", err)
				}
				b, err = NewPersistentPaperBroker(cfg, source, ledger, "changed-protection", nil)
				if err != nil {
					t.Fatal(err)
				}
				before := b.Snapshot()
				if strings.HasPrefix(stage, "fill") {
					source.bids, source.asks = [][]string{{"100", "10"}}, [][]string{{"100", "10"}}
				} else {
					source.bids, source.asks = [][]string{{"99", "10"}}, [][]string{{"101", "10"}}
				}
				now = now.Add(time.Minute)
				ledger.fail = strings.HasSuffix(stage, "_persist")
				fills, err := b.RefreshMakerOrders(now)
				if err == nil || !strings.Contains(err.Error(), "protection changed") || len(fills) != 0 {
					t.Fatalf("updated protection was ignored: fills=%+v err=%v", fills, err)
				}
				if ledger.fail {
					if !strings.Contains(err.Error(), "persist entry cancellation") {
						t.Fatalf("missing cancellation failure: %v", err)
					}
					ledger.fail = false
					fills, err = b.RefreshMakerOrders(now.Add(time.Second))
					if err == nil || !strings.Contains(err.Error(), "protection changed") || len(fills) != 0 {
						t.Fatalf("protection mismatch retry: fills=%+v err=%v", fills, err)
					}
				}
				after := b.Snapshot()
				if after.Balance != before.Balance || after.Fees != before.Fees || after.OpenPositions != before.OpenPositions {
					t.Fatalf("cancellation changed filled account: before=%+v after=%+v", before, after)
				}
				b, err = NewPersistentPaperBroker(cfg, source, ledger, "changed-protection", nil)
				if err != nil {
					t.Fatal(err)
				}
				orders := b.PendingOrders()
				if len(orders) != 1 || !orders[0].ReduceOnly || orders[0].LimitPrice != changedTP {
					t.Fatalf("old TP restored or actual protection lost: %+v", orders)
				}
				positions, err := b.GetPositions()
				if err != nil || len(positions) != 1 {
					t.Fatalf("partial position lost: %v %v", positions, err)
				}
				assertPaperFloat(t, "unchanged filled quantity", positions[0]["quantity"].(float64), 1)
			})
		}
	}
}

type paperRRMultiSymbolSource map[string]*fixedPaperMakerSource

func (s paperRRMultiSymbolSource) GetMarketPrice(symbol string) (float64, error) {
	return s[symbol].GetMarketPrice(symbol)
}
func (s paperRRMultiSymbolSource) GetDepth(symbol string, limit int) (*market.BinanceDepthSnapshot, error) {
	return s[symbol].GetDepth(symbol, limit)
}

func TestPaperEntryRRRejectionDoesNotSkipAnotherPositionsTakeProfit(t *testing.T) {
	for _, reprice := range []bool{false, true} {
		t.Run(fmt.Sprintf("reprice=%v", reprice), func(t *testing.T) { checkPaperRRRejectionExitIsolation(t, reprice) })
	}
}

func checkPaperRRRejectionExitIsolation(t *testing.T, reprice bool) {
	t.Helper()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	source := paperRRMultiSymbolSource{
		"ETHUSDT": {price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}},
		"MUUSDT":  {price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}},
	}
	st, err := store.New(filepath.Join(t.TempDir(), "risk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := PaperBrokerConfig{InitialBalance: 1000, MinRiskRewardRatio: 1, MakerFirst: true, MakerMaxReprices: 2, Clock: func() time.Time { return now }}
	b, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "risk-refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The unfilled entry precedes the other symbol's TP in pending order order.
	for _, sym := range []string{"ETHUSDT", "MUUSDT"} {
		if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: sym, Action: "open_long", Leverage: 1, PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 110}); err != nil {
			t.Fatal(err)
		}
	}
	source["MUUSDT"].asks = [][]string{{"100", "10"}}
	if fills, err := b.RefreshMakerOrders(now.Add(time.Second)); err != nil || len(fills) != 1 || fills[0].Symbol != "MUUSDT" {
		t.Fatalf("held-position setup: %+v %v", fills, err)
	}
	cfg.MinRiskRewardRatio = 2
	b, err = NewPersistentPaperBroker(cfg, source, st.Paper(), "risk-refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	source["ETHUSDT"].asks = [][]string{{"100", "10"}}
	source["MUUSDT"].price = 111
	source["MUUSDT"].bids = [][]string{{"111", "10"}}
	refreshAt := now.Add(2 * time.Second)
	if reprice {
		source["ETHUSDT"].asks = [][]string{{"101", "10"}}
		refreshAt = now.Add(time.Minute)
	}
	fills, err := b.RefreshMakerOrders(refreshAt)
	if err == nil || !strings.Contains(err.Error(), "risk/reward") {
		t.Fatalf("expected rejection diagnostic: %v", err)
	}
	if len(fills) != 1 || fills[0].Symbol != "MUUSDT" || !strings.HasPrefix(fills[0].Action, "take_profit") {
		t.Fatalf("entry rejection skipped an executable protective exit: fills=%+v err=%v", fills, err)
	}
	if positions, err := b.GetPositions(); err != nil || len(positions) != 0 {
		t.Fatalf("TP did not close held position: %+v %v", positions, err)
	}
}
