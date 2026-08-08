package kernel

import (
	"fmt"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

// ============================================================================
// Prompt Building - System Prompt
// ============================================================================

// BuildSystemPrompt builds System Prompt according to strategy configuration.
// availableBalances is optional for preview/backward compatibility; live
// decision generation passes the account's current available balance so the
// margin-based sizing guidance reflects the same budget used at execution.
func (e *StrategyEngine) BuildSystemPrompt(accountEquity float64, variant string, availableBalances ...float64) string {
	var sb strings.Builder
	riskControl := e.config.RiskControl
	availableBalance := accountEquity
	if len(availableBalances) > 0 && availableBalances[0] > 0 {
		availableBalance = availableBalances[0]
	}
	promptSections := e.config.PromptSections
	// System prompts are intentionally English-only. UI copy can be localized,
	// but the model contract should stay language-stable for an international
	// open-source project and for reproducible trading behavior.
	lang := LangEnglish
	zh := false
	singleSymbol, primarySymbol := e.singleSymbolInfo()

	// Configs created in the Chinese-UI era carry legacy stored prompt sections
	// and custom prompts written for a different contract; ignore them wholesale
	// and fall back to the canonical built-in English sections.
	legacyZhConfig := strings.EqualFold(strings.TrimSpace(e.config.Language), "zh")
	if legacyZhConfig {
		promptSections = store.PromptSectionsConfig{}
	}

	// 0. Data Dictionary & Schema (ensure AI understands all fields)
	sb.WriteString(GetSchemaPrompt(lang))
	sb.WriteString("\n\n")
	sb.WriteString("---\n\n")

	// 1. Role definition (editable; falls back to a generic intro in the
	//    correct language so we don't mix EN headings with ZH custom text).
	roleDefinition := englishOnlyPromptSection(promptSections.RoleDefinition)
	if roleDefinition != "" {
		sb.WriteString(roleDefinition)
		sb.WriteString("\n\n")
	} else if zh {
		sb.WriteString(fmt.Sprintf("# You are a professional %s multi-asset trading AI\n\n", e.promptExchangeLabel()))
		sb.WriteString("Your task is to make trading decisions based on the provided market data.\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("# You are a professional %s multi-asset trading AI\n\n", e.promptExchangeLabel()))
		sb.WriteString("Your task is to make trading decisions based on the provided market data.\n\n")
	}

	// 2. Trading mode variant
	writeModeVariant(&sb, variant, zh)

	// 3. Hard constraints (risk control).
	//
	// `singleSymbol` is true for strategies that deliberately trade just one
	// instrument (the quick-create flow, single-asset templates). For those,
	// the "BTC/ETH vs Altcoin" two-tier categorization is irrelevant and
	// actively misleading — we surface a single position-value limit instead.
	btcEthPosValueRatio := riskControl.BTCETHMaxPositionValueRatio
	if btcEthPosValueRatio <= 0 {
		btcEthPosValueRatio = 5.0
	}
	altcoinPosValueRatio := riskControl.AltcoinMaxPositionValueRatio
	if altcoinPosValueRatio <= 0 {
		altcoinPosValueRatio = 1.0
	}

	writeHardConstraints(&sb, accountEquity, availableBalance, riskControl, btcEthPosValueRatio, altcoinPosValueRatio, singleSymbol, primarySymbol, zh)

	// 4. Trading frequency (editable)
	tradingFrequency := englishOnlyPromptSection(promptSections.TradingFrequency)
	if tradingFrequency != "" {
		sb.WriteString(tradingFrequency)
		sb.WriteString("\n\n")
	} else if zh {
		sb.WriteString("# ⏱️ Trading Frequency Awareness\n\n")
		sb.WriteString("- Wait for quality setups instead of trading every cycle.\n")
		sb.WriteString("- Manage existing positions before opening new ones.\n\n")
	} else {
		sb.WriteString("# ⏱️ Trading Frequency Awareness\n\n")
		sb.WriteString("- Wait for quality setups instead of trading every cycle.\n")
		sb.WriteString("- Manage existing positions before opening new ones.\n\n")
	}
	writeTradeThrottleGuidance(&sb, riskControl.EffectiveTradeThrottle())

	// 5. Entry standards (editable)
	entryStandards := englishOnlyPromptSection(promptSections.EntryStandards)
	if entryStandards != "" {
		sb.WriteString(entryStandards)
		if zh {
			sb.WriteString("\n\nYou have the following indicator data:\n")
		} else {
			sb.WriteString("\n\nYou have the following indicator data:\n")
		}
		e.writeAvailableIndicators(&sb, zh)
		if zh {
			sb.WriteString(fmt.Sprintf("\n**Confidence ≥ %d** required to open positions.\n\n", riskControl.MinConfidence))
		} else {
			sb.WriteString(fmt.Sprintf("\n**Confidence ≥ %d** required to open positions.\n\n", riskControl.MinConfidence))
		}
	} else if zh {
		sb.WriteString("# 🎯 Entry Standards (Strict)\n\n")
		sb.WriteString("Only open positions when multiple signals resonate. You have:\n")
		e.writeAvailableIndicators(&sb, zh)
		sb.WriteString(fmt.Sprintf("\nFeel free to use any effective analysis method, but **confidence ≥ %d** is required to open positions; avoid low-quality behaviors such as single-indicator entries, contradictory signals, sideways chop, or re-entering immediately after a close.\n\n", riskControl.MinConfidence))
	} else {
		sb.WriteString("# 🎯 Entry Standards (Strict)\n\n")
		sb.WriteString("Only open positions when multiple signals resonate. You have:\n")
		e.writeAvailableIndicators(&sb, zh)
		sb.WriteString(fmt.Sprintf("\nFeel free to use any effective analysis method, but **confidence ≥ %d** is required to open positions; avoid low-quality behaviors such as single-indicator entries, contradictory signals, sideways chop, or re-entering immediately after a close.\n\n", riskControl.MinConfidence))
	}

	// 6. Decision process (editable)
	decisionProcess := englishOnlyPromptSection(promptSections.DecisionProcess)
	if decisionProcess != "" {
		sb.WriteString(decisionProcess)
		sb.WriteString("\n\n")
	} else if zh {
		sb.WriteString("# 📋 Decision Process\n\n")
		sb.WriteString("1. Check positions → take profit / stop loss?\n")
		sb.WriteString("2. Scan candidates + multi-timeframe → are there strong signals?\n")
		sb.WriteString("3. Write chain of thought first, then output structured JSON\n\n")
	} else {
		sb.WriteString("# 📋 Decision Process\n\n")
		sb.WriteString("1. Check positions → take profit / stop loss?\n")
		sb.WriteString("2. Scan candidates + multi-timeframe → are there strong signals?\n")
		sb.WriteString("3. Write chain of thought first, then output structured JSON\n\n")
	}

	// 7. Output format — schema spec stays in English (this is a parser
	//    contract; reasoning copy is localized below).
	writeOutputFormat(&sb, accountEquity, availableBalance, btcEthPosValueRatio, riskControl, singleSymbol, primarySymbol, zh)

	// 8. Custom Prompt.
	//
	// For single-symbol Hyperliquid XYZ assets (US equities, commodities,
	// forex), we replace any stored CustomPrompt with a built-in English
	// stock-trader template. This serves two purposes:
	//   1. The auto-generated CustomPrompt from the quick-create flow used
	//      to be Chinese (matching UI language), which produced an
	//      incoherent mixed-language final prompt that confused the LLM.
	//   2. It guarantees a stock-specific, US-equity-tuned briefing
	//      regardless of when the strategy was first created.
	customPrompt := englishOnlyPromptSection(e.config.CustomPrompt)
	if legacyZhConfig {
		customPrompt = ""
	}
	if singleSymbol && market.IsXyzDexAsset(primarySymbol) {
		customPrompt = buildXYZStockCustomPrompt(primarySymbol, riskControl.IsMarginBased())
	}

	if customPrompt != "" {
		if zh {
			sb.WriteString("# 📌 Personalized Trading Strategy\n\n")
		} else {
			sb.WriteString("# 📌 Personalized Trading Strategy\n\n")
		}
		sb.WriteString(customPrompt)
		sb.WriteString("\n\n")
		if zh {
			sb.WriteString("Note: the above personalized strategy supplements the basic rules and may not violate the core risk controls.\n")
		} else {
			sb.WriteString("Note: the above personalized strategy supplements the basic rules and may not violate the core risk controls.\n")
		}
	}

	return sb.String()
}

func (e *StrategyEngine) promptExchangeLabel() string {
	if e.Exchange() == "binance" {
		return "Binance USDⓈ-M"
	}
	return strings.ToUpper(e.Exchange())
}

func englishOnlyPromptSection(section string) string {
	trimmed := strings.TrimSpace(section)
	if trimmed == "" {
		return ""
	}
	if detectLanguage(trimmed) == LangChinese {
		return ""
	}
	return trimmed
}

func dynamicNotionalExample(availableBalance float64, leverage int) float64 {
	if availableBalance <= 0 || leverage <= 0 {
		return 0
	}
	// Match the execution-layer affordability estimate closely enough for the
	// model example while leaving the final sizing decision to the backend.
	marginFactor := 1.01/float64(leverage) + 0.001
	return availableBalance / marginFactor * 0.98
}

// buildXYZStockCustomPrompt returns the canonical English directional stock
// briefing for a single-symbol Hyperliquid XYZ perpetual. Symbol is inlined
// for LLM grounding so it never confuses the trading instrument.
func buildXYZStockCustomPrompt(symbol string, marginBased ...bool) string {
	var sb strings.Builder
	dynamicSizing := true
	if len(marginBased) > 0 {
		dynamicSizing = marginBased[0]
	}
	sb.WriteString(fmt.Sprintf("Trade ONLY the Hyperliquid USDC perpetual %s (US equity / xyz board).\n\n", symbol))
	sb.WriteString("Core stance: DIRECTIONAL, DATA-DRIVEN. You may open long or short; never force a trade when local price, volume, and indicator evidence disagree.\n\n")

	sb.WriteString("## Flat-Account Rule\n")
	sb.WriteString("If `Current Positions` is None / empty, evaluate both directions from scratch.\n")
	sb.WriteString("- Use `open_long` only when upside continuation or bullish reversal is confirmed.\n")
	sb.WriteString("- Use `open_short` only when downside continuation or bearish reversal is confirmed.\n")
	sb.WriteString("- Use `wait` when neither side meets the minimum confidence and risk/reward threshold.\n")
	sb.WriteString("- Do not raise confidence just to force an order; confidence must reflect the evidence.\n\n")

	sb.WriteString("## Long Entry Conditions\n")
	sb.WriteString("- Break of the prior session/intraday high on rising volume.\n")
	sb.WriteString("- Pullback to a clearly held intraday support (prior swing low, VWAP, EMA20/50) with a bullish reaction bar.\n")
	sb.WriteString("- Sector tape strength (broad US-equity bid, sympathy with peers in the same theme).\n")
	sb.WriteString("- Confirmed catalyst: earnings beat, guide up, sector rotation, macro tailwind.\n\n")

	sb.WriteString("## Short Entry Conditions\n")
	sb.WriteString("- Breakdown below intraday support or value area with expanding volume.\n")
	sb.WriteString("- Failed breakout, lower high, or bearish rejection at resistance.\n")
	sb.WriteString("- Local price and indicator structure show downside momentum, failed support, or weak demand below.\n")
	sb.WriteString("- Negative catalyst: earnings miss, guide down, sector weakness, macro headwind.\n\n")

	sb.WriteString("## Risk Guardrails (non-negotiable)\n")
	sb.WriteString("- Per-trade stop-loss: target a -20% Margin/Position PnL boundary. Convert it to an actual trigger price using the final leverage: for a long, entry × (1 - 0.20 / leverage); for a short, entry × (1 + 0.20 / leverage). Never send the percentage itself as a price; ALWAYS set a numeric `stop_loss`. Fees, slippage, funding, and exchange tick precision are handled by the execution layer.\n")
	sb.WriteString("- Take-profit: target at least R/R 2:1. With the unified -20% hard stop, a normal target is about +40% Margin/Position PnL; convert it to the absolute trigger price using the final leverage: long entry × (1 + 0.40 / leverage), short entry × (1 - 0.40 / leverage). `take_profit` is always the resulting price, never the percentage.\n")
	if dynamicSizing {
		sb.WriteString("- Position sizing: derive leveraged notional from current available margin and the selected leverage; the backend applies the final fee/overhead ceiling.\n")
	} else {
		sb.WriteString("- Position sizing: stay within the configured legacy notional value limit; the backend enforces that limit.\n")
	}
	sb.WriteString("- Prefer the configured leverage when the setup supports it; never exceed the configured maximum or ignore stop-loss risk.\n")
	sb.WriteString("- Do not flip directly from long to short or short to long in the same cycle. Manage or close the open position first.\n\n")

	sb.WriteString("## Position Management\n")
	sb.WriteString("- Trail stop to breakeven once +1R, take partial profits at +2R if momentum stalls.\n")
	sb.WriteString("- Cut quickly if price breaks the stop or the catalyst thesis fails.\n")
	sb.WriteString("- Holding past 45 minutes is fine; flipping in/out every cycle is not.\n\n")

	sb.WriteString("## Discipline\n")
	sb.WriteString(fmt.Sprintf("- Single-symbol mandate: never rotate into another ticker. The decision JSON `symbol` MUST be exactly \"%s\".\n", symbol))
	sb.WriteString("- Before every decision: check current price vs prior pivot, volume vs 5m/1h average, and the broader US-equity tape.\n")
	sb.WriteString("- If positions are open, prioritize managing them over piling on new ones.")
	return sb.String()
}

// singleSymbolInfo returns (true, "ARM-USDC") for static-coin strategies that
// trade exactly one instrument. Multi-symbol strategies return (false, "").
// The flag is used to drop crypto-specific "BTC/ETH vs Altcoin" labeling and
// to put the actual trading symbol into the JSON example.
func (e *StrategyEngine) singleSymbolInfo() (bool, string) {
	coinSource := e.config.CoinSource
	if coinSource.SourceType == "static" && len(coinSource.StaticCoins) == 1 {
		return true, strings.ToUpper(strings.TrimSpace(coinSource.StaticCoins[0]))
	}
	return false, ""
}

func writeModeVariant(sb *strings.Builder, variant string, zh bool) {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "aggressive":
		if zh {
			sb.WriteString("## Mode: Aggressive\n- Prioritize capturing trend breakouts; may scale in when confidence ≥ 70\n- Allow larger positions, but must strictly set stop-loss and explain the risk-reward ratio\n\n")
		} else {
			sb.WriteString("## Mode: Aggressive\n- Prioritize capturing trend breakouts; may scale in when confidence ≥ 70\n- Allow larger positions, but must strictly set stop-loss and explain the risk-reward ratio\n\n")
		}
	case "conservative":
		if zh {
			sb.WriteString("## Mode: Conservative\n- Open positions only when multiple signals resonate\n- Prioritize capital preservation; pause for multiple periods after consecutive losses\n\n")
		} else {
			sb.WriteString("## Mode: Conservative\n- Open positions only when multiple signals resonate\n- Prioritize capital preservation; pause for multiple periods after consecutive losses\n\n")
		}
	case "scalping":
		if zh {
			sb.WriteString("## Mode: Scalping\n- Focus on short-term momentum, smaller profit targets but require quick action\n- If price doesn't move as expected within two bars, immediately reduce position or stop-loss\n\n")
		} else {
			sb.WriteString("## Mode: Scalping\n- Focus on short-term momentum, smaller profit targets but require quick action\n- If price doesn't move as expected within two bars, immediately reduce position or stop-loss\n\n")
		}
	}
}

func formatThrottleDuration(minutes int) string {
	if minutes%60 == 0 {
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}

func writeTradeThrottleGuidance(sb *strings.Builder, throttle store.TradeThrottleConfig) {
	sb.WriteString("# Trade Throttle (Backend Enforced)\n\n")
	sb.WriteString("- The backend only limits how many new positions can be opened per hour and per decision cycle. It never blocks position closes: you decide when to cut losses or take profit.\n")
	sb.WriteString("- Every PnL figure you see uses Margin/Position PnL (leveraged return on margin). Price PnL is shown separately and is the unlevered price move for context only.\n")
	sb.WriteString(fmt.Sprintf("- Wait %s after closing a symbol before re-entry.\n", formatThrottleDuration(throttle.ReentryCooldownMinutes)))
	sb.WriteString(fmt.Sprintf("- Open no more than %d new positions per hour and %d per decision cycle.\n", throttle.MaxOpensPerHour, throttle.MaxOpensPerCycle))
	sb.WriteString("- Keep stops beyond thesis invalidation and targets far enough to cover fees; do not scalp noise.\n\n")
	sb.WriteString("# Open Position Management\n\n")
	sb.WriteString("- Use `update_position` instead of `hold` when an open position's protection should change. `new_stop_loss` and `new_take_profit` are always absolute exchange trigger prices, never Margin/Position PnL values or percentages. Convert every risk threshold to an actual price using the current leverage before output.\n")
	sb.WriteString("- Protection triggers use mark price. For an existing long position: stop < current mark < take-profit; for an existing short position: take-profit < current mark < stop. Do not substitute last/trade price for current mark.\n")
	sb.WriteString("- When only the stop should move, output `new_stop_loss` only and omit `new_take_profit`. Repeating the current take-profit is treated as no change, not as an extension.\n")
	sb.WriteString("- Once profit reaches +1R, move the stop to breakeven plus fees when market structure permits. At +2R, trail behind a confirmed 15m swing or volatility support/resistance. Never loosen a stop.\n")
	sb.WriteString("- The backend determines the fee-inclusive breakeven boundary from known costs or an explicitly labeled conservative fallback, then applies exchange tick safety. Do not invent or label a stop as breakeven based on an assumed fee rate.\n")
	sb.WriteString("- Protection state is explicit: `present` includes a confirmed absolute price; `confirmed_absent` permits an explicit directionally valid first take-profit; `unavailable` or `ambiguous` must never be guessed. If take-profit is unavailable, a separately valid tightened stop may be applied alone and recorded as degraded.\n")
	sb.WriteString("- For a runaway move: when price has completed most of the path to the existing target and momentum/volume still confirm a breakout, extend the take-profit one step and tighten the stop in the same `update_position` decision.\n")
	sb.WriteString("- Never move a take-profit farther merely to avoid a likely fill. If momentum weakens, keep the existing target and protect profit with the stop.\n")
	sb.WriteString("- If no protection level should change, use `hold`.\n\n")
}

func writeHardConstraints(sb *strings.Builder, accountEquity, availableBalance float64, riskControl store.RiskControlConfig, btcEthPosValueRatio, altcoinPosValueRatio float64, singleSymbol bool, primarySymbol string, zh bool) {
	if zh {
		sb.WriteString("# Hard Constraints (Risk Control)\n\n")
		sb.WriteString("## CODE ENFORCED (backend validation, cannot be bypassed):\n")
		sb.WriteString(fmt.Sprintf("- Max Positions: %d instruments simultaneously\n", riskControl.MaxPositions))
	} else {
		sb.WriteString("# Hard Constraints (Risk Control)\n\n")
		sb.WriteString("## CODE ENFORCED (backend validation, cannot be bypassed):\n")
		sb.WriteString(fmt.Sprintf("- Max Positions: %d instruments simultaneously\n", riskControl.MaxPositions))
	}

	if riskControl.IsMarginBased() {
		sb.WriteString("- Sizing mode: dynamic available-margin based; position_size_usd is the leveraged notional, not the margin amount\n")
		sb.WriteString(fmt.Sprintf("- Dynamic notional ceiling: current available margin × the selected leverage; current available margin example: %.0f USDT\n", availableBalance))
		sb.WriteString("- The backend applies fee/overhead buffers and may reduce the requested notional immediately before execution\n")
	} else if singleSymbol {
		ratio := altcoinPosValueRatio
		if btcEthPosValueRatio > ratio {
			ratio = btcEthPosValueRatio
		}
		sb.WriteString(fmt.Sprintf("- Position Value Limit (%s): max %.0f USDT (= equity %.0f × %.1fx)\n", primarySymbol, accountEquity*ratio, accountEquity, ratio))
	} else {
		sb.WriteString(fmt.Sprintf("- Position Value Limit (Altcoin/Stock): max %.0f USDT (= equity %.0f × %.1fx)\n", accountEquity*altcoinPosValueRatio, accountEquity, altcoinPosValueRatio))
		sb.WriteString(fmt.Sprintf("- Position Value Limit (BTC/ETH): max %.0f USDT (= equity %.0f × %.1fx)\n", accountEquity*btcEthPosValueRatio, accountEquity, btcEthPosValueRatio))
	}
	sb.WriteString(fmt.Sprintf("- Maximum total margin usage: %.0f%% of account equity, including open positions and pending entry orders\n", riskControl.MaxMarginUsage*100))
	if zh {
		sb.WriteString(fmt.Sprintf("- Min Position Size: ≥%.0f USDT\n\n", riskControl.MinPositionSize))
		sb.WriteString("## AI GUIDED (recommended):\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Min Position Size: ≥%.0f USDT\n\n", riskControl.MinPositionSize))
		sb.WriteString("## AI GUIDED (recommended):\n")
	}

	if singleSymbol {
		if zh {
			sb.WriteString(fmt.Sprintf("- Trading Leverage (%s): max %dx\n", primarySymbol, riskControl.MaxLeverage))
		} else {
			sb.WriteString(fmt.Sprintf("- Trading Leverage (%s): max %dx\n", primarySymbol, riskControl.MaxLeverage))
		}
	} else {
		if zh {
			sb.WriteString(fmt.Sprintf("- Trading Leverage: max %dx for every asset\n", riskControl.MaxLeverage))
		} else {
			sb.WriteString(fmt.Sprintf("- Trading Leverage: max %dx for every asset\n", riskControl.MaxLeverage))
		}
	}
	sb.WriteString("- Directional protection (mandatory for opening): open_long: stop_loss < entry/current price < take_profit; open_short: take_profit < entry/current price < stop_loss. If this cannot be satisfied, output wait.\n")
	sb.WriteString("- Unified hard stop boundary: -20% Margin/Position PnL. Convert it to the actual trigger price with the final leverage; do not send the percentage as a price.\n")
	sb.WriteString("- Take-profit thresholds are gross Margin/Position PnL, not prices; realized net PnL still includes fees, funding, and slippage. For a +40% target use long entry × (1 + 0.40 / leverage) or short entry × (1 - 0.40 / leverage), then send only that absolute price to the execution layer.\n")
	sb.WriteString(fmt.Sprintf("- Risk-Reward Ratio: ≥1:%.1f. For longs calculate (take_profit - entry_price) / (entry_price - stop_loss); for shorts calculate (entry_price - take_profit) / (stop_loss - entry_price). Both distances must be positive; never use the raw take_profit / stop_loss price ratio.\n", riskControl.MinRiskRewardRatio))
	if zh {
		sb.WriteString(fmt.Sprintf("- Min Confidence: ≥%d to open position\n\n", riskControl.MinConfidence))
	} else {
		sb.WriteString(fmt.Sprintf("- Min Confidence: ≥%d to open position\n\n", riskControl.MinConfidence))
	}
	sb.WriteString("- Do not default to a habitual leverage such as 5x, 8x, or 10x. Select leverage from this setup's stop distance, requested notional, and remaining margin budget.\n")
	sb.WriteString("- Prefer the lowest leverage that fits the requested notional inside the remaining margin budget while keeping liquidation safely beyond the stop loss; reduce notional when no safe leverage fits.\n")
	sb.WriteString("- The leverage limit is a ceiling, not a target. The backend may reduce notional to satisfy the total-margin limit and will never increase leverage automatically.\n\n")
	sb.WriteString("- `risk_usd` is a hard maximum loss budget to the stop, including a fee allowance; the backend reduces position_size_usd when needed.\n\n")

	// Position sizing guidance
	exampleRatio := btcEthPosValueRatio
	if singleSymbol {
		exampleRatio = altcoinPosValueRatio
		if btcEthPosValueRatio > exampleRatio {
			exampleRatio = btcEthPosValueRatio
		}
	}
	if zh {
		sb.WriteString("## Position Sizing Guidance\n")
		if riskControl.IsMarginBased() {
			sb.WriteString("Calculate position_size_usd from current available margin × the selected leverage. There is no fixed per-position equity-percentage cap in this mode.\n")
			sb.WriteString(fmt.Sprintf("- Current available-margin example: %.0f USDT; use the selected leverage to derive notional, and let the backend apply the final fee/overhead buffer.\n", availableBalance))
		} else {
			sb.WriteString("Calculate position_size_usd from your confidence and the Position Value Limits above:\n")
		}
		if riskControl.IsMarginBased() {
			sb.WriteString("- Choose a notional that fits the current available margin and selected leverage; the backend will reduce it if the account changes before execution.\n")
		} else {
			sb.WriteString("- High confidence (≥85): use 80-100%% of the position value limit\n")
			sb.WriteString("- Medium confidence (70-84): use 50-80%% of the position value limit\n")
			sb.WriteString("- Low confidence (60-69): use 30-50%% of the position value limit\n")
			sb.WriteString(fmt.Sprintf("- Example: equity %.0f × %.1fx = max %.0f USDT\n", accountEquity, exampleRatio, accountEquity*exampleRatio))
		}
		if riskControl.IsMarginBased() {
			sb.WriteString("- Use the current account available margin from the user prompt; do not use the legacy position-value ratio fields for margin-based sizing.\n\n")
		} else {
			sb.WriteString("- Use the configured Position Value Limit for legacy notional-based sizing.\n\n")
		}
	} else {
		sb.WriteString("## Position Sizing Guidance\n")
		if riskControl.IsMarginBased() {
			sb.WriteString("Calculate position_size_usd from current available margin × the selected leverage. There is no fixed per-position equity-percentage cap in this mode.\n")
			sb.WriteString(fmt.Sprintf("- Current available-margin example: %.0f USDT; use the selected leverage to derive notional, and let the backend apply the final fee/overhead buffer.\n", availableBalance))
		} else {
			sb.WriteString("Calculate position_size_usd from your confidence and the Position Value Limits above:\n")
		}
		if riskControl.IsMarginBased() {
			sb.WriteString("- Choose a notional that fits the current available margin and selected leverage; the backend will reduce it if the account changes before execution.\n")
		} else {
			sb.WriteString("- High confidence (≥85): use 80-100%% of the position value limit\n")
			sb.WriteString("- Medium confidence (70-84): use 50-80%% of the position value limit\n")
			sb.WriteString("- Low confidence (60-69): use 30-50%% of the position value limit\n")
			sb.WriteString(fmt.Sprintf("- Example: equity %.0f × %.1fx = max %.0f USDT\n", accountEquity, exampleRatio, accountEquity*exampleRatio))
		}
		if riskControl.IsMarginBased() {
			sb.WriteString("- Use the current account available margin from the user prompt; do not use the legacy position-value ratio fields for margin-based sizing.\n\n")
		} else {
			sb.WriteString("- Use the configured Position Value Limit for legacy notional-based sizing.\n\n")
		}
	}
}

func writeOutputFormat(sb *strings.Builder, accountEquity, availableBalance, btcEthPosValueRatio float64, riskControl store.RiskControlConfig, singleSymbol bool, primarySymbol string, zh bool) {
	// Output format schema MUST stay English/structural; parser depends on it.
	sb.WriteString("# Output Format (Strictly Follow)\n\n")
	if zh {
		sb.WriteString("**Must use XML tags <reasoning> and <decision> to separate chain of thought and decision JSON, avoiding parsing errors**\n\n")
	} else {
		sb.WriteString("**Must use XML tags <reasoning> and <decision> to separate chain of thought and decision JSON, avoiding parsing errors**\n\n")
	}
	sb.WriteString("## Format Requirements\n\n")
	sb.WriteString("<reasoning>\n")
	if zh {
		sb.WriteString("Your chain of thought analysis...\n- Briefly analyze your thinking process\n")
	} else {
		sb.WriteString("Your chain of thought analysis...\n- Briefly analyze your thinking process\n")
	}
	sb.WriteString("</reasoning>\n\n")
	sb.WriteString("<decision>\n")
	if zh {
		sb.WriteString("Step 2: JSON decision array\n\n")
	} else {
		sb.WriteString("Step 2: JSON decision array\n\n")
	}
	sb.WriteString("```json\n[\n")

	// Build a JSON example using the actual trading symbol when the strategy
	// is single-symbol. Falls back to the legacy BTC/ETH two-line example
	// only for multi-symbol strategies that genuinely have BTC/ETH on tap.
	if singleSymbol {
		lev := riskControl.MaxLeverage
		size := dynamicNotionalExample(availableBalance, lev)
		if riskControl.IsMarginBased() {
			// Dynamic available-margin example above.
		} else {
			size = riskControl.MaxPositionNotional(accountEquity, lev, market.IsXyzDexAsset(primarySymbol) || primarySymbol == "BTCUSDT" || primarySymbol == "ETHUSDT")
		}
		sb.WriteString(fmt.Sprintf("  {\"symbol\": \"%s\", \"action\": \"open_long\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 0, \"take_profit\": 0, \"confidence\": 85, \"risk_usd\": 0},\n", primarySymbol, lev, size))
		sb.WriteString(fmt.Sprintf("  {\"symbol\": \"%s\", \"action\": \"wait\", \"confidence\": %d}\n", primarySymbol, max(1, riskControl.MinConfidence-5)))
	} else {
		examplePositionSize := dynamicNotionalExample(availableBalance, riskControl.MaxLeverage)
		if !riskControl.IsMarginBased() {
			examplePositionSize = riskControl.MaxPositionNotional(accountEquity, riskControl.MaxLeverage, true)
		}
		sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300},\n",
			riskControl.MaxLeverage, examplePositionSize))
		sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\"}\n")
	}
	sb.WriteString("]\n```\n")
	sb.WriteString("</decision>\n\n")

	if zh {
		sb.WriteString("## Field Description\n\n")
		sb.WriteString("- `action`: open_long | open_short | close_long | close_short | update_position | hold | wait\n")
		sb.WriteString(fmt.Sprintf("- `confidence`: 0-100 and required for every action. For `wait`, it is the strongest rejected setup's entry confidence and must be below %d, not confidence in waiting.\n", riskControl.MinConfidence))
		sb.WriteString("- Required when opening: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd\n")
		sb.WriteString("- Required for `update_position`: absolute-price `new_stop_loss`, absolute-price `new_take_profit`, or both; include confidence. Never put Margin/Position PnL or a percentage in these fields.\n")
		sb.WriteString("- For `update_position`, output only the fields that change: stop-only protection changes must omit `new_take_profit`; a repeated current take-profit is a no-op.\n")
		sb.WriteString("- Existing-position direction: long has stop < current price < take-profit; short has take-profit < current price < stop. A take-profit extension must include a tightened stop in the same decision.\n")
		sb.WriteString("- Opening protection invariants: open_long requires stop_loss < entry/current price < take_profit; open_short requires take_profit < entry/current price < stop_loss. If invalid, output wait.\n")
		sb.WriteString("- In the single-symbol example, zero protection values are placeholders only; never output zero for an opening decision.\n")
		sb.WriteString("- **IMPORTANT**: all numeric values must be calculated numbers, NOT formulas/expressions (e.g. use `27.76`, not `3000 * 0.01`)\n")
		if singleSymbol {
			sb.WriteString(fmt.Sprintf("- **This strategy trades only %s.** The JSON `symbol` MUST match `%s` exactly — do not write `%s` variants that drop the suffix or add USDT.\n", primarySymbol, primarySymbol, primarySymbol))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("## Field Description\n\n")
		sb.WriteString("- `action`: open_long | open_short | close_long | close_short | update_position | hold | wait\n")
		sb.WriteString(fmt.Sprintf("- `confidence`: 0-100 and required for every action. For `wait`, it is the strongest rejected setup's entry confidence and must be below %d, not confidence in waiting.\n", riskControl.MinConfidence))
		sb.WriteString("- Required when opening: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd\n")
		sb.WriteString("- Required for `update_position`: absolute-price `new_stop_loss`, absolute-price `new_take_profit`, or both; include confidence. Never put Margin/Position PnL or a percentage in these fields.\n")
		sb.WriteString("- For `update_position`, output only the fields that change: stop-only protection changes must omit `new_take_profit`; a repeated current take-profit is a no-op.\n")
		sb.WriteString("- Existing-position direction: long has stop < current price < take-profit; short has take-profit < current price < stop. A take-profit extension must include a tightened stop in the same decision.\n")
		sb.WriteString("- Opening protection invariants: open_long requires stop_loss < entry/current price < take_profit; open_short requires take_profit < entry/current price < stop_loss. If invalid, output wait.\n")
		sb.WriteString("- In the single-symbol example, zero protection values are placeholders only; never output zero for an opening decision.\n")
		sb.WriteString("- **IMPORTANT**: all numeric values must be calculated numbers, NOT formulas/expressions (e.g. use `27.76`, not `3000 * 0.01`)\n")
		if singleSymbol {
			sb.WriteString(fmt.Sprintf("- **This strategy trades only %s.** The JSON `symbol` MUST match `%s` exactly — do not add USDT/USDC suffix variants.\n", primarySymbol, primarySymbol))
		}
		sb.WriteString("\n")
	}
}

