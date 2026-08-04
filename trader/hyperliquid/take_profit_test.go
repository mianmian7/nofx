package hyperliquid

import (
	"math"
	"testing"
)

func TestRoundTakeProfitPriceToSigfigsUsesProtectiveDirection(t *testing.T) {
	trader := &HyperliquidTrader{}
	if got := trader.roundTakeProfitPriceToSigfigs(99.0619, "LONG"); math.Abs(got-99.061) > 1e-9 {
		t.Fatalf("long target rounding = %.8f, want 99.06100000", got)
	}
	if got := trader.roundTakeProfitPriceToSigfigs(100.0419, "SHORT"); math.Abs(got-100.05) > 1e-9 {
		t.Fatalf("short target rounding = %.8f, want 100.05000000", got)
	}
}

func TestRoundStopLossPriceToSigfigsNeverLoosensProtection(t *testing.T) {
	trader := &HyperliquidTrader{}
	if got := trader.roundStopLossPriceToSigfigs(99.0619, "LONG"); math.Abs(got-99.062) > 1e-9 {
		t.Fatalf("long stop rounding = %.8f, want 99.06200000", got)
	}
	if got := trader.roundStopLossPriceToSigfigs(100.0419, "SHORT"); math.Abs(got-100.04) > 1e-9 {
		t.Fatalf("short stop rounding = %.8f, want 100.04000000", got)
	}
}
