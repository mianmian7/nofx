package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nofx/logger"
	"nofx/market"
	"nofx/provider/hyperliquid"
	"nofx/security"
	"nofx/store"
	"sort"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// Type Definitions
// ============================================================================

// PositionInfo position information
type PositionInfo struct {
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"` // "long" or "short"
	EntryPrice    float64 `json:"entry_price"`
	MarkPrice     float64 `json:"mark_price"`
	Quantity      float64 `json:"quantity"`
	Leverage      int     `json:"leverage"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	// UnrealizedPnLPct is Margin/Position PnL: unrealized PnL divided by
	// initial margin, expressed as a percentage. It is not price PnL.
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	// PricePnLPct is the unlevered price move, shown for context only and never
	// used for Margin/Position PnL risk gates.
	PricePnLPct       float64 `json:"price_pnl_pct"`
	PeakPnLPct        float64 `json:"peak_pnl_pct"` // Historical peak Margin/Position PnL percentage
	LiquidationPrice  float64 `json:"liquidation_price"`
	MarginUsed        float64 `json:"margin_used"`
	StopLoss          float64 `json:"stop_loss,omitempty"`   // Absolute exchange trigger price
	TakeProfit        float64 `json:"take_profit,omitempty"` // Absolute exchange trigger price; not a PnL percentage
	StopLossState     string  `json:"stop_loss_state,omitempty"`
	TakeProfitState   string  `json:"take_profit_state,omitempty"`
	ProtectionWarning string  `json:"protection_warning,omitempty"`
	UpdateTime        int64   `json:"update_time"` // Position update timestamp (milliseconds)
}

// AccountInfo account information
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // Account equity
	AvailableBalance float64 `json:"available_balance"` // Available balance
	UnrealizedPnL    float64 `json:"unrealized_pnl"`    // Unrealized profit/loss
	TotalPnL         float64 `json:"total_pnl"`         // Total profit/loss
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // Total profit/loss percentage
	MarginUsed       float64 `json:"margin_used"`       // Used margin
	MarginUsedPct    float64 `json:"margin_used_pct"`   // Margin usage rate
	PositionCount    int     `json:"position_count"`    // Number of positions
}

// CandidateCoin candidate coin (from coin pool)
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"`
}

// TradingStats trading statistics (for AI input)
type TradingStats struct {
	TotalTrades    int     `json:"total_trades"`     // Total number of trades (closed)
	WinRate        float64 `json:"win_rate"`         // Win rate (%)
	ProfitFactor   float64 `json:"profit_factor"`    // Profit factor
	SharpeRatio    float64 `json:"sharpe_ratio"`     // Sharpe ratio
	TotalPnL       float64 `json:"total_pnl"`        // Total profit/loss
	AvgWin         float64 `json:"avg_win"`          // Average win
	AvgLoss        float64 `json:"avg_loss"`         // Average loss
	MaxDrawdownPct float64 `json:"max_drawdown_pct"` // Maximum drawdown (%)
}

// RecentOrder recently completed order (for AI input)
type RecentOrder struct {
	Symbol       string  `json:"symbol"`        // Trading pair
	Side         string  `json:"side"`          // long/short
	EntryPrice   float64 `json:"entry_price"`   // Entry price
	ExitPrice    float64 `json:"exit_price"`    // Exit price
	RealizedPnL  float64 `json:"realized_pnl"`  // Realized profit/loss
	PnLPct       float64 `json:"pnl_pct"`       // Profit/loss percentage
	EntryTime    string  `json:"entry_time"`    // Entry time
	ExitTime     string  `json:"exit_time"`     // Exit time
	HoldDuration string  `json:"hold_duration"` // Hold duration, e.g. "2h30m"
}

