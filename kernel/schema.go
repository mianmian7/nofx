package kernel

// ============================================================================
// Trading Data Schema
// ============================================================================
// Bilingual data dictionary supporting Chinese and English.
// Ensures AI can fully understand data formats regardless of language.
// ============================================================================

const (
	SchemaVersion = "1.0.0"

	// HardStopMarginPositionPnLPct is the unified hard-stop boundary in
	// Margin/Position PnL percentage. Execution layers convert this percentage
	// to an exchange trigger price using the final effective leverage.
	HardStopMarginPositionPnLPct = -20.0
)

// Language represents the language type
type Language string

const (
	LangChinese Language = "zh-CN"
	LangEnglish Language = "en-US"
)

// ========== Bilingual Field Definitions ==========

// BilingualFieldDef defines a field with bilingual name, formula, and description
type BilingualFieldDef struct {
	NameZH    string // Chinese name
	NameEN    string // English name
	Unit      string // unit of measurement
	FormulaZH string // Chinese formula
	FormulaEN string // English formula
	DescZH    string // Chinese description
	DescEN    string // English description
}

// GetName returns the field name based on language
func (d BilingualFieldDef) GetName(lang Language) string {
	if lang == LangChinese {
		return d.NameZH
	}
	return d.NameEN
}

// GetFormula returns the formula based on language
func (d BilingualFieldDef) GetFormula(lang Language) string {
	if lang == LangChinese {
		return d.FormulaZH
	}
	return d.FormulaEN
}

// GetDesc returns the description based on language
func (d BilingualFieldDef) GetDesc(lang Language) string {
	if lang == LangChinese {
		return d.DescZH
	}
	return d.DescEN
}

// ========== Data Dictionary ==========

