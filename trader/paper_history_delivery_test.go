package trader

import (
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

func TestPaperMakerCloseImmediatelyReachesDurableHistory(t *testing.T) {
	for _, action := range []string{"close_long", "take_profit_long", "close_short", "take_profit_short"} {
		t.Run(action, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "history.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
			source := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "20"}}, asks: [][]string{{"100", "20"}}}
			cfg := PaperBrokerConfig{InitialBalance: 1000, MakerFirst: true, MakerFeeBPS: 2, Clock: func() time.Time { return now }}
			b, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "maker-history", newPaperTradeRecorder(st, "okx"))
			if err != nil {
				t.Fatal(err)
			}
			side, open, stop, target := "long", "open_long", 90.0, 120.0
			if action == "close_short" || action == "take_profit_short" {
				side, open, stop, target = "short", "open_short", 110, 80
			}
			accepted, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: open, PositionSizeUSD: 200, Leverage: 5, StopLoss: stop, TakeProfit: target})
			if err != nil {
				t.Fatal(err)
			}
			depth := &market.BinanceDepthSnapshot{Bids: source.bids, Asks: source.asks}
			if _, err = b.applyMakerFill(accepted.OrderID, accepted.Quantity, depth, now); err != nil {
				t.Fatal(err)
			}
			var order PaperFill
			if action == "close_long" || action == "close_short" {
				order, err = b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: action})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				for _, p := range b.PendingOrders() {
					if p.ReduceOnly {
						order = PaperFill{OrderID: p.OrderID, Quantity: p.RemainingQuantity}
					}
				}
			}
			if _, err = b.applyMakerFill(order.OrderID, order.Quantity/2, depth, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err = b.applyMakerFill(order.OrderID, order.Quantity/2, depth, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			records, err := st.Position().GetClosedPositions("maker-history", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("maker %s closed but history has %d rows", action, len(records))
			}
			perf := b.Performance()
			r := records[0]
			want := perf.ClosedTrades[0]
			if r.Side != side {
				t.Fatalf("wrong side %+v", r)
			}
			assertPaperFloat(t, "recorded quantity", r.Quantity, want.Quantity)
			assertPaperFloat(t, "recorded net PnL", r.RealizedPnL, want.RealizedPnL)
			assertPaperFloat(t, "recorded complete fees", r.Fee, want.Fee)
			if r.CloseReason != want.CloseReason {
				t.Fatalf("reason=%s, want ledger reason %s", r.CloseReason, want.CloseReason)
			}
			before := b.Snapshot()
			restored, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "maker-history", newPaperTradeRecorder(st, "okx"))
			if err != nil {
				t.Fatal(err)
			}
			records, err = st.Position().GetClosedPositions("maker-history", 10)
			if err != nil || len(records) != 1 {
				t.Fatalf("replay duplicated history: rows=%d err=%v", len(records), err)
			}
			assertPaperFloat(t, "balance unchanged by history replay", restored.Snapshot().Balance, before.Balance)
		})
	}
}