func (e *StrategyEngine) writeAvailableIndicators(sb *strings.Builder, zh bool) {
	indicators := e.config.Indicators
	kline := indicators.Klines

	label := func(en, zhStr string) string {
		if zh {
			return zhStr
		}
		return en
	}

	if zh {
		sb.WriteString(fmt.Sprintf("- %s price series", kline.PrimaryTimeframe))
		if kline.EnableMultiTimeframe {
			sb.WriteString(fmt.Sprintf(" + %s K-line series\n", kline.LongerTimeframe))
		} else {
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(fmt.Sprintf("- %s price series", kline.PrimaryTimeframe))
		if kline.EnableMultiTimeframe {
			sb.WriteString(fmt.Sprintf(" + %s K-line series\n", kline.LongerTimeframe))
		} else {
			sb.WriteString("\n")
		}
	}

	if indicators.EnableEMA {
		sb.WriteString("- " + label("EMA indicators", "EMA indicators"))
		if len(indicators.EMAPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (%s: %v)", label("periods", "periods"), indicators.EMAPeriods))
		}
		sb.WriteString("\n")
	}
	if indicators.EnableMACD {
		sb.WriteString("- " + label("MACD indicators", "MACD indicators") + "\n")
	}
	if indicators.EnableRSI {
		sb.WriteString("- " + label("RSI indicators", "RSI indicators"))
		if len(indicators.RSIPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (%s: %v)", label("periods", "periods"), indicators.RSIPeriods))
		}
		sb.WriteString("\n")
	}
	if indicators.EnableATR {
		sb.WriteString("- " + label("ATR indicators", "ATR indicators"))
		if len(indicators.ATRPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (%s: %v)", label("periods", "periods"), indicators.ATRPeriods))
		}
		sb.WriteString("\n")
	}
	if indicators.EnableBOLL {
		sb.WriteString("- " + label("Bollinger Bands (BOLL) - Upper/Middle/Lower bands", "Bollinger Bands (BOLL) - Upper/Middle/Lower bands"))
		if len(indicators.BOLLPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (%s: %v)", label("periods", "periods"), indicators.BOLLPeriods))
		}
		sb.WriteString("\n")
	}
	if indicators.EnableVolume {
		sb.WriteString("- " + label("Volume data", "Volume data") + "\n")
	}
	if indicators.EnableOI {
		sb.WriteString("- " + label("Open Interest (OI) data", "Open Interest (OI) data") + "\n")
	}
	if indicators.EnableFundingRate {
		sb.WriteString("- " + label("Funding rate", "Funding rate") + "\n")
	}
}

// ============================================================================
// Prompt Building - User Prompt
// ============================================================================

// BuildUserPrompt builds User Prompt based on strategy configuration
func (e *StrategyEngine) BuildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// System status. Keep the venue explicit so an AI never mistakes OKX or
	// Bitget data for Binance data when the symbols look identical.
	sb.WriteString(fmt.Sprintf("Exchange: %s | Time: %s | Period: #%d | Runtime: %d minutes\n\n",
		e.Exchange(), ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC market
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}

	// Account information
	sb.WriteString(fmt.Sprintf("Account: Equity %.2f | Balance %.2f (%.1f%%) | PnL %+.2f%% | Margin %.1f%% | Positions %d\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// Recently completed orders (placed before positions to ensure visibility)
	if len(ctx.RecentOrders) > 0 {
		sb.WriteString("## Recent Completed Trades\n")
		for i, order := range ctx.RecentOrders {
			resultStr := "Profit"
			if order.RealizedPnL < 0 {
				resultStr = "Loss"
			}
			sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %.4f Exit %.4f | %s: %+.2f USDT (%+.2f%%) | %s→%s (%s)\n",
				i+1, order.Symbol, order.Side,
				order.EntryPrice, order.ExitPrice,
				resultStr, order.RealizedPnL, order.PnLPct,
				order.EntryTime, order.ExitTime, order.HoldDuration))
		}
		sb.WriteString("\n")
	}

	// Historical trading statistics (helps AI understand past performance)
	if ctx.TradingStats != nil && ctx.TradingStats.TotalTrades > 0 {
		// Get language from strategy config
		lang := e.GetLanguage()

		// Win/Loss ratio
		var winLossRatio float64
		if ctx.TradingStats.AvgLoss > 0 {
			winLossRatio = ctx.TradingStats.AvgWin / ctx.TradingStats.AvgLoss
		}

		if lang == LangChinese {
			sb.WriteString("## Historical Trading Statistics\n")
			sb.WriteString(fmt.Sprintf("Total Trades: %d | Profit Factor: %.2f | Sharpe: %.2f | Win/Loss Ratio: %.2f\n",
				ctx.TradingStats.TotalTrades,
				ctx.TradingStats.ProfitFactor,
				ctx.TradingStats.SharpeRatio,
				winLossRatio))
			sb.WriteString(fmt.Sprintf("Total PnL: %+.2f USDT | Avg Win: +%.2f | Avg Loss: -%.2f | Max Drawdown: %.1f%%\n",
				ctx.TradingStats.TotalPnL,
				ctx.TradingStats.AvgWin,
				ctx.TradingStats.AvgLoss,
				ctx.TradingStats.MaxDrawdownPct))

			// Performance hints based on profit factor, sharpe, and drawdown
			if ctx.TradingStats.ProfitFactor >= 1.5 && ctx.TradingStats.SharpeRatio >= 1 {
				sb.WriteString("Performance: GOOD - maintain current strategy\n")
			} else if ctx.TradingStats.ProfitFactor < 1 {
				sb.WriteString("Performance: NEEDS IMPROVEMENT - improve win/loss ratio, optimize TP/SL\n")
			} else if ctx.TradingStats.MaxDrawdownPct > 30 {
				sb.WriteString("Performance: HIGH RISK - reduce position size, control drawdown\n")
			} else {
				sb.WriteString("Performance: NORMAL - room for optimization\n")
			}
		} else {
			sb.WriteString("## Historical Trading Statistics\n")
			sb.WriteString(fmt.Sprintf("Total Trades: %d | Profit Factor: %.2f | Sharpe: %.2f | Win/Loss Ratio: %.2f\n",
				ctx.TradingStats.TotalTrades,
				ctx.TradingStats.ProfitFactor,
				ctx.TradingStats.SharpeRatio,
				winLossRatio))
			sb.WriteString(fmt.Sprintf("Total PnL: %+.2f USDT | Avg Win: +%.2f | Avg Loss: -%.2f | Max Drawdown: %.1f%%\n",
				ctx.TradingStats.TotalPnL,
				ctx.TradingStats.AvgWin,
				ctx.TradingStats.AvgLoss,
				ctx.TradingStats.MaxDrawdownPct))

			// Performance hints based on profit factor, sharpe, and drawdown
			if ctx.TradingStats.ProfitFactor >= 1.5 && ctx.TradingStats.SharpeRatio >= 1 {
				sb.WriteString("Performance: GOOD - maintain current strategy\n")
			} else if ctx.TradingStats.ProfitFactor < 1 {
				sb.WriteString("Performance: NEEDS IMPROVEMENT - improve win/loss ratio, optimize TP/SL\n")
			} else if ctx.TradingStats.MaxDrawdownPct > 30 {
				sb.WriteString("Performance: HIGH RISK - reduce position size, control drawdown\n")
			} else {
				sb.WriteString("Performance: NORMAL - room for optimization\n")
			}
		}
		sb.WriteString("\n")
	}

	// Position information
	if len(ctx.Positions) > 0 {
		sb.WriteString("## Current Positions\n")
		for i, pos := range ctx.Positions {
			sb.WriteString(e.formatPositionInfo(i+1, pos, ctx))
		}
	} else {
		sb.WriteString("Current Positions: None\n\n")
	}

	// Candidate coins (exclude coins already in positions to avoid duplicate data)
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		// Normalize symbol to handle both "ETH" and "ETHUSDT" formats
		normalizedSymbol := e.normalizeCandidateSymbol(pos.Symbol)
		positionSymbols[normalizedSymbol] = true
	}

	sb.WriteString(fmt.Sprintf("## Candidate Coins (%d coins)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		// Skip if this coin is already a position (data already shown in positions section)
		normalizedCoinSymbol := e.normalizeCandidateSymbol(coin.Symbol)
		if positionSymbols[normalizedCoinSymbol] {
			continue
		}

		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := e.formatCoinSourceTag(coin.Sources)
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(e.formatMarketData(marketData))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	sb.WriteString("---\n\n")
	sb.WriteString("Now please analyze briefly and output the decision JSON.\n")

	return sb.String()
}

func (e *StrategyEngine) formatPositionInfo(index int, pos PositionInfo, ctx *Context) string {
	var sb strings.Builder

	holdingDuration := ""
	if pos.UpdateTime > 0 {
		durationMs := time.Now().UnixMilli() - pos.UpdateTime
		durationMin := durationMs / (1000 * 60)
		if durationMin < 60 {
			holdingDuration = fmt.Sprintf(" | Holding Duration %d min", durationMin)
		} else {
			durationHour := durationMin / 60
			durationMinRemainder := durationMin % 60
			holdingDuration = fmt.Sprintf(" | Holding Duration %dh %dm", durationHour, durationMinRemainder)
		}
	}

	positionValue := pos.Quantity * pos.MarkPrice
	if positionValue < 0 {
		positionValue = -positionValue
	}

	pricePnLPct := formatPricePnLPct(pos)
	sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %.4f Current mark %.4f | Qty %.4f | Position Value %.2f USDT | Margin/Position PnL%+.2f%% | Price PnL%+.2f%% | PnL Amount%+.2f USDT | Peak Margin/Position PnL%.2f%% | Stop-loss %s | Take-profit %s | Leverage %dx | Margin %.0f | Liq Price %.4f%s\n\n",
		index, pos.Symbol, strings.ToUpper(pos.Side),
		pos.EntryPrice, pos.MarkPrice, pos.Quantity, positionValue, pos.UnrealizedPnLPct, pricePnLPct, pos.UnrealizedPnL, pos.PeakPnLPct,
		formatProtectionLevel(pos.StopLoss, pos.StopLossState), formatProtectionLevel(pos.TakeProfit, pos.TakeProfitState), pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))
	if pos.ProtectionWarning != "" {
		sb.WriteString(fmt.Sprintf("   Protection snapshot warning: %s\n", pos.ProtectionWarning))
	}

	if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
		sb.WriteString(e.formatMarketData(marketData))
		sb.WriteString("\n")
	}

	return sb.String()
}

