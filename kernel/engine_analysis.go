package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/mcp/provider"
	"nofx/store"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MarketDataUnavailableError tells the trader loop that no AI call should be
// attempted because live decision data was not fresh enough. It is separate
// from an AI provider failure so the trader does not enter AI safe mode merely
// because Binance temporarily refused market data.
type MarketDataUnavailableError struct {
	Symbols []string
	Cause   error
}

const minFreshCandidateCoveragePercent = 80

func (err *MarketDataUnavailableError) Error() string {
	if err == nil {
		return "fresh market data unavailable"
	}
	if len(err.Symbols) == 0 {
		if err.Cause != nil {
			return fmt.Sprintf("fresh market data unavailable: %v", err.Cause)
		}
		return "fresh market data unavailable"
	}
	if err.Cause != nil {
		return fmt.Sprintf("fresh market data unavailable for %s: %v", strings.Join(err.Symbols, ", "), err.Cause)
	}
	return fmt.Sprintf("fresh market data unavailable for %s", strings.Join(err.Symbols, ", "))
}

func (err *MarketDataUnavailableError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// ============================================================================
// Pre-compiled regular expressions (performance optimization)
// ============================================================================

var (
	// Safe regex: precisely match ```json code blocks
	reJSONFence      = regexp.MustCompile(`(?is)` + "```json\\s*(\\[\\s*\\{.*?\\}\\s*\\])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?is)\[\s*\{.*?\}\s*\]`)
	reArrayHead      = regexp.MustCompile(`^\[\s*\{`)
	reArrayOpenSpace = regexp.MustCompile(`^\[\s+\{`)
	reInvisibleRunes = regexp.MustCompile("[\u200B\u200C\u200D\uFEFF]")

	// XML tag extraction (supports any characters in reasoning chain)
	reReasoningTag = regexp.MustCompile(`(?s)<reasoning>(.*?)</reasoning>`)
	reDecisionTag  = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
)

// ============================================================================
// Entry Functions - Main API
// ============================================================================

// GetFullDecision gets AI's complete trading decision (batch analysis of all coins and positions)
// Uses default strategy configuration - for production use GetFullDecisionWithStrategy with explicit config
func GetFullDecision(ctx *Context, mcpClient mcp.AIClient) (*FullDecision, error) {
	defaultConfig := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&defaultConfig)
	return GetFullDecisionWithStrategy(ctx, mcpClient, engine, "")
}

// callAIWithMetadata prefers the correlated MCP entry point so retries and the
// resulting FullDecision share one logical CallID. The plain interface remains
// a compatibility fallback for legacy clients; those clients may create their
// own provider-local diagnostic ID because they cannot accept metadata.
func callAIWithMetadata(client mcp.AIClient, metadata mcp.CallMetadata, systemPrompt, userPrompt string) (string, error) {
	if correlated, ok := client.(mcp.CorrelatedAIClient); ok {
		return correlated.CallWithMessagesWithMetadata(metadata, systemPrompt, userPrompt)
	}
	return client.CallWithMessages(systemPrompt, userPrompt)
}

type contextAwareAIClient interface {
	CallWithMessagesWithMetadataContext(context.Context, mcp.CallMetadata, string, string) (string, error)
}

func callAIWithMetadataContext(requestCtx context.Context, client mcp.AIClient, metadata mcp.CallMetadata, systemPrompt, userPrompt string) (string, error) {
	if cancellable, ok := client.(contextAwareAIClient); ok {
		return cancellable.CallWithMessagesWithMetadataContext(requestCtx, metadata, systemPrompt, userPrompt)
	}
	return callAIWithMetadata(client, metadata, systemPrompt, userPrompt)
}

func newLogicalCallID() string {
	return uuid.NewString()
}

func resolveDualBrainClients(client mcp.AIClient) (macroClient mcp.AIClient, jevClient *provider.JevClient) {
	if client == nil {
		return nil, nil
	}
	if jev, ok := client.(*provider.JevClient); ok {
		return nil, jev
	}
	if failover, ok := client.(interface{ CandidateClients() []mcp.AIClient }); ok {
		candidates := failover.CandidateClients()
		for _, c := range candidates {
			if jev, ok := c.(*provider.JevClient); ok && jevClient == nil {
				jevClient = jev
			} else if macroClient == nil {
				if embedder, ok := c.(mcp.ClientEmbedder); ok && embedder.BaseClient() != nil && embedder.BaseClient().Provider != mcp.ProviderJev {
					macroClient = c
				}
			}
		}
	}
	if jevClient == nil {
		jevClient = resolveJevClient(client)
	}
	return macroClient, jevClient
}

func resolveJevClient(client mcp.AIClient) *provider.JevClient {
	if client == nil {
		return nil
	}
	if jev, ok := client.(*provider.JevClient); ok {
		return jev
	}
	if failover, ok := client.(interface{ ActiveClient() mcp.AIClient }); ok {
		if active := failover.ActiveClient(); active != nil {
			if jev, ok := active.(*provider.JevClient); ok {
				return jev
			}
		}
	}
	if embedder, ok := client.(mcp.ClientEmbedder); ok {
		if base := embedder.BaseClient(); base != nil && base.Provider == mcp.ProviderJev {
			if jev, ok := client.(*provider.JevClient); ok {
				return jev
			}
		}
	}
	return nil
}

// GetFullDecisionWithStrategy uses StrategyEngine to get AI decision (unified prompt generation)
func GetFullDecisionWithStrategy(ctx *Context, mcpClient mcp.AIClient, engine *StrategyEngine, variant string) (*FullDecision, error) {
	return GetFullDecisionWithStrategyContext(context.Background(), ctx, mcpClient, engine, variant)
}

// GetFullDecisionWithStrategyContext is the cancellable decision entry point
// used by the trader lifecycle. The legacy entry point above remains available
// for backtests and callers that do not need cancellation.
func GetFullDecisionWithStrategyContext(requestCtx context.Context, ctx *Context, mcpClient mcp.AIClient, engine *StrategyEngine, variant string) (*FullDecision, error) {
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is nil")
	}
	if engine == nil {
		defaultConfig := store.GetDefaultStrategyConfig("en")
		engine = NewStrategyEngine(&defaultConfig)
	}

	// Clamp strategy limits to prevent token overflow
	engineConfig := engine.GetConfig()
	engineConfig.ClampLimits()

	// Token estimation check — block if exceeding the specific model's context limit
	estimate := engineConfig.EstimateTokens()

	// Determine context limit for the specific model being used
	contextLimit := 131072 // safe default (strictest common limit)
	var providerName string
	if embedder, ok := mcpClient.(mcp.ClientEmbedder); ok {
		base := embedder.BaseClient()
		providerName = base.Provider
		contextLimit = store.GetContextLimitForClient(base.Provider, base.Model)
	}

	if estimate.Total > contextLimit {
		logger.Errorf("🚫 Token estimate %d exceeds %s context limit %d — blocking analysis",
			estimate.Total, providerName, contextLimit)
		return nil, fmt.Errorf("estimated %d tokens exceeds model context limit of %d; reduce coins, timeframes, or K-line count",
			estimate.Total, contextLimit)
	}
	if estimate.Total*100/contextLimit >= 80 {
		logger.Infof("⚠️  Token estimate %d — approaching %s context limit %d",
			estimate.Total, providerName, contextLimit)
	}

	// 1. Fetch market data using strategy config
	if len(ctx.MarketDataMap) == 0 {
		if err := fetchMarketDataWithStrategyContext(requestCtx, ctx, engine); err != nil {
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}
	} else if ctx.RequireFreshMarketData && !ctx.MarketDataFetchedFresh {
		return nil, &MarketDataUnavailableError{Cause: fmt.Errorf("preloaded market data has no freshness proof")}
	}
	pruneCandidateCoinsWithoutMarketData(ctx)

	// Direct route for Jev and Dual-Brain (System 1 + 2)
	macroClient, jevClient := resolveDualBrainClients(mcpClient)
	if jevClient != nil {
		if macroClient != nil {
			return GetDualBrainDecision(requestCtx, ctx, macroClient, jevClient, engine)
		}
		return GetJevDecisionWithStrategyContext(requestCtx, ctx, jevClient, engine)
	}

	// 2. Build System Prompt using strategy engine
	riskConfig := engine.GetRiskControlConfig()
	systemPrompt := engine.BuildSystemPrompt(ctx.Account.TotalEquity, variant, ctx.Account.AvailableBalance)

	// 3. Build User Prompt using strategy engine
	userPrompt := engine.BuildUserPrompt(ctx)

	// 4. Call AI API. Generate one ID at the logical call boundary and pass it
	// through MCP retries; do not generate a second ID after the response.
	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	callID := newLogicalCallID()
	metadata := mcp.CallMetadata{
		CallID:     callID,
		CycleID:    ctx.CycleID,
		TraderID:   ctx.TraderID,
		StrategyID: ctx.StrategyID,
	}
	aiCallStart := time.Now()
	if ctx.OnAICallStarted != nil {
		ctx.OnAICallStarted(callID)
	}
	aiResponse, err := callAIWithMetadataContext(requestCtx, mcpClient, metadata, systemPrompt, userPrompt)
	aiCallDuration := time.Since(aiCallStart)
	if err != nil {
		// Keep the call ID and prompts available to the trader's failure record so
		// the MCP failure log remains correlated even when no response exists.
		return &FullDecision{
			CallID:              callID,
			SystemPrompt:        systemPrompt,
			UserPrompt:          userPrompt,
			AIRequestDurationMs: aiCallDuration.Milliseconds(),
		}, fmt.Errorf("AI API call failed: %w", err)
	}

	// 5. Parse AI response
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, riskConfig)

	if decision != nil {
		decision.CallID = callID
		decision.Timestamp = time.Now()
		decision.SystemPrompt = systemPrompt
		decision.UserPrompt = userPrompt
		decision.AIRequestDurationMs = aiCallDuration.Milliseconds()
		decision.RawResponse = aiResponse
	}

	if err != nil {
		return decision, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return decision, nil
}

