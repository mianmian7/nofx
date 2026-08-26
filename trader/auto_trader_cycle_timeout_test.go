package trader

import (
	"strings"
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
}
