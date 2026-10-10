package kernel

import (
	"context"
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp/provider"
	"nofx/store"
	"strings"
	"time"
)

// JevMarketFeature represents the distilled numerical features provided to Jev
type JevMarketFeature struct {
	Symbol             string  `json:"symbol"`
	CurrentPrice       float64 `json:"current_price"`
	PriceChange1hPct   float64 `json:"price_change_1h_pct"`
	PriceChange4hPct   float64 `json:"price_change_4h_pct"`
	RSI14              float64 `json:"rsi_14"`
	MACD               float64 `json:"macd"`
	EMA20              float64 `json:"ema_20"`
	EMA50              float64 `json:"ema_50"`
	EMATrend           string  `json:"ema_trend"`
	IndicatorTimeframe string  `json:"indicator_timeframe,omitempty"`
	ATR14              float64 `json:"atr_14"`
	ATRPercent         float64 `json:"atr_pct"`
	BOLLPosition       float64 `json:"boll_position"` // 0.0=lower, 0.5=middle, 1.0=upper
	HasPosition        bool    `json:"has_position"`
	PositionSide       string  `json:"position_side,omitempty"` // "long", "short", "none"
	PositionPnLPct     float64 `json:"position_pnl_pct,omitempty"`
}

// ExtractJevFeature extracts key market indicators from market.Data and position
func ExtractJevFeature(symbol string, data *market.Data, pos *PositionInfo) *JevMarketFeature {
	return extractJevFeature(symbol, data, pos, "")
}

func extractJevFeature(symbol string, data *market.Data, pos *PositionInfo, primary string) *JevMarketFeature {
	feat := &JevMarketFeature{
		Symbol:       symbol,
		HasPosition:  pos != nil,
		PositionSide: "none",
	}

	if pos != nil {
		feat.PositionSide = strings.ToLower(pos.Side)
		feat.PositionPnLPct = pos.UnrealizedPnLPct
	}

	if data == nil {
		return feat
	}

	feat.CurrentPrice = data.CurrentPrice
	feat.PriceChange1hPct = data.PriceChange1h
	feat.PriceChange4hPct = data.PriceChange4h
	feat.EMA20 = data.CurrentEMA20
	feat.MACD = data.CurrentMACD
	// Never combine a primary EMA20 with another timeframe's EMA50 or ATR.
	// With an explicit strategy timeframe, missing data must not silently fall back.
	timeframes := []string{"5m", "15m", "1h", "3m"}
	if primary != "" {
		timeframes = []string{primary}
	}
	for _, tf := range timeframes {
		if series, ok := data.TimeframeData[tf]; ok && series != nil {
			feat.IndicatorTimeframe = tf
			feat.EMA20 = 0
			if len(series.EMA20Values) > 0 {
				feat.EMA20 = series.EMA20Values[len(series.EMA20Values)-1]
			}
			feat.ATR14 = series.ATR14
			if len(series.MACDValues) > 0 {
				feat.MACD = series.MACDValues[len(series.MACDValues)-1]
			}
			if len(series.RSI14Values) > 0 {
				feat.RSI14 = series.RSI14Values[len(series.RSI14Values)-1]
			} else if len(series.RSI7Values) > 0 {
				feat.RSI14 = series.RSI7Values[len(series.RSI7Values)-1]
			}

			if len(series.EMA50Values) > 0 {
				feat.EMA50 = series.EMA50Values[len(series.EMA50Values)-1]
			}

			if len(series.BOLLUpper) > 0 && len(series.BOLLLower) > 0 {
				upper := series.BOLLUpper[len(series.BOLLUpper)-1]
				lower := series.BOLLLower[len(series.BOLLLower)-1]
				if upper > lower && data.CurrentPrice >= lower {
					feat.BOLLPosition = (data.CurrentPrice - lower) / (upper - lower)
				}
			}
			break
		}
	}

	if data.CurrentPrice > 0 && feat.ATR14 > 0 {
		feat.ATRPercent = feat.ATR14 / data.CurrentPrice * 100
	}
	if feat.RSI14 == 0 && data.CurrentRSI7 > 0 && primary == "" {
		feat.RSI14 = data.CurrentRSI7
	}

	if feat.EMA20 > 0 && feat.EMA50 > 0 {
		if feat.EMA20 > feat.EMA50 {
			feat.EMATrend = "bullish"
		} else {
			feat.EMATrend = "bearish"
		}
	} else {
		feat.EMATrend = "neutral"
	}

	return feat
}

