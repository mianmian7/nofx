package manager

import (
	"context"
	"fmt"
	"nofx/logger"
	"nofx/store"
	"nofx/trader"
	"sort"
	"strings"
	"sync"
	"time"
)

func traderLogTag(traderID, traderName string) string {
	if traderName != "" {
		return fmt.Sprintf("[trader_id=%s trader_name=%s]", traderID, traderName)
	}
	return fmt.Sprintf("[trader_id=%s]", traderID)
}

// CompetitionCache competition data cache
type CompetitionCache struct {
	data      map[string]interface{}
	timestamp time.Time
	mu        sync.RWMutex
}

// TraderManager manages multiple trader instances
type TraderManager struct {
	traders          map[string]*trader.AutoTrader // key: trader ID
	loadErrors       map[string]error              // key: trader ID, stores last load error
	competitionCache *CompetitionCache
	mu               sync.RWMutex
}

// NewTraderManager creates a trader manager
func NewTraderManager() *TraderManager {
	return &TraderManager{
		traders:    make(map[string]*trader.AutoTrader),
		loadErrors: make(map[string]error),
		competitionCache: &CompetitionCache{
			data: make(map[string]interface{}),
		},
	}
}

// GetLoadError returns the last load error for a trader
func (tm *TraderManager) GetLoadError(traderID string) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.loadErrors[traderID]
}

// GetTrader retrieves a trader by ID
func (tm *TraderManager) GetTrader(id string) (*trader.AutoTrader, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	t, exists := tm.traders[id]
	if !exists {
		return nil, fmt.Errorf("trader ID '%s' does not exist", id)
	}
	return t, nil
}

// GetAllTraders retrieves all traders
func (tm *TraderManager) GetAllTraders() map[string]*trader.AutoTrader {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make(map[string]*trader.AutoTrader)
	for id, t := range tm.traders {
		result[id] = t
	}
	return result
}

// GetTraderIDs retrieves all trader IDs
func (tm *TraderManager) GetTraderIDs() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	ids := make([]string, 0, len(tm.traders))
	for id := range tm.traders {
		ids = append(ids, id)
	}
	return ids
}

func (tm *TraderManager) sortedTraderIDsLocked() []string {
	traderIDs := make([]string, 0, len(tm.traders))
	for traderID := range tm.traders {
		traderIDs = append(traderIDs, traderID)
	}
	sort.Slice(traderIDs, func(firstIndex, secondIndex int) bool {
		firstTrader := tm.traders[traderIDs[firstIndex]]
		secondTrader := tm.traders[traderIDs[secondIndex]]
		if firstTrader.GetName() == secondTrader.GetName() {
			return traderIDs[firstIndex] < traderIDs[secondIndex]
		}
		return firstTrader.GetName() < secondTrader.GetName()
	})
	return traderIDs
}

func (tm *TraderManager) startupDelaysLocked() map[string]time.Duration {
	inputs := make([]startupStaggerInput, 0, len(tm.traders))
	for traderID, at := range tm.traders {
		inputs = append(inputs, startupStaggerInput{
			ID:              traderID,
			Name:            at.GetName(),
			ModelKey:        at.GetAIModelScheduleIdentity(),
			Exchange:        at.GetExchange(),
			ScanInterval:    at.GetScanInterval(),
			ConfiguredDelay: at.GetStartupDelay(),
		})
	}
	return planStartupStagger(inputs)
}

func (tm *TraderManager) launchTraderLocked(traderID, action string, startupDelay time.Duration, onError func(error)) error {
	at, exists := tm.traders[traderID]
	if !exists {
		return fmt.Errorf("trader ID '%s' does not exist", traderID)
	}
	go func() {
		logger.Infof("%s ▶️ %s; first cycle delay=%v", traderLogTag(traderID, at.GetName()), action, startupDelay)
		if err := at.RunWithStartupDelay(startupDelay); err != nil {
			logger.Warnf("%s runtime error: %v", traderLogTag(traderID, at.GetName()), err)
			if onError != nil {
				onError(err)
			}
		}
	}()
	return nil
}

// StartTrader starts one trader using the same model-aware phase plan as bulk
// startup and database restoration.
func (tm *TraderManager) StartTrader(traderID string) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	delays := tm.startupDelaysLocked()
	return tm.launchTraderLocked(traderID, "Starting trader runtime", delays[traderID], nil)
}

// StartAll starts all traders
func (tm *TraderManager) StartAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	logger.Info("🚀 Starting all traders...")
	delays := tm.startupDelaysLocked()
	for _, traderID := range tm.sortedTraderIDsLocked() {
		_ = tm.launchTraderLocked(traderID, "Starting trader runtime", delays[traderID], nil)
	}
}

