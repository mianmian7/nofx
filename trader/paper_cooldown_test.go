package trader

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
)

func paperCooldownTrader(b *PaperBroker) *AutoTrader {
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.RiskControl.TradeThrottle = &store.TradeThrottleConfig{
		ReentryCooldownMinutes: 240, MaxOpensPerHour: 2, MaxOpensPerCycle: 1,
	}
	return &AutoTrader{
		executionMode: ExecutionModePaper, exchange: "binance", isRunning: true,
		paperBroker: b, trader: &panicExchangeWriter{},
		config: AutoTraderConfig{StrategyConfig: &strategy}, sleeper: func(time.Duration) {},
	}
}

func paperCooldownOpen(action string) kernel.Decision {
	d := kernel.Decision{Symbol: "MUUSDT", Action: action, PositionSizeUSD: 100, Leverage: 1, StopLoss: 90, TakeProfit: 120}
	if action == "open_short" {
		d.StopLoss, d.TakeProfit = 110, 80
	}
	return d
}

func runPaperCooldownBatch(t *testing.T, at *AutoTrader, action string) *store.DecisionAction {
	t.Helper()
	r := &store.DecisionRecord{}
	at.executeDecisionBatch([]kernel.Decision{paperCooldownOpen(action)}, &kernel.Context{}, r)
	if len(r.Decisions) != 1 {
		t.Fatalf("missing batch decision: %+v", r)
	}
	return &r.Decisions[0]
}

func TestPaperCooldownBlocksFreshEntryAfterCompleteClose(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1000, Clock: func() time.Time { return now }}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatal(err)
	}
	open := paperCooldownOpen("open_long")
	if _, err := b.ExecuteDecision(&open); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(14 * time.Minute)
	before := b.Snapshot()
	action := runPaperCooldownBatch(t, paperCooldownTrader(b), "open_long")
	if action.Success || !strings.Contains(action.Error, "before re-entry") {
		t.Fatalf("Paper cooldown missed full close held outside trader_orders: %+v", action)
	}
	if after := b.Snapshot(); after.OpenPositions != 0 || after.PendingOrders != 0 || after.Balance != before.Balance || after.Fees != before.Fees {
		t.Fatalf("blocked entry mutated state: before=%+v after=%+v", before, after)
	}
}

func TestPaperCooldownExitKindsSidesBoundaryAndRestart(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, exit := range []string{"active", "stop_loss", "take_profit"} {
			t.Run(side+"/"+exit, func(t *testing.T) {
				now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
				prices := fixedPaperPriceSource{"MUUSDT": 100}
				st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				cfg := PaperBrokerConfig{InitialBalance: 1000, Clock: func() time.Time { return now }}
				b, err := NewPersistentPaperBroker(cfg, prices, st.Paper(), "cooldown", nil)
				if err != nil {
					t.Fatal(err)
				}
				d := paperCooldownOpen("open_" + side)
				if _, err := b.ExecuteDecision(&d); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Minute)
				if exit == "active" {
					if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: d.Symbol, Action: "close_" + side}); err != nil {
						t.Fatal(err)
					}
				} else {
					price := d.StopLoss
					if exit == "take_profit" {
						price = d.TakeProfit
					}
					fills, err := b.ProcessPrice(d.Symbol, price, now)
					if err != nil || len(fills) != 1 || !strings.HasPrefix(fills[0].Action, exit) {
						t.Fatalf("exit did not execute: %v %v", fills, err)
					}
				}
				closedAt := now
				b, err = NewPersistentPaperBroker(cfg, prices, st.Paper(), "cooldown", nil)
				if err != nil {
					t.Fatal(err)
				}
				at := paperCooldownTrader(b)
				now = closedAt.Add(240*time.Minute - time.Nanosecond)
				for _, action := range []string{"open_long", "open_short"} {
					r := runPaperCooldownBatch(t, at, action)
					if r.Success || !strings.Contains(r.Error, "before re-entry") {
						t.Fatalf("cooldown bypass for %s: %+v", action, r)
					}
				}
				now = closedAt.Add(240 * time.Minute)
				r := runPaperCooldownBatch(t, at, "open_"+side)
				if !r.Success {
					t.Fatalf("exact 240m boundary should permit entry: %+v", r)
				}
			})
		}
	}
}

