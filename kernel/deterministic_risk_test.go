package kernel

import (
	"nofx/market"
	"nofx/store"
	"testing"
)

func TestExtractATR(t *testing.T) {
	// Test from TimeframeData
	dataWithTF := &market.Data{
		CurrentPrice: 95000,
		TimeframeData: map[string]*market.TimeframeSeriesData{
			"5m": {ATR14: 120.5},
		},
	}
	if atr := ExtractATR(dataWithTF); atr != 120.5 {
		t.Errorf("ExtractATR = %f, want 120.5", atr)
	}

	// Test fallback to 1.5%
	dataFallback := &market.Data{
		CurrentPrice: 10000,
	}
	if atr := ExtractATR(dataFallback); atr != 150.0 {
		t.Errorf("ExtractATR fallback = %f, want 150.0", atr)
	}

	// Test nil
	if atr := ExtractATR(nil); atr != 0 {
		t.Errorf("ExtractATR(nil) = %f, want 0", atr)
	}
}

func TestCalculateATRParams_Long(t *testing.T) {
	currentPrice := 90000.0
	atr := 500.0
	accountEquity := 10000.0
	availableBalance := 8000.0
	riskConfig := store.RiskControlConfig{
		MaxLeverage: 10,
	}
	detConfig := DefaultDeterministicRiskConfig() // 1.8x ATR, 2.0 RRR, 1.5% risk

	dec, err := CalculateATRParams("BTCUSDT", "open_long", currentPrice, atr, accountEquity, availableBalance, riskConfig, detConfig, 80)
	if err != nil {
		t.Fatalf("CalculateATRParams returned error: %v", err)
	}

	if dec.Action != "open_long" {
		t.Errorf("Action = %s, want open_long", dec.Action)
	}
	expectedDist := 500.0 * 1.8 // 900
	expectedSL := currentPrice - expectedDist // 89100
	expectedTP := currentPrice + (expectedDist * 2.0) // 91800

	if dec.StopLoss != expectedSL {
		t.Errorf("StopLoss = %f, want %f", dec.StopLoss, expectedSL)
	}
	if dec.TakeProfit != expectedTP {
		t.Errorf("TakeProfit = %f, want %f", dec.TakeProfit, expectedTP)
	}

	// Invariant check: StopLoss < CurrentPrice < TakeProfit
	if !(dec.StopLoss < currentPrice && currentPrice < dec.TakeProfit) {
		t.Errorf("Invariant violated: SL=%f, Price=%f, TP=%f", dec.StopLoss, currentPrice, dec.TakeProfit)
	}

	// Sizing check
	if dec.PositionSizeUSD <= 0 {
		t.Errorf("PositionSizeUSD = %f, want > 0", dec.PositionSizeUSD)
	}
	if dec.RiskUSD <= 0 {
		t.Errorf("RiskUSD = %f, want > 0", dec.RiskUSD)
	}
}

func TestCalculateATRParams_Short(t *testing.T) {
	currentPrice := 90000.0
	atr := 500.0
	accountEquity := 10000.0
	availableBalance := 8000.0
	riskConfig := store.RiskControlConfig{
		MaxLeverage: 10,
	}
	detConfig := DefaultDeterministicRiskConfig()

	dec, err := CalculateATRParams("BTCUSDT", "open_short", currentPrice, atr, accountEquity, availableBalance, riskConfig, detConfig, 80)
	if err != nil {
		t.Fatalf("CalculateATRParams returned error: %v", err)
	}

	if dec.Action != "open_short" {
		t.Errorf("Action = %s, want open_short", dec.Action)
	}
	expectedDist := 500.0 * 1.8 // 900
	expectedSL := currentPrice + expectedDist // 90900
	expectedTP := currentPrice - (expectedDist * 2.0) // 88200

	if dec.StopLoss != expectedSL {
		t.Errorf("StopLoss = %f, want %f", dec.StopLoss, expectedSL)
	}
	if dec.TakeProfit != expectedTP {
		t.Errorf("TakeProfit = %f, want %f", dec.TakeProfit, expectedTP)
	}

	// Invariant check: TakeProfit < CurrentPrice < StopLoss
	if !(dec.TakeProfit < currentPrice && currentPrice < dec.StopLoss) {
		t.Errorf("Invariant violated: TP=%f, Price=%f, SL=%f", dec.TakeProfit, currentPrice, dec.StopLoss)
	}
}

func TestAntiWhipsawFilter(t *testing.T) {
	// 1. Ranging chop should be vetoed
	approved, reason := AntiWhipsawFilter("ranging_chop", 0.90, 0.05, 60)
	if approved {
		t.Errorf("Expected ranging_chop to be vetoed, got approved")
	}
	if len(reason) == 0 {
		t.Errorf("Expected veto reason")
	}

	// 2. Low confidence should be vetoed
	approved, _ = AntiWhipsawFilter("strong_trend_up", 0.55, 0.20, 60)
	if approved {
		t.Errorf("Expected low confidence to be vetoed, got approved")
	}

	// 3. Low probability separation should be vetoed
	approved, _ = AntiWhipsawFilter("strong_trend_up", 0.65, 0.60, 60)
	if approved {
		t.Errorf("Expected small probability diff (0.05) to be vetoed, got approved")
	}

	// 4. Strong trend with moderate-to-high confidence and clear separation should be approved
	approved, _ = AntiWhipsawFilter("strong_trend_up", 0.68, 0.30, 60)
	if !approved {
		t.Errorf("Expected strong signal to be approved, got vetoed")
	}
}
