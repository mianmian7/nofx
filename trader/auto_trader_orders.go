package trader

import (
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

const (
	// marginOverheadFactor and takerFeeRate approximate the total funds an
	// exchange reserves when opening a position:
	// totalRequired ≈ positionSize/leverage + positionSize*takerFeeRate + positionSize/leverage*1%
	//              = positionSize * (marginOverheadFactor/leverage + takerFeeRate)
	marginOverheadFactor = 1.01
	takerFeeRate         = 0.001

	// positionSizeSafetyFactor leaves a buffer below the maximum affordable
	// position size so a price move between sizing and execution cannot
	// trigger an insufficient-margin rejection.
	positionSizeSafetyFactor = 0.98
)

// executeDecisionWithRecord executes AI decision and records detailed information
func (at *AutoTrader) executeDecisionWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	if decision.Action == "open_long" || decision.Action == "open_short" {
		if provider, ok := at.trader.(leverageLimitProvider); ok {
			if err := clampDecisionToExchangeLeverageLimit(decision, provider); err != nil {
				return err
			}
			actionRecord.Leverage = decision.Leverage
		}
	}
	if at.executionMode == ExecutionModePaper && (decision.Action == "open_long" || decision.Action == "open_short") {
		if _, err := at.validateBinanceOpenMarket(decision.Symbol); err != nil {
			return err
		}
		if err := at.enforceOpenRiskBudget(decision); err != nil {
			return err
		}
	}
	if decision.Action == "update_position" {
		return at.executeUpdatePositionWithRecord(decision, actionRecord)
	}
	if at.executionMode == ExecutionModePaper {
		if at.paperBroker == nil {
			return fmt.Errorf("paper broker is not configured")
		}
		fill, err := at.paperBroker.ExecuteDecision(decision)
		if err != nil {
			return err
		}
		actionRecord.Action = decision.Action
		actionRecord.Symbol = fill.Symbol
		actionRecord.Quantity = fill.Quantity
		actionRecord.Leverage = decision.Leverage
		actionRecord.Price = fill.Price
		actionRecord.StopLoss = decision.StopLoss
		actionRecord.TakeProfit = decision.TakeProfit
		actionRecord.OrderID = fill.OrderID
		actionRecord.Timestamp = fill.Time
		actionRecord.Success = true
		return nil
	}
	if decision.Action == "open_long" || decision.Action == "open_short" {
		if err := validateExecutionSymbol(at.exchange, decision.Symbol); err != nil {
			return err
		}
	}
	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "hold", "wait":
		// No execution needed, just record
		return nil
	default:
		return fmt.Errorf("unknown action: %s", decision.Action)
	}
}

type managedPosition struct {
	symbol, side         string
	quantity             float64
	entry, current       float64
	stopLoss, takeProfit float64
}

func (at *AutoTrader) executeUpdatePositionWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	position, err := at.loadManagedPosition(decision.Symbol)
	if err != nil {
		return err
	}
	if err := validateProtectionUpdate(position, decision); err != nil {
		return err
	}

	actionRecord.Action = decision.Action
	actionRecord.Symbol = position.symbol
	actionRecord.Quantity = position.quantity
	actionRecord.Price = position.current
	actionRecord.StopLoss = decision.NewStopLoss
	actionRecord.TakeProfit = decision.NewTakeProfit
	actionRecord.Timestamp = time.Now().UTC()

	if at.executionMode == ExecutionModePaper {
		if at.paperBroker == nil {
			return fmt.Errorf("paper broker is not configured")
		}
		if _, err := at.paperBroker.ExecuteDecision(decision); err != nil {
			return err
		}
		actionRecord.Success = true
		return nil
	}

	positionSide := strings.ToUpper(position.side)
	if decision.NewStopLoss > 0 {
		if position.stopLoss <= 0 {
			return fmt.Errorf("cannot safely replace stop loss for %s: current stop price is unknown", position.symbol)
		}
		if err := at.replaceStopLoss(position, positionSide, decision.NewStopLoss); err != nil {
			return err
		}
	}
	if decision.NewTakeProfit > 0 {
		if position.takeProfit <= 0 {
			return fmt.Errorf("cannot safely replace take profit for %s: current target price is unknown", position.symbol)
		}
		if err := at.replaceTakeProfit(position, positionSide, decision.NewTakeProfit); err != nil {
			if decision.NewStopLoss <= 0 {
				return err
			}
			if restoreErr := at.replaceStopLoss(position, positionSide, position.stopLoss); restoreErr != nil {
				return fmt.Errorf("replace take profit: %v; CRITICAL: restore old stop %.4f failed: %v", err, position.stopLoss, restoreErr)
			}
			return fmt.Errorf("replace take profit: %w; old stop %.4f restored", err, position.stopLoss)
		}
	}
	actionRecord.Success = true
	return nil
}

