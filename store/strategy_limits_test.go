package store

import "testing"

func TestClampLimitsSupportsFiftyKlinesAndLeverage(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	cfg.Indicators.Klines.PrimaryCount = 50
	cfg.Indicators.Klines.LongerCount = 51
	cfg.RiskControl.BTCETHMaxLeverage = 125
	cfg.RiskControl.AltcoinMaxLeverage = 126

	cfg.ClampLimits()

	if got := cfg.Indicators.Klines.PrimaryCount; got != 50 {
		t.Fatalf("primary kline count = %d, want 50", got)
	}
	if got := cfg.Indicators.Klines.LongerCount; got != 50 {
		t.Fatalf("longer kline count = %d, want 50", got)
	}
	if got := cfg.RiskControl.BTCETHMaxLeverage; got != 125 {
		t.Fatalf("BTC/ETH max leverage = %d, want 125", got)
	}
	if got := cfg.RiskControl.AltcoinMaxLeverage; got != 125 {
		t.Fatalf("altcoin max leverage = %d, want 125", got)
	}
}
func TestMarginBasedPositionSizingLimits(t *testing.T) {
	risk := RiskControlConfig{
		PositionSizingMode:           "margin_based",
		AltcoinMaxPositionValueRatio: 1.5,
		AltcoinMaxMarginRatio:        0.15,
		BTCETHMaxPositionValueRatio:  2,
		BTCETHMaxMarginRatio:         0.1,
	}

	if got := risk.MaxPositionNotional(100, 3, false); got != 45 {
		t.Fatalf("altcoin notional cap = %.2f, want 45", got)
	}
	if got := risk.MaxPositionNotional(100, 20, false); got != 300 {
		t.Fatalf("margin-based notional cap = %.2f, want 300", got)
	}
	if got := risk.MaxPositionNotional(100, 5, true); got != 50 {
		t.Fatalf("major notional cap = %.2f, want 50", got)
	}

	risk.PositionSizingMode = "notional_based"
	if got := risk.MaxPositionNotional(100, 3, false); got != 150 {
		t.Fatalf("legacy notional cap = %.2f, want 150", got)
	}
}

func TestClampLimitsSupportsEditableAdvancedBounds(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	cfg.RiskControl.MaxPositions = 99
	cfg.RiskControl.MinConfidence = 0
	cfg.RiskControl.BTCETHMaxMarginRatio = 0
	cfg.RiskControl.AltcoinMaxMarginRatio = 2

	cfg.ClampLimits()

	if got := cfg.RiskControl.MaxPositions; got != 20 {
		t.Fatalf("max positions = %d, want 20", got)
	}
	if got := cfg.RiskControl.MinConfidence; got != 1 {
		t.Fatalf("min confidence = %d, want 1", got)
	}
	if got := cfg.RiskControl.BTCETHMaxMarginRatio; got != 0.01 {
		t.Fatalf("BTC/ETH margin ratio = %.2f, want 0.01", got)
	}
	if got := cfg.RiskControl.AltcoinMaxMarginRatio; got != 1 {
		t.Fatalf("altcoin margin ratio = %.2f, want 1", got)
	}
}

func TestNormalizeProductSchemaKeepsLegacySizingExplicit(t *testing.T) {
	cfg := StrategyConfig{}
	cfg.NormalizeProductSchema()
	if got := cfg.RiskControl.PositionSizingMode; got != "notional_based" {
		t.Fatalf("legacy sizing mode = %q, want notional_based", got)
	}
}