// DataDictionary defines the meaning of all fields
var DataDictionary = map[string]map[string]BilingualFieldDef{
	"AccountMetrics": {
		"Equity": {
			NameZH:    "Total Equity",
			NameEN:    "Total Equity",
			Unit:      "USDT",
			FormulaZH: "Available Balance + Unrealized PnL",
			FormulaEN: "Available Balance + Unrealized PnL",
			DescZH:    "Actual net value of the account, including unrealized P&L of all positions",
			DescEN:    "Actual account value including all unrealized P&L from positions",
		},
		"Balance": {
			NameZH:    "Available Balance",
			NameEN:    "Available Balance",
			Unit:      "USDT",
			FormulaZH: "Initial Capital + Realized PnL",
			FormulaEN: "Initial Capital + Realized PnL",
			DescZH:    "Funds available for opening new positions, excluding used margin",
			DescEN:    "Available funds for opening new positions, excluding used margin",
		},
		"PnL": {
			NameZH:    "Total PnL Percentage",
			NameEN:    "Total PnL Percentage",
			Unit:      "%",
			FormulaZH: "(Total Equity - Initial Capital) / Initial Capital × 100",
			FormulaEN: "(Total Equity - Initial Capital) / Initial Capital × 100",
			DescZH:    "Total return since system startup, +15.87% means 15.87% profit",
			DescEN:    "Total return since inception, +15.87% means 15.87% profit",
		},
		"Margin": {
			NameZH:    "Margin Usage Rate",
			NameEN:    "Margin Usage Rate",
			Unit:      "%",
			FormulaZH: "Total Used Margin / Total Equity × 100",
			FormulaEN: "Total Used Margin / Total Equity × 100",
			DescZH:    "数值越高表示占用保证金越多；开仓能力以当前可用保证金、所选杠杆和策略/交易所风控为准",
			DescEN:    "Higher values mean more margin is reserved; opening capacity follows current available margin, selected leverage, and active strategy/exchange controls",
		},
	},

	"TradeMetrics": {
		"Entry": {
			NameZH: "Entry Price",
			NameEN: "Entry Price",
			Unit:   "USDT",
			DescZH: "Average price when opening the position",
			DescEN: "Average price when opening position",
		},
		"Exit": {
			NameZH: "Exit Price",
			NameEN: "Exit Price",
			Unit:   "USDT",
			DescZH: "Average price when closing the position",
			DescEN: "Average price when closing position",
		},
		"Profit": {
			NameZH:    "Realized PnL",
			NameEN:    "Realized PnL",
			Unit:      "USDT",
			FormulaZH: "(Exit Price - Entry Price) / Entry Price × Leverage × Position Value",
			FormulaEN: "(Exit Price - Entry Price) / Entry Price × Leverage × Position Value",
			DescZH:    "Actual P&L of closed trades, including fees. Positive=profit, Negative=loss",
			DescEN:    "Actual profit/loss of closed trades including fees. Positive=profit, Negative=loss",
		},
		"PnL%": {
			NameZH:    "Realized Margin/Position PnL Percentage",
			NameEN:    "Realized Margin/Position PnL Percentage",
			Unit:      "%",
			FormulaZH: "Net Realized PnL / Initial Position Margin × 100",
			FormulaEN: "Net Realized PnL / Initial Position Margin × 100",
			DescZH:    "已平仓交易的保证金/仓位收益率，已包含费用；不是未加杠杆价格变动",
			DescEN:    "Return on a closed position's margin, including fees; not the unlevered price move",
		},
		"HoldDuration": {
			NameZH: "Holding Duration",
			NameEN: "Holding Duration",
			Unit:   "minutes",
			DescZH: "Time from open to close. <15min=scalping, 15min-4h=intraday, >4h=swing",
			DescEN: "Time from open to close. <15min=scalping, 15min-4h=intraday, >4h=swing",
		},
	},

	"PositionMetrics": {
		"UnrealizedPnL%": {
			NameZH:    "Unrealized Margin/Position PnL Percentage",
			NameEN:    "Unrealized Margin/Position PnL Percentage",
			Unit:      "%",
			FormulaZH: "Signed Unrealized PnL / Initial Position Margin × 100",
			FormulaEN: "Signed Unrealized PnL / Initial Position Margin × 100",
			DescZH:    "浮动保证金/仓位 PnL 百分比；不是未加杠杆的价格 PnL，平仓前会持续变化",
			DescEN:    "Floating Margin/Position PnL percentage; not unlevered Price PnL, and it changes until closed",
		},
		"PricePnL%": {
			NameZH:    "Unlevered Price PnL Percentage",
			NameEN:    "Unlevered Price PnL Percentage",
			Unit:      "%",
			FormulaZH: "(Current Price - Entry Price) / Entry Price × 100",
			FormulaEN: "(Current Price - Entry Price) / Entry Price × 100",
			DescZH:    "仅用于展示的未加杠杆价格变动，不用于 Margin/Position PnL 风控门槛",
			DescEN:    "Display-only unlevered price move; never use it for Margin/Position PnL risk gates",
		},
		"PeakPnL%": {
			NameZH: "Peak Margin/Position PnL Percentage",
			NameEN: "Peak Margin/Position PnL Percentage",
			Unit:   "%",
			DescZH: "该仓位达到过的最高未实现 Margin/Position PnL，用于判断是否止盈",
			DescEN: "Historical max unrealized Margin/Position PnL for this position. Used for take-profit decisions",
		},
		"Drawdown": {
			NameZH:    "Drawdown from Peak",
			NameEN:    "Drawdown from Peak",
			Unit:      "%",
			FormulaZH: "Current Margin/Position PnL% - Peak Margin/Position PnL%",
			FormulaEN: "Current Margin/Position PnL% - Peak Margin/Position PnL%",
			DescZH:    "Negative value means pulling back. E.g., Peak Margin/Position PnL +50%, Current +35%, Drawdown = -15 percentage points",
			DescEN:    "Negative = pulling back. E.g., Peak Margin/Position PnL +50%, Current +35%, Drawdown = -15 percentage points",
		},
		"Leverage": {
			NameZH: "Leverage",
			NameEN: "Leverage",
			Unit:   "x",
			DescZH: "3x means a 1% price move = 3% position PnL. Higher leverage = higher risk",
			DescEN: "3x means 1% price move = 3% position PnL. Higher leverage = higher risk",
		},
		"Margin": {
			NameZH:    "Margin Used",
			NameEN:    "Margin Used",
			Unit:      "USDT",
			FormulaZH: "Position Value / Leverage",
			FormulaEN: "Position Value / Leverage",
			DescZH:    "Amount of margin locked for this position",
			DescEN:    "Collateral locked for this position",
		},
		"LiqPrice": {
			NameZH: "Liquidation Price",
			NameEN: "Liquidation Price",
			Unit:   "USDT",
			DescZH: "Position will be force-closed when price reaches this value. 0.0000 = no liquidation risk",
			DescEN: "Price at which position will be force-closed. 0.0000 = no liquidation risk",
		},
	},

	"MarketData": {
		"Volume": {
			NameZH: "Volume",
			NameEN: "Volume",
			Unit:   "base asset",
			DescZH: "Trading volume in this period",
			DescEN: "Trading volume in this period",
		},
		"OI": {
			NameZH: "Open Interest",
			NameEN: "Open Interest",
			Unit:   "USDT",
			DescZH: "Total value of open contracts. Increasing OI = capital inflow, decreasing = outflow",
			DescEN: "Total value of open contracts. Increasing OI = capital inflow, decreasing = outflow",
		},
		"OIChange": {
			NameZH: "OI Change",
			NameEN: "OI Change",
			Unit:   "USDT & %",
			DescZH: "OI change within 1 hour. Used to judge the real market capital flow direction",
			DescEN: "OI change in 1 hour. Used to determine real capital flow direction",
		},
	},
}