func (at *AutoTrader) loadManagedPosition(symbol string) (managedPosition, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return managedPosition{}, fmt.Errorf("get positions for protection update: %w", err)
	}
	normalized := normalizedDecisionSymbol(at.exchange, symbol)
	var result managedPosition
	for _, position := range positions {
		positionSymbol, _ := position["symbol"].(string)
		if normalizedDecisionSymbol(at.exchange, positionSymbol) != normalized {
			continue
		}
		if result.symbol != "" {
			return managedPosition{}, fmt.Errorf("position side is ambiguous for %s", symbol)
		}
		result.symbol = positionSymbol
		result.side, _ = position["side"].(string)
		result.quantity = math.Abs(floatFromPosition(position, "positionAmt", "quantity"))
		result.entry = floatFromPosition(position, "entryPrice", "entry_price")
		result.current = floatFromPosition(position, "markPrice", "mark_price")
		result.stopLoss = floatFromPosition(position, "stop_loss", "stopLoss")
		result.takeProfit = floatFromPosition(position, "take_profit", "takeProfit")
	}
	if result.symbol == "" || result.quantity <= 0 || result.entry <= 0 {
		return managedPosition{}, fmt.Errorf("open position not found for %s", symbol)
	}
	if marketPrice, priceErr := at.trader.GetMarketPrice(result.symbol); priceErr == nil && marketPrice > 0 {
		result.current = marketPrice
	}
	if result.current <= 0 {
		return managedPosition{}, fmt.Errorf("current price unavailable for %s", symbol)
	}
	if result.stopLoss <= 0 || result.takeProfit <= 0 {
		orders, orderErr := at.trader.GetOpenOrders(result.symbol)
		if orderErr == nil {
			for _, order := range orders {
				if order.PositionSide != "" && !strings.EqualFold(order.PositionSide, result.side) {
					continue
				}
				orderType := strings.ToUpper(order.Type)
				trigger := order.StopPrice
				if trigger <= 0 {
					trigger = order.Price
				}
				switch {
				case strings.Contains(orderType, "TAKE_PROFIT"):
					result.takeProfit = trigger
				case strings.Contains(orderType, "STOP"):
					result.stopLoss = trigger
				}
			}
		}
	}
	return result, nil
}

