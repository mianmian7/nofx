package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"nofx/logger"
	"nofx/mcp"
	"nofx/mcp/provider"
	"nofx/store"
	"strings"
	"sync"
	"time"
)

// MacroBias represents the higher-timeframe regime and directional bias from System 2 LLM
type MacroBias struct {
	Bias       string    `json:"bias"` // "bullish_only", "bearish_only", "neutral", "risk_off"
	Confidence int       `json:"confidence"`
	Reasoning  string    `json:"reasoning"`
	UpdatedAt  time.Time `json:"updated_at"`
	ValidUntil time.Time `json:"valid_until"`
	Model      string    `json:"model"` // e.g. "grok-4.6" or "gemini-3.7-flash"
}

// IsValid checks if the macro bias is still within its validity window
func (m *MacroBias) IsValid() bool {
	if m == nil {
		return false
	}
	return time.Now().Before(m.ValidUntil)
}

// MacroBiasCache manages cached macro biases per trader / strategy
type MacroBiasCache struct {
	mu     sync.RWMutex
	biases map[string]*MacroBias
}

var globalMacroBiasCache = &MacroBiasCache{
	biases: make(map[string]*MacroBias),
}

// GetMacroBias retrieves cached bias if valid
func GetMacroBias(key string) *MacroBias {
	globalMacroBiasCache.mu.RLock()
	defer globalMacroBiasCache.mu.RUnlock()
	b, ok := globalMacroBiasCache.biases[key]
	if ok && b.IsValid() {
		return b
	}
	return nil
}

// SetMacroBias updates cached bias
func SetMacroBias(key string, bias *MacroBias) {
	globalMacroBiasCache.mu.Lock()
	defer globalMacroBiasCache.mu.Unlock()
	globalMacroBiasCache.biases[key] = bias
}

// EvaluateMacroBiasWithLLM calls the System 2 LLM to produce a macro trend & regime assessment
func EvaluateMacroBiasWithLLM(
	ctx context.Context,
	macroClient mcp.AIClient,
	kCtx *Context,
	engine *StrategyEngine,
) (*MacroBias, error) {
	if macroClient == nil {
		return nil, fmt.Errorf("macro AI client is nil")
	}

	modelName := "LLM"
	if embedder, ok := macroClient.(mcp.ClientEmbedder); ok && embedder.BaseClient() != nil {
		modelName = embedder.BaseClient().Model
	}

	systemPrompt := `# Role
You are a Senior Macro Crypto Market Strategist (System 2 Dual-Brain).
Your objective is to analyze higher-timeframe (4h/1h) market structure, EMA trend alignments, and volatility to define the directional bias for high-frequency execution.

# Output Format (Strict JSON)
Output ONLY a JSON object:
{
  "bias": "bullish_only" | "bearish_only" | "neutral" | "risk_off",
  "confidence": 85,
  "reasoning": "Brief explanation of 4h/1h trend structure and key invalidation levels",
  "valid_duration_minutes": 45
}

# Bias Definitions:
- "bullish_only": 4h/1h clearly bullish; only long setups allowed. Shorts strictly vetoed.
- "bearish_only": 4h/1h clearly bearish; only short setups allowed. Longs strictly vetoed.
- "neutral": Range-bound market with clear boundaries, multi-directional trades permitted with tight stops.
- "risk_off": High danger / extreme volatility / contradictory signals; NO new positions allowed.`

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Current Account Equity: $%.2f\n\n", kCtx.Account.TotalEquity))
	sb.WriteString("Higher-Timeframe Market Context:\n")

	for sym, mkt := range kCtx.MarketDataMap {
		if mkt == nil || mkt.CurrentPrice <= 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n## %s: Current Price $%.4f\n", sym, mkt.CurrentPrice))
		sb.WriteString(fmt.Sprintf("- 1h Change: %.2f%% | 4h Change: %.2f%%\n", mkt.PriceChange1h, mkt.PriceChange4h))
		sb.WriteString(fmt.Sprintf("- EMA20: %.4f | MACD: %.4f | RSI7: %.1f\n", mkt.CurrentEMA20, mkt.CurrentMACD, mkt.CurrentRSI7))

		for _, tf := range []string{"1h", "4h", "15m"} {
			if s, ok := mkt.TimeframeData[tf]; ok && s != nil {
				sb.WriteString(fmt.Sprintf("  [%s TF] ATR14: %.4f", tf, s.ATR14))
				if len(s.EMA20Values) > 0 && len(s.EMA50Values) > 0 {
					ema20 := s.EMA20Values[len(s.EMA20Values)-1]
					ema50 := s.EMA50Values[len(s.EMA50Values)-1]
					sb.WriteString(fmt.Sprintf(" | EMA20: %.4f vs EMA50: %.4f", ema20, ema50))
				}
				sb.WriteString("\n")
			}
		}
	}

	userPrompt := sb.String()

	resp, err := macroClient.CallWithRequest(&mcp.Request{
		Ctx: ctx, Messages: []mcp.Message{mcp.NewSystemMessage(systemPrompt), mcp.NewUserMessage(userPrompt)},
		Metadata: mcp.CallMetadata{TraderID: kCtx.TraderID, StrategyID: kCtx.StrategyID},
	})
	if err != nil {
		return nil, fmt.Errorf("macro LLM call failed: %w", err)
	}

	// Parse JSON output
	rawJSON := resp
	if start := strings.Index(resp, "{"); start >= 0 {
		if end := strings.LastIndex(resp, "}"); end > start {
			rawJSON = resp[start : end+1]
		}
	}

	var parsed struct {
		Bias                 string `json:"bias"`
		Confidence           int    `json:"confidence"`
		Reasoning            string `json:"reasoning"`
		ValidDurationMinutes int    `json:"valid_duration_minutes"`
	}

	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("invalid macro JSON: %w", err)
	}
	if !validMacroLabel(parsed.Bias) || parsed.Confidence < 0 || parsed.Confidence > 100 {
		return nil, fmt.Errorf("invalid macro bias or confidence")
	}

	duration := time.Duration(parsed.ValidDurationMinutes) * time.Minute
	if duration <= 0 || duration > 120*time.Minute {
		duration = 45 * time.Minute
	}

	bias := &MacroBias{
		Bias:       parsed.Bias,
		Confidence: parsed.Confidence,
		Reasoning:  parsed.Reasoning,
		UpdatedAt:  time.Now(),
		ValidUntil: time.Now().Add(duration),
		Model:      modelName,
	}

	logger.Infof("🧠 [Dual-Brain System 2] Updated Macro Bias from %s: Bias=%s (Conf=%d%%, Valid=%v) | %s",
		modelName, bias.Bias, bias.Confidence, duration, bias.Reasoning)

	return bias, nil
}

