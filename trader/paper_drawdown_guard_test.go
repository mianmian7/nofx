package trader

import (
	"nofx/kernel"
	"nofx/store"
	"path/filepath"
	"testing"
)

func TestPaperDrawdownGuardPersistsAndNeverBlocksExits(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "dd.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	cfg := PaperBrokerConfig{InitialBalance: 100, MaxPositions: 2, MaxDrawdownRatio: .08}
	b, e := NewPersistentPaperBroker(cfg, slotSource(), st.Paper(), "dd", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = slotOpen(b, "SOLUSDT"); e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	b.maxDrawdown = 8.1
	if e = b.persistLocked(); e != nil {
		t.Fatal(e)
	}
	b.mu.Unlock()
	b, e = NewPersistentPaperBroker(cfg, slotSource(), st.Paper(), "dd", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = slotOpen(b, "ETHUSDT"); e == nil {
		t.Fatal("persistent breaker bypassed after restart/recovery")
	}
	if _, e = b.ExecuteDecision(&kernel.Decision{Symbol: "SOLUSDT", Action: "close_long"}); e != nil {
		t.Fatal("protection exit blocked", e)
	}
	if b.Snapshot().OpenPositions != 0 {
		t.Fatal("exit didn't close")
	}
}
func TestPaperDrawdownGuardCancelsOnlyEntryRemainder(t *testing.T) {
	src := slotSource()
	b, e := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxDrawdownRatio: .08}, src)
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
	b.mu.Lock()
	b.maxDrawdown = 9
	b.mu.Unlock()
	if _, e = b.applyMakerFill(f.OrderID, .15, depth, b.now()); e == nil {
		t.Fatal("delayed entry ignored drawdown")
	}
	if b.Snapshot().OpenPositions != 1 || b.positions["SOLUSDT:long"].Quantity != .05 {
		t.Fatal("partial position corrupted")
	}
	if _, e = b.ExecuteDecision(&kernel.Decision{Symbol: "SOLUSDT", Action: "close_long"}); e != nil {
		t.Fatal(e)
	}
}
