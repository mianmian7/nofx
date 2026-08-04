package kernel

import (
	"strings"
	"testing"
)

func TestFormatCurrentPositionsKeepsMarginAndPricePnLExplicit(t *testing.T) {
	ctx := &Context{Positions: []PositionInfo{{
		Symbol:           "BTCUSDT",
		Side:             "long",
		EntryPrice:       100,
		MarkPrice:        99.1,
		Leverage:         20,
		UnrealizedPnLPct: -18,
	}}}

	for name, formatted := range map[string]string{
		"zh": formatCurrentPositionsZH(ctx),
		"en": formatCurrentPositionsEN(ctx),
	} {
		if !strings.Contains(formatted, "Margin/Position PnL -18.00%") {
			t.Errorf("%s formatter omitted margin/position PnL: %s", name, formatted)
		}
		if !strings.Contains(formatted, "Price PnL -0.90%") {
			t.Errorf("%s formatter omitted leverage-adjusted price PnL: %s", name, formatted)
		}
	}
}

func TestFormatCurrentPositionsWarnsNearUnifiedHardStop(t *testing.T) {
	for _, pnlPct := range []float64{-16.1, -15.9} {
		ctx := &Context{Positions: []PositionInfo{{
			Symbol:           "BTCUSDT",
			Side:             "long",
			EntryPrice:       100,
			MarkPrice:        99,
			Leverage:         20,
			UnrealizedPnLPct: pnlPct,
		}}}
		for name, formatted := range map[string]string{
			"zh": formatCurrentPositionsZH(ctx),
			"en": formatCurrentPositionsEN(ctx),
		} {
			warns := strings.Contains(formatted, "approaching")
			if (pnlPct == -16.1) != warns {
				t.Errorf("%s formatter warning for margin/position PnL %.1f = %v, want %v: %s", name, pnlPct, warns, pnlPct == -16.1, formatted)
			}
		}
	}
}

func TestTradingRulesUseUnifiedHardStopBoundary(t *testing.T) {
	maxLoss := TradingRules.RiskManagement["MaxPositionLoss"]
	stopLoss := TradingRules.ExitSignals["StopLoss"]
	want := HardStopMarginPositionPnLPct / 100
	if maxLoss.Value != want || stopLoss.Value != want {
		t.Fatalf("schema hard-stop values = %v and %v, want %.2f", maxLoss.Value, stopLoss.Value, want)
	}
	for name, description := range map[string]string{"max": maxLoss.DescEN, "stop": stopLoss.DescEN} {
		if !strings.Contains(description, "-20%") || !strings.Contains(description, "Margin/Position PnL") {
			t.Fatalf("%s schema description does not expose unified -20%% Margin/Position PnL boundary: %q", name, description)
		}
	}
	marginPnL := DataDictionary["PositionMetrics"]["UnrealizedPnL%"]
	pricePnL := DataDictionary["PositionMetrics"]["PricePnL%"]
	if !strings.Contains(marginPnL.NameEN, "Margin/Position PnL") || !strings.Contains(pricePnL.NameEN, "Price PnL") {
		t.Fatalf("position schema does not separate Margin/Position PnL and Price PnL")
	}
}

func TestTradingRulesUseMarginTakeProfitScaleOutThresholds(t *testing.T) {
	takeProfit := TradingRules.ExitSignals["TakeProfit"]
	if got, ok := takeProfit.Value.(float64); !ok || got != 0.40 {
		t.Fatalf("take-profit rule value = %#v, want 0.40 Margin/Position PnL", takeProfit.Value)
	}
	if !strings.Contains(takeProfit.DescEN, "+40% Margin/Position PnL") || !strings.Contains(takeProfit.DescEN, "absolute price") {
		t.Fatalf("take-profit rule description has ambiguous unit: %q", takeProfit.DescEN)
	}

	scaleOut := TradingRules.PositionControl["ScaleOut"]
	levels, ok := scaleOut.Value.([]map[string]interface{})
	if !ok || len(levels) != 3 {
		t.Fatalf("scale-out levels = %#v, want three Margin/Position PnL levels", scaleOut.Value)
	}
	for i, want := range []float64{0.20, 0.30, 0.40} {
		got, ok := levels[i]["pnl"].(float64)
		if !ok || got != want {
			t.Fatalf("scale-out level %d pnl = %#v, want %.2f Margin/Position PnL", i, levels[i]["pnl"], want)
		}
	}
	if !strings.Contains(scaleOut.DescEN, "Margin/Position PnL") || strings.Contains(scaleOut.DescEN, "+8%") {
		t.Fatalf("scale-out description has stale/ambiguous unit: %q", scaleOut.DescEN)
	}
}