func validateProtectionUpdate(position managedPosition, decision *kernel.Decision) error {
	if decision.NewStopLoss <= 0 && decision.NewTakeProfit <= 0 {
		return fmt.Errorf("update_position requires new_stop_loss or new_take_profit")
	}
	long := strings.EqualFold(position.side, "long")
	profitable := (long && position.current > position.entry) || (!long && position.current < position.entry)
	if decision.NewStopLoss > 0 {
		if long {
			if decision.NewStopLoss >= position.current {
				return fmt.Errorf("long stop %.4f must stay below current price %.4f", decision.NewStopLoss, position.current)
			}
			if position.stopLoss > 0 && decision.NewStopLoss <= position.stopLoss {
				return fmt.Errorf("long stop may only tighten: current %.4f, requested %.4f", position.stopLoss, decision.NewStopLoss)
			}
			if profitable && decision.NewStopLoss < position.entry*1.001 {
				return fmt.Errorf("profitable long stop must lock breakeven plus fees (minimum %.4f)", position.entry*1.001)
			}
		} else {
			if decision.NewStopLoss <= position.current {
				return fmt.Errorf("short stop %.4f must stay above current price %.4f", decision.NewStopLoss, position.current)
			}
			if position.stopLoss > 0 && decision.NewStopLoss >= position.stopLoss {
				return fmt.Errorf("short stop may only tighten: current %.4f, requested %.4f", position.stopLoss, decision.NewStopLoss)
			}
			if profitable && decision.NewStopLoss > position.entry*0.999 {
				return fmt.Errorf("profitable short stop must lock breakeven plus fees (maximum %.4f)", position.entry*0.999)
			}
		}
	}
	if decision.NewTakeProfit > 0 {
		if decision.NewStopLoss <= 0 {
			return fmt.Errorf("extending take profit requires a tightened new_stop_loss in the same decision")
		}
		if !profitable || decision.Confidence < 80 {
			return fmt.Errorf("take-profit extension requires an already profitable position and confidence >= 80")
		}
		if position.takeProfit <= 0 {
			return fmt.Errorf("current take-profit price is unavailable")
		}
		targetDistance := math.Abs(position.takeProfit - position.entry)
		progress := math.Abs(position.current-position.entry) / targetDistance
		if targetDistance <= 0 || progress < 0.55 {
			return fmt.Errorf("take-profit extension is premature: %.0f%% of the current target path completed, need at least 55%%", progress*100)
		}
		maxStep := math.Min(targetDistance*0.5, position.current*0.03)
		if long {
			if decision.NewTakeProfit <= math.Max(position.current, position.takeProfit) {
				return fmt.Errorf("long take profit may only extend beyond current target %.4f", position.takeProfit)
			}
			if decision.NewTakeProfit > position.takeProfit+maxStep {
				return fmt.Errorf("long take-profit extension is too large; maximum next target %.4f", position.takeProfit+maxStep)
			}
		} else {
			if decision.NewTakeProfit >= math.Min(position.current, position.takeProfit) {
				return fmt.Errorf("short take profit may only extend below current target %.4f", position.takeProfit)
			}
			if decision.NewTakeProfit < position.takeProfit-maxStep {
				return fmt.Errorf("short take-profit extension is too large; minimum next target %.4f", position.takeProfit-maxStep)
			}
		}
	}
	return nil
}

func (at *AutoTrader) replaceStopLoss(position managedPosition, positionSide string, requested float64) error {
	if err := at.trader.CancelStopLossOrders(position.symbol); err != nil {
		return fmt.Errorf("cancel current stop loss for %s: %w", position.symbol, err)
	}
	if err := at.trader.SetStopLoss(position.symbol, positionSide, position.quantity, requested); err != nil {
		restoreErr := at.trader.SetStopLoss(position.symbol, positionSide, position.quantity, position.stopLoss)
		if restoreErr != nil {
			return fmt.Errorf("set new stop loss: %v; CRITICAL: restore old stop %.4f failed: %v", err, position.stopLoss, restoreErr)
		}
		return fmt.Errorf("set new stop loss: %w; old stop %.4f restored", err, position.stopLoss)
	}
	return nil
}

func (at *AutoTrader) replaceTakeProfit(position managedPosition, positionSide string, requested float64) error {
	if err := at.trader.CancelTakeProfitOrders(position.symbol); err != nil {
		return fmt.Errorf("cancel current take profit for %s: %w", position.symbol, err)
	}
	if err := at.trader.SetTakeProfit(position.symbol, positionSide, position.quantity, requested); err != nil {
		restoreErr := at.trader.SetTakeProfit(position.symbol, positionSide, position.quantity, position.takeProfit)
		if restoreErr != nil {
			return fmt.Errorf("set new take profit: %v; CRITICAL: restore old target %.4f failed: %v", err, position.takeProfit, restoreErr)
		}
		return fmt.Errorf("set new take profit: %w; old target %.4f restored", err, position.takeProfit)
	}
	return nil
}

type leverageLimitProvider interface {
	GetMaxLeverage(symbol string) (int, error)
}

type notionalAwareLeverageLimitProvider interface {
	GetMaxLeverageForNotional(symbol string, notional float64) (int, error)
}