// BuildJevStateAndQuestions creates the typed question payload for Jev
func BuildJevStateAndQuestions(feat *JevMarketFeature) (interface{}, map[string]provider.JevQuestion) {
	state := map[string]interface{}{
		"symbol":              feat.Symbol,
		"current_price":       feat.CurrentPrice,
		"price_change_1h_pct": feat.PriceChange1hPct,
		"rsi_14":              feat.RSI14,
		"macd":                feat.MACD,
		"ema_trend":           feat.EMATrend,
		"atr_pct":             feat.ATRPercent,
		"bollinger_position":  feat.BOLLPosition,
		"has_position":        feat.HasPosition,
		"position_side":       feat.PositionSide,
		"position_pnl_pct":    feat.PositionPnLPct,
	}

	questions := map[string]provider.JevQuestion{
		"market_regime": {
			Type:         "choice",
			Instructions: "What is the current market regime based on technical indicators?",
			Criteria: map[string]string{
				"strong_trend_up":    "Clear upward momentum, price above EMAs, RSI > 50",
				"strong_trend_down":  "Clear downward momentum, price below EMAs, RSI < 50",
				"ranging_chop":       "Oscillating between Bollinger bands, flat EMAs, high whipsaw risk",
				"extreme_volatility": "Abnormal price/volume spikes, extreme risk",
			},
		},
	}

	if !feat.HasPosition {
		questions["entry_signal"] = provider.JevQuestion{
			Type:         "choice",
			Instructions: "Should we enter a trade right now?",
			Criteria: map[string]string{
				"open_long":  "High probability long breakout/trend continuation with indicator alignment",
				"open_short": "High probability short breakdown/trend continuation with indicator alignment",
				"wait":       "No clear edge, range-bound chop, or mixed indicator signals",
			},
		}
	} else {
		questions["position_action"] = provider.JevQuestion{
			Type:         "choice",
			Instructions: "How should we manage the existing position?",
			Criteria: map[string]string{
				"hold":        "Trend continues in our favor, maintain position",
				"exit_profit": "Momentum exhausting near target, close position to secure gains",
				"exit_loss":   "Trend reversed, invalidation criteria met, exit immediately",
			},
		}
	}

	return state, questions
}

// buildJevStateForStrategy keeps the legacy wire contract unchanged unless the
// strategy explicitly opts into the price/EMA information candidate. It does
// not change feature extraction, ATR sizing, action thresholds or exits.
func (e *StrategyEngine) buildJevStateForStrategy(feat *JevMarketFeature) (interface{}, map[string]provider.JevQuestion) {
	state, questions := BuildJevStateAndQuestions(feat)
	if !e.config.Indicators.JevPriceContext {
		return state, questions
	}
	values := state.(map[string]interface{})
	values["indicator_timeframe"] = feat.IndicatorTimeframe
	// A missing configured series must not label a fallback EMA with the wrong
	// timeframe. JSON null explicitly represents unavailable price levels.
	level := func(value float64) interface{} {
		if feat.IndicatorTimeframe == "" || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		return value
	}
	values["ema_20"] = level(feat.EMA20)
	values["ema_50"] = level(feat.EMA50)
	question := questions["market_regime"]
	question.Instructions = "What is the current market regime? Compare current_price with ema_20 and ema_50 from indicator_timeframe. ema_trend describes only the ordering of the EMAs, not whether price is above or below them. A null EMA or empty timeframe means that price comparison is unavailable; do not infer missing levels or a strong trend from that comparison."
	questions["market_regime"] = question
	return state, questions
}