func TestPaperCooldownStartsAtFinalQuantityNotPartialClose(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1000, Clock: func() time.Time { return now }}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatal(err)
	}
	d := paperCooldownOpen("open_long")
	if _, err := b.ExecuteDecision(&d); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	ratio := 0.4
	if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: d.Symbol, Action: "close_long", CloseRatio: &ratio}); err != nil {
		t.Fatal(err)
	}
	ps, err := b.GetPositions()
	if err != nil || len(ps) != 1 {
		t.Fatalf("partial fixture lost remainder: %v %v", ps, err)
	}
	assertPaperFloat(t, "remaining after partial", ps[0]["quantity"].(float64), 0.6)
	now = now.Add(3 * time.Hour)
	if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: d.Symbol, Action: "close_long"}); err != nil {
		t.Fatal(err)
	}
	closedAt := now
	now = now.Add(61 * time.Minute) // >4h since partial, <4h since complete close
	at := paperCooldownTrader(b)
	if r := runPaperCooldownBatch(t, at, "open_short"); r.Success || !strings.Contains(r.Error, "before re-entry") {
		t.Fatalf("partial timestamp used instead of full close: %+v", r)
	}
	now = closedAt.Add(4 * time.Hour)
	if r := runPaperCooldownBatch(t, at, "open_short"); !r.Success {
		t.Fatalf("final-close boundary did not expire: %+v", r)
	}
}

func TestPaperCooldownRefusesUnverifiableHistory(t *testing.T) {
	for _, anomaly := range []string{"missing_open", "terminal_mismatch", "duplicate", "conflicting_side", "orphan_dust_close", "quarantine", "quarantine_key", "unknown_action"} {
		t.Run(anomaly, func(t *testing.T) {
			now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			opened := PaperFill{OrderID: 1, Symbol: "MUUSDT", Action: "open_long", Side: "long", Quantity: 1, Price: 100, Time: now.Add(-2 * time.Minute)}
			closed := PaperFill{OrderID: 2, Symbol: "MUUSDT", Action: "close_long", Side: "long", Quantity: 1, Price: 100, Time: now.Add(-time.Minute)}
			fills := []PaperFill{opened, closed}
			state := map[string]any{"initial_balance": 1000, "balance": 1000, "positions": map[string]any{}, "next_id": 10}
			switch anomaly {
			case "missing_open":
				fills = []PaperFill{closed}
			case "terminal_mismatch":
				fills = []PaperFill{opened}
			case "duplicate":
				fills = []PaperFill{opened, opened, closed}
			case "conflicting_side":
				fills[0].Side, fills[1].Side = "short", "short"
			case "orphan_dust_close":
				closed.Quantity = 1e-15
				fills = []PaperFill{closed}
			case "quarantine":
				state["quarantined_positions"] = map[string]PaperPosition{"MUUSDT:long": {Symbol: "MUUSDT", Side: "long", Quantity: 1}}
			case "quarantine_key":
				state["quarantined_positions"] = map[string]PaperPosition{"MUUSDT:long": {Side: "long", Quantity: 1}}
			case "unknown_action":
				fills[1].Action = "unrecognized_exit"
			}
			state["fills"] = fills
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.Paper().SavePaperState("bad-history", raw); err != nil {
				t.Fatal(err)
			}
			b, err := NewPersistentPaperBroker(PaperBrokerConfig{InitialBalance: 1000, Clock: func() time.Time { return now }}, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "bad-history", nil)
			if err != nil {
				t.Fatal(err)
			}
			r := runPaperCooldownBatch(t, paperCooldownTrader(b), "open_long")
			if r.Success || !strings.Contains(r.Error, "cannot verify Paper re-entry") {
				t.Fatalf("invented a close time or allowed unknown history: %+v", r)
			}
		})
	}
}