// ========== Bilingual Rule Definitions ==========

// BilingualRuleDef defines a trading rule with bilingual description and reason
type BilingualRuleDef struct {
	Value    interface{} // rule value
	DescZH   string      // Chinese description
	DescEN   string      // English description
	ReasonZH string      // Chinese reason
	ReasonEN string      // English reason
}

// GetDesc returns the description based on language
func (d BilingualRuleDef) GetDesc(lang Language) string {
	if lang == LangChinese {
		return d.DescZH
	}
	return d.DescEN
}

// GetReason returns the reason based on language
func (d BilingualRuleDef) GetReason(lang Language) string {
	if lang == LangChinese {
		return d.ReasonZH
	}
	return d.ReasonEN
}

// ========== Trading Rules ==========

// TradingRules defines the trading rules
var TradingRules = struct {
	RiskManagement  map[string]BilingualRuleDef
	EntrySignals    map[string]BilingualRuleDef
	ExitSignals     map[string]BilingualRuleDef
	PositionControl map[string]BilingualRuleDef
}{
	RiskManagement: map[string]BilingualRuleDef{
		"MaxMarginUsage": {
			Value:    "dynamic",
			DescZH:   "开仓规模按当前可用保证金、实际杠杆和生效的策略/交易所风控动态计算",
			DescEN:   "Opening size is calculated dynamically from current available margin, selected leverage, and active strategy/exchange controls",
			ReasonZH: "避免用过时的固定百分比覆盖真实账户可用保证金",
			ReasonEN: "Avoid overriding the account's real available margin with a stale fixed percentage",
		},
		"MaxPositionLoss": {
			Value:    HardStopMarginPositionPnLPct / 100,
			DescZH:   "规则值为比例 -0.20（即 -20%）时，单仓位 Margin/Position PnL 必须止损",
			DescEN:   "Rule value is the fraction -0.20 (that is -20%): stop a single position at -20% Margin/Position PnL",
			ReasonZH: "Prevent excessive loss from single trade",
			ReasonEN: "Prevent excessive loss from single trade",
		},
		"MaxDailyLoss": {
			Value:    -0.10,
			DescZH:   "Stop trading when daily loss reaches -10%",
			DescEN:   "Stop trading when daily loss reaches -10%",
			ReasonZH: "Prevent emotional trading leading to consecutive losses",
			ReasonEN: "Prevent emotional trading leading to consecutive losses",
		},
		"PositionSizeLimit": {
			Value:    0.15,
			DescZH:   "Single position must not exceed 15% of total equity",
			DescEN:   "Single position must not exceed 15% of total equity",
			ReasonZH: "Avoid excessive risk concentration",
			ReasonEN: "Avoid excessive risk concentration",
		},
	},

	EntrySignals: map[string]BilingualRuleDef{
		"VolumeSpike": {
			Value:    2.0,
			DescZH:   "Consider entry when volume is 2x above average",
			DescEN:   "Consider entry when volume is 2x above average",
			ReasonZH: "Volume breakout usually indicates strong trend",
			ReasonEN: "Volume breakout usually indicates strong trend",
		},
		"OIChangeThreshold": {
			Value:    0.02,
			DescZH:   "OI change >2% in 1 hour is considered significant",
			DescEN:   "OI change >2% in 1 hour is considered significant",
			ReasonZH: "Large capital flows cause significant OI changes",
			ReasonEN: "Large capital flows cause significant OI changes",
		},
	},

	ExitSignals: map[string]BilingualRuleDef{
		"TrailingStop": {
			Value:    0.30,
			DescZH:   "Close position when PnL pulls back 30% from peak",
			DescEN:   "Close position when PnL pulls back 30% from peak",
			ReasonZH: "Lock in most profits, avoid profit giveback. E.g., Peak Margin/Position PnL +50%, close at +35%",
			ReasonEN: "Lock in most profits, avoid profit giveback. E.g., Peak Margin/Position PnL +50%, close at +35%",
		},
		"StopLoss": {
			Value:    HardStopMarginPositionPnLPct / 100,
			DescZH:   "规则值为比例 -0.20（即 -20% Margin/Position PnL）的硬止损",
			DescEN:   "Hard stop at rule fraction -0.20 (that is -20% Margin/Position PnL)",
			ReasonZH: "Strictly control maximum single-trade loss",
			ReasonEN: "Strictly control maximum single-trade loss",
		},
		"TakeProfit": {
			Value:    0.40,
			DescZH:   "规则值为比例 0.40（即 +40% Margin/Position PnL，针对 -20% 硬止损为 2R）；按最终杠杆换算绝对价格",
			DescEN:   "Rule value is fraction 0.40 (that is +40% Margin/Position PnL, 2R against the -20% hard stop); convert with final leverage to an absolute price",
			ReasonZH: "Cover fees, funding and slippage while preserving at least 2:1 risk/reward",
			ReasonEN: "Cover fees, funding and slippage while preserving at least 2:1 risk/reward",
		},
	},

	PositionControl: map[string]BilingualRuleDef{
		"ScaleIn": {
			Value:    map[string]interface{}{"enabled": true, "max_additions": 2, "price_requirement": 0.01},
			DescZH:   "Only add to winning positions, max 2 additions, price must be 1% above avg cost",
			DescEN:   "Only add to winning positions, max 2 additions, price must be 1% above avg cost",
			ReasonZH: "Add to winners, never average down losers",
			ReasonEN: "Add to winners, never average down losers",
		},
		"ScaleOut": {
			Value: []map[string]interface{}{
				{"pnl": 0.20, "close_pct": 0.33},
				{"pnl": 0.30, "close_pct": 0.50},
				{"pnl": 0.40, "close_pct": 1.00},
			},
			DescZH:   "Scale-out Margin/Position PnL: Close 33% at +20%, 50% at +30%, 100% at +40%; convert thresholds to actual prices with final leverage",
			DescEN:   "Scale-out Margin/Position PnL: Close 33% at +20%, 50% at +30%, 100% at +40%; convert thresholds to actual prices with final leverage",
			ReasonZH: "Lock profits while letting winners run",
			ReasonEN: "Lock profits while letting winners run",
		},
	},
}

