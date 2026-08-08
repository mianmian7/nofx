package trader

import (
	"nofx/kernel"
	"nofx/store"
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
				EntryPrice:       100,
				MarkPrice:        99,
				Leverage:         20,
				UnrealizedPnLPct: pnlPct,
				UpdateTime:       time.Now().Add(-heldFor).UnixMilli(),
			},
		},
	}
}

// Close decisions are owned by the AI: the exchange-side hard stop caps
// downside, so the throttle must never block any close regardless of holding
// time or PnL.
func TestTradeThrottleNeverBlocksCloseDecisions(t *testing.T) {
	at := &AutoTrader{}
	tests := []struct {
		name      string
		heldFor   time.Duration
		pnlPct    float64
		closeSide string
	}{
		{"early small loss", 20 * time.Minute, -0.3, "long"},
		{"early hard stop loss", 20 * time.Minute, -20.0, "long"},
		{"flat inside former noise window", 60 * time.Minute, 0.4, "long"},
		{"confirmed loss after hold", 60 * time.Minute, -6.0, "long"},
		{"big early profit", 20 * time.Minute, 40.0, "long"},
		{"moderate early profit", 20 * time.Minute, 12.0, "long"},
		{"short side any state", 30 * time.Minute, -3.0, "short"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := throttleContext("xyz:INTC", tc.closeSide, tc.heldFor, tc.pnlPct)
			action := "close_long"
			if tc.closeSide == "short" {
				action = "close_short"
			}
			if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: action}, ctx, 0); reason != "" {
				t.Fatalf("close %s %s should never be throttled, got %q", tc.closeSide, tc.name, reason)
			}
		})
	}
}

func TestTradeThrottleAllowsLongShortPairInCycle(t *testing.T) {
	at := &AutoTrader{}
	ctx := &kernel.Context{}

	// One open already queued this cycle (e.g. the long) — the second open
	// (the short) must still be allowed so a directional pair can open.
	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_short"}, ctx, 1)
	if reason != "" {
		t.Fatalf("expected the second (short) open in cycle to be allowed, got %q", reason)
	}
}

func TestTradeThrottleBlocksOpensOverCycleCap(t *testing.T) {
	at := &AutoTrader{}
	ctx := &kernel.Context{}

	// under the 6-per-cycle cap, a further open is allowed
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_long"}, ctx, 5); reason != "" {
		t.Fatalf("expected open within the 6-per-cycle cap to be allowed, got %q", reason)
	}
	// at the cap, the next open is blocked
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_long"}, ctx, 6); !strings.Contains(reason, "6 new position") {
		t.Fatalf("expected open beyond the 6-per-cycle cap to be blocked, got %q", reason)
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

func TestTradeThrottleUsesStrategyScopedProfile(t *testing.T) {
	profile := store.BigMoveTradeThrottleConfig()
	cfg := store.StrategyConfig{
		RiskControl: store.RiskControlConfig{TradeThrottle: &profile},
	}
	at := &AutoTrader{config: AutoTraderConfig{StrategyConfig: &cfg}}

	// The 4h BigMove profile no longer restricts closes.
	ctx := throttleContext("BTCUSDT", "long", 2*time.Hour, 0.4)
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "close_long"}, ctx, 0); reason != "" {
		t.Fatalf("strategy profile should not throttle a close, got %q", reason)
	}
	// The per-cycle open cap is still enforced.
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "open_long"}, &kernel.Context{}, 2); !strings.Contains(reason, "2 new position") {
		t.Fatalf("strategy profile should enforce its per-cycle cap, got %q", reason)
	}
}

func TestTradeThrottleLegacyFallbackKeepsOpenCap(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("BTCUSDT", "long", 2*time.Hour, 0.4)
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "close_long"}, ctx, 0); reason != "" {
		t.Fatalf("legacy trader should not throttle a close, got %q", reason)
	}
	// The 6-per-cycle open cap fallback is still enforced.
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "open_long"}, &kernel.Context{}, 6); !strings.Contains(reason, "6 new position") {
		t.Fatalf("legacy trader should keep the 6-per-cycle fallback, got %q", reason)
	}
}