func (e *StrategyEngine) formatCoinSourceTag(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	if len(sources) > 1 {
		return " (Multiple native sources)"
	}

	source := sources[0]
	switch source {
	case "static":
		return " (Manual selection)"
	case "hyper_all":
		return " (Hyperliquid All)"
	case "hyper_main":
		return " (Hyperliquid Top20)"
	}
	if strings.HasPrefix(source, "hyper_rank") {
		return " (Hyperliquid Dynamic Rank)"
	}
	if strings.HasSuffix(source, "_dynamic") {
		return fmt.Sprintf(" (%s dynamic candidates)", strings.TrimSuffix(source, "_dynamic"))
	}
	return ""
}

// ============================================================================
// Market Data Formatting
// ============================================================================

func (e *StrategyEngine) formatMarketData(data *market.Data) string {
	var sb strings.Builder
	indicators := e.config.Indicators
	exchange := data.Exchange
	if exchange == "" {
		exchange = e.Exchange()
	}

	// Clearly label the coin symbol
	sb.WriteString(fmt.Sprintf("=== %s %s Market Data ===\n\n", exchange, data.Symbol))
	sb.WriteString(fmt.Sprintf("current_price = %.4f", data.CurrentPrice))

	if indicators.EnableEMA {
		sb.WriteString(fmt.Sprintf(", current_ema20 = %.3f", data.CurrentEMA20))
	}

	if indicators.EnableMACD {
		sb.WriteString(fmt.Sprintf(", current_macd = %.3f", data.CurrentMACD))
	}

	if indicators.EnableRSI {
		sb.WriteString(fmt.Sprintf(", current_rsi7 = %.3f", data.CurrentRSI7))
	}

	sb.WriteString("\n\n")

	if indicators.EnableOI || indicators.EnableFundingRate {
		sb.WriteString(fmt.Sprintf("Additional data for %s:\n\n", data.Symbol))

		if indicators.EnableOI && data.OpenInterest != nil {
			sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
				data.OpenInterest.Latest, data.OpenInterest.Average))
		}

		if indicators.EnableFundingRate {
			sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))
		}
	}

	if len(data.TimeframeData) > 0 {
		timeframeOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}
		for _, tf := range timeframeOrder {
			if tfData, ok := data.TimeframeData[tf]; ok {
				sb.WriteString(fmt.Sprintf("=== %s Timeframe (oldest → latest) ===\n\n", strings.ToUpper(tf)))
				e.formatTimeframeSeriesData(&sb, tfData, indicators)
			}
		}
	} else {
		// Compatible with old data format
		if data.IntradaySeries != nil {
			klineConfig := indicators.Klines
			sb.WriteString(fmt.Sprintf("Intraday series (%s intervals, oldest → latest):\n\n", klineConfig.PrimaryTimeframe))

			if len(data.IntradaySeries.MidPrices) > 0 {
				sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
			}

			if indicators.EnableEMA && len(data.IntradaySeries.EMA20Values) > 0 {
				sb.WriteString(fmt.Sprintf("EMA indicators (20-period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
			}

			if indicators.EnableMACD && len(data.IntradaySeries.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
			}

			if indicators.EnableRSI {
				if len(data.IntradaySeries.RSI7Values) > 0 {
					sb.WriteString(fmt.Sprintf("RSI indicators (7-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
				}
				if len(data.IntradaySeries.RSI14Values) > 0 {
					sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
				}
			}

			if indicators.EnableVolume && len(data.IntradaySeries.Volume) > 0 {
				sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.IntradaySeries.Volume)))
			}

			if indicators.EnableATR {
				sb.WriteString(fmt.Sprintf("3m ATR (14-period): %.3f\n\n", data.IntradaySeries.ATR14))
			}
		}

		if data.LongerTermContext != nil && indicators.Klines.EnableMultiTimeframe {
			sb.WriteString(fmt.Sprintf("Longer-term context (%s timeframe):\n\n", indicators.Klines.LongerTimeframe))

			if indicators.EnableEMA {
				sb.WriteString(fmt.Sprintf("20-Period EMA: %.3f vs. 50-Period EMA: %.3f\n\n",
					data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))
			}

			if indicators.EnableATR {
				sb.WriteString(fmt.Sprintf("3-Period ATR: %.3f vs. 14-Period ATR: %.3f\n\n",
					data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))
			}

			if indicators.EnableVolume {
				sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
					data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))
			}

			if indicators.EnableMACD && len(data.LongerTermContext.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
			}

			if indicators.EnableRSI && len(data.LongerTermContext.RSI14Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
			}
		}
	}

	return sb.String()
}

