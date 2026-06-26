package kernel

import (
	"strings"
	"testing"

	"nofx/store"
)

func TestBuildSystemPromptUsesVergexClaw402Prompt(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "vergex_signal"
	cfg.CoinSource.VergexLimit = 5
	cfg.PromptSections.RoleDefinition = "# 你是一个专业的 Hyperliquid USDC 多资产交易AI"
	cfg.CustomPrompt = "只做多，不做空。"

	engine := NewStrategyEngine(&cfg)
	prompt := engine.BuildSystemPrompt(30, "balanced")

	if !strings.Contains(prompt, "NOFX Claw402 auto-trader") {
		t.Fatalf("prompt did not use the Claw402/Vergex TradeFi role:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Claw402.ai Signal Ranking") || !strings.Contains(prompt, "Signal Lab") || !strings.Contains(prompt, "Cost/Liquidation Heatmap") {
		t.Fatalf("prompt is missing Claw402/Vergex detail data guidance:\n%s", prompt)
	}
	if !strings.Contains(prompt, "open_short") {
		t.Fatalf("prompt should explicitly allow short entries:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Direction must be data-driven") {
		t.Fatalf("prompt should explain that direction is data-driven, not long-only:\n%s", prompt)
	}
	if !strings.Contains(prompt, "every open position must use exactly 10x") {
		t.Fatalf("prompt should force 10x leverage for Claw402 opens:\n%s", prompt)
	}
	if !strings.Contains(prompt, "use the full max notional per position") {
		t.Fatalf("prompt should force full-size Claw402 opens:\n%s", prompt)
	}
	if containsCJK(prompt) {
		t.Fatalf("system prompt must be English-only, got CJK text:\n%s", prompt)
	}
	legacyPhrases := []string{
		"Hyperliquid USDC 多资产交易AI",
		"只做多",
		"山寨币",
		"BTC/ETH",
		"LONG-ONLY",
		"Do not short",
		"MUST open a long",
	}
	for _, phrase := range legacyPhrases {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("prompt still contains legacy phrase %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptFallsBackToEnglishWhenConfiguredLanguageIsChinese(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT", "ETHUSDT"}
	cfg.CoinSource.VergexLimit = 0
	cfg.CoinSource.VergexMarketType = ""
	cfg.CoinSource.VergexChain = ""
	cfg.PromptSections.RoleDefinition = "# 你是中文系统提示词"
	cfg.PromptSections.TradingFrequency = "# 高频交易\n每分钟交易。"
	cfg.PromptSections.EntryStandards = "# 入场\n随便开仓。"
	cfg.PromptSections.DecisionProcess = "# 决策\n直接输出。"
	cfg.CustomPrompt = "中文偏好不应进入系统提示词。"

	engine := NewStrategyEngine(&cfg)
	prompt := engine.BuildSystemPrompt(30, "balanced")

	required := []string{
		"Data Dictionary & Trading Rules",
		"You are a professional Hyperliquid USDC multi-asset trading AI",
		"Trading Frequency Awareness",
		"Entry Standards",
		"Decision Process",
	}
	for _, phrase := range required {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("English fallback prompt missing %q:\n%s", phrase, prompt)
		}
	}
	if containsCJK(prompt) {
		t.Fatalf("system prompt must be English-only, got CJK text:\n%s", prompt)
	}
}

func TestBuildSystemPromptDoesNotForceLongOnlyForSingleXYZ(t *testing.T) {
	prompt := buildXYZStockCustomPrompt("XYZ:INTC")

	required := []string{
		"DIRECTIONAL, SIGNAL-DRIVEN",
		"You may open long or short",
		"open_short",
	}
	for _, phrase := range required {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("single XYZ prompt missing %q:\n%s", phrase, prompt)
		}
	}

	forbidden := []string{
		"LONG-ONLY",
		"Do not short",
		"MUST open a long",
		"Probing > waiting",
	}
	for _, phrase := range forbidden {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("single XYZ prompt still contains forced-long phrase %q:\n%s", phrase, prompt)
		}
	}
}

func containsCJK(text string) bool {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