// StopAll permanently stops all trader runtimes and background monitors.
func (tm *TraderManager) StopAll() {
	tm.mu.RLock()
	traders := make([]*trader.AutoTrader, 0, len(tm.traders))
	for _, t := range tm.traders {
		traders = append(traders, t)
	}
	tm.mu.RUnlock()

	logger.Info("⏹  Stopping all traders...")
	for _, t := range traders {
		t.Shutdown()
	}
}

// AutoStartRunningTraders automatically starts traders marked as running in the database
func (tm *TraderManager) AutoStartRunningTraders(st *store.Store) {
	// Get all trader configurations (single query)
	traderList, err := st.Trader().ListAll()
	if err != nil {
		logger.Infof("⚠️ Failed to get trader list: %v", err)
		return
	}

	// Build set of running trader IDs
	runningTraderIDs := make(map[string]bool)
	for _, traderCfg := range traderList {
		if traderCfg.IsRunning {
			runningTraderIDs[traderCfg.ID] = true
		}
	}

	if len(runningTraderIDs) == 0 {
		logger.Info("📋 No traders to auto-restore")
		return
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	startedCount := 0
	delays := tm.startupDelaysLocked()
	traderIDs := tm.sortedTraderIDsLocked()
	for _, traderID := range traderIDs {
		if runningTraderIDs[traderID] {
			_ = tm.launchTraderLocked(traderID, "Auto-restoring trader runtime", delays[traderID], nil)
			startedCount++
		}
	}

	if startedCount > 0 {
		logger.Infof("✓ Auto-restored %d traders", startedCount)
	}
}

// GetComparisonData retrieves comparison data
func (tm *TraderManager) GetComparisonData() (map[string]interface{}, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	comparison := make(map[string]interface{})
	traders := make([]map[string]interface{}, 0, len(tm.traders))

	for _, t := range tm.traders {
		account, err := t.GetAccountInfo()
		if err != nil {
			continue
		}

		status := t.GetStatus()

		traders = append(traders, map[string]interface{}{
			"trader_id":       t.GetID(),
			"trader_name":     t.GetName(),
			"ai_model":        t.GetAIModel(),
			"exchange":        t.GetExchange(),
			"total_equity":    account["total_equity"],
			"total_pnl":       account["total_pnl"],
			"total_pnl_pct":   account["total_pnl_pct"],
			"position_count":  account["position_count"],
			"margin_used_pct": account["margin_used_pct"],
			"call_count":      status["call_count"],
			"is_running":      status["is_running"],
			"invert_signals":  t.GetInvertSignals(),
		})
	}

	comparison["traders"] = traders
	comparison["count"] = len(traders)

	return comparison, nil
}

// GetCompetitionData retrieves competition data (all traders across platform)
func (tm *TraderManager) GetCompetitionData() (map[string]interface{}, error) {
	// Check if cache is valid (within 30 seconds)
	tm.competitionCache.mu.RLock()
	if time.Since(tm.competitionCache.timestamp) < 30*time.Second && len(tm.competitionCache.data) > 0 {
		// Return cached data
		cachedData := make(map[string]interface{})
		for k, v := range tm.competitionCache.data {
			cachedData[k] = v
		}
		tm.competitionCache.mu.RUnlock()
		logger.Infof("📋 Returning competition data cache (cache age: %.1fs)", time.Since(tm.competitionCache.timestamp).Seconds())
		return cachedData, nil
	}
	tm.competitionCache.mu.RUnlock()

	tm.mu.RLock()

	// Get all trader list (only those with ShowInCompetition = true)
	allTraders := make([]*trader.AutoTrader, 0, len(tm.traders))
	for id, t := range tm.traders {
		if t.GetShowInCompetition() {
			allTraders = append(allTraders, t)
			logger.Infof("📋 Competition data includes trader: %s (%s)", t.GetName(), id)
		} else {
			logger.Infof("📋 Competition data excludes trader (hidden): %s (%s)", t.GetName(), id)
		}
	}
	tm.mu.RUnlock()

	logger.Infof("🔄 Refreshing competition data, trader count: %d", len(allTraders))

	// Concurrently fetch trader data
	traders := tm.getConcurrentTraderData(allTraders)

	// Sort by profit rate (descending)
	sort.Slice(traders, func(i, j int) bool {
		pnlPctI, okI := traders[i]["total_pnl_pct"].(float64)
		pnlPctJ, okJ := traders[j]["total_pnl_pct"].(float64)
		if !okI {
			pnlPctI = 0
		}
		if !okJ {
			pnlPctJ = 0
		}
		return pnlPctI > pnlPctJ
	})

	// Limit to top 50
	totalCount := len(traders)
	limit := 50
	if len(traders) > limit {
		traders = traders[:limit]
	}

	comparison := make(map[string]interface{})
	comparison["traders"] = traders
	comparison["count"] = len(traders)
	comparison["total_count"] = totalCount // Total number of traders

	// Update cache
	tm.competitionCache.mu.Lock()
	tm.competitionCache.data = comparison
	tm.competitionCache.timestamp = time.Now()
	tm.competitionCache.mu.Unlock()

	return comparison, nil
}

// getConcurrentTraderData concurrently fetches data for multiple traders
func (tm *TraderManager) getConcurrentTraderData(traders []*trader.AutoTrader) []map[string]interface{} {
	type traderResult struct {
		index int
		data  map[string]interface{}
	}

	// Create result channel
	resultChan := make(chan traderResult, len(traders))

	// Concurrently fetch data for each trader
	for i, t := range traders {
		go func(index int, trader *trader.AutoTrader) {
			// Set timeout to 10 seconds for single trader (increased from 3s for DEX reliability)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Use channel for timeout control
			accountChan := make(chan map[string]interface{}, 1)
			errorChan := make(chan error, 1)

			go func() {
				account, err := trader.GetAccountInfo()
				if err != nil {
					errorChan <- err
				} else {
					accountChan <- account
				}
			}()

			status := trader.GetStatus()
			var traderData map[string]interface{}

			select {
			case account := <-accountChan:
				// Successfully got account info
				traderData = map[string]interface{}{
					"trader_id":              trader.GetID(),
					"trader_name":            trader.GetName(),
					"ai_model":               trader.GetAIModel(),
					"exchange":               trader.GetExchange(),
					"total_equity":           account["total_equity"],
					"total_pnl":              account["total_pnl"],
					"total_pnl_pct":          account["total_pnl_pct"],
					"position_count":         account["position_count"],
					"margin_used_pct":        account["margin_used_pct"],
					"is_running":             status["is_running"],
					"invert_signals":         trader.GetInvertSignals(),
					"system_prompt_template": trader.GetSystemPromptTemplate(),
				}
			case err := <-errorChan:
				// Failed to get account info
				logger.Infof("⚠️ Failed to get account info for trader %s (%s/%s): %v", trader.GetName(), trader.GetID(), trader.GetExchange(), err)
				traderData = map[string]interface{}{
					"trader_id":              trader.GetID(),
					"trader_name":            trader.GetName(),
					"ai_model":               trader.GetAIModel(),
					"exchange":               trader.GetExchange(),
					"total_equity":           0.0,
					"total_pnl":              0.0,
					"total_pnl_pct":          0.0,
					"position_count":         0,
					"margin_used_pct":        0.0,
					"is_running":             status["is_running"],
					"invert_signals":         trader.GetInvertSignals(),
					"system_prompt_template": trader.GetSystemPromptTemplate(),
					"error":                  "Failed to get account data",
				}
			case <-ctx.Done():
				// Timeout
				logger.Infof("⏰ Timeout (10s) getting account info for trader %s (%s/%s)", trader.GetName(), trader.GetID(), trader.GetExchange())
				traderData = map[string]interface{}{
					"trader_id":              trader.GetID(),
					"trader_name":            trader.GetName(),
					"ai_model":               trader.GetAIModel(),
					"exchange":               trader.GetExchange(),
					"total_equity":           0.0,
					"total_pnl":              0.0,
					"total_pnl_pct":          0.0,
					"position_count":         0,
					"margin_used_pct":        0.0,
					"is_running":             status["is_running"],
					"invert_signals":         trader.GetInvertSignals(),
					"system_prompt_template": trader.GetSystemPromptTemplate(),
					"error":                  "Request timeout",
				}
			}

			resultChan <- traderResult{index: index, data: traderData}
		}(i, t)
	}

	// Collect all results
	results := make([]map[string]interface{}, len(traders))
	for i := 0; i < len(traders); i++ {
		result := <-resultChan
		results[result.index] = result.data
	}

	return results
}

// GetTopTradersData retrieves top 5 traders data (for performance comparison)
func (tm *TraderManager) GetTopTradersData() (map[string]interface{}, error) {
	// Reuse competition data cache, as top 5 is filtered from all data
	competitionData, err := tm.GetCompetitionData()
	if err != nil {
		return nil, err
	}

	// Extract top 5 from competition data
	allTraders, ok := competitionData["traders"].([]map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid competition data format")
	}

	// Limit to top 5
	limit := 5
	topTraders := allTraders
	if len(allTraders) > limit {
		topTraders = allTraders[:limit]
	}

	result := map[string]interface{}{
		"traders": topTraders,
		"count":   len(topTraders),
	}

	return result, nil
}

// RemoveTrader removes a trader from memory (does not affect database)
// Used to force reload when updating trader configuration
// The trader and its background monitors are permanently stopped first.
func (tm *TraderManager) RemoveTrader(traderID string) {
	tm.mu.Lock()
	t, exists := tm.traders[traderID]
	if exists {
		// Remove the pointer while holding the manager lock, but never wait for a
		// trader's network-bound shutdown while the global lock is held. A stuck
		// cycle must not freeze unrelated queries, starts, or reloads.
		delete(tm.traders, traderID)
	}
	tm.mu.Unlock()

	if !exists {
		return
	}
	logger.Infof("⏹ Shutting down trader %s before removing from memory...", traderID)
	t.Shutdown()
	logger.Infof("✓ Trader %s removed from memory", traderID)
}

func ensureHyperliquidNativeStrategy(traderName, exchangeType string, cfg *store.StrategyConfig) {
	if cfg == nil || strings.ToLower(strings.TrimSpace(exchangeType)) != "hyperliquid" {
		return
	}

	source := strings.ToLower(strings.TrimSpace(cfg.CoinSource.SourceType))
	if source == "hyper_rank" || source == "static" || source == "hyper_all" || source == "hyper_main" {
		return
	}

	logger.Warnf("⚠️ Trader %s uses legacy coin source %q on Hyperliquid; forcing native stock ranking to avoid crypto fallback", traderName, cfg.CoinSource.SourceType)
	cfg.CoinSource.SourceType = "hyper_rank"
	cfg.CoinSource.UseHyperAll = false
	cfg.CoinSource.UseHyperMain = false
	if cfg.CoinSource.HyperRankCategory == "" {
		cfg.CoinSource.HyperRankCategory = "stock"
	}
	if cfg.CoinSource.HyperRankDirection == "" {
		cfg.CoinSource.HyperRankDirection = "gainers"
	}
	if cfg.CoinSource.HyperRankLimit <= 0 {
		cfg.CoinSource.HyperRankLimit = 5
	}
}

// LoadUserTradersFromStore loads traders from store for a specific user to memory
func (tm *TraderManager) LoadUserTradersFromStore(st *store.Store, userID string) error {
	return tm.loadUserTradersFromStore(st, userID, true)
}

// LoadUserTradersFromStoreWithoutAutoStart loads traders without starting
// running-only monitors or the automatic decision loop. It is used when a
// caller must restore an in-memory runtime before deciding whether to run it.
func (tm *TraderManager) LoadUserTradersFromStoreWithoutAutoStart(st *store.Store, userID string) error {
	return tm.loadUserTradersFromStore(st, userID, false)
}

func (tm *TraderManager) loadUserTradersFromStore(st *store.Store, userID string, autoStart bool) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Get all traders for the specified user
	traders, err := st.Trader().List(userID)
	if err != nil {
		return fmt.Errorf("failed to get trader list for user %s: %w", userID, err)
	}

	logger.Infof("📋 Loading trader configurations for user %s: %d traders", userID, len(traders))

	// Get AI model and exchange lists (query only once outside loop)
	aiModels, err := st.AIModel().List(userID)
	if err != nil {
		logger.Infof("⚠️ Failed to get AI model config for user %s: %v", userID, err)
		return fmt.Errorf("failed to get AI model config: %w", err)
	}

	exchanges, err := st.Exchange().List(userID)
	if err != nil {
		logger.Infof("⚠️ Failed to get exchange config for user %s: %v", userID, err)
		return fmt.Errorf("failed to get exchange config: %w", err)
	}

	// Load configuration for each trader
	newlyLoaded := make(map[string]struct{})
	for _, traderCfg := range traders {
		// Check if this trader is already loaded
		if _, exists := tm.traders[traderCfg.ID]; exists {
			// Trader already loaded - this is normal, no need to log
			continue
		}

		// Find AI model config from already queried list
		var aiModelCfg *store.AIModel
		for _, model := range aiModels {
			if model.ID == traderCfg.AIModelID {
				aiModelCfg = model
				break
			}
		}
		if aiModelCfg == nil {
			for _, model := range aiModels {
				if model.Provider == traderCfg.AIModelID {
					aiModelCfg = model
					break
				}
			}
		}

		if aiModelCfg == nil {
			logger.Infof("⚠️ AI model %s for trader %s does not exist, skipping", traderCfg.AIModelID, traderCfg.Name)
			continue
		}

		if !aiModelCfg.Enabled {
			logger.Infof("⚠️ AI model %s for trader %s is not enabled, skipping", traderCfg.AIModelID, traderCfg.Name)
			continue
		}

		// Find exchange config from already queried list
		var exchangeCfg *store.Exchange
		for _, exchange := range exchanges {
			if exchange.ID == traderCfg.ExchangeID {
				exchangeCfg = exchange
				break
			}
		}

		if exchangeCfg == nil {
			logger.Infof("⚠️ Exchange %s for trader %s does not exist, skipping", traderCfg.ExchangeID, traderCfg.Name)
			continue
		}

		if !exchangeCfg.Enabled {
			logger.Infof("⚠️ Exchange %s for trader %s is not enabled, skipping", traderCfg.ExchangeID, traderCfg.Name)
			continue
		}

		// Use existing method to load trader
		logger.Infof("📦 Loading trader %s (AI Model: %s, Exchange: %s/%s, Strategy ID: %s)", traderCfg.Name, aiModelCfg.Provider, exchangeCfg.ExchangeType, exchangeCfg.AccountName, traderCfg.StrategyID)
		err = tm.addTraderFromStore(traderCfg, aiModelCfg, exchangeCfg, st, autoStart)
		if err != nil {
			logger.Warnf("%s failed to load trader: %v", traderLogTag(traderCfg.ID, traderCfg.Name), err)
			// Save error for later retrieval
			tm.loadErrors[traderCfg.ID] = err
			} else {
				// Clear any previous error on success
				delete(tm.loadErrors, traderCfg.ID)
				newlyLoaded[traderCfg.ID] = struct{}{}
			}
		}
	if autoStart {
		delays := tm.startupDelaysLocked()
		for _, traderCfg := range traders {
			if traderCfg.IsRunning {
				if _, loaded := newlyLoaded[traderCfg.ID]; !loaded {
					continue
				}
				cfg := traderCfg
				_ = tm.launchTraderLocked(cfg.ID, "Auto-restoring trader runtime", delays[cfg.ID], func(error) {
					_ = st.Trader().UpdateStatus(cfg.UserID, cfg.ID, false)
				})
			}
		}
	}

	return nil
}