// ============================================================================
// Market Data Fetching
// ============================================================================

// fetchMarketDataWithStrategy fetches market data using strategy config (multiple timeframes)
func fetchMarketDataWithStrategy(ctx *Context, engine *StrategyEngine) error {
	return fetchMarketDataWithStrategyContext(context.Background(), ctx, engine)
}

func fetchMarketDataWithStrategyContext(requestCtx context.Context, ctx *Context, engine *StrategyEngine) error {
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	config := engine.GetConfig()
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.MarketDataFetchedFresh = false

	timeframes := config.Indicators.Klines.SelectedTimeframes
	primaryTimeframe := config.Indicators.Klines.PrimaryTimeframe
	klineCount := config.Indicators.Klines.PrimaryCount

	// Compatible with old configuration
	if len(timeframes) == 0 {
		if primaryTimeframe != "" {
			timeframes = append(timeframes, primaryTimeframe)
		} else {
			timeframes = append(timeframes, "3m")
		}
		if config.Indicators.Klines.LongerTimeframe != "" {
			timeframes = append(timeframes, config.Indicators.Klines.LongerTimeframe)
		}
	}
	if primaryTimeframe == "" {
		primaryTimeframe = timeframes[0]
	}
	if klineCount <= 0 {
		klineCount = 30
	}

	logger.Infof("📊 Strategy timeframes: %v, Primary: %s, Kline count: %d", timeframes, primaryTimeframe, klineCount)
	marketDataProvider := engine.MarketDataProvider()
	if marketDataProvider == nil {
		return fmt.Errorf("strategy market data provider is unavailable")
	}
	marketExchange := engine.Exchange()

	// 1. First fetch data for position coins (must fetch)
	for _, pos := range ctx.Positions {
		if err := requestCtx.Err(); err != nil {
			return err
		}
		var data *market.Data
		var err error
		if _, contractErr := engine.ValidateCandidateContract(pos.Symbol); contractErr != nil {
			return &MarketDataUnavailableError{Symbols: []string{pos.Symbol}, Cause: contractErr}
		}
		data, err = market.GetWithTimeframesForProviderContext(requestCtx, marketDataProvider, pos.Symbol, timeframes, primaryTimeframe, klineCount)
		if err != nil {
			logger.Infof("Failed to fetch %s market data for position %s: %v", marketExchange, pos.Symbol, err)
			if ctx.RequireFreshMarketData {
				return &MarketDataUnavailableError{Symbols: []string{pos.Symbol}, Cause: err}
			}
			continue
		}
		ctx.MarketDataMap[pos.Symbol] = data
	}

	// 2. Fetch data for all candidate coins
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	const minOIThresholdMillions = 15.0 // 15M USD minimum open interest value
	candidateSeen := make(map[string]struct{}, len(ctx.CandidateCoins))
	candidateAttempts := 0
	freshCandidates := 0
	failedCandidates := make([]string, 0)

	for _, coin := range ctx.CandidateCoins {
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if _, duplicate := candidateSeen[coin.Symbol]; duplicate {
			continue
		}
		candidateSeen[coin.Symbol] = struct{}{}
		candidateAttempts++
		if _, exists := ctx.MarketDataMap[coin.Symbol]; exists {
			freshCandidates++
			continue
		}

		var data *market.Data
		var err error
		if _, contractErr := engine.ValidateCandidateContract(coin.Symbol); contractErr != nil {
			logger.Infof("Skipping %s candidate %s: %v", marketExchange, coin.Symbol, contractErr)
			failedCandidates = append(failedCandidates, coin.Symbol)
			continue
		}
		data, err = market.GetWithTimeframesForProviderContext(requestCtx, marketDataProvider, coin.Symbol, timeframes, primaryTimeframe, klineCount)
		if err != nil {
			logger.Infof("Failed to fetch %s market data for %s: %v", marketExchange, coin.Symbol, err)
			failedCandidates = append(failedCandidates, coin.Symbol)
			continue
		}
		freshCandidates++

		// Liquidity filter (skip for xyz dex assets - they don't have OI data from Binance)
		isExistingPosition := positionSymbols[coin.Symbol]
		isXyzAsset := market.IsXyzDexAsset(coin.Symbol)
		if !isExistingPosition && !isXyzAsset && data.OpenInterest != nil && data.CurrentPrice > 0 {
			oiValue := data.OpenInterest.NotionalUSD
			if oiValue <= 0 {
				oiValue = data.OpenInterest.Latest * data.CurrentPrice
			}
			oiValueInMillions := oiValue / 1_000_000
			if oiValueInMillions < minOIThresholdMillions {
				logger.Infof("⚠️  %s OI value too low (%.2fM USD < %.1fM), skipping coin",
					coin.Symbol, oiValueInMillions, minOIThresholdMillions)
				continue
			}
		}

		ctx.MarketDataMap[coin.Symbol] = data
	}
	if ctx.RequireFreshMarketData {
		if err := ensureFreshCandidateCoverage(candidateAttempts, freshCandidates, failedCandidates); err != nil {
			return err
		}
	}
	// The provider contract is fresh-only even for paper analysis. An empty
	// candidate universe or a batch where every symbol failed must never reach
	// the AI with an empty MarketDataMap; that path was previously recorded as a
	// successful no-op and made market-data outages look like model decisions.
	if err := ensureDecisionMarketData(ctx, candidateAttempts, failedCandidates); err != nil {
		return err
	}

	logger.Infof("📊 Successfully fetched multi-timeframe market data for %d coins", len(ctx.MarketDataMap))
	ctx.MarketDataFetchedFresh = ctx.RequireFreshMarketData
	return nil
}

