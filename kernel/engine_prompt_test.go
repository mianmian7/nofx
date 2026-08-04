package kernel

import (
	"strings"
	"testing"

	"nofx/store"
)

func TestBuildSystemPromptUsesOrdinaryPromptForUnknownLegacySource(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "legacy_remote_source"
	cfg.PromptSections.RoleDefinition = "# You are a professional Hyperliquid USDC multi-asset trading AI"
	cfg.CustomPrompt = "Long only, no shorts."

	engine := NewStrategyEngine(&cfg)
	prompt := engine.BuildSystemPrompt(30, "balanced")

	for _, phrase := range []string{
		"Data Dictionary & Trading Rules",
		"You are a professional Binance USDⓈ-M multi-asset trading AI",
		"open_short",
		"current available margin",
	} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("ordinary prompt missing %q:\n%s", phrase, prompt)
		}
	}
	if containsCJK(prompt) {
		t.Fatalf("system prompt must be English-only, got CJK text:\n%s", prompt)
	}
	for _, phrase := range []string{
		"paid signal provider",
	} {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("ordinary prompt still contains removed remote phrase %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptFallsBackToEnglishWhenConfiguredLanguageIsChinese(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT", "ETHUSDT"}
	cfg.PromptSections.RoleDefinition = "# You are a Chinese system prompt"
	cfg.PromptSections.TradingFrequency = "# High-frequency trading\nTrade every minute."
	cfg.PromptSections.EntryStandards = "# Entry\nOpen positions freely."
	cfg.PromptSections.DecisionProcess = "# Decision\nOutput directly."
	cfg.CustomPrompt = "Chinese preference should not enter the system prompt."

	engine := NewStrategyEngine(&cfg)
	prompt := engine.BuildSystemPrompt(30, "balanced")

	required := []string{
		"Data Dictionary & Trading Rules",
		"You are a professional Binance USDⓈ-M multi-asset trading AI",
		"Trading Frequency Awareness",
		"Entry Standards",
		"Decision Process",
	}
	for _, phrase := range required {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("English fallback prompt missing %q:\n%s", phrase, prompt)
		}
	}
	if !strings.Contains(prompt, "at least 4h") {
		t.Fatalf("generic prompt should reflect the strategy throttle profile:\n%s", prompt)
	}
	if containsCJK(prompt) {
		t.Fatalf("system prompt must be English-only, got CJK text:\n%s", prompt)
	}
}

func TestMarginBasedPromptUsesAvailableMarginInsteadOfLegacyFixedCap(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"SAMSUNGUSDT"}
	cfg.RiskControl.PositionSizingMode = "margin_based"
	cfg.RiskControl.MaxLeverage = 5
	cfg.RiskControl.MaxMarginUsage = 0.5
	cfg.RiskControl.AltcoinMaxMarginRatio = 0.25
	cfg.RiskControl.AltcoinMaxPositionValueRatio = 2

	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(94.87, "balanced", 195)
	for _, phrase := range []string{
		"dynamic available-margin based",
		"current available margin",
		"selected leverage",
		"Maximum total margin usage: 50%",
		"Do not default to a habitual leverage",
		"Prefer the lowest leverage that fits",
		"will never increase leverage automatically",
		"risk_usd` is a hard maximum loss budget",
	} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("margin-based prompt missing dynamic sizing guidance %q:\n%s", phrase, prompt)
		}
	}
	for _, phrase := range []string{
		"25% of account equity",
		"2.0x account equity",
		"configured per-position margin budget",
	} {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("margin-based prompt still contains stale fixed-cap guidance %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptDoesNotForceLongOnlyForSingleXYZ(t *testing.T) {
	prompt := buildXYZStockCustomPrompt("XYZ:INTC")

	required := []string{
		"DIRECTIONAL, DATA-DRIVEN",
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
		"Signal Lab",
		"Heatmap",
	}
	for _, phrase := range forbidden {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("single XYZ prompt still contains forced-long phrase %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptStatesDirectionalProtectionAndRiskRewardRules(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT", "ETHUSDT"}

	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(100, "balanced", 100)
	for _, phrase := range []string{
		"open_long: stop_loss < entry/current price < take_profit",
		"open_short: take_profit < entry/current price < stop_loss",
		"(take_profit - entry_price) / (entry_price - stop_loss)",
		"(entry_price - take_profit) / (stop_loss - entry_price)",
		"zero protection values are placeholders only",
		"stop-only protection changes must omit `new_take_profit`",
		"a repeated current take-profit is a no-op",
		"backend determines the fee-inclusive breakeven boundary",
	} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("prompt missing directional protection rule %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptUsesUnifiedMarginStopBoundary(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}

	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(100, "balanced", 100)
	prompt += buildXYZStockCustomPrompt("BTCUSDT")
	for _, phrase := range []string{
		"Unified hard stop boundary: -20% Margin/Position PnL",
		"entry × (1 - 0.20 / leverage)",
		"entry × (1 + 0.20 / leverage)",
		"Never send the percentage itself as a price",
		"Fees, slippage, funding, and exchange tick precision",
	} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("prompt missing unified stop rule %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptUsesMarginTakeProfitPrices(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}

	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(100, "balanced", 100)
	for _, phrase := range []string{
		"Take-profit thresholds are gross Margin/Position PnL",
		"long entry × (1 + 0.40 / leverage)",
		"short entry × (1 - 0.40 / leverage)",
		"Price PnL is shown separately",
	} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("prompt missing Margin/Position PnL take-profit rule %q:\n%s", phrase, prompt)
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
