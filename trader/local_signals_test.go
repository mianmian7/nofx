package trader

import (
	"context"
	"fmt"
	"nofx/kernel"
	"nofx/market"
	"nofx/signals"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalSignalFailureBlocksOnlyOpenings(t *testing.T) {
	old := signals.Default()
	signals.SetDefault(nil)
	defer signals.SetDefault(old)
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "local_signals"
	cfg.CoinSource.LocalSignalMinScore = 60
	cfg.CoinSource.LocalSignalLimit = 5
	at := &AutoTrader{strategyEngine: kernel.NewStrategyEngine(&cfg)}
	for _, action := range []string{"open_long", "open_short"} {
		if err := at.validateLocalSignalOpening(&kernel.Decision{Symbol: "BTCUSDT", Action: action}); err == nil {
			t.Fatal("opening passed unavailable analysis")
		}
	}
	for _, action := range []string{"close_long", "close_short", "update_position", "wait", "hold"} {
		if err := at.validateLocalSignalOpening(&kernel.Decision{Symbol: "BTCUSDT", Action: action}); err != nil {
			t.Fatalf("risk management blocked: %v", err)
		}
	}
	cfg.CoinSource.SourceType = "static"
	if err := at.validateLocalSignalOpening(&kernel.Decision{Symbol: "BTCUSDT", Action: "open_long"}); err != nil {
		t.Fatalf("legacy strategy changed: %v", err)
	}
}

func TestLocalSignalFailureStillRunsHeldPositionCycleAndClose(t *testing.T) {
	old := signals.Default()
	signals.SetDefault(nil)
	defer signals.SetDefault(old)
	release := make(chan struct{})
	close(release)
	client := &controlledCycleAIClient{entered: make(chan struct{}), release: release, cycleIDs: make(chan string, 1), response: `[{"symbol":"BTCUSDT","action":"close_long","reasoning":"reduce risk"}]`}
	at, broker, st := newFullCycleTimeoutTrader(t, client)
	defer st.Close()
	at.positionFirstSeenTime = make(map[string]int64)
	at.peakPnLCache = make(map[string]float64)
	at.config.CycleTimeout = 3 * time.Second
	at.sleeper = func(time.Duration) {}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 130}); err != nil {
		t.Fatal(err)
	}
	cfg := at.config.StrategyConfig
	cfg.CoinSource.SourceType = "local_signals"
	cfg.CoinSource.LocalSignalMinScore = 60
	cfg.CoinSource.LocalSignalLimit = 5
	broker.setLocalSignalEntryValidator(cfg, "binance")
	ctx, err := at.buildTradingContext()
	if err != nil || len(ctx.Positions) != 1 || len(ctx.CandidateCoins) != 0 {
		t.Fatalf("candidate outage blocked held-position context: %+v %v", ctx, err)
	}
	ctx.MarketDataMap = map[string]*market.Data{"BTCUSDT": {Symbol: "BTCUSDT", CurrentPrice: 100}}
	at.tradingContextBuilder = func(context.Context) (*kernel.Context, error) { return ctx, nil }
	if err := at.runAutomaticCycle(); err != nil {
		t.Fatal(err)
	}
	if !client.enterOnce.Load() || broker.Snapshot().OpenPositions != 0 {
		t.Fatalf("empty candidates skipped AI exit: %+v", broker.Snapshot())
	}
	records, err := st.Decision().GetLatestRecords(at.id, 1)
	if err != nil || len(records) != 1 || len(records[0].Decisions) != 1 || !records[0].Decisions[0].Success {
		t.Fatalf("close not recorded: %+v %v", records, err)
	}
}

