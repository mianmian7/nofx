package kernel

import "testing"

func TestValidateDecisionAcceptsDisplayedRoundedPositionLimit(t *testing.T) {
	decision := &Decision{
		Symbol: "XAUUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 20, StopLoss: 4076, TakeProfit: 4106,
	}
	if err := validateDecision(decision, 39.26539201, 3, 3, 1, 0.5); err != nil {
		t.Fatalf("displayed 20 USDT limit must be executable: %v", err)
	}
}
