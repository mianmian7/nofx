package signals

import (
	"context"
	"nofx/market"
	"nofx/store"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Keep this test on the real Start -> Scan -> collect -> Snapshot path. Fake
// time makes hours of scan-duration drift reproducible without network or I/O.
type scheduleRepository struct{}

func (scheduleRepository) Latest(string) (*store.LocalSignalScan, error) { return nil, nil }
func (scheduleRepository) Save(*store.LocalSignalScan) error             { return nil }
func (scheduleRepository) History(string, string, int) ([]store.LocalSignal, error) {
	return nil, nil
}
func (scheduleRepository) HistoryBefore(string, string, int64, int) ([]store.LocalSignal, error) {
	return nil, nil
}
func (scheduleRepository) Prune(time.Time) error { return nil }

type scheduleProvider struct {
	market.MarketDataProvider
	exchange string
	delay    time.Duration
	mu       sync.Mutex
	starts   []time.Time
}

func (p *scheduleProvider) Exchange() string { return p.exchange }
func (p *scheduleProvider) Capabilities() market.MarketCapability {
	return market.CapabilityKlines
}
func (p *scheduleProvider) ListPerpetualSymbols(int) ([]string, error) {
	p.mu.Lock()
	p.starts = append(p.starts, time.Now())
	p.mu.Unlock()
	return []string{"BTCUSDT", "ETHUSDT"}, nil
}
func (p *scheduleProvider) GetKlinesFreshContext(ctx context.Context, symbol, tf string, _ int) ([]market.Kline, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(p.delay):
		return candles(time.Now(), true)[tf], nil
	}
}

func TestNextScanDeadline(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, exchange          string
		start, end, want        time.Duration
		catchingUp, wantCatchUp bool
	}{
		{"before phase", "binance", 0, time.Second, 5 * time.Second, false, false},
		{"exact phase", "binance", 5 * time.Second, 20 * time.Second, 305 * time.Second, false, false},
		{"okx phase", "okx", 20 * time.Second, 40 * time.Second, 320 * time.Second, false, false},
		{"bitget phase", "bitget", 35 * time.Second, 50 * time.Second, 335 * time.Second, false, false},
		{"crossed boundary", "binance", 299 * time.Second, 310 * time.Second, 311 * time.Second, false, true},
		{"finished exactly due", "binance", 299 * time.Second, 305 * time.Second, 306 * time.Second, false, true},
		{"missed several slots", "binance", 5 * time.Second, 910 * time.Second, 911 * time.Second, false, true},
		{"slow catchup cannot chain", "binance", 911 * time.Second, 1810 * time.Second, 2105 * time.Second, true, false},
		{"after catchup realigns", "binance", 311 * time.Second, 340 * time.Second, 605 * time.Second, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, catchUp := nextScanDeadline(tc.exchange, base.Add(tc.start), base.Add(tc.end), tc.catchingUp)
			if !got.Equal(base.Add(tc.want)) || catchUp != tc.wantCatchUp {
				t.Fatalf("deadline=%v catchUp=%v; want %v/%v", got.Sub(base), catchUp, tc.want, tc.wantCatchUp)
			}
		})
	}
}

func TestScheduledScansCancelInFlightAndWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := New(scheduleRepository{})
		service.provider = func(exchange string) (market.MarketDataProvider, error) {
			return &scheduleProvider{exchange: exchange, delay: time.Minute}, nil
		}
		service.Start(context.Background())
		time.Sleep(8 * time.Second) // Binance is fetching; the other timers are waiting.
		stopped := time.Now()
		service.Close()
		if !time.Now().Equal(stopped) || len(service.slots) != 0 {
			t.Fatal("shutdown must cancel in-flight requests and release all shared slots immediately")
		}
		for _, exchange := range Exchanges {
			view, _ := service.Snapshot(exchange)
			if view.Scanning || view.Ready {
				t.Fatalf("%s remained scanning/ready after cancellation", exchange)
			}
		}
	})
}

func TestScheduledScansPreserveTimeoutAndFailClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := New(scheduleRepository{})
		service.provider = func(exchange string) (market.MarketDataProvider, error) {
			return &scheduleProvider{exchange: exchange, delay: 45 * time.Second}, nil
		}
		service.Start(context.Background())
		defer service.Close()
		started := time.Now()
		time.Sleep(120*time.Second + 500*time.Millisecond)
		synctest.Wait()
		view, _ := service.Snapshot("binance")
		if view.Scanning || view.Ready || !strings.Contains(view.Error, "timed out") {
			t.Fatalf("slow quotes must retain the two-minute timeout and reject entries: %+v", view)
		}
		if _, err := service.Candidates("binance", 60, 5); err == nil {
			t.Fatal("timed-out scan must not supply opening candidates")
		}
		time.Sleep(121 * time.Second) // The single catch-up scan also times out.
		synctest.Wait()
		view, _ = service.Snapshot("binance")
		if view.Scanning || view.Ready || view.NextScanAt != started.Add(305*time.Second).UnixMilli() {
			t.Fatalf("slow catch-up must return to the fixed schedule without chaining: %+v", view)
		}
	})
}

func TestScheduledScansKeepClosedCandlesReadyAcrossBoundaries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := New(scheduleRepository{})
		providers := make(map[string]*scheduleProvider)
		for _, exchange := range Exchanges {
			providers[exchange] = &scheduleProvider{exchange: exchange, delay: 5 * time.Second}
		}
		service.provider = func(exchange string) (market.MarketDataProvider, error) {
			return providers[exchange], nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer func() { cancel(); service.Close() }()
		started := time.Now()
		service.Start(ctx)
		for cycle := 1; cycle <= 16; cycle++ {
			// Same phase that exposed the production bug: 2m36s after each
			// 15m close. Include hourly and four-hour closes in virtual time.
			at := started.Truncate(4 * time.Hour).Add(time.Duration(cycle)*15*time.Minute + 156*time.Second)
			time.Sleep(time.Until(at))
			synctest.Wait()
			for _, exchange := range Exchanges {
				if _, err := service.Candidates(exchange, 60, 5); err != nil {
					view, _ := service.Snapshot(exchange)
					t.Fatalf("%s unavailable at %s despite healthy quotes: %v (scan=%s next=%s)", exchange, at.Format(time.RFC3339), err, time.UnixMilli(view.ObservedAt).Format(time.RFC3339), time.UnixMilli(view.NextScanAt).Format(time.RFC3339))
				}
			}
		}
	})
}