func ensureDecisionMarketData(ctx *Context, attempted int, failedSymbols []string) error {
	if ctx != nil && len(ctx.MarketDataMap) > 0 {
		return nil
	}
	return &MarketDataUnavailableError{
		Symbols: append([]string(nil), failedSymbols...),
		Cause:   fmt.Errorf("no usable fresh market data for %d candidate coins", attempted),
	}
}

func ensureFreshCandidateCoverage(attempted, successful int, failedSymbols []string) error {
	if attempted <= 0 {
		return nil
	}
	if successful < 0 {
		successful = 0
	}
	if successful > attempted {
		successful = attempted
	}
	if successful*100 >= attempted*minFreshCandidateCoveragePercent {
		return nil
	}
	return &MarketDataUnavailableError{
		Symbols: append([]string(nil), failedSymbols...),
		Cause:   fmt.Errorf("fresh candidate coverage %d/%d is below %d%%", successful, attempted, minFreshCandidateCoveragePercent),
	}
}

func pruneCandidateCoinsWithoutMarketData(ctx *Context) {
	if ctx == nil || len(ctx.CandidateCoins) == 0 || len(ctx.MarketDataMap) == 0 {
		return
	}
	kept := make([]CandidateCoin, 0, len(ctx.CandidateCoins))
	for _, coin := range ctx.CandidateCoins {
		if _, ok := ctx.MarketDataMap[coin.Symbol]; ok {
			kept = append(kept, coin)
			continue
		}
		logger.Infof("⚠️  Skipping candidate %s in AI prompt: no valid market/K-line data", coin.Symbol)
	}
	ctx.CandidateCoins = kept
}