func clampDecisionToExchangeLeverageLimit(decision *kernel.Decision, provider leverageLimitProvider) error {
	maxLeverage := 0
	var err error
	if tiered, ok := provider.(notionalAwareLeverageLimitProvider); ok && decision.PositionSizeUSD > 0 {
		maxLeverage, err = tiered.GetMaxLeverageForNotional(decision.Symbol, decision.PositionSizeUSD)
	} else {
		maxLeverage, err = provider.GetMaxLeverage(decision.Symbol)
	}
	if err != nil {
		return fmt.Errorf("failed to verify exchange leverage limit for %s: %w", decision.Symbol, err)
	}
	if maxLeverage <= 0 {
		return fmt.Errorf("exchange returned invalid leverage limit %d for %s", maxLeverage, decision.Symbol)
	}
	if decision.Leverage > maxLeverage {
		logger.Infof("  ⚠️ %s decision leverage %dx exceeds exchange maximum %dx; reducing to %dx", decision.Symbol, decision.Leverage, maxLeverage, maxLeverage)
		decision.Leverage = maxLeverage
	}
	return nil
}

func numericBalanceField(balance map[string]interface{}, key string) float64 {
	if value, ok := balance[key].(float64); ok && !math.IsNaN(value) && !math.IsInf(value, 0) {
		return value
	}
	return 0
}

func calculateMaximumAffordableNotional(availableMarginBudget float64, leverage int) float64 {
	if availableMarginBudget <= 0 || leverage <= 0 {
		return 0
	}
	marginFactor := marginOverheadFactor/float64(leverage) + takerFeeRate
	return availableMarginBudget / marginFactor
}

func calculateRiskLimitedNotional(
	action string,
	entryPrice float64,
	stopLoss float64,
	riskUSD float64,
) (float64, bool, error) {
	if riskUSD <= 0 {
		return 0, false, nil
	}
	if entryPrice <= 0 || stopLoss <= 0 {
		return 0, false, fmt.Errorf(
			"risk-based sizing requires positive entry and stop prices: entry %.8f stop %.8f",
			entryPrice,
			stopLoss,
		)
	}

	switch action {
	case "open_long":
		if stopLoss >= entryPrice {
			return 0, false, fmt.Errorf(
				"long stop loss %.8f must be below current entry price %.8f",
				stopLoss,
				entryPrice,
			)
		}
	case "open_short":
		if stopLoss <= entryPrice {
			return 0, false, fmt.Errorf(
				"short stop loss %.8f must be above current entry price %.8f",
				stopLoss,
				entryPrice,
			)
		}
	default:
		return 0, false, nil
	}

	stopDistanceRatio := math.Abs(entryPrice-stopLoss) / entryPrice
	// Include a conservative round-trip taker-fee allowance so risk_usd
	// remains an upper bound rather than excluding execution costs.
	totalLossRatio := stopDistanceRatio + 2*takerFeeRate
	if totalLossRatio <= 0 {
		return 0, false, fmt.Errorf("invalid stop-loss distance for risk-based sizing")
	}
	return riskUSD / totalLossRatio, true, nil
}

func calculateRemainingStrategyMargin(
	equity float64,
	maxMarginUsage float64,
	positionMargin float64,
	openOrderMargin float64,
) float64 {
	if equity <= 0 || maxMarginUsage <= 0 {
		return 0
	}
	maximumStrategyMargin := equity * math.Min(maxMarginUsage, 1)
	usedStrategyMargin := math.Max(positionMargin, 0) + math.Max(openOrderMargin, 0)
	return math.Max(maximumStrategyMargin-usedStrategyMargin, 0)
}

func estimatePositionMargin(positions []map[string]interface{}) float64 {
	totalPositionMargin := 0.0
	for _, position := range positions {
		if margin := numericBalanceField(position, "initial_margin"); margin > 0 {
			totalPositionMargin += margin
			continue
		}
		if margin := numericBalanceField(position, "margin_used"); margin > 0 {
			totalPositionMargin += margin
			continue
		}
		markPrice := numericBalanceField(position, "markPrice")
		quantity := math.Abs(numericBalanceField(position, "positionAmt"))
		leverage := numericBalanceField(position, "leverage")
		if markPrice > 0 && quantity > 0 && leverage > 0 {
			totalPositionMargin += markPrice * quantity / leverage
		}
	}
	return totalPositionMargin
}