// ========== OI Interpretation ==========

// OIInterpretation defines bilingual market interpretations for OI changes
type OIInterpretationType struct {
	OIUp_PriceUp struct {
		ZH string
		EN string
	}
	OIUp_PriceDown struct {
		ZH string
		EN string
	}
	OIDown_PriceUp struct {
		ZH string
		EN string
	}
	OIDown_PriceDown struct {
		ZH string
		EN string
	}
}

var OIInterpretation = OIInterpretationType{
	OIUp_PriceUp: struct {
		ZH string
		EN string
	}{
		ZH: "Strong bullish trend (new longs opening, capital flowing into long positions)",
		EN: "Strong bullish trend (new longs opening, capital flowing into long positions)",
	},
	OIUp_PriceDown: struct {
		ZH string
		EN string
	}{
		ZH: "Strong bearish trend (new shorts opening, capital flowing into short positions)",
		EN: "Strong bearish trend (new shorts opening, capital flowing into short positions)",
	},
	OIDown_PriceUp: struct {
		ZH string
		EN string
	}{
		ZH: "Shorts covering (shorts stopped out, potential reversal)",
		EN: "Shorts covering (shorts stopped out, potential reversal)",
	},
	OIDown_PriceDown: struct {
		ZH string
		EN string
	}{
		ZH: "Longs closing (longs stopped out, potential reversal)",
		EN: "Longs closing (longs stopped out, potential reversal)",
	},
}