// LoadTradersFromStore loads all traders from store to memory (new API)
func (tm *TraderManager) LoadTradersFromStore(st *store.Store) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Get all users
	userIDs, err := st.User().GetAllIDs()
	if err != nil {
		return fmt.Errorf("failed to get user list: %w", err)
	}

	logger.Infof("📋 Found %d users, loading all trader configurations...", len(userIDs))

	var allTraders []*store.Trader
	for _, userID := range userIDs {
		// Get traders for each user
		traders, err := st.Trader().List(userID)
		if err != nil {
			logger.Infof("⚠️ Failed to get traders for user %s: %v", userID, err)
			continue
		}
		logger.Infof("📋 User %s: %d traders", userID, len(traders))
		allTraders = append(allTraders, traders...)
	}

	logger.Infof("📋 Total loaded trader configurations: %d", len(allTraders))

	// Get AI model and exchange configs for each trader
	for _, traderCfg := range allTraders {
		// Get AI model config
		aiModels, err := st.AIModel().List(traderCfg.UserID)
		if err != nil {
			logger.Infof("⚠️  Failed to get AI model config: %v", err)
			continue
		}

		var aiModelCfg *store.AIModel
		// Prioritize exact match on model.ID
		for _, model := range aiModels {
			if model.ID == traderCfg.AIModelID {
				aiModelCfg = model
				break
			}
		}
		// If no exact match, try matching provider (for backward compatibility)
		if aiModelCfg == nil {
			for _, model := range aiModels {
				if model.Provider == traderCfg.AIModelID {
					aiModelCfg = model
					logger.Infof("⚠️  Trader %s using legacy provider match: %s -> %s", traderCfg.Name, traderCfg.AIModelID, model.ID)
					break
				}
			}
		}

		if aiModelCfg == nil {
			logger.Infof("⚠️  AI model %s for trader %s does not exist, skipping", traderCfg.AIModelID, traderCfg.Name)
			continue
		}

		if !aiModelCfg.Enabled {
			logger.Infof("⚠️  AI model %s for trader %s is not enabled, skipping", traderCfg.AIModelID, traderCfg.Name)
			continue
		}

		// Get exchange config
		exchanges, err := st.Exchange().List(traderCfg.UserID)
		if err != nil {
			logger.Infof("⚠️  Failed to get exchange config: %v", err)
			continue
		}

		var exchangeCfg *store.Exchange
		for _, exchange := range exchanges {
			if exchange.ID == traderCfg.ExchangeID {
				exchangeCfg = exchange
				break
			}
		}

		if exchangeCfg == nil {
			logger.Infof("⚠️  Exchange %s for trader %s does not exist, skipping", traderCfg.ExchangeID, traderCfg.Name)
			continue
		}

		if !exchangeCfg.Enabled {
			logger.Infof("⚠️  Exchange %s for trader %s is not enabled, skipping", traderCfg.ExchangeID, traderCfg.Name)
			continue
		}

		// Add the fully initialized trader to TraderManager.
		err = tm.addTraderFromStore(traderCfg, aiModelCfg, exchangeCfg, st, true)
		if err != nil {
			logger.Warnf("%s failed to add trader: %v", traderLogTag(traderCfg.ID, traderCfg.Name), err)
			continue
		}
	}
	delays := tm.startupDelaysLocked()
	for _, traderCfg := range allTraders {
		if traderCfg.IsRunning {
			cfg := traderCfg
			_ = tm.launchTraderLocked(cfg.ID, "Auto-restoring trader runtime", delays[cfg.ID], func(error) {
				_ = st.Trader().UpdateStatus(cfg.UserID, cfg.ID, false)
			})
		}
	}

	logger.Infof("✓ Successfully loaded %d traders to memory", len(tm.traders))
	return nil
}

