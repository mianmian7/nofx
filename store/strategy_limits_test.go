package store

import "testing"

func TestClampLimitsSupportsFiftyKlinesAndLeverage(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	cfg.Indicators.Klines.PrimaryCount = 50
	cfg.Indicators.Klines.LongerCount = 51
	cfg.RiskControl.BTCETHMaxLeverage = 125
	cfg.RiskControl.AltcoinMaxLeverage = 126

	cfg.ClampLimits()

	if got := cfg.Indicators.Klines.PrimaryCount; got != 50 {
		t.Fatalf("primary kline count = %d, want 50", got)
	}
	if got := cfg.Indicators.Klines.LongerCount; got != 50 {
		t.Fatalf("longer kline count = %d, want 50", got)
	}
	if got := cfg.RiskControl.BTCETHMaxLeverage; got != 125 {
		t.Fatalf("BTC/ETH max leverage = %d, want 125", got)
	}
	if got := cfg.RiskControl.AltcoinMaxLeverage; got != 125 {
		t.Fatalf("altcoin max leverage = %d, want 125", got)
	}
}
