package trader

import (
	"testing"
	"time"
)

func TestRunWithStartupDelayDoesNotMutateConfiguredDelay(t *testing.T) {
	at := &AutoTrader{
		config:         AutoTraderConfig{StartupDelay: 7 * time.Minute, ScanInterval: time.Minute},
		runStopCh:      make(chan struct{}),
		runAttemptHook: func() {},
	}
	done := make(chan error, 1)
	go func() {
		done <- at.RunWithStartupDelay(time.Hour)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		at.runLifecycleMu.Lock()
		active := at.runActive
		at.runLifecycleMu.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime did not become active")
		}
		time.Sleep(time.Millisecond)
	}
	at.Stop()
	if err := <-done; err != nil {
		t.Fatalf("RunWithStartupDelay returned %v", err)
	}
	if got := at.GetStartupDelay(); got != 7*time.Minute {
		t.Fatalf("configured delay mutated to %v, want 7m", got)
	}
}