type backgroundMonitoringStarter interface {
	StartBackgroundMonitoring()
}

func startBackgroundMonitoringIfRunning(at backgroundMonitoringStarter, isRunning bool) {
	if isRunning {
		at.StartBackgroundMonitoring()
	}
}

// addTraderFromStore internal method: adds trader from store configuration
func buildTraderAIModelCandidates(traderCfg *store.Trader, primaryModel *store.AIModel, st *store.Store) ([]trader.AIModelCandidate, error) {
	primaryAPIKey := store.ResolveAIModelAPIKey(primaryModel)
	if primaryAPIKey == "" {
		return nil, fmt.Errorf("primary AI model %s is missing credentials", primaryModel.ID)
	}
	primaryModelName := strings.TrimSpace(traderCfg.PrimaryModelName)
	if primaryModelName == "" {
		primaryModelName = primaryModel.CustomModelName
	}

	candidates := []trader.AIModelCandidate{
		{
			ID:           primaryModel.ID,
			Provider:     primaryModel.Provider,
			APIKey:       primaryAPIKey,
			CustomAPIURL: primaryModel.CustomAPIURL,
			ModelName:    primaryModelName,
		},
	}

	for _, modelName := range store.DecodeStringList(traderCfg.FallbackModelNames) {
		if modelName == primaryModelName {
			continue
		}
		candidates = append(candidates, trader.AIModelCandidate{
			ID:           primaryModel.ID,
			Provider:     primaryModel.Provider,
			APIKey:       primaryAPIKey,
			CustomAPIURL: primaryModel.CustomAPIURL,
			ModelName:    modelName,
		})
	}

	configuredFallbackIDs := store.DecodeStringList(traderCfg.FallbackAIModelIDs)
	if len(configuredFallbackIDs) == 0 {
		return candidates, nil
	}

	availableModels, err := st.AIModel().List(traderCfg.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to load fallback AI models: %w", err)
	}

	modelsByID := make(map[string]*store.AIModel, len(availableModels))
	for _, model := range availableModels {
		modelsByID[model.ID] = model
	}
	for _, fallbackID := range configuredFallbackIDs {
		fallbackModel, exists := modelsByID[fallbackID]
		if !exists || fallbackModel == nil {
			return nil, fmt.Errorf("fallback AI model %s does not exist", fallbackID)
		}
		fallbackAPIKey := store.ResolveAIModelAPIKey(fallbackModel)
		if !fallbackModel.Enabled || fallbackAPIKey == "" {
			return nil, fmt.Errorf("fallback AI model %s is disabled or missing credentials", fallbackID)
		}
		candidates = append(candidates, trader.AIModelCandidate{
			ID:           fallbackModel.ID,
			Provider:     fallbackModel.Provider,
			APIKey:       fallbackAPIKey,
			CustomAPIURL: fallbackModel.CustomAPIURL,
			ModelName:    fallbackModel.CustomModelName,
		})
	}

	return candidates, nil
}

