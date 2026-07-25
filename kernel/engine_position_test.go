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
	if err := validateDecision(decision, 39.26539201, 3, 3, 1, 0.5); err != nil {
		t.Fatalf("displayed 20 USDT limit must be executable: %v", err)
	}
}

func TestMarginBasedValidationUsesExecutionBudgetInsteadOfFixedPerPositionRatio(t *testing.T) {
	risk := store.RiskControlConfig{
		PositionSizingMode:           "margin_based",
		AltcoinMaxLeverage:           50,
		BTCETHMaxLeverage:            50,
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
