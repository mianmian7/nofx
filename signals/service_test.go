package signals

import (
	"context"
	"fmt"
	"nofx/market"
	"nofx/store"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testProvider struct {
	market.MarketDataProvider
	fail atomic.Bool
}

func (p *testProvider) Exchange() string                      { return "binance" }
func (p *testProvider) Capabilities() market.MarketCapability { return market.CapabilityKlines }
func (p *testProvider) ListPerpetualSymbols(int) ([]string, error) {
	return []string{"BTCUSDT", "ETHUSDT"}, nil
}
func (p *testProvider) GetKlinesFreshContext(ctx context.Context, symbol, tf string, _ int) ([]market.Kline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.fail.Load() && symbol == "BTCUSDT" {
		return nil, fmt.Errorf("unavailable")
	}
	return candles(time.Now(), true)[tf], nil
}

func testService(t *testing.T) (*Service, *testProvider, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := &testProvider{}
	s := New(st.LocalSignal())
	s.provider = func(string) (market.MarketDataProvider, error) { return p, nil }
	return s, p, st
}

func TestServicePersistRestartAndExecutionBoundary(t *testing.T) {
	s, _, st := testService(t)
	if err := s.Scan(context.Background(), "binance"); err != nil {
		t.Fatal(err)
	}
	results, err := s.Candidates("binance", 60, 5)
	if err != nil || len(results) != 2 {
		t.Fatalf("%v %+v", err, results)
	}
	if err := s.ValidateOpen("binance", "BTCUSDT", "open_long", 60, 5); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][3]string{{"binance", "BTCUSDC", "open_long"}, {"okx", "BTCUSDT", "open_long"}, {"binance", "BTCUSDT", "open_short"}} {
		if err := s.ValidateOpen(tc[0], tc[1], tc[2], 60, 5); err == nil {
			t.Fatalf("accepted mismatched order %v", tc)
		}
	}
	if err := s.ValidateOpen("binance", "BTCUSDT", "open_long", 101, 5); err == nil {
		t.Fatal("score gate bypassed")
	}
	copy, _ := s.Snapshot("binance")
	copy.Results[0].Direction = "short"
	if err := s.ValidateOpen("binance", "BTCUSDT", "open_long", 60, 5); err != nil {
		t.Fatal("caller mutated cache")
	}
	if err := s.Scan(context.Background(), "binance"); err != nil {
		t.Fatal(err)
	}
	history, err := s.History("binance", "BTCUSDT", 96)
	if err != nil || len(history) != 1 {
		t.Fatalf("same candle duplicated: %d %v", len(history), err)
	}
	restarted := New(st.LocalSignal())
	view, _ := restarted.Snapshot("binance")
	if view.Ready || len(view.Results) != 2 {
		t.Fatalf("restart reused trade-ready cache: %+v", view)
	}
	if err := restarted.ValidateOpen("binance", "BTCUSDT", "open_long", 60, 5); err == nil {
		t.Fatal("restart opened before refresh")
	}
}

func TestFailedScanCannotResurrectOldSymbols(t *testing.T) {
	s, p, _ := testService(t)
	if err := s.Scan(context.Background(), "binance"); err != nil {
		t.Fatal(err)
	}
	p.fail.Store(true)
	if err := s.Scan(context.Background(), "binance"); err == nil {
		t.Fatal("partial scan below coverage passed")
	}
	view, _ := s.Snapshot("binance")
	if view.Ready || view.Succeeded != 1 || len(view.Failures) != 1 {
		t.Fatalf("bad failure status %+v", view)
	}
	for _, r := range view.Results {
		if r.Symbol == "BTCUSDT" {
			t.Fatal("old result copied into fresh scan")
		}
	}
	if err := s.ValidateOpen("binance", "ETHUSDT", "open_long", 60, 5); err == nil {
		t.Fatal("low coverage allowed trade")
	}
}

func TestExactCandleBoundaryGraceAndRecovery(t *testing.T) {
	s, _, _ := testService(t)
	boundary := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	now := boundary.Add(-time.Millisecond)
	s.now = func() time.Time { return now }
	row, err := Analyze("binance", "BTCUSDT", candles(now, true), now)
	if err != nil {
		t.Fatal(err)
	}
	s.latest["binance"] = &store.LocalSignalScan{Exchange: "binance", ObservedAt: now.UnixMilli(), Expected: 1, Succeeded: 1, Results: []store.LocalSignal{row}}
	s.ready["binance"] = true
	for _, offset := range []time.Duration{-time.Millisecond, 0, 2*time.Minute - time.Millisecond} {
		now = boundary.Add(offset)
		view, _ := s.Snapshot("binance")
		if !view.Ready {
			t.Fatalf("invalid before end of documented grace: %s", offset)
		}
	}
	now = boundary.Add(2 * time.Minute)
	view, _ := s.Snapshot("binance")
	if view.Ready || !view.Stale {
		t.Fatal("old candles remained ready after exact two-minute grace")
	}
	updated, err := Analyze("binance", "BTCUSDT", candles(now, true), now)
	if err != nil {
		t.Fatal(err)
	}
	s.latest["binance"].Results = []store.LocalSignal{updated}
	s.latest["binance"].ObservedAt = now.UnixMilli()
	view, _ = s.Snapshot("binance")
	if !view.Ready {
		t.Fatal("latest 15m/1h/4h closes did not restore readiness")
	}
}

func TestCandleBoundaryMakesScanNotReadyUntilRefresh(t *testing.T) {
	s, _, _ := testService(t)
	if err := s.Scan(context.Background(), "binance"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for i := range s.latest["binance"].Results {
		s.latest["binance"].Results[i].Frames[0].ClosedAt -= (30 * time.Minute).Milliseconds()
	}
	s.mu.Unlock()
	view, err := s.Snapshot("binance")
	if err != nil || view.Ready || !view.Stale {
		t.Fatalf("fresh fetch time hid stale candles: %+v %v", view, err)
	}
	if _, err := s.Candidates("binance", 60, 5); err == nil {
		t.Fatal("stale candles misreported as a healthy no-signal wait")
	}
}

func TestCancelledScanReleasesSlots(t *testing.T) {
	s, _, _ := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Scan(ctx, "binance"); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
	if len(s.slots) != 0 {
		t.Fatal("cancelled scan leaked semaphore")
	}
	if err := s.Scan(context.Background(), "binance"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.latest["binance"].ObservedAt = time.Now().Add(-11 * time.Minute).UnixMilli()
	s.mu.Unlock()
	if _, err := s.Candidates("binance", 60, 5); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("stale scan accepted: %v", err)
	}
}