// ========== Common Mistakes ==========

// CommonMistake defines a common mistake with bilingual fields
type CommonMistake struct {
	ErrorZH   string
	ErrorEN   string
	ExampleZH string
	ExampleEN string
	CorrectZH string
	CorrectEN string
}

var CommonMistakes = []CommonMistake{
	{
		ErrorZH:   "Confusing realized and unrealized P&L",
		ErrorEN:   "Confusing realized and unrealized P&L",
		ExampleZH: "Adding historical trade P&L with current position P&L",
		ExampleEN: "Adding historical trade P&L with current position P&L",
		CorrectZH: "Realized P&L is already included in account balance, don't double count",
		CorrectEN: "Realized P&L is already included in account balance, don't double count",
	},
	{
		ErrorZH:   "Ignoring leverage's impact on P&L",
		ErrorEN:   "Ignoring leverage's impact on P&L",
		ExampleZH: "Price up 1%, thinking profit is 1%",
		ExampleEN: "Price up 1%, thinking profit is 1%",
		CorrectZH: "With 3x leverage, a 1% favorable price move is about +3% Margin/Position PnL before fees",
		CorrectEN: "With 3x leverage, a 1% favorable price move is about +3% Margin/Position PnL before fees",
	},
	{
		ErrorZH:   "Not understanding Peak Margin/Position PnL's importance",
		ErrorEN:   "Not understanding Peak Margin/Position PnL's importance",
		ExampleZH: "Only watching current PnL, ignoring drawdown",
		ExampleEN: "Only watching current PnL, ignoring drawdown",
		CorrectZH: "When current Margin/Position PnL pulls back from its peak, consider taking profit to lock in gains",
		CorrectEN: "When current Margin/Position PnL pulls back from its peak, consider taking profit to lock in gains",
	},
	{
		ErrorZH:   "Ignoring Open Interest changes",
		ErrorEN:   "Ignoring Open Interest changes",
		ExampleZH: "Only watching price candles, not capital flows",
		ExampleEN: "Only watching price candles, not capital flows",
		CorrectZH: "Use OI changes to validate trend authenticity and sustainability",
		CorrectEN: "Use OI changes to validate trend authenticity and sustainability",
	},
}

// ========== Prompt Generation Functions ==========

// GetSchemaPrompt generates schema description text for AI prompts
func GetSchemaPrompt(lang Language) string {
	if lang == LangChinese {
		return getSchemaPromptZH()
	}
	return getSchemaPromptEN()
}

