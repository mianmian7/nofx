package kernel

import (
	"nofx/market"
	"nofx/mcp/provider"
	"nofx/store"
	"testing"
)

func TestExtractJevFeature(t *testing.T) {
	mktData := &market.Data{
		CurrentPrice:  95000,
		CurrentEMA20:  94000,
		CurrentRSI7:   65.0,
		PriceChange1h: 1.2,
		TimeframeData: map[string]*market.TimeframeSeriesData{
			"5m": {
				ATR14:       250,
				RSI14Values: []float64{62.5},
				EMA20Values: []float64{94000},
				EMA50Values: []float64{93000},
				BOLLUpper:   []float64{96000},
				BOLLLower:   []float64{92000},
			},
		},
	}

	pos := &PositionInfo{
		Symbol:           "BTCUSDT",
		Side:             "long",
		UnrealizedPnLPct: 5.5,
	}

	feat := ExtractJevFeature("BTCUSDT", mktData, pos)

	if feat.Symbol != "BTCUSDT" {
		t.Errorf("Symbol = %s, want BTCUSDT", feat.Symbol)
	}
	if feat.CurrentPrice != 95000 {
		t.Errorf("CurrentPrice = %f, want 95000", feat.CurrentPrice)
	}
	if feat.RSI14 != 62.5 {
		t.Errorf("RSI14 = %f, want 62.5", feat.RSI14)
	}
	if feat.ATR14 != 250 {
		t.Errorf("ATR14 = %f, want 250", feat.ATR14)
	}
	if feat.EMATrend != "bullish" {
		t.Errorf("EMATrend = %s, want bullish", feat.EMATrend)
	}
	if !feat.HasPosition || feat.PositionSide != "long" {
		t.Errorf("Position info mismatch: hasPos=%v, side=%s", feat.HasPosition, feat.PositionSide)
	}
}

func TestBuildJevStateAndQuestions(t *testing.T) {
	// Without position
	featNoPos := &JevMarketFeature{
		Symbol:       "ETHUSDT",
		CurrentPrice: 3000,
		HasPosition:  false,
	}
	_, questionsNoPos := BuildJevStateAndQuestions(featNoPos)
	if _, ok := questionsNoPos["entry_signal"]; !ok {
		t.Errorf("Expected entry_signal question for symbol without position")
	}
	if _, ok := questionsNoPos["position_action"]; ok {
		t.Errorf("Did not expect position_action question for symbol without position")
	}

	// With position
	featWithPos := &JevMarketFeature{
		Symbol:       "ETHUSDT",
		CurrentPrice: 3000,
		HasPosition:  true,
		PositionSide: "long",
	}
	_, questionsWithPos := BuildJevStateAndQuestions(featWithPos)
	if _, ok := questionsWithPos["position_action"]; !ok {
		t.Errorf("Expected position_action question for symbol with position")
	}
}

func TestEvaluateJevSignal_OpenLong(t *testing.T) {
	feat := &JevMarketFeature{
		Symbol:       "BTCUSDT",
		CurrentPrice: 90000,
		ATR14:        500,
		HasPosition:  false,
	}

	resp := &provider.JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]provider.JevAnswer{
			"market_regime": {
				Choice:     "strong_trend_up",
				Confidence: 0.90,
			},
			"entry_signal": {
				Choice:     "open_long",
				Confidence: 0.85,
				Probabilities: map[string]float64{
					"open_long":  0.85,
					"open_short": 0.05,
					"wait":       0.10,
				},
			},
		},
	}

	riskConfig := store.RiskControlConfig{MaxLeverage: 10, MinConfidence: 75}
	detConfig := DefaultDeterministicRiskConfig()

	dec, err := EvaluateJevSignal(resp, feat, 10000, 8000, riskConfig, detConfig)
	if err != nil {
		t.Fatalf("EvaluateJevSignal error: %v", err)
	}

	if dec.Action != "open_long" {
		t.Errorf("Action = %s, want open_long", dec.Action)
	}
	if dec.Symbol != "BTCUSDT" {
		t.Errorf("Symbol = %s, want BTCUSDT", dec.Symbol)
	}
	if dec.StopLoss >= dec.TakeProfit || dec.StopLoss >= feat.CurrentPrice {
		t.Errorf("Invalid SL/TP: SL=%f, Price=%f, TP=%f", dec.StopLoss, feat.CurrentPrice, dec.TakeProfit)
	}
}

func TestEvaluateJevSignal_PositionExit(t *testing.T) {
	feat := &JevMarketFeature{
		Symbol:         "BTCUSDT",
		CurrentPrice:   90000,
		HasPosition:    true,
		PositionSide:   "long",
		PositionPnLPct: 10.0,
	}

	resp := &provider.JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]provider.JevAnswer{
			"market_regime": {
				Choice: "strong_trend_up",
			},
			"position_action": {
				Choice:     "exit_profit",
				Confidence: 0.92,
			},
		},
	}

	riskConfig := store.RiskControlConfig{MaxLeverage: 10}
	detConfig := DefaultDeterministicRiskConfig()

	dec, err := EvaluateJevSignal(resp, feat, 10000, 8000, riskConfig, detConfig)
	if err != nil {
		t.Fatalf("EvaluateJevSignal error: %v", err)
	}

	if dec.Action != "close_long" {
		t.Errorf("Action = %s, want close_long", dec.Action)
	}
}