// EvaluateJevSignal translates Jev answers into a fully-formed Decision using deterministic risk rules
func EvaluateJevSignal(
	resp *provider.JevResponse,
	feat *JevMarketFeature,
	accountEquity float64,
	availableBalance float64,
	riskConfig store.RiskControlConfig,
	detConfig DeterministicRiskConfig,
) (*Decision, error) {
	if resp == nil || resp.Answers == nil {
		return nil, fmt.Errorf("empty jev response")
	}

	regimeAns := resp.Answers["market_regime"]
	marketRegime := regimeAns.Choice

	// 1. Manage existing position
	if feat.HasPosition {
		posActionAns := resp.Answers["position_action"]
		choice := posActionAns.Choice
		conf := int(posActionAns.Confidence * 100)
		if conf <= 0 {
			conf = 75
		}

		switch choice {
		case "exit_profit", "exit_loss":
			action := "close_long"
			if feat.PositionSide == "short" {
				action = "close_short"
			}
			return &Decision{
				Symbol:     feat.Symbol,
				Action:     action,
				Confidence: conf,
				Reasoning:  fmt.Sprintf("Jev System 1 exit signal: %s (conf=%d%%, regime=%s)", choice, conf, marketRegime),
			}, nil

		default:
			return &Decision{
				Symbol:     feat.Symbol,
				Action:     "hold",
				Confidence: conf,
				Reasoning:  fmt.Sprintf("Jev System 1 hold signal (conf=%d%%, regime=%s)", conf, marketRegime),
			}, nil
		}
	}

	// 2. Evaluate opening entry signal
	entryAns, ok := resp.Answers["entry_signal"]
	if !ok {
		return &Decision{
			Symbol:     feat.Symbol,
			Action:     "wait",
			Confidence: 50,
			Reasoning:  "No entry signal provided by Jev",
		}, nil
	}

	action := entryAns.Choice
	if action == "" || action == "wait" {
		conf := int(entryAns.Confidence * 100)
		if conf == 0 {
			conf = 50
		}
		return &Decision{
			Symbol:     feat.Symbol,
			Action:     "wait",
			Confidence: conf,
			Reasoning:  fmt.Sprintf("Jev System 1 wait (regime=%s, conf=%d%%)", marketRegime, conf),
		}, nil
	}

	// Confidence belongs to the selected action, not whichever label happened
	// to have the highest probability. Invalid/missing evidence fails closed.
	wait := func(reason string) (*Decision, error) {
		return &Decision{Symbol: feat.Symbol, Action: "wait", Reasoning: reason}, nil
	}
	if (action != "open_long" && action != "open_short") ||
		(action == "open_long" && marketRegime != "strong_trend_up") ||
		(action == "open_short" && marketRegime != "strong_trend_down") {
		return wait("Jev entry blocked: missing, unsafe or directionally conflicting regime")
	}
	topProb, secondProb := entryAns.Confidence, 0.0
	if entryAns.Probabilities != nil {
		var found bool
		topProb, found = entryAns.Probabilities[action]
		if !found {
			return wait("Jev entry blocked: selected action probability missing")
		}
		for label, p := range entryAns.Probabilities {
			if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
				return wait("Jev entry blocked: invalid probability")
			}
			if label != action {
				secondProb = math.Max(secondProb, p)
			}
		}
	}
	if math.IsNaN(topProb) || math.IsInf(topProb, 0) || topProb <= 0 || topProb > 1 {
		return wait("Jev entry blocked: invalid confidence")
	}
	confidence := int(topProb * 100)
	if riskConfig.MinConfidence > detConfig.MinConfidence {
		detConfig.MinConfidence = riskConfig.MinConfidence
	}

	// 3. Anti-whipsaw filter
	approved, vetoReason := AntiWhipsawFilter(marketRegime, topProb, secondProb, detConfig.MinConfidence)
	if !approved {
		logger.Infof("🚫 [Jev Anti-Whipsaw] Vetoed %s on %s: %s", action, feat.Symbol, vetoReason)
		return &Decision{
			Symbol:     feat.Symbol,
			Action:     "wait",
			Confidence: max(1, detConfig.MinConfidence-10),
			Reasoning:  fmt.Sprintf("Jev signal %s vetoed by anti-whipsaw filter: %s", action, vetoReason),
		}, nil
	}

	// 4. Calculate exact SL, TP, and position size using ATR
	dec, err := CalculateATRParams(feat.Symbol, action, feat.CurrentPrice, feat.ATR14, accountEquity, availableBalance, riskConfig, detConfig, confidence)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate ATR params: %w", err)
	}

	dec.Symbol = feat.Symbol
	return dec, nil
}