// getSchemaPromptZH generates the Chinese prompt
func getSchemaPromptZH() string {
	prompt := "# 📖 Data Dictionary & Trading Rules\n\n"
	prompt += "## 📊 Field Definitions\n\n"

	// Account metrics
	prompt += "### Account Metrics\n"
	for key, field := range DataDictionary["AccountMetrics"] {
		prompt += formatFieldDefZH(key, field)
	}

	// Trade metrics
	prompt += "\n### Trade Metrics\n"
	for key, field := range DataDictionary["TradeMetrics"] {
		prompt += formatFieldDefZH(key, field)
	}

	// Position metrics
	prompt += "\n### Position Metrics\n"
	for key, field := range DataDictionary["PositionMetrics"] {
		prompt += formatFieldDefZH(key, field)
	}

	// Market data
	prompt += "\n### Market Data\n"
	for key, field := range DataDictionary["MarketData"] {
		prompt += formatFieldDefZH(key, field)
	}

	// OI interpretation
	prompt += "\n## 💹 Open Interest (OI) Change Interpretation\n\n"
	prompt += "- **OI Up + Price Up**: " + OIInterpretation.OIUp_PriceUp.ZH + "\n"
	prompt += "- **OI Up + Price Down**: " + OIInterpretation.OIUp_PriceDown.ZH + "\n"
	prompt += "- **OI Down + Price Up**: " + OIInterpretation.OIDown_PriceUp.ZH + "\n"
	prompt += "- **OI Down + Price Down**: " + OIInterpretation.OIDown_PriceDown.ZH + "\n"

	return prompt
}

// getSchemaPromptEN generates the English prompt
func getSchemaPromptEN() string {
	prompt := "# 📖 Data Dictionary & Trading Rules\n\n"
	prompt += "## 📊 Field Definitions\n\n"

	// Account Metrics
	prompt += "### Account Metrics\n"
	for key, field := range DataDictionary["AccountMetrics"] {
		prompt += formatFieldDefEN(key, field)
	}

	// Trade Metrics
	prompt += "\n### Trade Metrics\n"
	for key, field := range DataDictionary["TradeMetrics"] {
		prompt += formatFieldDefEN(key, field)
	}

	// Position Metrics
	prompt += "\n### Position Metrics\n"
	for key, field := range DataDictionary["PositionMetrics"] {
		prompt += formatFieldDefEN(key, field)
	}

	// Market Data
	prompt += "\n### Market Data\n"
	for key, field := range DataDictionary["MarketData"] {
		prompt += formatFieldDefEN(key, field)
	}

	// OI Interpretation
	prompt += "\n## 💹 Open Interest (OI) Change Interpretation\n\n"
	prompt += "- **OI Up + Price Up**: " + OIInterpretation.OIUp_PriceUp.EN + "\n"
	prompt += "- **OI Up + Price Down**: " + OIInterpretation.OIUp_PriceDown.EN + "\n"
	prompt += "- **OI Down + Price Up**: " + OIInterpretation.OIDown_PriceUp.EN + "\n"
	prompt += "- **OI Down + Price Down**: " + OIInterpretation.OIDown_PriceDown.EN + "\n"

	return prompt
}

// formatFieldDefZH formats a field definition in Chinese
func formatFieldDefZH(key string, field BilingualFieldDef) string {
	result := "- **" + key + "** (" + field.NameZH + "): " + field.DescZH
	if field.FormulaZH != "" {
		result += " | Formula: `" + field.FormulaZH + "`"
	}
	if field.Unit != "" {
		result += " | Unit: " + field.Unit
	}
	result += "\n"
	return result
}

// formatFieldDefEN formats a field definition in English
func formatFieldDefEN(key string, field BilingualFieldDef) string {
	result := "- **" + key + "** (" + field.NameEN + "): " + field.DescEN
	if field.FormulaEN != "" {
		result += " | Formula: `" + field.FormulaEN + "`"
	}
	if field.Unit != "" {
		result += " | Unit: " + field.Unit
	}
	result += "\n"
	return result
}
