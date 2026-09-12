package store

import (
	"encoding/json"
	"testing"
)

// The older web editor rebuilds the known risk fields and omits new ones.
// Its ordinary save must not implicitly disable an already-enabled Paper cap.
func TestMergeStrategyConfigPreservesPaperRiskForLegacyEditor(t *testing.T) {
	base := GetDefaultStrategyConfig("en")
	base.RiskControl.PaperMaxTradeRiskRatio = .01
	base.RiskControl.PaperMaxPortfolioRiskRatio = .02
	for _, legacyTopLevel := range []bool{false, true} {
		raw, err := json.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		var patch map[string]any
		if err = json.Unmarshal(raw, &patch); err != nil {
			t.Fatal(err)
		}
		ai := patch["ai_config"].(map[string]any)
		risk := ai["risk_control"].(map[string]any)
		delete(risk, "paper_max_trade_risk_ratio")
		delete(risk, "paper_max_portfolio_risk_ratio")
		risk["min_confidence"] = 82
		if legacyTopLevel {
			patch = map[string]any{"risk_control": risk}
		}
		got, err := MergeStrategyConfig(base, patch)
		if err != nil {
			t.Fatal(err)
		}
		got.ClampLimits()
		if got.RiskControl.PaperMaxTradeRiskRatio != .01 || got.RiskControl.PaperMaxPortfolioRiskRatio != .02 || got.RiskControl.MinConfidence != 82 {
			t.Fatalf("legacy save dropped Paper risk caps (top-level=%v): %+v", legacyTopLevel, got.RiskControl)
		}
	}
}