// ============================================================================
// AI Response Parsing
// ============================================================================

func parseFullDecisionResponse(aiResponse string, accountEquity float64, riskConfig store.RiskControlConfig) (*FullDecision, error) {
	cotTrace := extractCoTTrace(aiResponse)

	rawDecisions, err := extractRawDecisionItems(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("failed to extract decisions: %w", err)
	}

	decisions := make([]Decision, 0, len(rawDecisions))
	originalIndexes := make([]int, 0, len(rawDecisions))
	rejected := make([]RejectedDecision, 0)
	for index, rawDecision := range rawDecisions {
		var decision Decision
		if err := json.Unmarshal(rawDecision, &decision); err != nil {
			var identity struct {
				Symbol string `json:"symbol"`
				Action string `json:"action"`
			}
			_ = json.Unmarshal(rawDecision, &identity)
			rejected = append(rejected, RejectedDecision{
				Index:    index,
				Decision: Decision{Symbol: identity.Symbol, Action: identity.Action},
				Reason:   err.Error(),
			})
			continue
		}
		decisions = append(decisions, decision)
		originalIndexes = append(originalIndexes, index)
	}

	validated, validationRejected := validateDecisionBatchSafely(decisions, accountEquity, riskConfig)
	for index := range validationRejected {
		localIndex := validationRejected[index].Index
		if localIndex >= 0 && localIndex < len(originalIndexes) {
			validationRejected[index].Index = originalIndexes[localIndex]
		}
	}
	rejected = append(rejected, validationRejected...)

	// A per-item parse failure degrades the batch just like a validation
	// failure. Keep only independently validated closes; never execute an open
	// or update action beside a rejected sibling.
	if len(rejected) > 0 && len(validationRejected) == 0 {
		safeCloses := make([]Decision, 0, len(validated))
		for index, decision := range validated {
			if isRiskReducingClose(decision) {
				safeCloses = append(safeCloses, decision)
				continue
			}
			rejected = append(rejected, RejectedDecision{
				Index:    originalIndexes[index],
				Decision: decision,
				Reason:   "batch degraded after another action failed parsing; only validated close actions are retained",
			})
		}
		validated = safeCloses
	}

	if len(rejected) > 0 {
		decision := &FullDecision{
			CoTTrace:          cotTrace,
			Decisions:         validated,
			RejectedDecisions: rejected,
			Degraded:          true,
		}
		if len(validated) == 0 {
			return decision, fmt.Errorf("decision validation failed: %s", rejected[0].Reason)
		}
		return decision, nil
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: validated,
	}, nil
}

