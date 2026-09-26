package kernel

import (
	"nofx/mcp/provider"
	"nofx/store"
	"testing"
	"time"
)

func TestEvaluateJevSignalWithMacro_BullishOnly(t *testing.T) {
	feat := &JevMarketFeature{
		Symbol:       "BTCUSDT",
		CurrentPrice: 90000,
		ATR14:        500,
		HasPosition:  false,
	}

	// 1. Jev proposes open_short
	respShort := &provider.JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]provider.JevAnswer{
			"market_regime": {
				Choice:     "strong_trend_down",
				Confidence: 0.85,
			},
			"entry_signal": {
				Choice:     "open_short",
				Confidence: 0.82,
				Probabilities: map[string]float64{
					"open_short": 0.82,
					"open_long":  0.05,
					"wait":       0.13,
				},
			},
		},
	}

	riskConfig := store.RiskControlConfig{MaxLeverage: 10, MinConfidence: 75}
	detConfig := DefaultDeterministicRiskConfig()

	// Macro is bullish_only (System 2 Grok)
	macroBias := &MacroBias{
		Bias:       "bullish_only",
		Confidence: 90,
		Reasoning:  "4h EMA20>50 golden cross",
		Model:      "grok-4.6",
		ValidUntil: time.Now().Add(30 * time.Minute),
	}

	// Should be VETOED to wait because macro is bullish_only
	dec, err := EvaluateJevSignalWithMacro(respShort, feat, 10000, 8000, riskConfig, detConfig, macroBias)
	if err != nil {
		t.Fatalf("EvaluateJevSignalWithMacro error: %v", err)
	}

	if dec.Action != "wait" {
		t.Errorf("Action = %s, want wait (should be vetoed by bullish macro)", dec.Action)
	}

	// 2. Jev proposes open_long under bullish_only -> Should be APPROVED
	respLong := &provider.JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]provider.JevAnswer{
			"market_regime": {
				Choice:     "strong_trend_up",
				Confidence: 0.88,
			},
			"entry_signal": {
				Choice:     "open_long",
				Confidence: 0.84,
				Probabilities: map[string]float64{
					"open_long":  0.84,
					"open_short": 0.05,
					"wait":       0.11,
				},
			},
		},
	}

	decLong, err := EvaluateJevSignalWithMacro(respLong, feat, 10000, 8000, riskConfig, detConfig, macroBias)
	if err != nil {
		t.Fatalf("EvaluateJevSignalWithMacro long error: %v", err)
	}

	if decLong.Action != "open_long" {
		t.Errorf("Action = %s, want open_long (aligned with macro)", decLong.Action)
	}
}

func TestEvaluateJevSignalWithMacro_RiskOff(t *testing.T) {
	feat := &JevMarketFeature{
		Symbol:       "ETHUSDT",
		CurrentPrice: 3000,
		ATR14:        20,
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
				Confidence: 0.88,
				Probabilities: map[string]float64{
					"open_long": 0.88,
					"wait":      0.12,
				},
			},
		},
	}

	riskConfig := store.RiskControlConfig{MaxLeverage: 10, MinConfidence: 75}
	detConfig := DefaultDeterministicRiskConfig()

	macroBias := &MacroBias{
		Bias:       "risk_off",
		Confidence: 95,
		Reasoning:  "Fed rate decision impending, high danger",
		Model:      "gemini-3.7-flash",
		ValidUntil: time.Now().Add(30 * time.Minute),
	}

	dec, err := EvaluateJevSignalWithMacro(resp, feat, 10000, 8000, riskConfig, detConfig, macroBias)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if dec.Action != "wait" {
		t.Errorf("Action = %s, want wait (blocked by risk_off)", dec.Action)
	}
}