func (e *StrategyEngine) formatTimeframeSeriesData(sb *strings.Builder, data *market.TimeframeSeriesData, indicators store.IndicatorConfig) {
	if len(data.Klines) > 0 {
		sb.WriteString("Time(UTC)      Open      High      Low       Close     Volume\n")
		for i, k := range data.Klines {
			t := time.Unix(k.Time/1000, 0).UTC()
			timeStr := t.Format("01-02 15:04")
			marker := ""
			if i == len(data.Klines)-1 {
				marker = "  <- current"
			}
			sb.WriteString(fmt.Sprintf("%-14s %-9.4f %-9.4f %-9.4f %-9.4f %-12.2f%s\n",
				timeStr, k.Open, k.High, k.Low, k.Close, k.Volume, marker))
		}
		sb.WriteString("\n")
	} else if len(data.MidPrices) > 0 {
		sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidPrices)))
		if indicators.EnableVolume && len(data.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.Volume)))
		}
	}

	if indicators.EnableEMA {
		if len(data.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
		}
		if len(data.EMA50Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
		}
	}

	if indicators.EnableMACD && len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
	}

	if indicators.EnableRSI {
		if len(data.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
		}
		if len(data.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
		}
	}

	if indicators.EnableATR && data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %.4f\n", data.ATR14))
	}

	if indicators.EnableBOLL && len(data.BOLLUpper) > 0 {
		sb.WriteString(fmt.Sprintf("BOLL Upper: %s\n", formatFloatSlice(data.BOLLUpper)))
		sb.WriteString(fmt.Sprintf("BOLL Middle: %s\n", formatFloatSlice(data.BOLLMiddle)))
		sb.WriteString(fmt.Sprintf("BOLL Lower: %s\n", formatFloatSlice(data.BOLLLower)))
	}

	sb.WriteString("\n")
}

func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprintf("%.4f", v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}
