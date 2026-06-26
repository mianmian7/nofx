package trader

import (
	"nofx/kernel"
	"strings"
	"testing"
	"time"
)

func throttleContext(symbol, side string, heldFor time.Duration, pnlPct float64) *kernel.Context {
	return &kernel.Context{
		Positions: []kernel.PositionInfo{
			{
				Symbol:           symbol,
				Side:             side,
				UnrealizedPnLPct: pnlPct,
				UpdateTime:       time.Now().Add(-heldFor).UnixMilli(),
			},
		},
	}
}

func TestTradeThrottleBlocksEarlyNoiseClose(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 20*time.Minute, -0.3)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0)
	if !strings.Contains(reason, "min AI-managed hold") {
		t.Fatalf("expected early close to be blocked by min hold, got %q", reason)
	}
}

func TestTradeThrottleAllowsEarlyHardStop(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 20*time.Minute, -3.0)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0)
	if reason != "" {
		t.Fatalf("expected hard stop close to pass, got %q", reason)
	}
}

func TestTradeThrottleBlocksFlatCloseInsideNoiseWindow(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 60*time.Minute, 0.4)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0)
	if !strings.Contains(reason, "noise band") {
		t.Fatalf("expected flat close to be blocked inside noise window, got %q", reason)
	}
}

func TestTradeThrottleAllowsConfirmedLossAfterMinimumHold(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 60*time.Minute, -1.2)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0)
	if reason != "" {
		t.Fatalf("expected confirmed loss after min hold to pass, got %q", reason)
	}
}

func TestTradeThrottleBlocksSecondOpenInCycle(t *testing.T) {
	at := &AutoTrader{}
	ctx := &kernel.Context{}

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_long"}, ctx, 1)
	if !strings.Contains(reason, "only 1 new position") {
		t.Fatalf("expected second open in cycle to be blocked, got %q", reason)
	}
}

func TestTradeThrottleBlocksOpeningAgainstExistingPosition(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 2*time.Hour, 1.0)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_short"}, ctx, 0)
	if !strings.Contains(reason, "already has an open") {
		t.Fatalf("expected opposite open to be blocked when position exists, got %q", reason)
	}
}