func isRiskReducingClose(decision Decision) bool {
	return decision.Action == "close_long" || decision.Action == "close_short"
}

func validateDecisionBatchSafely(decisions []Decision, accountEquity float64, riskConfig store.RiskControlConfig) ([]Decision, []RejectedDecision) {
	validated := make([]Decision, 0, len(decisions))
	rejected := make([]RejectedDecision, 0)
	validClose := make([]Decision, 0, len(decisions))

	for index := range decisions {
		decision := decisions[index]
		if err := validateDecisionWithRisk(&decision, accountEquity, riskConfig); err != nil {
			rejected = append(rejected, RejectedDecision{Index: index, Decision: decisions[index], Reason: err.Error()})
			continue
		}
		validated = append(validated, decision)
		if isRiskReducingClose(decision) {
			validClose = append(validClose, decision)
		}
	}
	if len(rejected) == 0 {
		return validated, nil
	}

	// A partially invalid batch is degraded. Only independently validated close
	// actions may continue; opening and update_position actions are dropped so a
	// malformed sibling cannot bypass portfolio-level risk intent.
	for index, decision := range decisions {
		if isRiskReducingClose(decision) {
			continue
		}
		alreadyRejected := false
		for _, item := range rejected {
			if item.Index == index {
				alreadyRejected = true
				break
			}
		}
		if !alreadyRejected {
			rejected = append(rejected, RejectedDecision{
				Index:    index,
				Decision: decision,
				Reason:   "batch degraded after another action failed validation; only validated close actions are retained",
			})
		}
	}
	return validClose, rejected
}

