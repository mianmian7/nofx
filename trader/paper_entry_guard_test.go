package trader

import (
	"context"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

func testPaperEvidence(now time.Time) *kernel.PaperEntryEvidence {
	return &kernel.PaperEntryEvidence{Symbol: "BTCUSDT", Action: "open_long", ObservedAt: now, ValidUntil: now.Truncate(15 * time.Minute).Add(15 * time.Minute), TrendBoundary: 95, Invalidation: 94}
}
func testGuardOpen(now time.Time) *kernel.Decision {
	return &kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 60, StopLoss: 90, TakeProfit: 130, PaperEntryEvidence: testPaperEvidence(now)}
}
func guardSource() *fixedPaperMakerSource {
	return &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
}

func TestPaperEntryGuardSubmissionPendingRestoreAndReprice(t *testing.T) {
	for _, scenario := range []string{"expiry-fill", "expiry-reprice", "price-invalidated", "missing-after-restore", "partial-remainder", "valid-after-restore"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 1, 1, 0, 7, 0, 0, time.UTC)
			st, err := store.New(filepath.Join(t.TempDir(), "guard.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MakerMaxReprices: 3, RequireEntryEvidence: true, Clock: func() time.Time { return now }}
			src := guardSource()
			b, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "guard", nil)
			if err != nil {
				t.Fatal(err)
			}
			invalid := testGuardOpen(now)
			invalid.PaperEntryEvidence = nil
			if _, err = b.ExecuteDecision(invalid); err == nil {
				t.Fatal("missing evidence admitted")
			}
			d := testGuardOpen(now)
			if _, err = b.ExecuteDecision(d); err != nil {
				t.Fatal(err)
			}
			// Broker owns a copy; a later caller mutation cannot renew its permit.
			d.PaperEntryEvidence.ValidUntil = now.Add(time.Hour)
			order := hourlyEntries(b)[0]
			if order.EntryEvidence.ValidUntil.Equal(d.PaperEntryEvidence.ValidUntil) {
				t.Fatal("caller can mutate persisted evidence")
			}
			depth, _ := src.GetDepth("BTCUSDT", 5)
			if scenario == "partial-remainder" {
				if _, err = b.applyMakerFill(order.OrderID, .1, depth, now); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing-after-restore" {
				b.mu.Lock()
				o := b.pendingOrders[order.OrderID]
				o.EntryEvidence = nil
				b.pendingOrders[order.OrderID] = o
				err = b.persistLocked()
				b.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			b, err = NewPersistentPaperBroker(cfg, src, st.Paper(), "guard", nil)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "expiry-fill" || scenario == "expiry-reprice" || scenario == "partial-remainder" {
				now = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
			}
			if scenario == "price-invalidated" {
				depth.Bids = [][]string{{"93", "10"}}
				depth.Asks = [][]string{{"93", "10"}}
			}
			order = hourlyEntries(b)[0]
			if scenario == "expiry-reprice" {
				_, err = b.expireOrRepriceMakerOrder(order.OrderID, depth, now)
			} else {
				_, err = b.applyMakerFill(order.OrderID, order.RemainingQuantity, depth, now)
			}
			if scenario == "valid-after-restore" {
				if err != nil || b.Snapshot().OpenPositions != 1 {
					t.Fatalf("valid restored fill failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid pending remainder filled/repriced")
			}
			if len(hourlyEntries(b)) != 0 {
				t.Fatal("invalid entry reservation not released")
			}
			if scenario == "partial-remainder" {
				if b.Snapshot().OpenPositions != 1 {
					t.Fatal("partial filled position was deleted")
				}
				if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "update_position", NewStopLoss: 92}); err != nil {
					t.Fatalf("protection blocked: %v", err)
				}
				if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "close_long"}); err != nil {
					t.Fatalf("exit blocked: %v", err)
				}
				for _, o := range b.PendingOrders() {
					if o.Action == "close_long" {
						if _, err = b.applyMakerFill(o.OrderID, o.RemainingQuantity, depth, now); err != nil {
							t.Fatal(err)
						}
					}
				}
				if b.Snapshot().OpenPositions != 0 {
					t.Fatal("expired evidence prevented exit")
				}
			} else if b.Snapshot().OpenPositions != 0 || len(b.RecentFills(0)) != 0 {
				t.Fatal("rejected order changed ledger")
			}
			restored, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "guard", nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(hourlyEntries(restored)) != 0 {
				t.Fatal("cancellation not persisted")
			}
		})
	}
}

func TestPaperEntryGuardTakerAndLegacyOptOut(t *testing.T) {
	now := time.Now().UTC()
	for _, enabled := range []bool{true, false} {
		b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, RequireEntryEvidence: enabled}, guardSource())
		if err != nil {
			t.Fatal(err)
		}
		d := testGuardOpen(now)
		d.PaperEntryEvidence = nil
		_, err = b.ExecuteDecision(d)
		if enabled && err == nil {
			t.Fatal("taker bypassed guard")
		}
		if !enabled && err != nil {
			t.Fatal("opt-out contract changed", err)
		}
		if enabled {
			d.PaperEntryEvidence = testPaperEvidence(now)
			if _, err = b.ExecuteDecision(d); err != nil {
				t.Fatal("valid taker blocked", err)
			}
		}
	}
	for _, mode := range []ExecutionMode{ExecutionModePaper, ExecutionModeLive} {
		cfg := &store.StrategyConfig{PaperEntryGuard: true}
		at := &AutoTrader{executionMode: mode, config: AutoTraderConfig{StrategyConfig: cfg}}
		if err := at.attachPaperEntryEvidence(&kernel.Decision{Action: "close_long"}, nil, now); err != nil {
			t.Fatal("exit gated", err)
		}
		err := at.attachPaperEntryEvidence(&kernel.Decision{Action: "open_long"}, nil, now)
		if mode == ExecutionModeLive && err != nil {
			t.Fatal("Paper rule leaked into live")
		}
		if mode == ExecutionModePaper && err == nil {
			t.Fatal("missing context passed")
		}
	}
}

func TestPaperEntryGuardBatchPreservesExitBesideInvalidOpen(t *testing.T) {
	// Existing position is opened before enabling the opt-in policy.
	b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100}, guardSource())
	if err != nil {
		t.Fatal(err)
	}
	d := testGuardOpen(time.Now().UTC())
	if _, err = b.ExecuteDecision(d); err != nil {
		t.Fatal(err)
	}
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.PaperEntryGuard = true
	b.config.RequireEntryEvidence = true
	at := &AutoTrader{executionMode: ExecutionModePaper, paperBroker: b, isRunning: true, exchange: "binance", config: AutoTraderConfig{StrategyConfig: &cfg}}
	record := &store.DecisionRecord{Success: true}
	ctx := &kernel.Context{MarketDataMap: map[string]*market.Data{}}
	at.executeDecisionBatchContext(context.Background(), []kernel.Decision{{Symbol: "BTCUSDT", Action: "close_long"}, {Symbol: "ETHUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 60, StopLoss: 90, TakeProfit: 130}}, ctx, record)
	if b.Snapshot().OpenPositions != 0 {
		t.Fatal("exit blocked by invalid opening")
	}
	if len(record.Decisions) != 2 || !record.Decisions[0].Success || record.Decisions[1].Success {
		t.Fatalf("wrong per-action audit: %+v", record.Decisions)
	}
}
