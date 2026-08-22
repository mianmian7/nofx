package kernel

import (
	"errors"
	"strings"
	"testing"
)

func TestFreshCandidateCoverageAllowsIsolatedFailure(t *testing.T) {
	if err := ensureFreshCandidateCoverage(10, 8, []string{"A", "B"}); err != nil {
		t.Fatalf("80%% fresh coverage rejected: %v", err)
	}
}

func TestFreshCandidateCoverageFailsClosedBelowThreshold(t *testing.T) {
	err := ensureFreshCandidateCoverage(10, 7, []string{"A", "B", "C"})
	var unavailable *MarketDataUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want MarketDataUnavailableError", err)
	}
	if len(unavailable.Symbols) != 3 {
		t.Fatalf("failed symbols = %v", unavailable.Symbols)
	}
}

func TestFreshCandidateCoverageIgnoresEmptyUniverse(t *testing.T) {
	if err := ensureFreshCandidateCoverage(0, 0, nil); err != nil {
		t.Fatalf("empty universe rejected: %v", err)
	}
}

func TestDecisionMarketDataFailsClosedForEmptyUniverse(t *testing.T) {
	err := ensureDecisionMarketData(&Context{}, 0, nil)
	var unavailable *MarketDataUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want MarketDataUnavailableError", err)
	}
	if !strings.Contains(err.Error(), "no usable fresh market data") {
		t.Fatalf("error = %v, want explicit empty-market diagnostic", err)
	}
}