// GetJevDecisionWithStrategyContext executes the complete Jev System 1 decision pipeline
func GetJevDecisionWithStrategyContext(
	ctx context.Context,
	kCtx *Context,
	jevClient *provider.JevClient,
	engine *StrategyEngine,
) (*FullDecision, error) {
	if kCtx == nil {
		return nil, fmt.Errorf("context is nil")
	}
	if jevClient == nil {
		return nil, fmt.Errorf("jev client is nil")
	}

	detConfig := DefaultDeterministicRiskConfig()
	riskConfig := engine.GetRiskControlConfig()
	if riskConfig.MaxLeverage > 0 {
		detConfig.MaxLeverage = riskConfig.MaxLeverage
	}
	if riskConfig.MinConfidence > 0 {
		detConfig.MinConfidence = riskConfig.MinConfidence
	}

	var decisions []Decision
	positionMap := make(map[string]*PositionInfo)
	for i := range kCtx.Positions {
		pos := &kCtx.Positions[i]
		positionMap[pos.Symbol] = pos
	}

	symbolsToEvaluate := make([]string, 0)
	// First evaluate existing positions
	for _, pos := range kCtx.Positions {
		symbolsToEvaluate = append(symbolsToEvaluate, pos.Symbol)
	}
	// Then evaluate candidate coins
	for _, coin := range kCtx.CandidateCoins {
		if _, exists := positionMap[coin.Symbol]; !exists {
			symbolsToEvaluate = append(symbolsToEvaluate, coin.Symbol)
		}
	}

	startTime := time.Now()
	for _, sym := range symbolsToEvaluate {
		mktData := kCtx.MarketDataMap[sym]
		if mktData == nil || mktData.CurrentPrice <= 0 {
			continue
		}

		pos := positionMap[sym]
		feat := extractJevFeature(sym, mktData, pos, engine.config.Indicators.Klines.PrimaryTimeframe)
		state, questions := engine.buildJevStateForStrategy(feat)
		engine.addPaperEntryState(state, questions, mktData, time.Now().UTC())

		req := &provider.JevRequest{
			State:     state,
			Questions: questions,
		}

		jevResp, err := jevClient.Evaluate(ctx, req)
		if err != nil {
			logger.Warnf("⚠️ [Jev] Failed evaluation for %s: %v", sym, err)
			continue
		}

		dec, err := EvaluateJevSignal(jevResp, feat, kCtx.Account.TotalEquity, kCtx.Account.AvailableBalance, riskConfig, detConfig)
		if err != nil {
			logger.Warnf("⚠️ [Jev] Failed to translate signal for %s: %v", sym, err)
			continue
		}

		gateJevEntryTrend(dec, mktData)
		if dec != nil {
			decisions = append(decisions, *dec)
		}
	}

	duration := time.Since(startTime)
	callID := newLogicalCallID()
	if kCtx.OnAICallStarted != nil {
		kCtx.OnAICallStarted(callID)
	}

	validated, rejected := validateDecisionBatchSafely(decisions, kCtx.Account.TotalEquity, riskConfig)

	fullDecision := &FullDecision{
		CallID:              callID,
		Decisions:           validated,
		RejectedDecisions:   rejected,
		AIRequestDurationMs: duration.Milliseconds(),
		Timestamp:           time.Now(),
		CoTTrace:            fmt.Sprintf("Jev System 1 evaluated %d symbols in %dms (actions: %d validated, %d rejected)", len(symbolsToEvaluate), duration.Milliseconds(), len(validated), len(rejected)),
	}

	return fullDecision, nil
}
