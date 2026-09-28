package signals

import (
	"context"
	"nofx/market"
	"nofx/store"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type driftRepository struct{}

func (driftRepository) Latest(string) (*store.LocalSignalScan, error)            { return nil, nil }
func (driftRepository) Save(*store.LocalSignalScan) error                        { return nil }
func (driftRepository) History(string, string, int) ([]store.LocalSignal, error) { return nil, nil }
func (driftRepository) HistoryBefore(string, string, int64, int) ([]store.LocalSignal, error) {
	return nil, nil
}
func (driftRepository) Prune(time.Time) error { return nil }

type driftProvider struct {
	testProvider
	exchange string
	delay    time.Duration
	mu       sync.Mutex
	starts   []time.Time
}

func (p *driftProvider) Exchange() string { return p.exchange }
func (p *driftProvider) ListPerpetualSymbols(int) ([]string, error) {
	p.mu.Lock()
	p.starts = append(p.starts, time.Now())
	p.mu.Unlock()
	return []string{"BTCUSDT", "ETHUSDT"}, nil
}
func (p *driftProvider) GetKlinesFreshContext(ctx context.Context, symbol, tf string, _ int) ([]market.Kline, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(p.delay):
	}
	// A provider may return only completed bars; never let fixture future
	// candles make a scan crossing a boundary appear fresh accidentally.
	return closedBars(candles(time.Now(), true)[tf], tf, time.Now())
}

// Exercise real timers/collect/Fresh across 15m, 1h and 4h boundaries, including
// shared provider slots. No injected observation timestamp can fake freshness.
func TestScanScheduleRefreshesBeforeCandleGraceExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New(driftRepository{})
		s.provider = func(ex string) (market.MarketDataProvider, error) {
			return &driftProvider{exchange: ex, delay: 5 * time.Second}, nil
		}
		s.Start(context.Background())
		defer s.Close()
		origin := time.Now()
		for i := 1; i <= 17; i++ {
			time.Sleep(time.Until(origin.Add(time.Duration(i)*15*time.Minute + 150*time.Second)))
			synctest.Wait()
			for _, ex := range Exchanges {
				view, err := s.Snapshot(ex)
				if err != nil {
					t.Fatal(err)
				}
				if !view.Ready {
					t.Fatalf("%s healthy scanning missed candle-close readiness at %s: stale=%v scanning=%v scan_age=%v error=%q", ex, time.Now(), view.Stale, view.Scanning, time.Since(time.UnixMilli(view.ObservedAt)), view.Error)
				}
				if time.UnixMilli(view.NextScanAt) != nextAlignedScan(ex, time.Now()) {
					t.Fatalf("%s next scan not aligned: %v", ex, view.NextScanAt)
				}
			}
		}
	})
}

func TestScanScheduleRecoversStartupCandleBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(14*time.Minute + 50*time.Second)
		s := New(driftRepository{})
		s.provider = func(ex string) (market.MarketDataProvider, error) {
			return &driftProvider{exchange: ex, delay: 5 * time.Second}, nil
		}
		s.Start(context.Background())
		defer s.Close()
		time.Sleep(2*time.Minute + 40*time.Second)
		synctest.Wait()
		for _, ex := range Exchanges {
			view, _ := s.Snapshot(ex)
			if !view.Ready {
				t.Fatalf("%s did not recover a scan crossing the startup candle boundary: %s", ex, view.Error)
			}
		}
	})
}

func TestNextScanTimeSkipsMissedSlots(t *testing.T) {
	origin := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i, ex := range Exchanges {
		slot := origin.Add(time.Duration(5+i*15) * time.Second)
		if got := nextAlignedScan(ex, origin); got != slot {
			t.Fatalf("%s first slot %v", ex, got)
		}
		if got := nextAlignedScan(ex, slot); got != slot.Add(ScanInterval) {
			t.Fatalf("%s repeated current slot %v", ex, got)
		}
		late := origin.Add(12 * time.Minute)
		if got := nextAlignedScan(ex, late); got != slot.Add(15*time.Minute) {
			t.Fatalf("%s catch-up burst %v", ex, got)
		}
	}
}

func TestSlowScansRemainUnavailableAndDoNotCatchUpInBursts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New(driftRepository{})
		providers := map[string]*driftProvider{}
		for _, ex := range Exchanges {
			providers[ex] = &driftProvider{exchange: ex, delay: 3 * time.Minute}
		}
		s.provider = func(ex string) (market.MarketDataProvider, error) { return providers[ex], nil }
		s.Start(context.Background())
		defer s.Close()
		time.Sleep(17*time.Minute + 45*time.Second)
		synctest.Wait()
		for ex, p := range providers {
			v, _ := s.Snapshot(ex)
			if v.Ready || v.Error == "" {
				t.Fatalf("%s slow provider accepted: %+v", ex, v)
			}
			p.mu.Lock()
			starts := append([]time.Time(nil), p.starts...)
			p.mu.Unlock()
			if len(starts) > 5 {
				t.Fatalf("%s timed-out scans caught up in a burst: %v", ex, starts)
			}
			catchUps := 0
			for i := 1; i < len(starts); i++ {
				// Returning to the next fixed slot after a catch-up can be
				// shorter than five minutes; only off-slot starts are catch-ups.
				if nextAlignedScan(ex, starts[i].Add(-time.Nanosecond)) != starts[i] {
					catchUps++
				}
			}
			if catchUps > 1 {
				t.Fatalf("%s repeated catch-up burst: %v", ex, starts)
			}
		}
	})
}