// GetDualBrainDecision executes the dual-brain coordination pipeline:
// 1. Checks/refreshes System 2 MacroBias from macroClient (Grok/Gemini/DeepSeek)
// 2. Executes System 1 Jev fast evaluation under MacroBias guidance
func GetDualBrainDecision(
	ctx context.Context,
	kCtx *Context,
	macroClient mcp.AIClient,
	jevClient *provider.JevClient,
	engine *StrategyEngine,
) (*FullDecision, error) {
	if kCtx == nil {
		return nil, fmt.Errorf("context is nil")
	}

	cacheKey := kCtx.TraderID
	if cacheKey == "" {
		cacheKey = kCtx.StrategyID
	}

	macroBias := GetMacroBias(cacheKey)
	if macroBias == nil && macroClient != nil {
		var err error
		macroBias, err = EvaluateMacroBiasWithLLM(ctx, macroClient, kCtx, engine)
		if err != nil {
			logger.Warnf("⚠️ [Dual-Brain] Failed to evaluate macro bias: %v, blocking new entries", err)
			macroBias = &MacroBias{
				Bias:       "risk_off",
				Confidence: 0,
				Reasoning:  "Macro LLM unavailable; new entries blocked, exits remain enabled",
				UpdatedAt:  time.Now(),
				ValidUntil: time.Now().Add(10 * time.Minute),
				Model:      "fallback",
			}
		}
		SetMacroBias(cacheKey, macroBias)
	}

	// Now run Jev with MacroBias constraints
	return GetJevDecisionWithMacroContext(ctx, kCtx, jevClient, engine, macroBias)
}

