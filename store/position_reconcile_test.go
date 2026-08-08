package store

import (
	"testing"
	"time"
)

func newReconcileTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func openRow(t *testing.T, st *Store, traderID, exchangeID, symbol, side string, qty, pnl float64, entryMs int64) int64 {
	t.Helper()
	pos := &TraderPosition{
		TraderID:      traderID,
		ExchangeID:    exchangeID,
		ExchangeType:  "hyperliquid",
		Symbol:        symbol,
		Side:          side,
		Quantity:      qty,
		EntryQuantity: qty,
		EntryPrice:    100,
		EntryTime:     entryMs,
		RealizedPnL:   pnl,
		Status:        "OPEN",
		Source:        "sync",
		CreatedAt:     entryMs,
		UpdatedAt:     entryMs,
	}
	if err := st.Position().CreateOpenPosition(pos); err != nil {
		t.Fatalf("create open position: %v", err)
	}
	return pos.ID
}

func TestReconcileClosesZombiesKeepsLiveAndTrims(t *testing.T) {
	st := newReconcileTestStore(t)
	const exch = "ex-hl"
	base := time.Now().Add(-48 * time.Hour).UnixMilli()

	// Zombie under the CURRENT trader id: exchange holds nothing for DRAM.
	zombieID := openRow(t, st, "trader-now", exch, "xyz:DRAM", "LONG", 6.8, -20.34, base)

	// Zombie left by a PRIOR autopilot incarnation on the SAME exchange —
	// this is the case a per-trader-id reconcile would miss.
	legacyID := openRow(t, st, "trader-old", exch, "SOLUSDT", "SHORT", 6.94, -3.5, base+500)

	// Duplicates for SP500 (different incarnations): newest survives trimmed,
	// older closes.
	oldSP := openRow(t, st, "trader-old", exch, "xyz:SP500", "LONG", 0.07, -1.5, base+1000)
	newSP := openRow(t, st, "trader-now", exch, "xyz:SP500", "LONG", 0.124, 2.5, base+2000)

	// Healthy row exactly matching live — untouched.
	healthy := openRow(t, st, "trader-now", exch, "BTCUSDT", "LONG", 0.01, 0, base+3000)

	// Row on a DIFFERENT exchange account — must be out of scope.
	otherExch := openRow(t, st, "trader-now", "ex-other", "ETHUSDT", "LONG", 2.0, 0, base+4000)

	live := map[string]float64{
		LivePositionKey("xyz:SP500", "long"): 0.057,
		LivePositionKey("BTCUSDT", "long"):   0.01,
	}

	closed, err := st.Position().ReconcileOpenPositionsWithLive(exch, live)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if closed != 3 {
		t.Fatalf("expected 3 zombies closed (DRAM + legacy SOL + old SP500), got %d", closed)
	}

	get := func(id int64) *TraderPosition {
		t.Helper()
		var pos TraderPosition
		if err := st.Position().db.Where("id = ?", id).First(&pos).Error; err != nil {
			t.Fatalf("load position %d: %v", id, err)
		}
		return &pos
	}

	if dram := get(zombieID); dram.Status != "CLOSED" || dram.RealizedPnL != -20.34 || dram.CloseReason != "reconcile" {
		t.Fatalf("DRAM zombie should close via reconcile keeping PnL, got %+v", dram)
	}
	if sol := get(legacyID); sol.Status != "CLOSED" {
		t.Fatalf("legacy-incarnation SOL zombie on same exchange should close, got %+v", sol)
	}
	if sp := get(oldSP); sp.Status != "CLOSED" {
		t.Fatalf("older duplicate SP500 row should close, got %+v", sp)
	}
	if sp := get(newSP); sp.Status != "OPEN" || sp.Quantity > 0.0571 || sp.Quantity < 0.0569 {
		t.Fatalf("newest SP500 row should stay open trimmed to live 0.057, got status=%s qty=%v", sp.Status, sp.Quantity)
	}
	if btc := get(healthy); btc.Status != "OPEN" || btc.Quantity != 0.01 {
		t.Fatalf("healthy row must be untouched, got %+v", btc)
	}
	if eth := get(otherExch); eth.Status != "OPEN" {
		t.Fatalf("row on a different exchange must be out of scope, got %+v", eth)
	}
}

