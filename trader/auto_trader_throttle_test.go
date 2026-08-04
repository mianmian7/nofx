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

func TestTradeThrottleUsesMarginPositionPnLForEarlyRiskExit(t *testing.T) {
	at := &AutoTrader{}
	// A 1% long price loss at 20x is -20% Margin/Position PnL.
	ctx := throttleContext("xyz:INTC", "long", 20*time.Minute, -20.0)

	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0); reason != "" {
		t.Fatalf("margin-PnL hard stop should bypass minimum hold, got %q", reason)
	}
}

func TestTradeThrottleBlocksEarlyNoiseClose(t *testing.T) {
	at := &AutoTrader{}
	// -0.3% Margin/Position PnL is only -0.015% Price PnL at 20x.
	ctx := throttleContext("xyz:INTC", "long", 20*time.Minute, -0.3)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0)
	if !strings.Contains(reason, "min AI-managed hold") {
		t.Fatalf("expected early close to be blocked by min hold, got %q", reason)
	}
	if !strings.Contains(reason, "Margin/Position PnL") || !strings.Contains(reason, "Price PnL") {
		t.Fatalf("expected both PnL units in throttle reason, got %q", reason)
	}
}

func TestTradeThrottleAllowsEarlyHardStop(t *testing.T) {
	at := &AutoTrader{}
	// -20% Margin/Position PnL at 20x is only -1% Price PnL.
	ctx := throttleContext("xyz:INTC", "long", 20*time.Minute, -20.0)

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
	ctx := throttleContext("xyz:INTC", "long", 60*time.Minute, -6.0)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0)
	if reason != "" {
		t.Fatalf("expected confirmed loss after min hold to pass, got %q", reason)
	}
}

func TestTradeThrottleUsesMarginPnLForEarlyTakeProfitBypass(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 20*time.Minute, 40.0)
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0); reason != "" {
		t.Fatalf("+40%% Margin/Position PnL should bypass minimum hold, got %q", reason)
	}

	ctx = throttleContext("xyz:INTC", "long", 20*time.Minute, 12.0)
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "close_long"}, ctx, 0); !strings.Contains(reason, "Margin/Position PnL") {
		t.Fatalf("+12%% margin PnL should not be treated as the migrated +40%% target, got %q", reason)
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

func TestPositionPnLMetricsKeepMarginAndPriceUnitsExplicit(t *testing.T) {
	pos := &kernel.PositionInfo{UnrealizedPnLPct: -18, Leverage: 20}
	if got := positionMarginPnLPct(pos); got != -18 {
		t.Fatalf("margin/position PnL = %.2f, want -18.00", got)
	}
	if got := positionPricePnLPct(pos); got != -0.9 {
		t.Fatalf("price PnL = %.2f, want -0.90", got)
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

	ctx := throttleContext("BTCUSDT", "long", 2*time.Hour, 0.4)
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "close_long"}, ctx, 0); !strings.Contains(reason, "min AI-managed hold") {
		t.Fatalf("strategy profile should enforce its 4h minimum hold, got %q", reason)
	}
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "open_long"}, &kernel.Context{}, 2); !strings.Contains(reason, "2 new position") {
		t.Fatalf("strategy profile should enforce its per-cycle cap, got %q", reason)
	}
}

func TestTradeThrottleLegacyFallbackRemainsIndependent(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("BTCUSDT", "long", 2*time.Hour, 0.4)
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "close_long"}, ctx, 0); reason != "" {
		t.Fatalf("legacy trader should keep the 90m noise window fallback, got %q", reason)
	}
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "BTCUSDT", Action: "open_long"}, &kernel.Context{}, 2); reason != "" {
		t.Fatalf("legacy trader should keep the 6-per-cycle fallback, got %q", reason)
	}
}
