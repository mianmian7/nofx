package trader

import (
	"fmt"
	"testing"
)

func TestPaperPositionSlotsPersistenceFailurePreservesReservation(t *testing.T) {
	ledger := &failingLedger{}
	src := slotSource()
	b, e := NewPersistentPaperBroker(PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxPositions: 1}, src, ledger, "slots", nil)
	if e != nil {
		t.Fatal(e)
	}
	ledger.failSave = true
	if _, e = slotOpen(b, "SOLUSDT"); e == nil {
		t.Fatal("expected placement failure")
	}
	if len(b.PendingOrders()) != 0 {
		t.Fatal("failed placement left a phantom slot")
	}
	ledger.failSave = false
	f, e := slotOpen(b, "SOLUSDT")
	if e != nil {
		t.Fatal(e)
	}
	ledger.failSave = true
	if e = b.CancelOrder("SOLUSDT", fmt.Sprint(f.OrderID)); e == nil {
		t.Fatal("expected cancellation failure")
	}
	if len(b.PendingOrders()) != 1 {
		t.Fatal("failed cancellation released a live reservation")
	}
	depth, _ := src.GetDepth("SOLUSDT", 5)
	if _, e = b.applyMakerFill(f.OrderID, f.Quantity, depth, b.now()); e == nil {
		t.Fatal("expected fill persistence failure")
	}
	if b.Snapshot().OpenPositions != 0 || len(b.PendingOrders()) != 1 || b.Snapshot().Balance != 100 {
		t.Fatal("failed fill corrupted ledger")
	}
	ledger.failSave = false
	if _, e = b.applyMakerFill(f.OrderID, f.Quantity, depth, b.now()); e != nil {
		t.Fatal(e)
	}
	if b.Snapshot().OpenPositions != 1 {
		t.Fatal("retry didn't fill")
	}
}
