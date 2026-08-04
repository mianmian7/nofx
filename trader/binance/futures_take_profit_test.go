package binance

import (
	"math"
	"testing"
)

func TestNormalizeTakeProfitTriggerPriceUsesProtectiveTickDirection(t *testing.T) {
	if got := normalizeTakeProfitTriggerPrice(99.06, 0.1, "LONG"); math.Abs(got-99.0) > 1e-9 {
		t.Fatalf("long target tick rounding = %.8f, want 99.00000000", got)
	}
	if got := normalizeTakeProfitTriggerPrice(100.04, 0.1, "SHORT"); math.Abs(got-100.1) > 1e-9 {
		t.Fatalf("short target tick rounding = %.8f, want 100.10000000", got)
	}
}

func TestNormalizeStopLossTriggerPriceNeverLoosensProtection(t *testing.T) {
	if got := normalizeStopLossTriggerPrice(99.06, 0.1, "LONG"); math.Abs(got-99.1) > 1e-9 {
		t.Fatalf("long stop tick rounding = %.8f, want 99.10000000", got)
	}
	if got := normalizeStopLossTriggerPrice(100.04, 0.1, "SHORT"); math.Abs(got-100.0) > 1e-9 {
		t.Fatalf("short stop tick rounding = %.8f, want 100.00000000", got)
	}
}