// Context trading context (complete information passed to AI)
type Context struct {
	CurrentTime    string                             `json:"current_time"`
	RuntimeMinutes int                                `json:"runtime_minutes"`
	CallCount      int                                `json:"call_count"`
	TraderID       string                             `json:"trader_id"`
	StrategyID     string                             `json:"strategy_id"`
	Account        AccountInfo                        `json:"account"`
	Positions      []PositionInfo                     `json:"positions"`
	CandidateCoins []CandidateCoin                    `json:"candidate_coins"`
	PromptVariant  string                             `json:"prompt_variant,omitempty"`
	TradingStats   *TradingStats                      `json:"trading_stats,omitempty"`
	RecentOrders   []RecentOrder                      `json:"recent_orders,omitempty"`
	MarketDataMap  map[string]*market.Data            `json:"-"`
	MultiTFMarket  map[string]map[string]*market.Data `json:"-"`
	// RequireFreshMarketData is set by live traders. When enabled, the strategy
	// fetcher fails closed instead of using a public K-line snapshot that was
	// retained through an upstream outage.
	RequireFreshMarketData bool     `json:"-"`
	MarketDataFetchedFresh bool     `json:"-"`
	Timeframes             []string `json:"-"`
}

// Decision AI trading decision
type Decision struct {
	Symbol string `json:"symbol"`
	Action string `json:"action"` // Standard: "open_long", "open_short", "close_long", "close_short", "update_position", "hold", "wait"
	// Grid actions: "place_buy_limit", "place_sell_limit", "cancel_order", "cancel_all_orders", "pause_grid", "resume_grid", "adjust_grid"

	// Opening position parameters
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`       // Absolute exchange trigger price
	TakeProfit      float64 `json:"take_profit,omitempty"`     // Absolute exchange trigger price; not a PnL percentage
	NewStopLoss     float64 `json:"new_stop_loss,omitempty"`   // Absolute exchange trigger price
	NewTakeProfit   float64 `json:"new_take_profit,omitempty"` // Absolute exchange trigger price; not a PnL percentage

	// Grid trading parameters
	Price      float64 `json:"price,omitempty"`       // Limit order price (for grid)
	Quantity   float64 `json:"quantity,omitempty"`    // Order quantity (for grid)
	LevelIndex int     `json:"level_index,omitempty"` // Grid level index
	OrderID    string  `json:"order_id,omitempty"`    // Order ID (for cancel)

	// Common parameters
	Confidence int     `json:"confidence,omitempty"` // Confidence level (0-100)
	RiskUSD    float64 `json:"risk_usd,omitempty"`   // Maximum USD risk
	Reasoning  string  `json:"reasoning"`
}

// FullDecision AI's complete decision (including chain of thought)
type FullDecision struct {
	CallID              string     `json:"call_id,omitempty"`
	SystemPrompt        string     `json:"system_prompt"`
	UserPrompt          string     `json:"user_prompt"`
	CoTTrace            string     `json:"cot_trace"`
	Decisions           []Decision `json:"decisions"`
	RawResponse         string     `json:"raw_response"`
	Timestamp           time.Time  `json:"timestamp"`
	AIRequestDurationMs int64      `json:"ai_request_duration_ms,omitempty"`
}

// ============================================================================
// StrategyEngine - Core Strategy Execution Engine
// ============================================================================

// StrategyEngine strategy execution engine
type StrategyEngine struct {
	config             *store.StrategyConfig
	binanceCandidates  func(limit int) ([]string, error)
	marketDataProvider market.MarketDataProvider
	exchange           string
	contractCacheMu    sync.Mutex
	contractCache      map[string]*market.ContractSpec
	contractCacheAt    map[string]time.Time
}

// NewStrategyEngine creates a Binance-backed strategy engine for backwards
// compatibility with callers that do not specify an exchange.
func NewStrategyEngine(config *store.StrategyConfig) *StrategyEngine {
	return NewStrategyEngineForExchange(config, "binance")
}

// NewStrategyEngineForExchange creates a strategy engine whose public market
// data and contract checks use the requested exchange. It deliberately keeps
// the requested exchange when provider construction fails instead of silently
// switching analysis to Binance.
func NewStrategyEngineForExchange(config *store.StrategyConfig, exchange string) *StrategyEngine {
	normalizedExchange := strings.ToLower(strings.TrimSpace(exchange))
	if normalizedExchange == "" {
		normalizedExchange = "binance"
	}
	var binanceCandidates func(limit int) ([]string, error)
	if normalizedExchange == "binance" {
		binanceClient := market.NewAPIClient()
		binanceCandidates = binanceClient.GetBinanceDynamicSymbols
	}
	marketDataProvider, providerErr := market.NewMarketDataProvider(normalizedExchange)
	if providerErr != nil {
		logger.Warnf("Unable to create %s market provider; keeping the requested exchange unavailable: %v", normalizedExchange, providerErr)
		marketDataProvider = market.NewUnavailableMarketDataProvider(normalizedExchange, providerErr)
	}
	return &StrategyEngine{
		config:             config,
		binanceCandidates:  binanceCandidates,
		marketDataProvider: marketDataProvider,
		exchange:           normalizedExchange,
		contractCache:      make(map[string]*market.ContractSpec),
		contractCacheAt:    make(map[string]time.Time),
	}
}

// GetRiskControlConfig gets risk control configuration
func (e *StrategyEngine) GetRiskControlConfig() store.RiskControlConfig {
	return e.config.RiskControl
}

// GetLanguage returns the language from config or falls back to auto-detection
func (e *StrategyEngine) GetLanguage() Language {
	switch e.config.Language {
	case "zh":
		return LangChinese
	case "en":
		return LangEnglish
	default:
		// Fall back to auto-detection from prompt content for backward compatibility
		return detectLanguage(e.config.PromptSections.RoleDefinition)
	}
}

// GetConfig gets complete strategy configuration
func (e *StrategyEngine) GetConfig() *store.StrategyConfig {
	return e.config
}

// MarketDataProvider returns the exchange-specific public market-data source
// used by strategy analysis and paper trading.
func (e *StrategyEngine) MarketDataProvider() market.MarketDataProvider {
	if e == nil {
		return nil
	}
	return e.marketDataProvider
}

func (e *StrategyEngine) Exchange() string {
	if e == nil || strings.TrimSpace(e.exchange) == "" {
		return "binance"
	}
	return e.exchange
}

func (e *StrategyEngine) normalizeCandidateSymbol(symbol string) string {
	if e != nil && e.marketDataProvider != nil {
		return e.marketDataProvider.NormalizeSymbol(symbol)
	}
	return market.NormalizeForExchange("binance", symbol)
}

func (e *StrategyEngine) isHyperliquidExchange() bool {
	if e == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(e.exchange)) {
	case "hyperliquid", "hyperliquid-xyz", "xyz":
		return true
	default:
		return false
	}
}

// ValidateCandidateContract verifies that a symbol is an active USDT
// perpetual on a native unified venue. Legacy CoinAnk-only venues retain
// their existing K-line validation path until native contract metadata exists.
func (e *StrategyEngine) ValidateCandidateContract(symbol string) (*market.ContractSpec, error) {
	if e == nil || e.marketDataProvider == nil {
		return nil, fmt.Errorf("strategy market-data provider is unavailable")
	}
	if e.Exchange() != "binance" && e.Exchange() != "okx" && e.Exchange() != "bitget" {
		return nil, nil
	}
	normalizedSymbol := e.marketDataProvider.NormalizeSymbol(symbol)
	const contractCacheTTL = 10 * time.Minute
	e.contractCacheMu.Lock()
	if e.contractCache == nil {
		e.contractCache = make(map[string]*market.ContractSpec)
	}
	if e.contractCacheAt == nil {
		e.contractCacheAt = make(map[string]time.Time)
	}
	if cachedSpec, found := e.contractCache[normalizedSymbol]; found && time.Since(e.contractCacheAt[normalizedSymbol]) < contractCacheTTL {
		e.contractCacheMu.Unlock()
		return cachedSpec, nil
	}
	e.contractCacheMu.Unlock()

	spec, err := e.marketDataProvider.GetContractSpec(normalizedSymbol)
	if err != nil {
		return nil, fmt.Errorf("verify %s contract %s: %w", e.Exchange(), normalizedSymbol, err)
	}
	if spec == nil {
		return nil, fmt.Errorf("verify %s contract %s: empty contract metadata", e.Exchange(), normalizedSymbol)
	}
	if spec.Status != "" && !isActiveContractStatus(spec.Status) {
		return nil, fmt.Errorf("%s contract %s is not active (status=%s)", e.Exchange(), normalizedSymbol, spec.Status)
	}
	if spec.QuoteAsset != "" && !strings.EqualFold(spec.QuoteAsset, "USDT") {
		return nil, fmt.Errorf("%s contract %s settles in %s, not USDT", e.Exchange(), normalizedSymbol, spec.QuoteAsset)
	}
	e.contractCacheMu.Lock()
	e.contractCache[normalizedSymbol] = spec
	e.contractCacheAt[normalizedSymbol] = time.Now()
	e.contractCacheMu.Unlock()
	return spec, nil
}

func isActiveContractStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "trading", "live", "normal", "enabled", "online":
		return true
	default:
		return false
	}
}

// ============================================================================
// Candidate Coins
// ============================================================================

// GetCandidateCoins gets candidate coins based on strategy configuration
func (e *StrategyEngine) GetCandidateCoins() ([]CandidateCoin, error) {
	var candidates []CandidateCoin
	symbolSources := make(map[string][]string)

	coinSource := e.config.CoinSource

	switch coinSource.SourceType {
	case "binance_dynamic":
		coins, err := e.getBinanceDynamicCoins(coinSource.BinanceDynamicLimit)
		if err != nil {
			return nil, err
		}
		return e.filterExcludedCoins(coins), nil

	case "static":
		for _, symbol := range coinSource.StaticCoins {
			symbol = market.NormalizeForExchange("binance", symbol)
			candidates = append(candidates, CandidateCoin{
				Symbol:  symbol,
				Sources: []string{"static"},
			})
		}

		return e.filterExcludedCoins(candidates), nil

	case "hyper_all":
		if !e.isHyperliquidExchange() {
			return nil, fmt.Errorf("Hyperliquid candidate source requires a Hyperliquid trader, current exchange is %s", e.Exchange())
		}
		// All Hyperliquid perp coins
		if !coinSource.UseHyperAll {
			logger.Infof("⚠️  source_type is 'hyper_all' but use_hyper_all is false, falling back to static coins")
			for _, symbol := range coinSource.StaticCoins {
				symbol = market.Normalize(symbol)
				candidates = append(candidates, CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"static"},
				})
			}
			return e.filterExcludedCoins(candidates), nil
		}
		coins, err := e.getHyperAllCoins()
		if err != nil {
			return nil, err
		}
		return e.filterExcludedCoins(coins), nil

	case "hyper_main":
		if !e.isHyperliquidExchange() {
			return nil, fmt.Errorf("Hyperliquid candidate source requires a Hyperliquid trader, current exchange is %s", e.Exchange())
		}
		// Top N Hyperliquid coins by 24h volume
		if !coinSource.UseHyperMain {
			logger.Infof("⚠️  source_type is 'hyper_main' but use_hyper_main is false, falling back to static coins")
			for _, symbol := range coinSource.StaticCoins {
				symbol = market.Normalize(symbol)
				candidates = append(candidates, CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"static"},
				})
			}
			return e.filterExcludedCoins(candidates), nil
		}
		coins, err := e.getHyperMainCoins(coinSource.HyperMainLimit)
		if err != nil {
			return nil, err
		}
		return e.filterExcludedCoins(coins), nil

	case "hyper_rank":
		if !e.isHyperliquidExchange() {
			return nil, fmt.Errorf("Hyperliquid candidate source requires a Hyperliquid trader, current exchange is %s", e.Exchange())
		}
		coins, err := e.getHyperRankCoins(coinSource.HyperRankCategory, coinSource.HyperRankDirection, coinSource.HyperRankLimit)
		if err != nil {
			return nil, err
		}
		return e.filterExcludedCoins(coins), nil

	case "mixed":
		if (coinSource.UseHyperAll || coinSource.UseHyperMain) && !e.isHyperliquidExchange() {
			return nil, fmt.Errorf("mixed strategy enables Hyperliquid candidates but trader exchange is %s", e.Exchange())
		}
		if coinSource.UseHyperAll {
			hyperCoins, err := e.getHyperAllCoins()
			if err != nil {
				logger.Infof("⚠️  Failed to get Hyperliquid All coins: %v", err)
			} else {
				for _, coin := range hyperCoins {
					symbolSources[coin.Symbol] = append(symbolSources[coin.Symbol], "hyper_all")
				}
			}
		}

		if coinSource.UseHyperMain {
			hyperMainCoins, err := e.getHyperMainCoins(coinSource.HyperMainLimit)
			if err != nil {
				logger.Infof("⚠️  Failed to get Hyperliquid Main coins: %v", err)
			} else {
				for _, coin := range hyperMainCoins {
					symbolSources[coin.Symbol] = append(symbolSources[coin.Symbol], "hyper_main")
				}
			}
		}

		for _, symbol := range coinSource.StaticCoins {
			symbol = market.NormalizeForExchange("binance", symbol)
			if _, exists := symbolSources[symbol]; !exists {
				symbolSources[symbol] = []string{"static"}
			} else {
				symbolSources[symbol] = append(symbolSources[symbol], "static")
			}
		}

		for symbol, sources := range symbolSources {
			candidates = append(candidates, CandidateCoin{
				Symbol:  symbol,
				Sources: sources,
			})
		}
		return e.filterExcludedCoins(candidates), nil

	default:
		return nil, fmt.Errorf("unknown coin source type: %s", coinSource.SourceType)
	}
}

func (e *StrategyEngine) getBinanceDynamicCoins(limit int) ([]CandidateCoin, error) {
	var symbols []string
	var err error
	if e.Exchange() != "binance" && e.marketDataProvider != nil {
		symbols, err = e.marketDataProvider.ListPerpetualSymbols(limit)
	} else {
		if e.binanceCandidates == nil {
			return nil, fmt.Errorf("Binance dynamic candidate source is unavailable")
		}
		symbols, err = e.binanceCandidates(limit)
	}
	if err != nil {
		return nil, fmt.Errorf("load %s dynamic candidates: %w", e.Exchange(), err)
	}
	candidates := make([]CandidateCoin, 0, len(symbols))
	for _, symbol := range symbols {
		candidates = append(candidates, CandidateCoin{
			Symbol:  e.normalizeCandidateSymbol(symbol),
			Sources: []string{e.Exchange() + "_dynamic"},
		})
	}
	return candidates, nil
}

// filterExcludedCoins removes excluded coins from the candidates list
func (e *StrategyEngine) filterExcludedCoins(candidates []CandidateCoin) []CandidateCoin {
	// Build excluded set for O(1) lookup
	excluded := make(map[string]bool)
	for _, coin := range e.config.CoinSource.ExcludedCoins {
		normalized := e.normalizeCandidateSymbol(coin)
		excluded[normalized] = true
	}

	// Filter out excluded coins
	filtered := make([]CandidateCoin, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Symbol = e.normalizeCandidateSymbol(candidate.Symbol)
		if !excluded[candidate.Symbol] {
			filtered = append(filtered, candidate)
		} else {
			logger.Infof("🚫 Excluded coin: %s", candidate.Symbol)
		}
	}

	return filtered
}

// getHyperAllCoins returns all available Hyperliquid perpetual coins
func (e *StrategyEngine) getHyperAllCoins() ([]CandidateCoin, error) {
	ctx := context.Background()
	symbols, err := hyperliquid.GetAllCoinSymbols(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Hyperliquid coins: %w", err)
	}

	var candidates []CandidateCoin
	for _, symbol := range symbols {
		// Add USDT suffix for compatibility
		normalizedSymbol := market.Normalize(symbol + "USDT")
		candidates = append(candidates, CandidateCoin{
			Symbol:  normalizedSymbol,
			Sources: []string{"hyper_all"},
		})
	}
	logger.Infof("✅ Loaded %d Hyperliquid coins (hyper_all)", len(candidates))
	return candidates, nil
}

// getHyperMainCoins returns top N Hyperliquid coins by 24h volume
func (e *StrategyEngine) getHyperMainCoins(limit int) ([]CandidateCoin, error) {
	if limit <= 0 {
		limit = 20
	}

	ctx := context.Background()
	symbols, err := hyperliquid.GetMainCoinSymbols(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get Hyperliquid main coins: %w", err)
	}

	var candidates []CandidateCoin
	for _, symbol := range symbols {
		// Add USDT suffix for compatibility
		normalizedSymbol := market.Normalize(symbol + "USDT")
		candidates = append(candidates, CandidateCoin{
			Symbol:  normalizedSymbol,
			Sources: []string{"hyper_main"},
		})
	}
	logger.Infof("✅ Loaded %d Hyperliquid main coins (hyper_main) by 24h volume", len(candidates))
	return candidates, nil
}

func clampHyperRankLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 10 {
		return 10
	}
	return limit
}

func (e *StrategyEngine) getHyperRankCoins(category, direction string, limit int) ([]CandidateCoin, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		category = "stock"
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction == "" {
		direction = "gainers"
	}
	limit = clampHyperRankLimit(limit)

	ctx := context.Background()
	var ranked []struct {
		symbol string
		info   hyperliquid.CoinInfo
		cat    string
	}

	if category == "crypto" || category == "all" {
		coins, err := hyperliquid.GetPerpDexCoins(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to get Hyperliquid crypto ranking: %w", err)
		}
		for _, coin := range coins {
			ranked = append(ranked, struct {
				symbol string
				info   hyperliquid.CoinInfo
				cat    string
			}{symbol: market.Normalize(coin.Symbol + "USDT"), info: coin, cat: "crypto"})
		}
	}

	if category != "crypto" {
		coins, err := hyperliquid.GetPerpDexCoins(ctx, "xyz")
		if err != nil {
			return nil, fmt.Errorf("failed to get Hyperliquid XYZ ranking: %w", err)
		}
		for _, coin := range coins {
			base := strings.TrimPrefix(coin.Symbol, "xyz:")
			cat := hyperliquid.XYZCategory(base)
			if category != "all" && cat != category {
				continue
			}
			ranked = append(ranked, struct {
				symbol string
				info   hyperliquid.CoinInfo
				cat    string
			}{symbol: hyperliquid.FormatCoinForAPI("xyz:" + base), info: coin, cat: cat})
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		switch direction {
		case "losers":
			return ranked[i].info.Change24hPct < ranked[j].info.Change24hPct
		case "volume":
			return ranked[i].info.Volume24h > ranked[j].info.Volume24h
		default:
			return ranked[i].info.Change24hPct > ranked[j].info.Change24hPct
		}
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	candidates := make([]CandidateCoin, 0, len(ranked))
	source := fmt.Sprintf("hyper_rank_%s_%s", category, direction)
	for _, item := range ranked {
		candidates = append(candidates, CandidateCoin{Symbol: item.symbol, Sources: []string{source}})
	}
	logger.Infof("✅ Loaded %d Hyperliquid rank coins (%s/%s, capped at %d)", len(candidates), category, direction, limit)
	return candidates, nil
}

// ============================================================================
// External & Quant Data
// ============================================================================

// FetchMarketData fetches market data based on strategy configuration
func (e *StrategyEngine) FetchMarketData(symbol string) (*market.Data, error) {
	return market.Get(symbol)
}

// FetchExternalData fetches external data sources
func (e *StrategyEngine) FetchExternalData() (map[string]interface{}, error) {
	externalData := make(map[string]interface{})

	for _, source := range e.config.Indicators.ExternalDataSources {
		data, err := e.fetchSingleExternalSource(source)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch external data source [%s]: %v", source.Name, err)
			continue
		}
		externalData[source.Name] = data
	}

	return externalData, nil
}

func (e *StrategyEngine) fetchSingleExternalSource(source store.ExternalDataSource) (interface{}, error) {
	// SSRF Protection: Validate URL before making request
	if err := security.ValidateURL(source.URL); err != nil {
		return nil, fmt.Errorf("external source URL validation failed: %w", err)
	}

	timeout := time.Duration(source.RefreshSecs) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Use SSRF-safe HTTP client
	client := security.SafeHTTPClient(timeout)

	req, err := http.NewRequest(source.Method, source.URL, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range source.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if source.DataPath != "" {
		result = extractJSONPath(result, source.DataPath)
	}

	return result, nil
}

func extractJSONPath(data interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return nil
		}
	}

	return current
}

// ============================================================================
// Helper Functions
// ============================================================================

// detectLanguage detects language from text content
// Returns LangChinese if text contains Chinese characters, otherwise LangEnglish
func detectLanguage(text string) Language {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return LangChinese
		}
	}
	return LangEnglish
}