func extractAccountMarginUsage(
	balance map[string]interface{},
	positions []map[string]interface{},
) (float64, float64) {
	openOrderMargin := numericBalanceField(balance, "totalOpenOrderInitialMargin")
	positionMargin := numericBalanceField(balance, "totalPositionInitialMargin")
	if positionMargin <= 0 {
		// Some exchange APIs expose only aggregate initial margin. Their aggregate
		// commonly includes open orders, so subtract the separately reported open
		// order amount before treating the remainder as position margin.
		aggregateInitialMargin := numericBalanceField(balance, "totalInitialMargin")
		if aggregateInitialMargin > 0 {
			positionMargin = math.Max(aggregateInitialMargin-openOrderMargin, 0)
		}
	}
	if positionMargin <= 0 && len(positions) > 0 {
		positionMargin = estimatePositionMargin(positions)
	}
	return positionMargin, openOrderMargin
}

// enforceOpenRiskBudget is shared by Paper and Live. It applies the selected
// sizing model, the strategy's portfolio margin limit, and the account's
// actual available-balance ceiling.
func (at *AutoTrader) enforceOpenRiskBudget(decision *kernel.Decision, entryPrices ...float64) error {
	if at.config.StrategyConfig == nil {
		return nil
	}
	if decision.Leverage <= 0 {
		return fmt.Errorf("leverage must be greater than 0: %d", decision.Leverage)
	}
	account := at.trader
	if at.executionMode == ExecutionModePaper {
		if at.paperBroker == nil {
			return fmt.Errorf("paper broker is not configured")
		}
		account = at.paperBroker
	}
	positions, err := account.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to verify positions for margin budget: %w", err)
	}
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == strings.TrimPrefix(decision.Action, "open_") {
			return fmt.Errorf("%s already has %s position", decision.Symbol, pos["side"])
		}
	}
	balance, err := account.GetBalance()
	if err != nil {
		return fmt.Errorf("failed to verify balance for margin budget: %w", err)
	}
	equity := numericBalanceField(balance, "totalEquity")
	if equity <= 0 {
		equity = numericBalanceField(balance, "totalWalletBalance") + numericBalanceField(balance, "totalUnrealizedProfit")
	}
	available := numericBalanceField(balance, "availableBalance")
	if equity <= 0 || available < 0 {
		return fmt.Errorf("invalid account values for margin budget: equity %.2f available %.2f", equity, available)
	}

	adjusted, capped := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol, decision.Leverage)
	if capped {
		decision.PositionSizeUSD = adjusted
	}
	if available <= 0 {
		return fmt.Errorf("no available balance for a new position")
	}

	entryPrice := 0.0
	if len(entryPrices) > 0 {
		entryPrice = entryPrices[0]
	}
	if decision.RiskUSD > 0 && entryPrice <= 0 {
		entryPrice, err = account.GetMarketPrice(decision.Symbol)
		if err != nil {
			return fmt.Errorf("failed to get current price for risk-based sizing: %w", err)
		}
	}
	maxByRisk, riskLimitEnabled, err := calculateRiskLimitedNotional(
		decision.Action,
		entryPrice,
		decision.StopLoss,
		decision.RiskUSD,
	)
	if err != nil {
		return err
	}
	if riskLimitEnabled && decision.PositionSizeUSD > maxByRisk {
		logger.Infof(
			"  ⚠️ [RISK CONTROL] Position %.2f USDT exceeds risk_usd %.2f at entry %.8f and stop %.8f; reducing to %.2f USDT",
			decision.PositionSizeUSD,
			decision.RiskUSD,
			entryPrice,
			decision.StopLoss,
			maxByRisk,
		)
		decision.PositionSizeUSD = maxByRisk
	}

	riskControl := at.config.StrategyConfig.RiskControl
	maxMarginUsage := riskControl.MaxMarginUsage
	if maxMarginUsage <= 0 {
		// Preserve compatibility with old strategy JSON that predates this field.
		maxMarginUsage = 1
	}
	maxMarginUsage = math.Min(maxMarginUsage, 1)
	positionMargin, openOrderMargin := extractAccountMarginUsage(balance, positions)
	remainingStrategyMargin := calculateRemainingStrategyMargin(
		equity,
		maxMarginUsage,
		positionMargin,
		openOrderMargin,
	)
	if remainingStrategyMargin <= 0 {
		return fmt.Errorf(
			"strategy margin usage limit reached: used %.2f of %.2f USDT",
			positionMargin+openOrderMargin,
			equity*maxMarginUsage,
		)
	}

	availableMarginBudget := math.Min(available, remainingStrategyMargin)
	maxByPortfolio := calculateMaximumAffordableNotional(availableMarginBudget, decision.Leverage)
	if decision.PositionSizeUSD > maxByPortfolio {
		adjustedPositionSize := maxByPortfolio * positionSizeSafetyFactor
		logger.Infof(
			"  ⚠️ [RISK CONTROL] Position %.2f USDT exceeds remaining margin budget %.2f USDT at %dx; reducing to %.2f USDT (max margin usage %.0f%%)",
			decision.PositionSizeUSD,
			availableMarginBudget,
			decision.Leverage,
			adjustedPositionSize,
			maxMarginUsage*100,
		)
		decision.PositionSizeUSD = adjustedPositionSize
	}
	return at.enforceMinPositionSize(decision.PositionSizeUSD)
}
func validateExecutionSymbol(exchange, symbol string) error {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if exchange == "hyperliquid" {
		return fmt.Errorf("Hyperliquid execution is disabled; configure Binance Futures instead")
	}
	normalized := strings.ToUpper(strings.TrimSpace(symbol))
	if normalized == "" || strings.HasPrefix(normalized, "XYZ:") || strings.HasSuffix(normalized, "-USDC") {
		return fmt.Errorf("refusing to execute residual Hyperliquid symbol %q on %s", symbol, exchange)
	}
	if exchange != "binance" {
		return nil
	}
	if !strings.HasSuffix(normalized, "USDT") || strings.ContainsAny(normalized, ":-_/ ") {
		return fmt.Errorf("refusing to execute non-Binance symbol %q on Binance Futures", symbol)
	}
	base := strings.TrimSuffix(normalized, "USDT")
	if base == "" {
		return fmt.Errorf("refusing to execute invalid Binance symbol %q", symbol)
	}
	for _, r := range base {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("refusing to execute invalid Binance symbol %q", symbol)
		}
	}
	return nil
}

