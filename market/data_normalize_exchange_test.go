package market

import "testing"

func TestNormalizeForExchangePreservesExplicitBinanceTradFiSymbols(t *testing.T) {
	for _, symbol := range []string{"MUUSDT", "SKHYNIXUSDT", "SAMSUNGUSDT", "EWYUSDT", "SNDKUSDT"} {
		if got := NormalizeForExchange("binance", symbol); got != symbol {
			t.Fatalf("NormalizeForExchange(binance, %q) = %q, want unchanged", symbol, got)
		}
	}
}

func TestLegacyNormalizeStillRecognizesHyperliquidXYZ(t *testing.T) {
	if got := Normalize("xyz:MU"); got != "xyz:MU" {
		t.Fatalf("Normalize(xyz:MU) = %q, want xyz:MU", got)
	}
}