func extractCoTTrace(response string) string {
	if match := reReasoningTag.FindStringSubmatch(response); match != nil && len(match) > 1 {
		logger.Infof("✓ Extracted reasoning chain using <reasoning> tag")
		return strings.TrimSpace(match[1])
	}

	if decisionIdx := strings.Index(response, "<decision>"); decisionIdx > 0 {
		logger.Infof("✓ Extracted content before <decision> tag as reasoning chain")
		return strings.TrimSpace(response[:decisionIdx])
	}

	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		logger.Infof("⚠️  Extracted reasoning chain using old format ([ character separator)")
		return strings.TrimSpace(response[:jsonStart])
	}

	return strings.TrimSpace(response)
}

func extractDecisions(response string) ([]Decision, error) {
	rawDecisions, err := extractRawDecisionItems(response)
	if err != nil {
		return nil, err
	}
	decisions := make([]Decision, 0, len(rawDecisions))
	for _, rawDecision := range rawDecisions {
		var decision Decision
		if err := json.Unmarshal(rawDecision, &decision); err != nil {
			return nil, fmt.Errorf("JSON decision parsing failed: %w\nJSON item: %s", err, rawDecision)
		}
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func extractRawDecisionItems(response string) ([]json.RawMessage, error) {
	s := removeInvisibleRunes(response)
	s = strings.TrimSpace(s)
	s = fixEnglishNumberWords(s)
	s = fixMissingQuotes(s)

	var jsonPart string
	if match := reDecisionTag.FindStringSubmatch(s); match != nil && len(match) > 1 {
		jsonPart = strings.TrimSpace(match[1])
		logger.Infof("✓ Extracted JSON using <decision> tag")
	} else {
		jsonPart = s
		logger.Infof("⚠️  <decision> tag not found, searching JSON in full text")
	}

	jsonPart = fixEnglishNumberWords(jsonPart)
	jsonPart = fixMissingQuotes(jsonPart)

	jsonContent := ""
	if m := reJSONFence.FindStringSubmatch(jsonPart); m != nil && len(m) > 1 {
		jsonContent = strings.TrimSpace(m[1])
	} else {
		jsonContent = strings.TrimSpace(reJSONArray.FindString(jsonPart))
	}

	if jsonContent == "" {
		logger.Infof("⚠️  [SafeFallback] AI didn't output JSON decision, entering safe wait mode")
		cotSummary := jsonPart
		if len(cotSummary) > 240 {
			cotSummary = cotSummary[:240] + "..."
		}
		fallbackJSON, err := json.Marshal([]Decision{{
			Symbol:    "ALL",
			Action:    "wait",
			Reasoning: fmt.Sprintf("Model didn't output structured JSON decision, entering safe wait; summary: %s", cotSummary),
		}})
		if err != nil {
			return nil, fmt.Errorf("failed to build safe fallback decision: %w", err)
		}
		jsonContent = string(fallbackJSON)
	}

	jsonContent = compactArrayOpen(jsonContent)
	jsonContent = fixEnglishNumberWords(jsonContent)
	jsonContent = fixMissingQuotes(jsonContent)
	if err := validateJSONFormat(jsonContent); err != nil {
		return nil, fmt.Errorf("JSON format validation failed: %w\nJSON content: %s\nFull response:\n%s", err, jsonContent, response)
	}

	var rawDecisions []json.RawMessage
	if err := json.Unmarshal([]byte(jsonContent), &rawDecisions); err != nil {
		return nil, fmt.Errorf("JSON parsing failed: %w\nJSON content: %s", err, jsonContent)
	}
	return rawDecisions, nil
}

var englishNumberWords = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
	"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
	"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14,
	"fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
	"hundred": 100, "one hundred": 100,
}

var reConfidenceWord = regexp.MustCompile(`(?i)"confidence"\s*:\s*([a-zA-Z\-]+(?:\s+[a-zA-Z\-]+)?)`)

func parseEnglishNumber(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if val, ok := englishNumberWords[s]; ok {
		return val, true
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == ' '
	})
	if len(parts) == 2 {
		tens, ok1 := englishNumberWords[parts[0]]
		units, ok2 := englishNumberWords[parts[1]]
		if ok1 && ok2 {
			return tens + units, true
		}
	}
	return 0, false
}