func (at *AutoTrader) validateBinanceOpenMarket(symbol string) (*market.MarketAvailability, error) {
	if at == nil || (at.executionMode != ExecutionModePaper && !strings.EqualFold(at.exchange, "binance")) {
		return nil, nil
	}
	// Tests and specialized in-memory traders may construct AutoTrader directly.
	// Production instances always receive this client from NewAutoTrader.
	if at.binanceMarketClient == nil {
		return nil, nil
	}
	availability, err := at.binanceMarketClient.ValidateMarketAvailability(symbol)
	if err != nil {
		return nil, fmt.Errorf("refusing to open %s while Binance public market data is unavailable: %w", symbol, err)
	}
	return availability, nil
}

// executeOpenLongWithRecord executes open long position and records detailed information
func (at *AutoTrader) executeOpenLongWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  📈 Open long: %s", decision.Symbol)

	// ⚠️ Get current positions for multiple checks
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// [CODE ENFORCED] Check max positions limit
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}

	// Check if there's already a position in the same symbol and direction
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
			return fmt.Errorf("❌ %s already has long position, close it first", decision.Symbol)
		}
	}

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange)
	if err != nil {
		return fmt.Errorf("failed to get market data for %s: %w", decision.Symbol, err)
	}
	availability, err := at.validateBinanceOpenMarket(decision.Symbol)
	if err != nil {
		return err
	}
	if availability != nil {
		marketData.CurrentPrice = availability.Price
	}

	// Get balance (needed for multiple checks)
	balance, err := at.trader.GetBalance()
	if err != nil {
		return fmt.Errorf("failed to get account balance: %w", err)
	}
	availableBalance := 0.0
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Get equity for position value ratio check
	equity := 0.0
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		equity = eq
	} else if eq, ok := balance["totalWalletBalance"].(float64); ok && eq > 0 {
		equity = eq
	} else {
		equity = availableBalance // Fallback to available balance
	}

	at.applyAutopilotFullSizeOpen(decision, equity)
	if provider, ok := at.trader.(leverageLimitProvider); ok {
		if err := clampDecisionToExchangeLeverageLimit(decision, provider); err != nil {
			return err
		}
	}
	if err := at.enforceOpenRiskBudget(decision, marketData.CurrentPrice); err != nil {
		return err
	}

	// [CODE ENFORCED] Position Value Ratio Check: position_value <= equity × ratio
	adjustedPositionSize, wasCapped := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol, decision.Leverage)
	if wasCapped {
		decision.PositionSizeUSD = adjustedPositionSize
	}

	// ⚠️ Auto-adjust position size if insufficient margin
	marginFactor := marginOverheadFactor/float64(decision.Leverage) + takerFeeRate
	maxAffordablePositionSize := availableBalance / marginFactor

	actualPositionSize := decision.PositionSizeUSD
	if actualPositionSize > maxAffordablePositionSize {
		adjustedSize := maxAffordablePositionSize * positionSizeSafetyFactor
		logger.Infof("  ⚠️ Position size %.2f exceeds max affordable %.2f, auto-reducing to %.2f",
			actualPositionSize, maxAffordablePositionSize, adjustedSize)
		actualPositionSize = adjustedSize
		decision.PositionSizeUSD = actualPositionSize
	}

	// [CODE ENFORCED] Minimum position size check
	if err := at.enforceMinPositionSize(decision.PositionSizeUSD); err != nil {
		return err
	}

	// Calculate quantity with adjusted position size
	quantity := actualPositionSize / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// Set margin mode
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Continue execution, doesn't affect trading
	}

	// Open position
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return fmt.Errorf("failed to open long position for %s: %w", decision.Symbol, err)
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	logger.Infof("  ✓ Position opened successfully, order ID: %v, quantity: %.4f", order["orderId"], quantity)

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "open_long", quantity, marketData.CurrentPrice, decision.Leverage, 0)

	// Record position opening time
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// Set stop loss and take profit
	if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
		logger.Infof("  ⚠ Failed to set stop loss: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "LONG", quantity, decision.TakeProfit); err != nil {
		logger.Infof("  ⚠ Failed to set take profit: %v", err)
	}

	return nil
}

