package trader

import (
	"errors"
	"testing"

	"nofx/kernel"
)

type stubLeverageLimitProvider struct {
	max int
	err error
}

func (s stubLeverageLimitProvider) GetMaxLeverage(string) (int, error) {
	return s.max, s.err
}

func TestClampDecisionToExchangeLeverageLimit(t *testing.T) {
	decision := &kernel.Decision{Symbol: "DRAMUSDT", Action: "open_long", Leverage: 50}
	if err := clampDecisionToExchangeLeverageLimit(decision, stubLeverageLimitProvider{max: 20}); err != nil {
		t.Fatalf("clamp failed: %v", err)
	}
	if decision.Leverage != 20 {
		t.Fatalf("decision leverage = %d, want 20", decision.Leverage)
	}
}

func TestClampDecisionToExchangeLeverageLimitFailsClosed(t *testing.T) {
	decision := &kernel.Decision{Symbol: "SNDKUSDT", Action: "open_long", Leverage: 50}
	if err := clampDecisionToExchangeLeverageLimit(decision, stubLeverageLimitProvider{err: errors.New("bracket unavailable")}); err == nil {
		t.Fatal("expected leverage bracket failure")
	}
	if decision.Leverage != 50 {
		t.Fatalf("decision leverage changed on failure: %d", decision.Leverage)
	}
}