func fixEnglishNumberWords(jsonStr string) string {
	return reConfidenceWord.ReplaceAllStringFunc(jsonStr, func(match string) string {
		sub := reConfidenceWord.FindStringSubmatch(match)
		if len(sub) > 1 {
			word := strings.TrimSpace(sub[1])
			if num, ok := parseEnglishNumber(word); ok {
				return fmt.Sprintf(`"confidence": %d`, num)
			}
		}
		return match
	})
}

func fixMissingQuotes(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")

	jsonStr = strings.ReplaceAll(jsonStr, "［", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "］", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "｛", "{")
	jsonStr = strings.ReplaceAll(jsonStr, "｝", "}")
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":")
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",")

	jsonStr = strings.ReplaceAll(jsonStr, "【", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "】", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "〔", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "〕", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "、", ",")

	jsonStr = strings.ReplaceAll(jsonStr, "　", " ")

	return jsonStr
}

func validateJSONFormat(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)

	if !reArrayHead.MatchString(trimmed) {
		if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed[:min(20, len(trimmed))], "{") {
			return fmt.Errorf("not a valid decision array (must contain objects {}), actual content: %s", trimmed[:min(50, len(trimmed))])
		}
		return fmt.Errorf("JSON must start with [{ (whitespace allowed), actual: %s", trimmed[:min(20, len(trimmed))])
	}

	if strings.Contains(jsonStr, "~") {
		return fmt.Errorf("JSON cannot contain range symbol ~, all numbers must be precise single values")
	}

	for i := 0; i < len(jsonStr)-4; i++ {
		if jsonStr[i] >= '0' && jsonStr[i] <= '9' &&
			jsonStr[i+1] == ',' &&
			jsonStr[i+2] >= '0' && jsonStr[i+2] <= '9' &&
			jsonStr[i+3] >= '0' && jsonStr[i+3] <= '9' &&
			jsonStr[i+4] >= '0' && jsonStr[i+4] <= '9' {
			return fmt.Errorf("JSON numbers cannot contain thousand separator comma, found: %s", jsonStr[i:min(i+10, len(jsonStr))])
		}
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func removeInvisibleRunes(s string) string {
	return reInvisibleRunes.ReplaceAllString(s, "")
}

func compactArrayOpen(s string) string {
	return reArrayOpenSpace.ReplaceAllString(strings.TrimSpace(s), "[{")
}
