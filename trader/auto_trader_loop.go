package trader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/provider/hyperliquid"
	"nofx/store"
	"strconv"
	"strings"
	"time"
)

// runCycle runs one trading cycle (using AI full decision-making)
func (at *AutoTrader) runCycle() error {
	return at.runCycleContext(context.Background())
}

func (at *AutoTrader) runCycleContext(requestCtx context.Context) error {
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	if err := requestCtx.Err(); err != nil {
		return err
	}
	at.isRunningMutex.Lock()
	at.callCount++
	callCount := at.callCount
	at.isRunningMutex.Unlock()
	at.cycleStateMu.Lock()
	at.cycleStartedAt = time.Now().UTC()
	at.cyclePhase = "context"
	at.lastCycleError = ""
	at.cycleStateMu.Unlock()

	logger.Info("\n" + strings.Repeat("=", 70) + "\n")
	logger.Infof("⏰ %s - AI decision cycle #%d", time.Now().Format("2006-01-02 15:04:05"), callCount)
	logger.Info(strings.Repeat("=", 70))

	// 0. Check if trader is stopped (early exit to prevent trades after Stop() is called)
	at.isRunningMutex.RLock()
	running := at.isRunning
	at.isRunningMutex.RUnlock()
	if !running {
		at.logInfof("⏹ Trader is stopped, aborting cycle #%d", callCount)
		return nil
	}
	if err := requestCtx.Err(); err != nil {
		return err
	}

	if err := at.reloadStrategyConfigIfChanged(); err != nil {
		at.logWarnf("⚠️ Strategy refresh failed, using current in-memory config: %v", err)
	}
	at.setCyclePhase("account_context")

	// Create decision record
	record := &store.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. Check if trading needs to be stopped
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		at.logWarnf("⏸ Risk control: Trading paused, remaining %.0f minutes", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Risk control paused, remaining %.0f minutes", remaining.Minutes())
		if err := at.saveDecision(record); err != nil {
			at.logWarnf("⚠ Failed to save decision record: %v", err)
		}
		return nil
	}

	// 2. Reset daily P&L (reset every day)
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		logger.Info("📅 Daily P&L reset")
	}

	// 4. Collect trading context
	ctx, err := at.buildTradingContext()
	if err != nil {
		at.logErrorf("failed to build trading context: %v", err)
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Failed to build trading context: %v", err)
		if saveErr := at.saveDecision(record); saveErr != nil {
			at.logWarnf("⚠ Failed to save decision record: %v", saveErr)
		}
		return fmt.Errorf("failed to build trading context: %w", err)
	}
	if err := requestCtx.Err(); err != nil {
		return err
	}

	// Save equity snapshot independently (decoupled from AI decision, used for drawing profit curve)
	// NOTE: Must be called BEFORE candidate coins check to ensure equity is always recorded
	at.saveEquitySnapshot(ctx)

	// An empty candidate universe is a market-data/configuration failure, not a
	// successful no-op. Record it explicitly so proxy/upstream outages cannot be
	// mistaken for a model decision or a healthy trading cycle.
	if len(ctx.CandidateCoins) == 0 {
		at.logInfof("ℹ️ No candidate coins available, skipping this cycle")
		record.Success = false
		record.ErrorMessage = "No candidate coins available; market-data candidate universe is empty"
		record.ExecutionLog = append(record.ExecutionLog, record.ErrorMessage)
		record.AccountState = store.AccountSnapshot{
			TotalBalance:          ctx.Account.TotalEquity,
			AvailableBalance:      ctx.Account.AvailableBalance,
			TotalUnrealizedProfit: ctx.Account.UnrealizedPnL,
			PositionCount:         ctx.Account.PositionCount,
			InitialBalance:        at.initialBalance,
		}
		if err := at.saveDecision(record); err != nil {
			at.logWarnf("⚠ Failed to save decision record: %v", err)
		}
		return nil
	}

	logger.Info(strings.Repeat("=", 70))
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	at.logInfof("📊 Account equity: %.2f USDT | Available: %.2f USDT | Positions: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 5. Use strategy engine to call AI for decision
	at.setCyclePhase("market_data_ai")
	at.logInfof("🤖 Requesting AI analysis and decision... [Strategy Engine]")
	aiDecision, err := kernel.GetFullDecisionWithStrategyContext(requestCtx, ctx, at.mcpClient, at.strategyEngine, "balanced")

	if aiDecision != nil && aiDecision.AIRequestDurationMs > 0 {
		record.AIRequestDurationMs = aiDecision.AIRequestDurationMs
		at.logInfof("⏱️ AI call duration: %.2f seconds", float64(record.AIRequestDurationMs)/1000)
		record.ExecutionLog = append(record.ExecutionLog,
			fmt.Sprintf("AI call duration: %d ms", record.AIRequestDurationMs))
	}

	// Save chain of thought, decisions, and input prompt even if there's an error (for debugging)
	if aiDecision != nil {
		record.CallID = aiDecision.CallID
		record.SystemPrompt = aiDecision.SystemPrompt // Save system prompt
		record.InputPrompt = aiDecision.UserPrompt
		record.CoTTrace = aiDecision.CoTTrace
		record.RawResponse = aiDecision.RawResponse // Save raw AI response for debugging
		if len(aiDecision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(aiDecision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	// Record AI charge (track cost regardless of decision outcome).
	// Use the effective model name (custom model, e.g. "gpt-5.6") so the
	// per-call price lookup matches what was actually invoked — at.aiModel is
	// the provider ID and would fall back to the default price.
	// Prefer the gateway-reported settled amount (upto scheme) over the flat
	// catalog estimate when the client exposes it.
	if aiDecision != nil && at.store != nil {
		chargeModel := at.config.CustomModelName
		if chargeModel == "" {
			chargeModel = at.aiModel
		}
		var chargeErr error
		if r, ok := at.mcpClient.(interface{ LastCallCostUSD() (float64, bool) }); ok {
			if actual, has := r.LastCallCostUSD(); has {
				chargeErr = at.store.AICharge().RecordWithCost(at.id, chargeModel, at.config.AIModel, actual)
			} else {
				chargeErr = at.store.AICharge().Record(at.id, chargeModel, at.config.AIModel)
			}
		} else {
			chargeErr = at.store.AICharge().Record(at.id, chargeModel, at.config.AIModel)
		}
		if chargeErr != nil {
			at.logWarnf("⚠️ Failed to record AI charge: %v", chargeErr)
		}
	}

	if err != nil {
		var marketDataErr *kernel.MarketDataUnavailableError
		if errors.As(err, &marketDataErr) {
			at.logWarnf("⏭️ Skipping live AI decision: %v", marketDataErr)
			record.Success = false
			record.ErrorMessage = fmt.Sprintf("Skipped AI decision because fresh %s market data was unavailable: %v", at.exchange, marketDataErr)
			record.ExecutionLog = append(record.ExecutionLog, record.ErrorMessage)
			if saveErr := at.saveDecision(record); saveErr != nil {
				at.logWarnf("⚠ Failed to save decision record: %v", saveErr)
			}
			// This is an upstream market-data safety stop, not an AI failure;
			// do not increment consecutive AI failures or enter AI safe mode.
			return nil
		}
		at.consecutiveAIFailures++
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Failed to get AI decision: %v", err)

		// Activate safe mode after 3 consecutive failures
		if at.consecutiveAIFailures >= 3 && !at.isSafeMode() {
			at.setSafeMode(true, fmt.Sprintf("AI failed %d consecutive times: %v", at.consecutiveAIFailures, err))
			at.logErrorf("🛡️ SAFE MODE ACTIVATED — AI failed %d times in a row. No new positions will be opened. Existing positions are protected with current stop-loss settings.", at.consecutiveAIFailures)
			at.logErrorf("🛡️ Reason: %v", err)
			at.logErrorf("🛡️ Action: Will keep trying AI each cycle. Safe mode auto-deactivates when AI recovers.")
		}

		// Prompts and chain-of-thought are deliberately not written to the
		// runtime log on failure. The decision record retains existing data
		// for authorized debugging, while the call ID links it to MCP logs.
		if aiDecision != nil && aiDecision.CallID != "" {
			at.logInfof("AI decision failure correlated with call_id=%s", aiDecision.CallID)
		}

		if saveErr := at.saveDecision(record); saveErr != nil {
			at.logWarnf("⚠ Failed to save decision record: %v", saveErr)
		}

		// In safe mode, don't return error — keep the loop running to retry next cycle
		if at.isSafeMode() {
			at.logWarnf("🛡️ Safe mode: skipping this cycle, will retry in %v", at.config.ScanInterval)
			return nil
		}

		return fmt.Errorf("failed to get AI decision: %w", err)
	}
	at.setCyclePhase("execution")

	// AI succeeded — reset failure counter and deactivate safe mode
	if at.consecutiveAIFailures > 0 {
		at.logInfof("✅ AI recovered after %d consecutive failures", at.consecutiveAIFailures)
	}
	at.consecutiveAIFailures = 0
	if at.isSafeMode() {
		at.logInfof("🛡️ SAFE MODE DEACTIVATED — AI is working again. Resuming normal trading.")
		at.setSafeMode(false, "")
	}

	// // 5. Print system prompt
	// logger.Infof("\n" + strings.Repeat("=", 70))
	// logger.Infof("📋 System prompt [template: %s]", at.systemPromptTemplate)
	// logger.Info(strings.Repeat("=", 70))
	// logger.Info(decision.SystemPrompt)
	// logger.Infof(strings.Repeat("=", 70) + "\n")

	// 6. Print AI chain of thought
	// logger.Infof("\n" + strings.Repeat("-", 70))
	// logger.Info("💭 AI chain of thought analysis:")
	// logger.Info(strings.Repeat("-", 70))
	// logger.Info(decision.CoTTrace)
	// logger.Infof(strings.Repeat("-", 70) + "\n")

	// 7. Print AI decisions
	// logger.Infof("📋 AI decision list (%d items):\n", len(kernel.Decisions))
	// for i, d := range kernel.Decisions {
	//     logger.Infof("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
	//     if d.Action == "open_long" || d.Action == "open_short" {
	//        logger.Infof("      Leverage: %dx | Position: %.2f USDT | Stop loss: %.4f | Take profit: %.4f",
	//           d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
	//     }
	// }
	logger.Info()
	// Apply signal inversion if enabled
	if at.invertSignals && len(aiDecision.Decisions) > 0 {
		at.logInfof("🔄 [InvertSignals] Mirroring AI entry decisions while preserving exits for actual positions...")
		aiDecision.Decisions = invertDecisions(aiDecision.Decisions)
		decisionJSON, _ := json.MarshalIndent(aiDecision.Decisions, "", "  ")
		record.DecisionJSON = string(decisionJSON)
	}

	// 8. Sort decisions: ensure close positions first, then open positions (prevent position stacking overflow)
	sortedDecisions := sortDecisionsByPriority(aiDecision.Decisions)
	sortedDecisions = at.filterDecisionsToStrategyUniverse(sortedDecisions, ctx)
	// Per-cycle long/short coverage: if the AI left a direction uncovered, force
	// the strongest bullish/bearish candidate (account-sized, risk-enforced).
	sortedDecisions = at.ensureLongShortCoverage(sortedDecisions, ctx, ctx.Account.TotalEquity)

	logger.Info("🔄 Execution order (optimized): Close positions first → Open positions later")
	for i, d := range sortedDecisions {
		logger.Infof("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	logger.Info()

	// Check if trader is stopped before executing any decisions (prevent trades after Stop())
	at.isRunningMutex.RLock()
	running = at.isRunning
	at.isRunningMutex.RUnlock()
	if !running {
		at.logInfof("⏹ Trader stopped before decision execution, aborting cycle #%d", callCount)
		return nil
	}

	// Safe mode: filter out open positions, only allow close/hold
	if at.isSafeMode() {
		filtered := make([]kernel.Decision, 0)
		for _, d := range sortedDecisions {
			if d.Action == "open_long" || d.Action == "open_short" {
				at.logWarnf("🛡️ Safe mode: BLOCKED %s %s (no new positions allowed)", d.Action, d.Symbol)
				continue
			}
			filtered = append(filtered, d)
		}
		sortedDecisions = filtered
		if len(sortedDecisions) == 0 {
			at.logInfof("🛡️ Safe mode: all decisions were open positions, nothing to execute")
		}
	}

	// Execute decisions and record results. Trade throttle is applied here,
	// immediately before order placement, so AI churn cannot become live orders.
	opensAllowedThisCycle := 0
	for _, d := range sortedDecisions {
		// Check if trader is stopped before each decision (allow immediate stop during execution)
		at.isRunningMutex.RLock()
		running = at.isRunning
		at.isRunningMutex.RUnlock()
		if !running {
			at.logInfof("⏹ Trader stopped during decision execution, aborting remaining decisions")
			break
		}

		actionRecord := store.DecisionAction{
			Action:     d.Action,
			Symbol:     d.Symbol,
			Quantity:   0,
			Leverage:   d.Leverage,
			Price:      0,
			StopLoss:   d.StopLoss,
			TakeProfit: d.TakeProfit,
			Confidence: d.Confidence,
			Reasoning:  d.Reasoning,
			Timestamp:  time.Now().UTC(),
			Success:    false,
		}

		if reason := at.tradeThrottleReason(d, ctx, opensAllowedThisCycle); reason != "" {
			at.logWarnf("🧊 %s %s blocked: %s", d.Symbol, d.Action, reason)
			actionRecord.Error = reason
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("🧊 %s %s blocked: %s", d.Symbol, d.Action, reason))
			record.Decisions = append(record.Decisions, actionRecord)
			continue
		}
		if isOpenAction(d.Action) {
			opensAllowedThisCycle++
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			actionRecord.Error = err.Error()
			if isProtectionUpdateRejected(err) {
				at.logWarnf("⚠️ %s %s rejected by protection policy: %v", d.Symbol, d.Action, err)
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("⚠️ %s %s rejected by protection policy: %v", d.Symbol, d.Action, err))
			} else {
				at.logErrorf("❌ Failed to execute decision (%s %s): %v", d.Symbol, d.Action, err)
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s failed: %v", d.Symbol, d.Action, err))
			}
		} else {
			actionRecord.Success = true
			if actionRecord.Degraded {
				degradedLog := fmt.Sprintf("⚠️ %s %s partial/degraded: %s", d.Symbol, d.Action, actionRecord.DegradedReason)
				at.logWarnf("%s", degradedLog)
				record.ExecutionLog = append(record.ExecutionLog, degradedLog)
			} else {
				record.ExecutionLog = append(record.ExecutionLog, formatDecisionSuccessLog(d, at.entryConfidenceThreshold()))
			}
			// Brief delay after successful execution
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 9. Save decision record
	if err := at.saveDecision(record); err != nil {
		at.logWarnf("⚠ Failed to save decision record: %v", err)
	}

	return nil
}

func (at *AutoTrader) entryConfidenceThreshold() int {
	if at.config.StrategyConfig != nil && at.config.StrategyConfig.RiskControl.MinConfidence > 0 {
		return at.config.StrategyConfig.RiskControl.MinConfidence
	}
	return 75
}

func formatDecisionSuccessLog(d kernel.Decision, entryThreshold int) string {
	base := fmt.Sprintf("✓ %s %s succeeded", d.Symbol, d.Action)
	if d.Action == "wait" {
		if d.Confidence > 0 {
			return fmt.Sprintf("%s · entry confidence %d%% < threshold %d%%", base, d.Confidence, entryThreshold)
		}
		return fmt.Sprintf("%s · entry confidence unavailable (threshold %d%%)", base, entryThreshold)
	}
	if d.Confidence > 0 {
		return fmt.Sprintf("%s · confidence %d%%", base, d.Confidence)
	}
	return base
}

func normalizeUniverseSymbol(exchange, symbol string) string {
	return market.NormalizeForExchange(exchange, strings.TrimSpace(symbol))
}

// universeBaseKey returns the bare base ticker used for fuzzy candidate
// matching. The AI sometimes echoes a candidate as "QNTUSDC", "QNT-USDC",
// "QNTUSDT", or even just "QNT" instead of the canonical "xyz:QNT" we
// supplied in the prompt. All of those resolve to the same Hyperliquid
// instrument, so we accept any of them when the base matches an allowed
// candidate's base.
func universeBaseKey(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "" {
		return ""
	}
	// Drop any xyz:/XYZ: prefix.
	s = strings.TrimPrefix(s, "XYZ:")
	// Drop common quote suffixes in order of specificity.
	for _, suf := range []string{"-USDC", "-USDT", "USDC", "USDT", "USD"} {
		if strings.HasSuffix(s, suf) && len(s) > len(suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	// Normalize alias (TESLA -> TSLA, ROBINHOOD -> HOOD, etc.).
	return hyperliquid.NormalizeXYZAlias(s)
}

func isOpenDecision(action string) bool {
	a := strings.ToLower(strings.TrimSpace(action))
	return a == "open_long" || a == "open_short"
}

// isWaitDecision reports whether the action is a no-op that never places an
// order. The engine's safe fallback (and the AI itself) signals a global wait
// with an "ALL" or empty symbol, which is only meaningful for these actions.
func isWaitDecision(action string) bool {
	a := strings.ToLower(strings.TrimSpace(action))
	return a == "wait" || a == "hold"
}

func (at *AutoTrader) filterDecisionsToStrategyUniverse(decisions []kernel.Decision, ctx *kernel.Context) []kernel.Decision {
	if ctx == nil || len(decisions) == 0 {
		return decisions
	}

	allowed := make(map[string]bool, len(ctx.CandidateCoins))
	allowedBases := make(map[string]bool, len(ctx.CandidateCoins))
	for _, coin := range ctx.CandidateCoins {
		allowed[normalizeUniverseSymbol(at.exchange, coin.Symbol)] = true
		if base := universeBaseKey(coin.Symbol); base != "" {
			allowedBases[base] = true
		}
	}

	positions := make(map[string]bool, len(ctx.Positions))
	positionBases := make(map[string]bool, len(ctx.Positions))
	for _, pos := range ctx.Positions {
		positions[normalizeUniverseSymbol(at.exchange, pos.Symbol)] = true
		if base := universeBaseKey(pos.Symbol); base != "" {
			positionBases[base] = true
		}
	}

	filtered := make([]kernel.Decision, 0, len(decisions))
	for _, d := range decisions {
		// The engine's safe fallback and the AI both use an "ALL" or empty
		// symbol to signal a global wait. Check the raw value before exchange
		// normalization: NormalizeForExchange rewrites "ALL" -> "ALLUSDT" and
		// "" -> "USDT", which would make the sentinel unrecognizable and trip
		// the candidate-universe warning on every cycle. Wait decisions carry
		// no order intent, so letting them through is safe and preserves the
		// wait record in the decision history.
		rawSymbol := strings.ToUpper(strings.TrimSpace(d.Symbol))
		if (rawSymbol == "" || rawSymbol == "ALL") && isWaitDecision(d.Action) {
			filtered = append(filtered, d)
			continue
		}

		sym := normalizeUniverseSymbol(at.exchange, d.Symbol)
		if sym == "" || sym == "ALL" {
			filtered = append(filtered, d)
			continue
		}

		if allowed[sym] || positions[sym] {
			filtered = append(filtered, d)
			continue
		}

		// Fall back to base-level match so AI outputs like "QNTUSDC" still
		// resolve to candidate "xyz:QNT". Rewrite the decision's symbol to
		// the canonical form so downstream order placement works.
		if base := universeBaseKey(d.Symbol); base != "" {
			if allowedBases[base] || positionBases[base] {
				if canonical := canonicalUniverseSymbolForBase(ctx, base); canonical != "" {
					d.Symbol = canonical
				}
				filtered = append(filtered, d)
				continue
			}
		}

		if isOpenDecision(d.Action) {
			at.logWarnf("🚫 Blocked AI %s for %s: symbol is outside strategy candidate universe", d.Action, d.Symbol)
		} else {
			at.logWarnf("🚫 Dropped AI decision for %s: symbol is outside strategy candidate universe", d.Symbol)
		}
	}
	return filtered
}

// canonicalUniverseSymbolForBase finds the canonical candidate or position
// symbol that has the given base. Used to rewrite an AI decision's symbol
// (e.g. "QNTUSDC") to the form our order pipeline expects ("xyz:QNT").
func canonicalUniverseSymbolForBase(ctx *kernel.Context, base string) string {
	for _, coin := range ctx.CandidateCoins {
		if universeBaseKey(coin.Symbol) == base {
			return coin.Symbol
		}
	}
	for _, pos := range ctx.Positions {
		if universeBaseKey(pos.Symbol) == base {
			return pos.Symbol
		}
	}
	return ""
}

// buildTradingContext builds trading context
func (at *AutoTrader) buildTradingContext() (*kernel.Context, error) {
	if at.executionMode == ExecutionModePaper && at.paperBroker != nil {
		if err := at.paperBroker.RefreshOpenPositions(); err != nil {
			return nil, fmt.Errorf("failed to refresh paper positions: %w", err)
		}
	}
	// 1. Get account information
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get account balance: %w", err)
	}

	// Get account fields
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0
	totalEquity := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Use totalEquity directly if provided by trader (more accurate)
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		totalEquity = eq
	} else {
		// Fallback: Total Equity = Wallet balance + Unrealized profit
		totalEquity = totalWalletBalance + totalUnrealizedProfit
	}

	// 2. Get position information
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	var positionInfos []kernel.PositionInfo
	managedSnapshots := make([]managedPosition, 0, len(positions))
	totalMarginUsed := 0.0

	// Current position key set (for cleaning up closed position records)
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // Short position quantity is negative, convert to positive
		}

		// Skip closed positions (quantity = 0), prevent "ghost positions" from being passed to AI
		if quantity == 0 {
			continue
		}

		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// Calculate the actual position-margin denominator. Providers that
		// expose initial margin are authoritative; otherwise use entry notional
		// rather than the moving mark notional.
		leverage := positionLeverageFromMap(pos)
		marginUsed := positionInitialMargin(pos, entryPrice, markPrice, quantity, leverage)
		totalMarginUsed += marginUsed

		// Calculate P&L percentage (based on margin, considering leverage)
		pnlPct := calculatePnLPercentage(unrealizedPnl, marginUsed)
		pricePnLPct := calculatePricePnLPct(entryPrice, markPrice, side)

		// Get position open time from exchange (preferred) or fallback to local tracking
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true

		var updateTime int64
		// Priority 1: Get from database (trader_positions table) - most accurate
		if at.store != nil {
			if dbPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, side); err == nil && dbPos != nil {
				if dbPos.EntryTime > 0 {
					updateTime = dbPos.EntryTime
				}
			}
		}
		// Priority 2: Get from exchange API (Bybit: createdTime, OKX: createdTime)
		if updateTime == 0 {
			if createdTime, ok := pos["createdTime"].(int64); ok && createdTime > 0 {
				updateTime = createdTime
			}
		}
		// Priority 3: Fallback to local tracking
		if updateTime == 0 {
			if _, exists := at.positionFirstSeenTime[posKey]; !exists {
				at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
			}
			updateTime = at.positionFirstSeenTime[posKey]
		}

		// Get peak profit rate for this position
		at.peakPnLCacheMutex.RLock()
		peakPnlPct := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()

		protectionSnapshot := managedPositionFromMap(pos)
		at.enrichManagedPosition(&protectionSnapshot)
		managedSnapshots = append(managedSnapshots, protectionSnapshot)

		positionInfos = append(positionInfos, kernel.PositionInfo{
			Symbol:            symbol,
			Side:              side,
			EntryPrice:        entryPrice,
			MarkPrice:         markPrice,
			Quantity:          quantity,
			Leverage:          leverage,
			UnrealizedPnL:     unrealizedPnl,
			UnrealizedPnLPct:  pnlPct,
			PricePnLPct:       pricePnLPct,
			PeakPnLPct:        peakPnlPct,
			LiquidationPrice:  liquidationPrice,
			MarginUsed:        marginUsed,
			StopLoss:          protectionSnapshot.stopLoss,
			TakeProfit:        protectionSnapshot.takeProfit,
			StopLossState:     string(protectionSnapshot.stopLossState),
			TakeProfitState:   string(protectionSnapshot.takeProfitState),
			ProtectionWarning: protectionSnapshot.protectionErr,
			UpdateTime:        updateTime,
		})
	}
	at.replaceManagedPositionSnapshots(managedSnapshots)

	// Clean up closed position records
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}

	// 3. Use strategy engine to get candidate coins (must have strategy engine)
	var candidateCoins []kernel.CandidateCoin
	if at.strategyEngine == nil {
		at.logWarnf("⚠️ No strategy engine configured, skipping candidate coins")
	} else {
		coins, err := at.strategyEngine.GetCandidateCoins()
		if err != nil {
			// Candidate discovery is part of the market-data safety boundary. Do
			// not turn an upstream/proxy failure into an apparently successful
			// empty-candidate cycle that can be reported as a no-op.
			at.logWarnf("⚠️ Failed to get candidate coins: %v", err)
			return nil, fmt.Errorf("failed to get candidate coins: %w", err)
		} else {
			candidateCoins = coins
			logger.Infof("📋 [%s] Strategy engine fetched candidate coins: %d", at.name, len(candidateCoins))
		}
	}

	// 4. Calculate total P&L
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. Get leverage from strategy config
	strategyConfig := at.strategyEngine.GetConfig()
	maxLeverage := strategyConfig.RiskControl.MaxLeverage
	logger.Infof("📋 [%s] Strategy maximum leverage: %dx", at.name, maxLeverage)
	at.isRunningMutex.RLock()
	callCount := at.callCount
	at.isRunningMutex.RUnlock()

	// 6. Build context
	ctx := &kernel.Context{
		CurrentTime:            time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		RuntimeMinutes:         int(time.Since(at.startTime).Minutes()),
		CallCount:              callCount,
		TraderID:               at.id,
		StrategyID:             at.config.StrategyID,
		RequireFreshMarketData: at.executionMode == ExecutionModeLive,
		Account: kernel.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			UnrealizedPnL:    totalUnrealizedProfit,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
	}

	// 7. Add recent closed trades (if store is available)
	if at.store != nil {
		// Get recent 10 closed trades for AI context
		recentTrades, err := at.store.Position().GetRecentTrades(at.id, 10)
		if err != nil {
			at.logWarnf("⚠️ Failed to get recent trades: %v", err)
		} else {
			logger.Infof("📊 [%s] Found %d recent closed trades for AI context", at.name, len(recentTrades))
			for _, trade := range recentTrades {
				// Convert Unix timestamps to formatted strings for AI readability
				entryTimeStr := ""
				if trade.EntryTime > 0 {
					entryTimeStr = time.Unix(trade.EntryTime, 0).UTC().Format("01-02 15:04 UTC")
				}
				exitTimeStr := ""
				if trade.ExitTime > 0 {
					exitTimeStr = time.Unix(trade.ExitTime, 0).UTC().Format("01-02 15:04 UTC")
				}

				ctx.RecentOrders = append(ctx.RecentOrders, kernel.RecentOrder{
					Symbol:       trade.Symbol,
					Side:         trade.Side,
					EntryPrice:   trade.EntryPrice,
					ExitPrice:    trade.ExitPrice,
					RealizedPnL:  trade.RealizedPnL,
					PnLPct:       trade.PnLPct,
					EntryTime:    entryTimeStr,
					ExitTime:     exitTimeStr,
					HoldDuration: trade.HoldDuration,
				})
			}
		}
		// Get trading statistics for AI context
		stats, err := at.store.Position().GetFullStats(at.id, at.initialBalance)
		if err != nil {
			at.logWarnf("⚠️ Failed to get trading stats: %v", err)
		} else if stats == nil {
			at.logWarnf("⚠️ GetFullStats returned nil")
		} else if stats.TotalTrades == 0 {
			at.logWarnf("⚠️ GetFullStats returned 0 trades")
		} else {
			ctx.TradingStats = &kernel.TradingStats{
				TotalTrades:    stats.TotalTrades,
				WinRate:        stats.WinRate,
				ProfitFactor:   stats.ProfitFactor,
				SharpeRatio:    stats.SharpeRatio,
				TotalPnL:       stats.TotalPnL,
				AvgWin:         stats.AvgWin,
				AvgLoss:        stats.AvgLoss,
				MaxDrawdownPct: stats.MaxDrawdownPct,
			}
			logger.Infof("📈 [%s] Trading stats: %d trades, %.1f%% win rate, PF=%.2f, Sharpe=%.2f, DD=%.1f%%",
				at.name, stats.TotalTrades, stats.WinRate, stats.ProfitFactor, stats.SharpeRatio, stats.MaxDrawdownPct)
		}
	} else {
		at.logWarnf("⚠️ Store is nil, cannot get recent trades")
	}

	return ctx, nil
}

// sortDecisionsByPriority sorts decisions: close positions first, then open positions, finally hold/wait
// This avoids position stacking overflow when changing positions
func sortDecisionsByPriority(decisions []kernel.Decision) []kernel.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// Define priority
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short":
			return 1 // Highest priority: close positions first
		case "update_position":
			return 2 // Protect existing profits before considering new entries
		case "open_long", "open_short":
			return 3 // Open positions later
		case "hold", "wait":
			return 4 // Lowest priority: wait
		default:
			return 999 // Unknown actions at the end
		}
	}

	// Copy decision list
	sorted := make([]kernel.Decision, len(decisions))
	copy(sorted, decisions)

	// Sort by priority
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

