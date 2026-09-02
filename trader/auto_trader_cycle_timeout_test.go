package trader

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunAutomaticCycleTimesOutBlockedCycle(t *testing.T) {
	release := make(chan struct{})
	at := &AutoTrader{
		config: AutoTraderConfig{ScanInterval: 10 * time.Millisecond},
		cycleRunner: func() error {
			<-release
			return nil
		},
	}

	result := make(chan error, 1)
	go func() { result <- at.runAutomaticCycle() }()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("runAutomaticCycle error = %v, want timeout", err)
		}
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("runAutomaticCycle remained blocked after the cycle deadline")
	}
	close(release)

	// Verify that a timeout does NOT request stop on the trader
	at.runLifecycleMu.Lock()
	stopRequested := at.runStopRequested
	at.runLifecycleMu.Unlock()
	if stopRequested {
		t.Fatal("runAutomaticCycle requested stop after a single timeout, expected trader to remain running")
	}
}

func TestRunAutomaticCycleConsecutiveTimeoutsTriggerSafeMode(t *testing.T) {
	at := &AutoTrader{
		config: AutoTraderConfig{ScanInterval: 5 * time.Millisecond},
		cycleRunnerWithContext: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	if at.isSafeMode() {
		t.Fatal("expected safe mode to be disabled initially")
	}

	// 1st timeout
	_ = at.runAutomaticCycle()
	if at.isSafeMode() {
		t.Fatal("expected safe mode to remain disabled after 1 timeout")
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}

	// 2nd timeout
	_ = at.runAutomaticCycle()
	if at.isSafeMode() {
		t.Fatal("expected safe mode to remain disabled after 2 timeouts")
	}
	if at.consecutiveAIFailures != 2 {
		t.Fatalf("consecutiveAIFailures = %d, want 2", at.consecutiveAIFailures)
	}

	// 3rd timeout -> should trigger Safe Mode
	_ = at.runAutomaticCycle()
	if !at.isSafeMode() {
		t.Fatal("expected safe mode to be enabled after 3 consecutive timeouts")
	}
	if at.consecutiveAIFailures != 3 {
		t.Fatalf("consecutiveAIFailures = %d, want 3", at.consecutiveAIFailures)
	}
}

func TestRunContinuesAcrossCycleTimeout(t *testing.T) {
	var cycleCount atomic.Int32
	secondCycleReached := make(chan struct{})

	at := &AutoTrader{
		id:   "test-retry-trader",
		name: "Test Retry Trader",
		config: AutoTraderConfig{
			ScanInterval: 10 * time.Millisecond,
		},
		cycleRunnerWithContext: func(ctx context.Context) error {
			current := cycleCount.Add(1)
			if current == 1 {
				// First cycle: block until timeout
				<-ctx.Done()
				return ctx.Err()
			}
			// Second cycle reached successfully
			select {
			case <-secondCycleReached:
			default:
				close(secondCycleReached)
			}
			return nil
		},
	}

	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()

	select {
	case <-secondCycleReached:
		// Succeeded: the loop retried after the first cycle timed out!
	case <-time.After(1 * time.Second):
		t.Fatal("second cycle was not reached after first cycle timeout; loop stopped unexpectedly")
	}

	// Stop trader
	at.Stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned unexpected error on Stop: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not exit after Stop")
	}
}
