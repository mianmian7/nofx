package trader

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

func decisionWindowData(observed time.Time) *market.Data {
	data := &market.Data{Symbol: "BTCUSDT", CurrentPrice: 103, ClosedIndicatorsAt: observed, TimeframeData: map[string]*market.TimeframeSeriesData{}}
	for name, duration := range map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour} {
		last := observed.Truncate(duration).Add(-duration)
		data.TimeframeData[name] = &market.TimeframeSeriesData{
			EMA20Values: []float64{100}, EMA50Values: []float64{95},
			Klines: []market.KlineBar{
				{Time: last.Add(-duration).UnixMilli(), Open: 100, High: 104, Low: 98, Close: 101},
				{Time: last.UnixMilli(), Open: 101, High: 104, Low: 99, Close: 102},
			},
		}
	}
	return data
}

func TestPaperDecisionWindowAvoidsPredictablyExpiredEntry(t *testing.T) {
	// Reproduce a scheduler phase four seconds before the close and an eight
	// second model response. The actual executor must still reject stale data;
	// the repair must move data collection, not renew that old evidence.
	start := time.Date(2026, 1, 1, 2, 59, 56, 0, time.UTC)
	wallStart := time.Now()
	at := &AutoTrader{executionMode: ExecutionModePaper, runStopCh: make(chan struct{}), config: AutoTraderConfig{
		CycleTimeout: time.Second, StrategyConfig: &store.StrategyConfig{PaperEntryGuard: true},
	}}
	calls := 0
	at.cycleRunnerWithContext = func(ctx context.Context) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 500*time.Millisecond {
			return fmt.Errorf("window wait consumed the actual decision budget")
		}
		observed := start.Add(time.Since(wallStart))
		data := decisionWindowData(observed)
		decision := &kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", StopLoss: 95}
		return at.attachPaperEntryEvidence(decision, &kernel.Context{MarketDataMap: map[string]*market.Data{"BTCUSDT": data}}, observed.Add(8*time.Second))
	}
	if err := at.runAutomaticCycleAt(start); err != nil {
		t.Fatalf("scheduler sent a predictably expiring setup to the model: %v", err)
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want exactly one", calls)
	}
}

func TestPaperDecisionWindowBoundariesAndOptOut(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := &AutoTrader{executionMode: ExecutionModePaper, config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{PaperEntryGuard: true}}}
	for _, tc := range []struct{ offset, want time.Duration }{
		{0, time.Second}, {time.Second, 0}, {13*time.Minute + 59*time.Second, 0},
		{14 * time.Minute, 61 * time.Second}, {14*time.Minute + 56*time.Second, 5 * time.Second},
		{15 * time.Minute, time.Second}, {24*time.Hour + 14*time.Minute + 56*time.Second, 5 * time.Second},
	} {
		for _, now := range []time.Time{base.Add(tc.offset), base.Add(tc.offset).In(time.FixedZone("local", 8*3600))} {
			if got := at.paperDecisionWindowDelay(now); got != tc.want {
				t.Errorf("delay at %s = %s, want %s", now, got, tc.want)
			}
		}
	}
	for _, tc := range []struct {
		name string
		mode ExecutionMode
		cfg  *store.StrategyConfig
	}{
		{"live", ExecutionModeLive, &store.StrategyConfig{PaperEntryGuard: true}},
		{"legacy", ExecutionModePaper, &store.StrategyConfig{}},
		{"nil", ExecutionModePaper, nil},
		{"grid", ExecutionModePaper, &store.StrategyConfig{StrategyType: "grid_trading", PaperEntryGuard: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := &AutoTrader{executionMode: tc.mode, config: AutoTraderConfig{StrategyConfig: tc.cfg}}
			if got := at.paperDecisionWindowDelay(base.Add(14*time.Minute + 56*time.Second)); got != 0 {
				t.Fatalf("opt-out delayed %s", got)
			}
			calls := 0
			at.cycleRunner = func() error { calls++; return nil }
			if err := at.runAutomaticCycleAt(base.Add(14*time.Minute + 56*time.Second)); err != nil || calls != 1 {
				t.Fatalf("legacy automatic cycle changed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestPaperDecisionWindowStopReleasesSlotWithoutCallingAI(t *testing.T) {
	stop := make(chan struct{})
	at := &AutoTrader{executionMode: ExecutionModePaper, runStopCh: stop, consecutiveAIFailures: 2,
		config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{PaperEntryGuard: true}}}
	var calls atomic.Int32
	at.cycleRunner = func() error { calls.Add(1); return nil }
	done := make(chan error, 1)
	now := time.Date(2026, 1, 1, 0, 14, 56, 0, time.UTC)
	go func() { done <- at.runAutomaticCycleAt(now) }()
	deadline := time.Now().Add(time.Second)
	for !at.cycleInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !at.cycleInFlight.Load() {
		close(stop)
		t.Fatal("scheduler did not reserve the single-flight slot")
	}
	if err := at.runAutomaticCycleAt(now); !errors.Is(err, ErrDecisionCycleInFlight) {
		close(stop)
		t.Fatalf("overlapping cycle admitted: %v", err)
	}
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the scheduling wait")
	}
	if calls.Load() != 0 || at.cycleInFlight.Load() || at.consecutiveAIFailures != 2 {
		t.Fatalf("cancelled wait called AI, retained a slot or changed failure history: calls=%d", calls.Load())
	}
}
