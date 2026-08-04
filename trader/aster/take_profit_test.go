package aster

import (
	"math"
	"testing"
)

func TestRoundTakeProfitToTickSizeUsesProtectiveDirection(t *testing.T) {
	if got := roundTakeProfitToTickSize(99.06, 0.1, "LONG"); math.Abs(got-99.0) > 1e-9 {
		t.Fatalf("long target rounding = %.8f, want 99.00000000", got)
	}
	if got := roundTakeProfitToTickSize(100.04, 0.1, "SHORT"); math.Abs(got-100.1) > 1e-9 {
		t.Fatalf("short target rounding = %.8f, want 100.10000000", got)
	}
}

func TestRoundStopLossToTickSizeNeverLoosensProtection(t *testing.T) {
	if got := roundStopLossToTickSize(99.06, 0.1, "LONG"); math.Abs(got-99.1) > 1e-9 {
		t.Fatalf("long stop rounding = %.8f, want 99.10000000", got)
	}
	if got := roundStopLossToTickSize(100.04, 0.1, "SHORT"); math.Abs(got-100.0) > 1e-9 {
		t.Fatalf("short stop rounding = %.8f, want 100.00000000", got)
	}
}