func floatFromPosition(position map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		switch value := position[key].(type) {
		case float64:
			return value
		case float32:
			return float64(value)
		case int:
			return float64(value)
		case int64:
			return float64(value)
		case json.Number:
			if parsed, err := value.Float64(); err == nil {
				return parsed
			}
		case string:
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

// invertDecisions mirrors entry signals while preserving exits for the actual
// positions visible to the AI. Protective prices exchange roles because a
// long stop below market becomes a short take-profit, and vice versa.
func invertDecisions(decisions []kernel.Decision) []kernel.Decision {
	inverted := make([]kernel.Decision, len(decisions))
	for decisionIndex, decision := range decisions {
		invertedDecision := decision
		switch decision.Action {
		case "open_long":
			invertedDecision.Action = "open_short"
			invertedDecision.Reasoning = "[Inverted Signal: open_long -> open_short] " + decision.Reasoning
			invertedDecision.StopLoss = decision.TakeProfit
			invertedDecision.TakeProfit = decision.StopLoss
		case "open_short":
			invertedDecision.Action = "open_long"
			invertedDecision.Reasoning = "[Inverted Signal: open_short -> open_long] " + decision.Reasoning
			invertedDecision.StopLoss = decision.TakeProfit
			invertedDecision.TakeProfit = decision.StopLoss
		}
		inverted[decisionIndex] = invertedDecision
	}
	return inverted
}