// GetJevDecisionWithMacroContext evaluates Jev signals with macro veto rules
func GetJevDecisionWithMacroContext(
	ctx context.Context,
	kCtx *Context,
	jevClient *provider.JevClient,
	engine *StrategyEngine,
	macroBias *MacroBias,
) (*FullDecision, error) {
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
	for _, pos := range kCtx.Positions {
		symbolsToEvaluate = append(symbolsToEvaluate, pos.Symbol)
	}
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

		dec, err := EvaluateJevSignalWithMacro(jevResp, feat, kCtx.Account.TotalEquity, kCtx.Account.AvailableBalance, riskConfig, detConfig, macroBias)
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

	cot := fmt.Sprintf("Dual-Brain System 1+2 (Macro: %s by %s) evaluated %d symbols in %dms (actions: %d validated, %d rejected)",
		macroBiasString(macroBias), macroBiasModel(macroBias), len(symbolsToEvaluate), duration.Milliseconds(), len(validated), len(rejected))

	fullDecision := &FullDecision{
		CallID:              callID,
		Decisions:           validated,
		RejectedDecisions:   rejected,
		AIRequestDurationMs: duration.Milliseconds(),
		Timestamp:           time.Now(),
		CoTTrace:            cot,
	}

	return fullDecision, nil
}

func macroBiasString(m *MacroBias) string {
	if m == nil {
		return "none"
	}
	return m.Bias
}

func macroBiasModel(m *MacroBias) string {
	if m == nil {
		return "none"
	}
	return m.Model
}

// EvaluateJevSignalWithMacro applies macro trend veto gates on top of standard Jev evaluation
func EvaluateJevSignalWithMacro(
	resp *provider.JevResponse,
	feat *JevMarketFeature,
	accountEquity float64,
	availableBalance float64,
	riskConfig store.RiskControlConfig,
	detConfig DeterministicRiskConfig,
	macroBias *MacroBias,
) (*Decision, error) {
	dec, err := EvaluateJevSignal(resp, feat, accountEquity, availableBalance, riskConfig, detConfig)
	if err != nil || dec == nil {
		return dec, err
	}

	// If position is already open and action is to exit/hold, macro bias does not block protective exits
	if feat.HasPosition {
		return dec, nil
	}

	// Missing/expired/unknown evidence must not turn the dual-brain strategy
	// into an unguarded single-brain strategy. Exits were handled above.
	if macroBias == nil || !macroBias.IsValid() || !validMacroLabel(macroBias.Bias) || macroBias.Confidence < 60 || macroBias.Confidence > 100 {
		if dec.Action == "open_long" || dec.Action == "open_short" {
			dec.Action = "wait"
			dec.Reasoning = "Dual-Brain Veto: macro evidence unavailable, expired or untrusted"
		}
		return dec, nil
	}
	// Apply System 2 Macro Bias Veto on new opening orders
	if macroBias != nil && macroBias.IsValid() {
		if macroBias.Bias == "risk_off" && (dec.Action == "open_long" || dec.Action == "open_short") {
			logger.Infof("🚫 [Dual-Brain Veto] %s %s blocked by %s risk_off mode: %s", feat.Symbol, dec.Action, macroBias.Model, macroBias.Reasoning)
			dec.Action = "wait"
			dec.Reasoning = fmt.Sprintf("Dual-Brain Veto: %s risk_off blocks opening (%s)", macroBias.Model, macroBias.Reasoning)
		} else if macroBias.Bias == "bullish_only" && dec.Action == "open_short" {
			logger.Infof("🚫 [Dual-Brain Veto] %s open_short blocked by %s bullish_only bias (%s)", feat.Symbol, macroBias.Model, macroBias.Reasoning)
			dec.Action = "wait"
			dec.Reasoning = fmt.Sprintf("Dual-Brain Veto: Jev short signal vetoed by %s bullish macro trend (%s)", macroBias.Model, macroBias.Reasoning)
		} else if macroBias.Bias == "bearish_only" && dec.Action == "open_long" {
			logger.Infof("🚫 [Dual-Brain Veto] %s open_long blocked by %s bearish_only bias (%s)", feat.Symbol, macroBias.Model, macroBias.Reasoning)
			dec.Action = "wait"
			dec.Reasoning = fmt.Sprintf("Dual-Brain Veto: Jev long signal vetoed by %s bearish macro trend (%s)", macroBias.Model, macroBias.Reasoning)
		} else if dec.Action == "open_long" || dec.Action == "open_short" {
			// Resonating alignment! Boost conviction in reasoning
			dec.Reasoning = fmt.Sprintf("Dual-Brain Alignment [%s Macro: %s] + %s", macroBias.Model, macroBias.Bias, dec.Reasoning)
		}
	}

	return dec, nil
}
