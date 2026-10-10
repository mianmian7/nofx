package kernel

import (
	"strings"
	"testing"

	"nofx/market"
	"nofx/store"
)

func TestGuidedMarketPromptPreservesSmallPrices(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.ExecutionGuidance = true
	cfg.Indicators.EnableEMA = true
	cfg.Indicators.EnableATR = true
	engine := NewStrategyEngine(&cfg)
	series := &market.TimeframeSeriesData{Timeframe: "15m", Klines: []market.KlineBar{{Time: 1800000000000, Open: .00000812, High: .00000815, Low: .00000810, Close: .00000813, Volume: 100}}, EMA20Values: []float64{.00000811}, EMA50Values: []float64{.00000809}, ATR14: .00000003}
	data := &market.Data{Symbol: "TESTUSDT", CurrentPrice: .00000813, CurrentEMA20: .00000811, TimeframeData: map[string]*market.TimeframeSeriesData{"15m": series}}
	text := engine.formatMarketData(data)
	for _, price := range []string{"0.00000813", "0.00000811", "0.00000809", "0.00000003", "0.00000815"} {
		if !strings.Contains(text, price) {
			t.Errorf("lost price %s in prompt: %s", price, text)
		}
	}
	if strings.Contains(text, "current_price = 0.0000,") {
		t.Fatal("small positive quote was rendered as zero")
	}
}