func TestReconcileNoOpenRowsIsNoop(t *testing.T) {
	st := newReconcileTestStore(t)
	closed, err := st.Position().ReconcileOpenPositionsWithLive("ex-empty", map[string]float64{})
	if err != nil || closed != 0 {
		t.Fatalf("expected clean noop, got closed=%d err=%v", closed, err)
	}
}

// TestReconcilePositionsFromLiveNormalizesSymbols verifies the convenience
// wrapper normalizes exchange-native symbols (e.g. "SKHYNIXUSDT", "MUUSDT")
// to the canonical stored form (e.g. "xyz:SKHYNIX", "xyz:MU") before matching,
// exactly like the order-sync path that wrote the rows.
func TestReconcilePositionsFromLiveNormalizesSymbols(t *testing.T) {
	st := newReconcileTestStore(t)
	const exch = "ex-bitget"
	base := time.Now().Add(-24 * time.Hour).UnixMilli()

	// Stored rows use the canonical normalized symbol (xyz: prefix for XYZ
	// assets), matching what SyncOrdersFromBitget writes.
	zombieMU := openRow(t, st, "trader-a", exch, "xyz:MU", "SHORT", 0.22, 0, base)
	healthySKH := openRow(t, st, "trader-a", exch, "xyz:SKHYNIX", "SHORT", 0.13, 0, base+1000)

	// Exchange-native live output (Bitget GetPositions returns "SKHYNIXUSDT",
	// NOT the xyz: prefix). MU is absent from the live book — its DB row must
	// be closed as a zombie; SKHYNIX matches live and stays open.
	live := []map[string]interface{}{
		{"symbol": "SKHYNIXUSDT", "side": "short", "positionAmt": float64(0.13)},
	}

	closed, err := st.Position().ReconcilePositionsFromLive(exch, live, func(symbol string) string {
		return normalizeForReconcileTest(symbol)
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if closed != 1 {
		t.Fatalf("expected MU zombie (0.22) closed, SKHYNIX healthy row kept; got %d closed", closed)
	}

	var pos TraderPosition
	if err := st.Position().db.Where("id = ?", zombieMU).First(&pos).Error; err != nil {
		t.Fatalf("load MU row: %v", err)
	}
	if pos.Status != "CLOSED" {
		t.Fatalf("MU row should close as zombie, got status=%s", pos.Status)
	}
	var skh TraderPosition
	if err := st.Position().db.Where("id = ?", healthySKH).First(&skh).Error; err != nil {
		t.Fatalf("load SKHYNIX row (id=%d): %v", healthySKH, err)
	}
	if skh.Status != "OPEN" || skh.Quantity != 0.13 {
		t.Fatalf("SKHYNIX row must stay open at live qty, got status=%s qty=%v", skh.Status, skh.Quantity)
	}
}

// TestReconcilePositionsFromLiveIgnoresZeroQuantity verifies zero/absent
// position amounts never enter the live map.
func TestReconcilePositionsFromLiveIgnoresZeroQuantity(t *testing.T) {
	st := newReconcileTestStore(t)
	const exch = "ex-zero"
	base := time.Now().Add(-24 * time.Hour).UnixMilli()
	rowID := openRow(t, st, "trader-a", exch, "BTCUSDT", "LONG", 0.01, 0, base)

	live := []map[string]interface{}{
		{"symbol": "BTCUSDT", "side": "long", "positionAmt": float64(0)},
		{"symbol": "", "side": "long", "positionAmt": float64(1)},
		{"symbol": "BTCUSDT", "side": "long", "positionAmt": 0.0},
	}

	closed, err := st.Position().ReconcilePositionsFromLive(exch, live, func(symbol string) string { return symbol })
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if closed != 1 {
		t.Fatalf("expected the zero-amount row to become a zombie and close, got %d closed", closed)
	}
	var pos TraderPosition
	if err := st.Position().db.Where("id = ?", rowID).First(&pos).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if pos.Status != "CLOSED" {
		t.Fatalf("row should close as zombie when live qty is zero, got status=%s", pos.Status)
	}
}

// normalizeForReconcileTest mirrors market.Normalize's XYZ-asset handling for
// the reconcile wrapper test without importing the market package.
func normalizeForReconcileTest(symbol string) string {
	switch symbol {
	case "MUUSDT":
		return "xyz:MU"
	case "SKHYNIXUSDT":
		return "xyz:SKHYNIX"
	}
	return symbol
}
