package kernel

import "testing"

func TestValidateDecisionAcceptsPositionProtectionUpdate(t *testing.T) {
	decision := &Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 133.8, NewTakeProfit: 141, Confidence: 82,
	}
	if err := validateDecision(decision, 100, 20, 5, 2); err != nil {
		t.Fatalf("update_position rejected: %v", err)
	}
}

func TestValidateDecisionRejectsEmptyPositionProtectionUpdate(t *testing.T) {
	decision := &Decision{Symbol: "SKHYUSDT", Action: "update_position", Confidence: 82}
	if err := validateDecision(decision, 100, 20, 5, 2); err == nil {
		t.Fatal("empty update_position should be rejected")
	}
}
