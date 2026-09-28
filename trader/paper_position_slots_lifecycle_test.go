package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/store"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func slotSource() *fixedPaperMakerSource {
	return &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
}
func slotOpen(b *PaperBroker, sym string) (PaperFill, error) {
	return b.ExecuteDecision(&kernel.Decision{Symbol: sym, Action: "open_long", PositionSizeUSD: 20, Leverage: 3, StopLoss: 95, TakeProfit: 115})
}
func TestPaperPositionSlotsConcurrentCancelAndReuse(t *testing.T) {
	b, e := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxPositions: 1}, slotSource())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, e := slotOpen(b, fmt.Sprintf("COIN%dUSDT", i)); e == nil {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d", accepted.Load())
	}
	o := b.PendingOrders()[0]
	if e = b.CancelOrder(o.Symbol, fmt.Sprint(o.OrderID)); e != nil {
		t.Fatal(e)
	}
	if _, e = slotOpen(b, "SOLUSDT"); e != nil {
		t.Fatal("canceled slot not released", e)
	}
}
func TestPaperPositionSlotsPartialRepriceAndProtection(t *testing.T) {
	src := slotSource()
	b, e := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxPositions: 1, MakerMaxReprices: 1, MakerTimeout: time.Second}, src)
	if e != nil {
		t.Fatal(e)
	}
	f, e := slotOpen(b, "SOLUSDT")
	if e != nil {
		t.Fatal(e)
	}
	depth, _ := src.GetDepth("SOLUSDT", 5)
	if _, e = b.applyMakerFill(f.OrderID, .05, depth, b.now()); e != nil {
		t.Fatal(e)
	}
	if _, e = b.expireOrRepriceMakerOrder(f.OrderID, depth, b.now().Add(2*time.Second)); e != nil {
		t.Fatal("own partial reservation rejected", e)
	}
	var entry PaperPendingOrder
	for _, o := range b.PendingOrders() {
		if !o.ReduceOnly {
			entry = o
		}
	}
	if _, e = b.applyMakerFill(entry.OrderID, entry.RemainingQuantity, depth, b.now()); e != nil {
		t.Fatal(e)
	}
	if _, e = slotOpen(b, "ETHUSDT"); e == nil {
		t.Fatal("held position didn't occupy slot")
	}
	if _, e = b.ExecuteDecision(&kernel.Decision{Symbol: "SOLUSDT", Action: "close_long"}); e != nil {
		t.Fatal("exit blocked", e)
	}
	for _, o := range b.PendingOrders() {
		if o.Action == "close_long" {
			if _, e = b.applyMakerFill(o.OrderID, o.RemainingQuantity, depth, b.now()); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = slotOpen(b, "ETHUSDT"); e != nil {
		t.Fatal("closed slot not released", e)
	}
}
func TestPaperPositionSlotsRestoreAndLowerLimit(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "slots.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	src := slotSource()
	cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxPositions: 2}
	b, e := NewPersistentPaperBroker(cfg, src, st.Paper(), "slots", nil)
	if e != nil {
		t.Fatal(e)
	}
	first, e := slotOpen(b, "SOLUSDT")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = slotOpen(b, "ETHUSDT"); e != nil {
		t.Fatal(e)
	}
	cfg.MaxPositions = 1
	b, e = NewPersistentPaperBroker(cfg, src, st.Paper(), "slots", nil)
	if e != nil {
		t.Fatal(e)
	}
	depth, _ := src.GetDepth("SOLUSDT", 5)
	if _, e = b.applyMakerFill(first.OrderID, first.Quantity, depth, b.now()); e == nil {
		t.Fatal("restored over-limit entries allowed")
	}
	entries := b.PendingOrders()
	if len(entries) != 1 {
		t.Fatal("only rejected entry should be canceled", entries)
	}
	if _, e = b.applyMakerFill(entries[0].OrderID, entries[0].RemainingQuantity, depth, b.now()); e != nil {
		t.Fatal(e)
	}
	if b.Snapshot().OpenPositions != 1 {
		t.Fatal("unexpected restored position count")
	}
}
func TestPaperPositionSlotsTakerAndLegacy(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			b, e := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MaxPositions: limit}, slotSource())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = slotOpen(b, "SOLUSDT"); e != nil {
				t.Fatal(e)
			}
			_, e = slotOpen(b, "ETHUSDT")
			if (e != nil) != (limit == 1) {
				t.Fatalf("limit %d error %v", limit, e)
			}
		})
	}
}