func TestLocalSignalOutageCannotBlockIndependentPaperProtection(t *testing.T) {
	previous := signals.Default()
	signals.SetDefault(nil)
	defer signals.SetDefault(previous)
	for _, target := range []float64{89, 111} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			prices := &mutablePaperPriceSource{price: 100}
			broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1000}, prices)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 110}); err != nil {
				t.Fatal(err)
			}
			at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 2*time.Millisecond)
			cfg := at.strategyEngine.GetConfig()
			cfg.CoinSource.SourceType = "local_signals"
			cfg.CoinSource.LocalSignalMinScore = 60
			cfg.CoinSource.LocalSignalLimit = 5
			at.config.StrategyConfig = cfg
			broker.setLocalSignalEntryValidator(cfg, "binance")
			at.cycleRunner = func() error { return fmt.Errorf("AI unavailable") }
			defer at.Shutdown()
			done := make(chan error, 1)
			go func() { done <- at.Run() }()
			waitForPaperCondition(t, time.Second, func() bool { return prices.Calls() > 1 })
			prices.Set(target)
			waitForPaperCondition(t, time.Second, func() bool { return broker.Snapshot().OpenPositions == 0 })
			at.Stop()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("paper test runtime did not stop")
			}
		})
	}
}

func TestLocalSignalPendingRestartAndPolicyChangesReleaseReservation(t *testing.T) {
	for _, change := range []string{"expired", "reversed", "score", "source", "restart"} {
		t.Run(change, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "pending.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			direction, score, minimum, expired := "long", 80.0, 60.0, false
			validator := func(_ string, action string) error {
				if expired || direction != "long" || score < minimum || action != "open_long" {
					return fmt.Errorf("analysis no longer permits entry")
				}
				return nil
			}
			cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MakerFeeBPS: 2, EntryValidator: validator}
			src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
			broker, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "signal-pending", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 60, StopLoss: 90, TakeProfit: 130}); err != nil {
				t.Fatal(err)
			}
			if broker.Snapshot().AvailableBalance >= 100 {
				t.Fatal("entry did not reserve margin")
			}
			switch change {
			case "expired":
				expired = true
			case "reversed":
				direction = "short"
			case "score":
				minimum = 90
			case "source":
				strategy := store.GetDefaultStrategyConfig("en")
				strategy.CoinSource.SourceType = "local_signals"
				strategy.CoinSource.LocalSignalMinScore = 60
				strategy.CoinSource.LocalSignalLimit = 5
				previous := signals.Default()
				signals.SetDefault(nil)
				defer signals.SetDefault(previous)
				broker.setLocalSignalEntryValidator(&strategy, "binance")
			case "restart":
				cfg.EntryValidator = func(string, string) error { return fmt.Errorf("restart has not refreshed analysis") }
				broker, err = NewPersistentPaperBroker(cfg, src, st.Paper(), "signal-pending", nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := broker.RefreshMakerOrders(broker.now()); err == nil {
				t.Fatal("invalidated entry filled")
			}
			after := broker.Snapshot()
			if after.OpenPositions != 0 || after.PendingOrders != 0 || after.AvailableBalance != 100 || after.Fees != 0 || len(broker.RecentFills(0)) != 0 {
				t.Fatalf("rejected entry left exposure or reserved funds: %+v", after)
			}
			restored, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "signal-pending", nil)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Snapshot().PendingOrders != 0 || restored.Snapshot().AvailableBalance != 100 {
				t.Fatal("cancellation did not survive restart")
			}
		})
	}
}

func TestPendingPaperEntryRevalidatesLocalAnalysis(t *testing.T) {
	// A delayed entry must not fill using a signal approved before the AI call
	// or before a restart. Cancel only its remainder; protective exits survive.
	b := &PaperBroker{config: PaperBrokerConfig{EntryValidator: func(string, string) error { return fmt.Errorf("signal expired") }}, pendingOrders: map[int64]PaperPendingOrder{}, positions: map[string]PaperPosition{}, marks: map[string]float64{}, markTimes: map[string]time.Time{}}
	order := PaperPendingOrder{OrderID: 1, Symbol: "BTCUSDT", Action: "open_long", Side: "long", Quantity: 1, RemainingQuantity: 1}
	b.pendingOrders[1] = order
	_, err := b.applyPendingOpenFillLocked(order, 1, 100, time.Now())
	if err == nil {
		t.Fatal("expired signal filled")
	}
	if len(b.positions) != 0 || len(b.pendingOrders) != 0 {
		t.Fatalf("entry not cancelled: %+v", b.pendingOrders)
	}
	if len(b.orderEvents) != 1 {
		t.Fatal("entry rejection missing audit event")
	}
}
