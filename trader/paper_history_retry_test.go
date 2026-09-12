package trader

import (
	"encoding/json"
	"fmt"
	"nofx/kernel"
	"nofx/store"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type retryPaperHistoryRecorder struct {
	mu      sync.Mutex
	fail    bool
	records map[int64]PaperClosedTrade
}

func (r *retryPaperHistoryRecorder) RecordPaperClose(rec PaperClosedTrade, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return fmt.Errorf("temporary history failure")
	}
	if r.records == nil {
		r.records = map[int64]PaperClosedTrade{}
	}
	r.records[rec.ExitOrderID] = rec
	return nil
}

func TestPaperHistoryReplayFailureIsDurableAndRetriesWhileIdle(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := fixedPaperPriceSource{"MUUSDT": 100}
	cfg := PaperBrokerConfig{InitialBalance: 100, TakerFeeBPS: 5}
	b, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 20, Leverage: 2, StopLoss: 90, TakeProfit: 120}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatal(err)
	}
	rec := &retryPaperHistoryRecorder{fail: true}
	b, err = NewPersistentPaperBroker(cfg, src, st.Paper(), "retry", rec)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.Paper().LoadPaperState("retry")
	if err != nil {
		t.Fatal(err)
	}
	var saved paperBrokerState
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingCloseRecords) != 1 {
		t.Fatalf("failed replay was not durable: %s", raw)
	}
	if b.HasActiveExecution() {
		t.Fatal("fixture must be idle after final close")
	}
	rec.mu.Lock()
	rec.fail = false
	rec.mu.Unlock()
	at := &AutoTrader{executionMode: ExecutionModePaper, paperBroker: b, config: AutoTraderConfig{PaperRiskMonitorInterval: 5 * time.Millisecond}, stopMonitorCh: make(chan struct{})}
	at.startPaperRiskMonitor()
	defer func() { close(at.stopMonitorCh); at.monitorWg.Wait() }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		count := len(rec.records)
		rec.mu.Unlock()
		b.mu.RLock()
		pending := len(b.pendingCloseRecords)
		b.mu.RUnlock()
		if count == 1 && pending == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("idle history retry did not deliver and acknowledge the close")
}