// executeOpenShortWithRecord executes open short position and records detailed information
func (at *AutoTrader) executeOpenShortWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  📉 Open short: %s", decision.Symbol)

	// ⚠️ Get current positions for multiple checks
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// [CODE ENFORCED] Check max positions limit
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}

	// Check if there's already a position in the same symbol and direction
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
			return fmt.Errorf("❌ %s already has short position, close it first", decision.Symbol)
		}
	}

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange)
	if err != nil {
		return fmt.Errorf("failed to get market data for %s: %w", decision.Symbol, err)
	}
	availability, err := at.validateBinanceOpenMarket(decision.Symbol)
	if err != nil {
		return err
	}
	if availability != nil {
		marketData.CurrentPrice = availability.Price
	}

	// Get balance (needed for multiple checks)
	balance, err := at.trader.GetBalance()
	if err != nil {
		return fmt.Errorf("failed to get account balance: %w", err)
	}
	availableBalance := 0.0
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Get equity for position value ratio check
	equity := 0.0
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		equity = eq
	} else if eq, ok := balance["totalWalletBalance"].(float64); ok && eq > 0 {
		equity = eq
	} else {
		equity = availableBalance // Fallback to available balance
	}

	at.applyAutopilotFullSizeOpen(decision, equity)
	if provider, ok := at.trader.(leverageLimitProvider); ok {
		if err := clampDecisionToExchangeLeverageLimit(decision, provider); err != nil {
			return err
		}
	}
	if err := at.enforceOpenRiskBudget(decision, marketData.CurrentPrice); err != nil {
		return err
	}

	// [CODE ENFORCED] Position Value Ratio Check: position_value <= equity × ratio
	adjustedPositionSize, wasCapped := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol, decision.Leverage)
	if wasCapped {
		decision.PositionSizeUSD = adjustedPositionSize
	}

	// ⚠️ Auto-adjust position size if insufficient margin
	marginFactor := marginOverheadFactor/float64(decision.Leverage) + takerFeeRate
	maxAffordablePositionSize := availableBalance / marginFactor

	actualPositionSize := decision.PositionSizeUSD
	if actualPositionSize > maxAffordablePositionSize {
		adjustedSize := maxAffordablePositionSize * positionSizeSafetyFactor
		logger.Infof("  ⚠️ Position size %.2f exceeds max affordable %.2f, auto-reducing to %.2f",
			actualPositionSize, maxAffordablePositionSize, adjustedSize)
		actualPositionSize = adjustedSize
		decision.PositionSizeUSD = actualPositionSize
	}

	// [CODE ENFORCED] Minimum position size check
	if err := at.enforceMinPositionSize(decision.PositionSizeUSD); err != nil {
		return err
	}

	// Calculate quantity with adjusted position size
	quantity := actualPositionSize / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// Set margin mode
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Continue execution, doesn't affect trading
	}

	// Open position
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return fmt.Errorf("failed to open short position for %s: %w", decision.Symbol, err)
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	logger.Infof("  ✓ Position opened successfully, order ID: %v, quantity: %.4f", order["orderId"], quantity)

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "open_short", quantity, marketData.CurrentPrice, decision.Leverage, 0)

	// Record position opening time
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// Set stop loss and take profit
	if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
		logger.Infof("  ⚠ Failed to set stop loss: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "SHORT", quantity, decision.TakeProfit); err != nil {
		logger.Infof("  ⚠ Failed to set take profit: %v", err)
	}

	return nil
}

