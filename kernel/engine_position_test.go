package kernel

import (
	"testing"

	"nofx/store"
)

func TestValidateDecisionAcceptsDisplayedRoundedPositionLimit(t *testing.T) {
	decision := &Decision{
		Symbol: "XAUUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 20, StopLoss: 4076, TakeProfit: 4106,
	}
	if err := validateDecision(decision, 39.26539201, 3, 1, 0.5); err != nil {
		t.Fatalf("displayed 20 USDT limit must be executable: %v", err)
	}
}

func TestMarginBasedValidationUsesExecutionBudgetInsteadOfFixedPerPositionRatio(t *testing.T) {
	risk := store.RiskControlConfig{
		PositionSizingMode:           "margin_based",
		MaxLeverage:                  50,
		AltcoinMaxMarginRatio:        0.25,
		BTCETHMaxMarginRatio:         0.25,
		AltcoinMaxPositionValueRatio: 2,
		BTCETHMaxPositionValueRatio:  2,
		MinPositionSize:              12,
		MinRiskRewardRatio:           1.5,
	}
	decision := &Decision{
		Symbol:          "SAMSUNGUSDT",
		Action:          "open_short",
		Leverage:        8,
		PositionSizeUSD: 220,
		StopLoss:        200,
		TakeProfit:      160,
		Confidence:      70,
	}

	if err := validateDecisionWithRisk(decision, 94.87, risk); err != nil {
		t.Fatalf("margin-based sizing must not reject a valid decision because of the legacy fixed cap: %v", err)
	}
}

func TestUnifiedMaximumLeverageAppliesToEveryAssetClass(t *testing.T) {
	risk := store.RiskControlConfig{
		PositionSizingMode:           "margin_based",
		MaxLeverage:                  7,
		AltcoinMaxPositionValueRatio: 2,
		BTCETHMaxPositionValueRatio:  2,
		MinPositionSize:              12,
		MinRiskRewardRatio:           1.5,
	}
	decisions := []Decision{
		{
			Symbol:          "BTCUSDT",
			Action:          "open_long",
			Leverage:        20,
			PositionSizeUSD: 100,
			StopLoss:        90,
			TakeProfit:      120,
		},
		{
			Symbol:          "SOLUSDT",
			Action:          "open_short",
			Leverage:        20,
			PositionSizeUSD: 100,
			StopLoss:        120,
			TakeProfit:      90,
		},
	}

	if err := validateDecisionsWithRisk(decisions, 100, risk); err != nil {
		t.Fatalf("validate decisions with unified leverage: %v", err)
	}
	for decisionIndex, decision := range decisions {
		if decision.Leverage != 7 {
			t.Fatalf("decision %d leverage = %d, want unified 7", decisionIndex, decision.Leverage)
		}
	}
}

func TestValidateDecision_CloseRatioValidation(t *testing.T) {
	risk := store.RiskControlConfig{}

	// 1. Omitted close_ratio is valid (defaults to full close)
	dOmitted := &Decision{
		Symbol: "BTCUSDT",
		Action: "close_long",
	}
	if err := validateDecisionWithRisk(dOmitted, 100, risk); err != nil {
		t.Fatalf("omitted close_ratio must be valid: %v", err)
	}

	// 2. Valid ratios: (0.0, 1.0]
	validCases := []float64{0.01, 0.5, 0.99, 1.0}
	for _, ratio := range validCases {
		r := ratio
		d := &Decision{
			Symbol:     "BTCUSDT",
			Action:     "close_long",
			CloseRatio: &r,
		}
		if err := validateDecisionWithRisk(d, 100, risk); err != nil {
			t.Errorf("expected close_ratio %.2f to be valid, got: %v", ratio, err)
		}
	}

	// 3. Explicitly zero, negative, or > 1.0 must be rejected
	invalidCases := []float64{0.0, -0.1, 1.01, 2.0}
	for _, ratio := range invalidCases {
		r := ratio
		d := &Decision{
			Symbol:     "BTCUSDT",
			Action:     "close_short",
			CloseRatio: &r,
		}
		if err := validateDecisionWithRisk(d, 100, risk); err == nil {
			t.Errorf("expected close_ratio %.2f to be invalid, got nil error", ratio)
		}
	}

	// 4. close_ratio on non-close actions must be rejected
	rVal := 0.5
	dOpen := &Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 20,
		StopLoss:        4076,
		TakeProfit:      4106,
		CloseRatio:      &rVal,
	}
	if err := validateDecisionWithRisk(dOpen, 100, risk); err == nil {
		t.Errorf("close_ratio on open_long must be rejected")
	}
}
