package kernel

import (
	"encoding/json"
	"nofx/store"
	"strings"
	"testing"
)

func TestPaperRuntimePolicyIncludesMigratedCustomRulesAndAccountCaps(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.RiskControl.PaperMaxTradeRiskRatio = .01
	cfg.RiskControl.PaperMaxPortfolioRiskRatio = .02
	cfg.CustomPrompt = "NOFX-PAPER-POLICY-20260912-v2/test: Evaluate both directions. Disabled optional OI is not an entry veto."
	cfg.PromptSections.EntryStandards = "# Entry Standards\nChoose structure and ATR stop before risk sizing."
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded store.StrategyConfig
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	prompt := NewStrategyEngine(&decoded).BuildSystemPrompt(100, "balanced")
	for _, want := range []string{cfg.CustomPrompt, cfg.PromptSections.EntryStandards, "single-position stop-risk cap: 1.00%", "portfolio stop-risk cap: 2.00%", "currently 1.0000 USDT", "NOT a default entry stop", "Legacy positions above this cap block new entries"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("actual system prompt missing %q", want)
		}
	}
	if containsCJK(prompt) {
		t.Fatal("migrated policy violated existing English model contract")
	}
}