func TestPaperCooldownMakerChainsAndCompletedPartialCloseOrders(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
	st, err := store.New(filepath.Join(t.TempDir(), "maker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := PaperBrokerConfig{InitialBalance: 1000, MakerFirst: true, MakerMaxReprices: 2, MakerTimeout: 15 * time.Second, Clock: func() time.Time { return now }}
	b, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "maker-cooldown", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := paperCooldownOpen("open_long")
	d.PositionSizeUSD = 200
	first, err := b.ExecuteDecision(&d)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(16 * time.Second)
	if _, err := b.RefreshMakerOrders(now); err != nil {
		t.Fatal(err)
	}
	orders := b.PendingOrders()
	if len(orders) != 1 || orders[0].OrderID == first.OrderID {
		t.Fatalf("reprice fixture missing chain: %+v", orders)
	}
	for i := 0; i < 2; i++ {
		source.asks = [][]string{{"99", "1"}}
		now = now.Add(time.Second)
		if fills, err := b.RefreshMakerOrders(now); err != nil || len(fills) != 1 {
			t.Fatalf("split entry: %+v %v", fills, err)
		}
	}
	source.bids, source.asks = [][]string{{"99", "10"}}, [][]string{{"100", "10"}}
	ratio := 0.5
	if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long", CloseRatio: &ratio}); err != nil {
		t.Fatal(err)
	}
	for _, qty := range []string{"0.4", "0.6"} {
		source.bids = [][]string{{"101", qty}}
		now = now.Add(time.Second)
		if fills, err := b.RefreshMakerOrders(now); err != nil || len(fills) != 1 {
			t.Fatalf("split partial close: %+v %v", fills, err)
		}
	}
	partialCompleted := now
	ps, err := b.GetPositions()
	if err != nil || len(ps) != 1 {
		t.Fatalf("completed partial-close ORDER flattened position: %v %v", ps, err)
	}
	assertPaperFloat(t, "remaining position", ps[0]["quantity"].(float64), 1)
	now = now.Add(3 * time.Hour)
	if _, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatal(err)
	}
	source.bids = [][]string{{"101", "10"}}
	now = now.Add(time.Second)
	if fills, err := b.RefreshMakerOrders(now); err != nil || len(fills) != 1 {
		t.Fatalf("final maker close: %+v %v", fills, err)
	}
	closedAt := now
	b, err = NewPersistentPaperBroker(cfg, source, st.Paper(), "maker-cooldown", nil)
	if err != nil {
		t.Fatal(err)
	}
	at := paperCooldownTrader(b)
	now = partialCompleted.Add(4*time.Hour + time.Minute)
	if r := runPaperCooldownBatch(t, at, "open_short"); r.Success || !strings.Contains(r.Error, "before re-entry") {
		t.Fatalf("maker partial/reprice history bypassed cooldown: %+v", r)
	}
	now = closedAt.Add(4 * time.Hour)
	if r := runPaperCooldownBatch(t, at, "open_short"); !r.Success {
		t.Fatalf("maker history blocked beyond full-close boundary: %+v", r)
	}
}

func TestPaperCooldownLegacyRiskSideAndSymbolIsolation(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, exit := range []string{"stop_loss", "take_profit"} {
			t.Run(side+"/"+exit, func(t *testing.T) {
				now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
				// Captured legacy schema: side omitted; a single known open
				// side makes the generic risk exit unambiguous.
				fills := []PaperFill{
					{OrderID: 1, Symbol: "MUUSDT", Action: "open_" + side, Quantity: 1, Time: now.Add(-2 * time.Minute)},
					{OrderID: 2, Symbol: "MUUSDT", Action: exit, Quantity: 1, Time: now.Add(-time.Minute)},
					{OrderID: 3, Symbol: "ETHUSDT", Action: "close_long", Quantity: 9, Time: now}, // unrelated invalid history
				}
				raw, err := json.Marshal(map[string]any{"initial_balance": 1000, "balance": 1000, "positions": map[string]any{}, "fills": fills, "next_id": 4})
				if err != nil {
					t.Fatal(err)
				}
				st, err := store.New(filepath.Join(t.TempDir(), "legacy.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				if err := st.Paper().SavePaperState("legacy", raw); err != nil {
					t.Fatal(err)
				}
				b, err := NewPersistentPaperBroker(PaperBrokerConfig{InitialBalance: 1000, Clock: func() time.Time { return now }}, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "legacy", nil)
				if err != nil {
					t.Fatal(err)
				}
				at := paperCooldownTrader(b)
				if r := runPaperCooldownBatch(t, at, "open_long"); r.Success || !strings.Contains(r.Error, "before re-entry") {
					t.Fatalf("unambiguous legacy side not reconstructed: %+v", r)
				}
				now = now.Add(4 * time.Hour)
				if r := runPaperCooldownBatch(t, at, "open_long"); !r.Success {
					t.Fatalf("unrelated history poisoned MU: %+v", r)
				}
			})
		}
	}
}

func TestPaperCooldownDoesNotAffectLiveThrottleBranch(t *testing.T) {
	// No broker: a Paper-history lookup would fail, but live still uses its
	// unchanged order store path. Do not construct or invoke a live writer.
	at := &AutoTrader{executionMode: ExecutionModeLive, exchange: "binance"}
	if reason := at.tradeThrottleReason(paperCooldownOpen("open_long"), &kernel.Context{}, 0); reason != "" {
		t.Fatalf("Paper-only history leaked into live: %s", reason)
	}
}