// executeCloseLongWithRecord executes close long position and records detailed information
func (at *AutoTrader) executeCloseLongWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 Close long: %s", decision.Symbol)

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange)
	if err != nil {
		return fmt.Errorf("failed to get market data for %s: %w", decision.Symbol, err)
	}
	actionRecord.Price = marketData.CurrentPrice

	// Normalize symbol for database lookup
	normalizedSymbol := market.NormalizeForExchange(at.exchange, decision.Symbol)

	// Get entry price and quantity - prioritize local database for accurate quantity
	var entryPrice float64
	var quantity float64

	// First try to get from local database (more accurate for quantity)
	if at.store != nil {
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, normalizedSymbol, "LONG"); err == nil && openPos != nil {
			quantity = openPos.Quantity
			entryPrice = openPos.EntryPrice
			logger.Infof("  📊 Using local position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
		}
	}

	// Fallback to exchange API if local data not found
	if quantity == 0 {
		positions, err := at.trader.GetPositions()
		if err == nil {
			for _, pos := range positions {
				if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
					if ep, ok := pos["entryPrice"].(float64); ok {
						entryPrice = ep
					}
					if amt, ok := pos["positionAmt"].(float64); ok && amt > 0 {
						quantity = amt
					}
					break
				}
			}
		}
		logger.Infof("  📊 Using exchange position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
	}

	// Close position
	order, err := at.trader.CloseLong(decision.Symbol, 0) // 0 = close all
	if err != nil {
		return fmt.Errorf("failed to close long position for %s: %w", decision.Symbol, err)
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "close_long", quantity, marketData.CurrentPrice, 0, entryPrice)

	logger.Infof("  ✓ Position closed successfully")
	return nil
}

// executeCloseShortWithRecord executes close short position and records detailed information
func (at *AutoTrader) executeCloseShortWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 Close short: %s", decision.Symbol)

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange)
	if err != nil {
		return fmt.Errorf("failed to get market data for %s: %w", decision.Symbol, err)
	}
	actionRecord.Price = marketData.CurrentPrice

	// Normalize symbol for database lookup
	normalizedSymbol := market.NormalizeForExchange(at.exchange, decision.Symbol)

	// Get entry price and quantity - prioritize local database for accurate quantity
	var entryPrice float64
	var quantity float64

	// First try to get from local database (more accurate for quantity)
	if at.store != nil {
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, normalizedSymbol, "SHORT"); err == nil && openPos != nil {
			quantity = openPos.Quantity
			entryPrice = openPos.EntryPrice
			logger.Infof("  📊 Using local position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
		}
	}

	// Fallback to exchange API if local data not found
	if quantity == 0 {
		positions, err := at.trader.GetPositions()
		if err == nil {
			for _, pos := range positions {
				if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
					if ep, ok := pos["entryPrice"].(float64); ok {
						entryPrice = ep
					}
					if amt, ok := pos["positionAmt"].(float64); ok {
						quantity = -amt // positionAmt is negative for short
					}
					break
				}
			}
		}
		logger.Infof("  📊 Using exchange position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
	}

	// Close position
	order, err := at.trader.CloseShort(decision.Symbol, 0) // 0 = close all
	if err != nil {
		return fmt.Errorf("failed to close short position for %s: %w", decision.Symbol, err)
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "close_short", quantity, marketData.CurrentPrice, 0, entryPrice)

	logger.Infof("  ✓ Position closed successfully")
	return nil
}