func (tm *TraderManager) addTraderFromStore(traderCfg *store.Trader, aiModelCfg *store.AIModel, exchangeCfg *store.Exchange, st *store.Store, autoStart bool) error {
	if _, exists := tm.traders[traderCfg.ID]; exists {
		return fmt.Errorf("trader ID '%s' already exists", traderCfg.ID)
	}
	if exchangeCfg != nil && strings.EqualFold(strings.TrimSpace(exchangeCfg.ExchangeType), "hyperliquid") {
		return fmt.Errorf("unsupported trading platform hyperliquid: configure Binance Futures instead")
	}

	// Load strategy config (must have strategy)
	var strategyConfig *store.StrategyConfig
	strategyConfigRaw := ""
	if traderCfg.StrategyID != "" {
		strategy, err := st.Strategy().Get(traderCfg.UserID, traderCfg.StrategyID)
		if err != nil {
			return fmt.Errorf("failed to load strategy %s for trader %s: %w", traderCfg.StrategyID, traderCfg.Name, err)
		}
		strategyConfigRaw = strategy.Config
		// Parse JSON config
		strategyConfig, err = strategy.ParseConfig()
		if err != nil {
			return fmt.Errorf("failed to parse strategy config for trader %s: %w", traderCfg.Name, err)
		}
		strategyConfig.ClampLimits()
		// Sizing comes from the strategy's own RiskControl (a hardcoded
		// 6-position × equity×1.2 override used to live here, silently ignoring
		// the user's configuration). ClampLimits bounds the values and the
		// margin auto-reduce at order time keeps the book solvent.
		logger.Infof("✓ Trader %s loaded strategy config: %s (maxPos=%d, posRatio=%.1f)", traderCfg.Name, strategy.Name, strategyConfig.RiskControl.MaxPositions, strategyConfig.RiskControl.AltcoinMaxPositionValueRatio)
		ensureHyperliquidNativeStrategy(traderCfg.Name, exchangeCfg.ExchangeType, strategyConfig)
	} else {
		return fmt.Errorf("trader %s has no strategy configured", traderCfg.Name)
	}

	if exchangeCfg.ExchangeType == "hyperliquid" && !exchangeCfg.HyperliquidBuilderApproved {
		return fmt.Errorf("Hyperliquid trading authorization is incomplete for exchange %s; reconnect Hyperliquid wallet and complete trading authorization before starting trader %s", exchangeCfg.AccountName, traderCfg.Name)
	}

	scanIntervalMinutes := traderCfg.ScanIntervalMinutes
	if scanIntervalMinutes <= 0 {
		scanIntervalMinutes = 15
	} else if scanIntervalMinutes < 3 {
		scanIntervalMinutes = 3
	}
	startupDelayMinutes := traderCfg.StartupDelayMinutes
	if startupDelayMinutes < 0 || startupDelayMinutes >= scanIntervalMinutes {
		logger.Warnf("⚠️ Invalid startup delay %d for trader %s; resetting to zero", startupDelayMinutes, traderCfg.Name)
		startupDelayMinutes = 0
	}
	modelCandidates, err := buildTraderAIModelCandidates(traderCfg, aiModelCfg, st)
	if err != nil {
		return fmt.Errorf("failed to configure AI models for trader %s: %w", traderCfg.Name, err)
	}
	primaryModelName := strings.TrimSpace(traderCfg.PrimaryModelName)
	if primaryModelName == "" {
		primaryModelName = aiModelCfg.CustomModelName
	}

	// Build AutoTraderConfig for StrategyEngine.
	traderConfig := trader.AutoTraderConfig{
		ID:                    traderCfg.ID,
		Name:                  traderCfg.Name,
		StrategyID:            traderCfg.StrategyID,
		AIModel:               aiModelCfg.Provider,
		Exchange:              exchangeCfg.ExchangeType, // Exchange type: binance/bybit/okx/etc
		ExchangeID:            exchangeCfg.ID,           // Exchange account UUID (for multi-account)
		ExecutionMode:         trader.ExecutionMode(traderCfg.ExecutionMode),
		BinanceAPIKey:         "",
		BinanceSecretKey:      "",
		HyperliquidPrivateKey: "",
		HyperliquidTestnet:    exchangeCfg.Testnet,
		UseQwen:               aiModelCfg.Provider == "qwen",
		DeepSeekKey:           "",
		QwenKey:               "",
		CustomAPIURL:          aiModelCfg.CustomAPIURL,
		CustomModelName:       primaryModelName,
		AIModelCandidates:     modelCandidates,
		ScanInterval:          time.Duration(scanIntervalMinutes) * time.Minute,
		StartupDelay:          time.Duration(startupDelayMinutes) * time.Minute,
		InitialBalance:        traderCfg.InitialBalance,
		IsCrossMargin:         traderCfg.IsCrossMargin,
		ShowInCompetition:     traderCfg.ShowInCompetition,
		InvertSignals:         traderCfg.InvertSignals,
		StrategyConfig:        strategyConfig,
		StrategyConfigRaw:     strategyConfigRaw,
	}

	logger.Infof("📊 Loading trader %s: ScanIntervalMinutes=%d, StartupDelayMinutes=%d, ScanInterval=%v, AI candidates=%d",
		traderCfg.Name, scanIntervalMinutes, startupDelayMinutes, traderConfig.ScanInterval, len(modelCandidates))

	// Set API keys based on exchange type (convert EncryptedString to string)
	switch exchangeCfg.ExchangeType {
	case "binance":
		traderConfig.BinanceAPIKey = string(exchangeCfg.APIKey)
		traderConfig.BinanceSecretKey = string(exchangeCfg.SecretKey)
	case "bybit":
		traderConfig.BybitAPIKey = string(exchangeCfg.APIKey)
		traderConfig.BybitSecretKey = string(exchangeCfg.SecretKey)
	case "okx":
		traderConfig.OKXAPIKey = string(exchangeCfg.APIKey)
		traderConfig.OKXSecretKey = string(exchangeCfg.SecretKey)
		traderConfig.OKXPassphrase = string(exchangeCfg.Passphrase)
	case "bitget":
		traderConfig.BitgetAPIKey = string(exchangeCfg.APIKey)
		traderConfig.BitgetSecretKey = string(exchangeCfg.SecretKey)
		traderConfig.BitgetPassphrase = string(exchangeCfg.Passphrase)
	case "gate":
		traderConfig.GateAPIKey = string(exchangeCfg.APIKey)
		traderConfig.GateSecretKey = string(exchangeCfg.SecretKey)
	case "kucoin":
		traderConfig.KuCoinAPIKey = string(exchangeCfg.APIKey)
		traderConfig.KuCoinSecretKey = string(exchangeCfg.SecretKey)
		traderConfig.KuCoinPassphrase = string(exchangeCfg.Passphrase)
	case "hyperliquid":
		traderConfig.HyperliquidPrivateKey = string(exchangeCfg.APIKey)
		traderConfig.HyperliquidWalletAddr = exchangeCfg.HyperliquidWalletAddr
		traderConfig.HyperliquidUnifiedAcct = exchangeCfg.HyperliquidUnifiedAcct
	case "aster":
		traderConfig.AsterUser = exchangeCfg.AsterUser
		traderConfig.AsterSigner = exchangeCfg.AsterSigner
		traderConfig.AsterPrivateKey = string(exchangeCfg.AsterPrivateKey)
	case "lighter":
		traderConfig.LighterPrivateKey = string(exchangeCfg.LighterPrivateKey)
		traderConfig.LighterWalletAddr = exchangeCfg.LighterWalletAddr
		traderConfig.LighterAPIKeyPrivateKey = string(exchangeCfg.LighterAPIKeyPrivateKey)
		traderConfig.LighterAPIKeyIndex = exchangeCfg.LighterAPIKeyIndex
		traderConfig.LighterTestnet = exchangeCfg.Testnet
	case "indodax":
		traderConfig.IndodaxAPIKey = string(exchangeCfg.APIKey)
		traderConfig.IndodaxSecretKey = string(exchangeCfg.SecretKey)
	}

	// Set API keys based on AI model (convert EncryptedString to string)
	switch aiModelCfg.Provider {
	case "qwen":
		traderConfig.QwenKey = string(aiModelCfg.APIKey)
	case "deepseek":
		traderConfig.DeepSeekKey = string(aiModelCfg.APIKey)
	default:
		// For other providers (grok, openai, claude, gemini, kimi, etc.), use CustomAPIKey
		traderConfig.CustomAPIKey = string(aiModelCfg.APIKey)
	}

	// Create trader instance
	at, err := trader.NewAutoTrader(traderConfig, st, traderCfg.UserID)
	if err != nil {
		return fmt.Errorf("failed to create trader: %w", err)
	}

	// Set custom prompt (if exists)
	if traderCfg.CustomPrompt != "" {
		at.SetCustomPrompt(traderCfg.CustomPrompt)
		at.SetOverrideBasePrompt(traderCfg.OverrideBasePrompt)
		if traderCfg.OverrideBasePrompt {
			logger.Infof("✓ Set custom trading strategy prompt (overriding base prompt)")
		} else {
			logger.Infof("✓ Set custom trading strategy prompt (supplementing base prompt)")
		}
	}

	tm.traders[traderCfg.ID] = at
	startBackgroundMonitoringIfRunning(at, traderCfg.IsRunning && autoStart)
	logger.Infof("✓ Trader '%s' (%s + %s/%s) loaded to memory", traderCfg.Name, aiModelCfg.Provider, exchangeCfg.ExchangeType, exchangeCfg.AccountName)

	return nil
}
