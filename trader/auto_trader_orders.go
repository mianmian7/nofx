package trader

import (
	"errors"
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strconv"
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

	// Unified hard stop boundary, expressed as Margin/Position PnL percent.
	// The execution layer converts it to a price using the final leverage.
	unifiedMarginStopLossPct = kernel.HardStopMarginPositionPnLPct

	// Live exchanges do not currently expose a consistent fee-rate or paid-entry
	// commission API through Trader. These values are therefore an explicit
	// conservative execution fallback, not claimed Bitget/Binance fee rates:
	// reuse the existing 0.10% taker sizing allowance on entry and exit, add a
	// 0.05% adverse market-exit allowance, and require a 0.01% positive buffer.
	protectionFallbackSlippageRate   = 0.0005
	protectionFallbackProfitRate     = 0.0001
	protectionNormalizationTolerance = 0.0001
)

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
		availability, err := at.validateOpenMarket(decision.Symbol)
		if err != nil {
			return err
		}
		if at.paperBroker == nil {
			return fmt.Errorf("paper broker is not configured")
		}
		entryPrice := 0.0
		if availability != nil {
			entryPrice = availability.Price
		}
		if entryPrice <= 0 {
			entryPrice, err = at.paperBroker.GetMarketPrice(decision.Symbol)
			if err != nil {
				return fmt.Errorf("failed to get current price for %s: %w", decision.Symbol, err)
			}
		}
		if err := at.normalizeStopLossAtEntry(decision, entryPrice); err != nil {
			return err
		}
		if err := validateOpenProtection(decision.Action, entryPrice, decision.StopLoss, decision.TakeProfit); err != nil {
			return err
		}
		if err := at.enforceOpenRiskBudget(decision, entryPrice); err != nil {
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
	stopLossState        ProtectionLevelStatus
	takeProfitState      ProtectionLevelStatus
	stopLossOrderID      string
	takeProfitOrderID    string
	protectionErr        string
	priceTick            float64
	breakevenCosts       protectionBreakevenCosts
	breakevenCostsKnown  bool
}

type protectionBreakevenCosts struct {
	EntryFeeQuote    float64
	ExitFeeRate      float64
	FundingCostQuote float64
	SlippageRate     float64
	ProfitBufferRate float64
	Source           string
}

type protectionPriceTickProvider interface {
	GetProtectionPriceTick(symbol string) (float64, error)
}

type protectionBreakevenCostProvider interface {
	GetProtectionBreakevenCosts(symbol, side string, entryPrice, quantity float64) (protectionBreakevenCosts, error)
}

type protectionOrderModifier interface {
	ModifyProtectionOrder(symbol, orderID, kind, positionSide string, quantity, triggerPrice float64) error
}

type protectionUpdatePlan struct {
	initialTakeProfit bool
	degradedReason    string
}

// protectionUpdateRejectedError marks a decision that reached the position
// protection validator but violated a backend risk invariant. Keeping this
// distinct from exchange/network errors lets the decision loop log an
// expected model rejection at warning level while retaining its reason in the
// persisted action error and execution log.
type protectionUpdateRejectedError struct {
	err error
}

func (e *protectionUpdateRejectedError) Error() string {
	return fmt.Sprintf("protection_update_rejected: %v", e.err)
}

func (e *protectionUpdateRejectedError) Unwrap() error {
	return e.err
}

func isProtectionUpdateRejected(err error) bool {
	var rejection *protectionUpdateRejectedError
	return errors.As(err, &rejection)
}

func (at *AutoTrader) executeUpdatePositionWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	position, err := at.loadManagedPosition(decision.Symbol)
	if err != nil {
		return err
	}
	updatePlan, err := buildProtectionUpdatePlan(position, decision)
	if err != nil {
		return &protectionUpdateRejectedError{err: err}
	}

	actionRecord.Action = decision.Action
	actionRecord.Symbol = position.symbol
	actionRecord.Quantity = position.quantity
	actionRecord.Price = position.current
	actionRecord.StopLoss = decision.NewStopLoss
	actionRecord.TakeProfit = decision.NewTakeProfit
	actionRecord.Timestamp = time.Now().UTC()
	if updatePlan.degradedReason != "" {
		actionRecord.Degraded = true
		actionRecord.DegradedReason = updatePlan.degradedReason
	}

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
		if position.stopLossState != ProtectionPresent || position.stopLoss <= 0 {
			return fmt.Errorf("cannot safely replace stop loss for %s: current stop state=%s price=%.8f", position.symbol, position.stopLossState, position.stopLoss)
		}
		if err := at.replaceStopLoss(position, positionSide, decision.NewStopLoss); err != nil {
			return err
		}
	}
	if decision.NewTakeProfit > 0 {
		var replaceErr error
		if updatePlan.initialTakeProfit {
			replaceErr = at.setInitialTakeProfit(position, positionSide, decision.NewTakeProfit)
		} else {
			replaceErr = at.replaceTakeProfit(position, positionSide, decision.NewTakeProfit)
		}
		if replaceErr != nil {
			if decision.NewStopLoss <= 0 {
				return replaceErr
			}
			if updatePlan.initialTakeProfit {
				return fmt.Errorf("set initial take profit: %w; tightened stop remains active", replaceErr)
			}
			if restoreErr := at.replaceStopLoss(position, positionSide, position.stopLoss); restoreErr != nil {
				return fmt.Errorf("replace take profit: %v; CRITICAL: restore old stop %.4f failed: %v", replaceErr, position.stopLoss, restoreErr)
			}
			return fmt.Errorf("replace take profit: %w; old stop %.4f restored", replaceErr, position.stopLoss)
		}
	}
	actionRecord.Success = true
	return nil
}

func (at *AutoTrader) loadManagedPosition(symbol string) (managedPosition, error) {
	if snapshot, ok := at.consumeManagedPositionSnapshot(symbol); ok {
		return snapshot, nil
	}
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
		result = managedPositionFromMap(position)
	}
	if result.symbol == "" || result.quantity <= 0 || result.entry <= 0 {
		return managedPosition{}, fmt.Errorf("open position not found for %s", symbol)
	}
	if result.current <= 0 {
		return managedPosition{}, fmt.Errorf("current mark price unavailable for %s", symbol)
	}
	if at.executionMode == ExecutionModePaper && at.paperBroker != nil {
		paperMark, markErr := at.paperBroker.GetMarketPrice(result.symbol)
		if markErr != nil || paperMark <= 0 {
			return managedPosition{}, fmt.Errorf("current paper mark price unavailable for %s: %w", symbol, markErr)
		}
		result.current = paperMark
	}
	at.enrichManagedPosition(&result)
	return result, nil
}

func (at *AutoTrader) enrichManagedPosition(result *managedPosition) {
	at.populateManagedPositionProtection(result)
	if provider, ok := at.trader.(protectionPriceTickProvider); ok {
		if tick, tickErr := provider.GetProtectionPriceTick(result.symbol); tickErr == nil && tick > 0 && !math.IsNaN(tick) && !math.IsInf(tick, 0) {
			result.priceTick = tick
		}
	}
	result.breakevenCosts = fallbackProtectionBreakevenCosts(result.entry, result.quantity)
	result.breakevenCostsKnown = true
	if provider, ok := at.trader.(protectionBreakevenCostProvider); ok {
		costs, costErr := provider.GetProtectionBreakevenCosts(result.symbol, result.side, result.entry, result.quantity)
		if costErr != nil {
			result.breakevenCostsKnown = false
			result.breakevenCosts.Source = fmt.Sprintf("provider unavailable: %v", costErr)
		} else if err := validateProtectionBreakevenCosts(costs); err != nil {
			result.breakevenCostsKnown = false
			result.breakevenCosts.Source = fmt.Sprintf("provider invalid: %v", err)
		} else {
			result.breakevenCosts = costs
			result.breakevenCostsKnown = true
		}
	}
}

func managedPositionFromMap(position map[string]interface{}) managedPosition {
	result := managedPosition{}
	result.symbol, _ = position["symbol"].(string)
	result.side, _ = position["side"].(string)
	result.quantity = math.Abs(floatFromPosition(position, "positionAmt", "quantity"))
	result.entry = floatFromPosition(position, "entryPrice", "entry_price")
	result.current = floatFromPosition(position, "markPrice", "mark_price")
	result.stopLoss, result.stopLossState = protectionLevelFromPosition(position, "stop_loss", "stopLoss")
	result.takeProfit, result.takeProfitState = protectionLevelFromPosition(position, "take_profit", "takeProfit")
	return result
}

func protectionLevelFromPosition(position map[string]interface{}, keys ...string) (float64, ProtectionLevelStatus) {
	for _, key := range keys {
		value, exists := position[key]
		if !exists {
			continue
		}
		var price float64
		var err error
		switch typed := value.(type) {
		case float64:
			price = typed
		case float32:
			price = float64(typed)
		case int:
			price = float64(typed)
		case int64:
			price = float64(typed)
		case string:
			price, err = strconv.ParseFloat(strings.TrimSpace(typed), 64)
		default:
			err = fmt.Errorf("unsupported price type %T", value)
		}
		if err != nil {
			return 0, ProtectionUnavailable
		}
		if price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0) {
			return price, ProtectionPresent
		}
		if price == 0 {
			return 0, ProtectionConfirmedAbsent
		}
		return 0, ProtectionUnavailable
	}
	return 0, ProtectionUnavailable
}

func (at *AutoTrader) populateManagedPositionProtection(position *managedPosition) {
	if provider, ok := at.trader.(ProtectionSnapshotProvider); ok {
		snapshot, err := provider.GetProtectionSnapshot(position.symbol, position.side)
		applyProtectionLevelSnapshot(position, true, snapshot.StopLoss)
		applyProtectionLevelSnapshot(position, false, snapshot.TakeProfit)
		if err != nil {
			position.protectionErr = err.Error()
		}
		return
	}
	if position.stopLossState == ProtectionPresent && position.takeProfitState == ProtectionPresent {
		return
	}
	orders, err := at.trader.GetOpenOrders(position.symbol)
	if err != nil {
		if position.stopLossState != ProtectionPresent {
			position.stopLossState = ProtectionUnavailable
		}
		if position.takeProfitState != ProtectionPresent {
			position.takeProfitState = ProtectionUnavailable
		}
		position.protectionErr = err.Error()
		return
	}
	if position.stopLossState != ProtectionPresent {
		position.stopLossState = ProtectionConfirmedAbsent
	}
	if position.takeProfitState != ProtectionPresent {
		position.takeProfitState = ProtectionConfirmedAbsent
	}
	for _, order := range orders {
		if order.PositionSide != "" && !strings.EqualFold(order.PositionSide, position.side) {
			continue
		}
		trigger := order.StopPrice
		if trigger <= 0 {
			trigger = order.Price
		}
		orderType := strings.ToUpper(order.Type)
		switch {
		case strings.Contains(orderType, "TAKE_PROFIT"):
			mergeProtectionOrder(position, false, trigger, order.OrderID)
		case strings.Contains(orderType, "STOP"):
			mergeProtectionOrder(position, true, trigger, order.OrderID)
		}
	}
}

func applyProtectionLevelSnapshot(position *managedPosition, stopLoss bool, level ProtectionLevelSnapshot) {
	status := level.Status
	if status == "" {
		status = ProtectionUnavailable
	}
	if level.Price <= 0 && status == ProtectionPresent {
		status = ProtectionUnavailable
	}
	if stopLoss {
		position.stopLoss, position.stopLossState, position.stopLossOrderID = level.Price, status, level.OrderID
		return
	}
	position.takeProfit, position.takeProfitState, position.takeProfitOrderID = level.Price, status, level.OrderID
}

func mergeProtectionOrder(position *managedPosition, stopLoss bool, trigger float64, orderID string) {
	if trigger <= 0 || math.IsNaN(trigger) || math.IsInf(trigger, 0) {
		if stopLoss {
			position.stopLossState = ProtectionUnavailable
		} else {
			position.takeProfitState = ProtectionUnavailable
		}
		return
	}
	if stopLoss {
		if position.stopLossState == ProtectionPresent {
			position.stopLoss, position.stopLossOrderID, position.stopLossState = 0, "", ProtectionAmbiguous
			return
		}
		position.stopLoss, position.stopLossOrderID, position.stopLossState = trigger, orderID, ProtectionPresent
		return
	}
	if position.takeProfitState == ProtectionPresent {
		position.takeProfit, position.takeProfitOrderID, position.takeProfitState = 0, "", ProtectionAmbiguous
		return
	}
	position.takeProfit, position.takeProfitOrderID, position.takeProfitState = trigger, orderID, ProtectionPresent
}

func (at *AutoTrader) replaceManagedPositionSnapshots(snapshots []managedPosition) {
	next := make(map[string]managedPosition, len(snapshots))
	ambiguous := make(map[string]bool)
	for _, snapshot := range snapshots {
		key := normalizedDecisionSymbol(at.exchange, snapshot.symbol)
		if _, exists := next[key]; exists {
			delete(next, key)
			ambiguous[key] = true
			continue
		}
		if ambiguous[key] {
			continue
		}
		next[key] = snapshot
	}
	at.protectionSnapshotMu.Lock()
	at.protectionSnapshots = next
	at.protectionSnapshotMu.Unlock()
}

func (at *AutoTrader) consumeManagedPositionSnapshot(symbol string) (managedPosition, bool) {
	key := normalizedDecisionSymbol(at.exchange, symbol)
	at.protectionSnapshotMu.Lock()
	defer at.protectionSnapshotMu.Unlock()
	snapshot, ok := at.protectionSnapshots[key]
	if ok {
		delete(at.protectionSnapshots, key)
	}
	return snapshot, ok
}

func fallbackProtectionBreakevenCosts(entryPrice, quantity float64) protectionBreakevenCosts {
	return protectionBreakevenCosts{
		EntryFeeQuote:    entryPrice * quantity * takerFeeRate,
		ExitFeeRate:      takerFeeRate,
		SlippageRate:     protectionFallbackSlippageRate,
		ProfitBufferRate: protectionFallbackProfitRate,
		Source:           "conservative fallback: existing 0.10% taker allowance each side + 0.05% exit slippage allowance + 0.01% profit buffer; not an exchange fee quote",
	}
}

func validateProtectionBreakevenCosts(costs protectionBreakevenCosts) error {
	values := []float64{costs.EntryFeeQuote, costs.ExitFeeRate, costs.FundingCostQuote, costs.SlippageRate, costs.ProfitBufferRate}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("non-finite breakeven cost")
		}
	}
	if costs.EntryFeeQuote < 0 || costs.ExitFeeRate < 0 || costs.ExitFeeRate >= 1 || costs.SlippageRate < 0 || costs.SlippageRate >= 1 || costs.ProfitBufferRate < 0 {
		return fmt.Errorf("invalid breakeven cost rates")
	}
	if strings.TrimSpace(costs.Source) == "" {
		return fmt.Errorf("breakeven cost source is empty")
	}
	return nil
}

// feeInclusiveBreakevenBoundary returns an absolute mark-trigger price. Entry
// fee and settled funding are quote-currency amounts; exit fee, adverse market
// slippage and the positive profit buffer are rates. This keeps every term in
// the same quote-price/position-PnL unit before dividing by quantity.
func feeInclusiveBreakevenBoundary(position managedPosition) (float64, error) {
	if position.entry <= 0 || position.quantity <= 0 || math.IsNaN(position.entry) || math.IsInf(position.entry, 0) {
		return 0, fmt.Errorf("breakeven calculation requires positive finite entry price and quantity")
	}
	if !position.breakevenCostsKnown {
		return 0, fmt.Errorf("breakeven costs are unavailable: %s", position.breakevenCosts.Source)
	}
	if err := validateProtectionBreakevenCosts(position.breakevenCosts); err != nil {
		return 0, err
	}
	costs := position.breakevenCosts
	entryNotional := position.entry * position.quantity
	fixedCost := costs.EntryFeeQuote + costs.FundingCostQuote + entryNotional*costs.ProfitBufferRate
	long := strings.EqualFold(position.side, "long")
	var boundary float64
	if long {
		denominator := position.quantity * (1 - costs.SlippageRate) * (1 - costs.ExitFeeRate)
		boundary = (entryNotional + fixedCost) / denominator
	} else {
		denominator := position.quantity * (1 + costs.SlippageRate) * (1 + costs.ExitFeeRate)
		boundary = (entryNotional - fixedCost) / denominator
	}
	if boundary <= 0 || math.IsNaN(boundary) || math.IsInf(boundary, 0) {
		return 0, fmt.Errorf("breakeven calculation produced invalid boundary %.8f", boundary)
	}
	return boundary, nil
}

func ensureManagedPositionProtectionMetadata(position *managedPosition) {
	if position.quantity <= 0 {
		// Validation-only tests and legacy callers omitted quantity because the
		// former percentage shortcut did not need it. A unit quantity preserves
		// the exact per-unit boundary without weakening production validation,
		// where loadManagedPosition already requires a positive quantity.
		position.quantity = 1
	}
	if position.stopLossState == "" {
		if position.stopLoss > 0 {
			position.stopLossState = ProtectionPresent
		} else {
			position.stopLossState = ProtectionUnavailable
		}
	}
	if position.takeProfitState == "" {
		if position.takeProfit > 0 {
			position.takeProfitState = ProtectionPresent
		} else {
			position.takeProfitState = ProtectionUnavailable
		}
	}
	if !position.breakevenCostsKnown && position.breakevenCosts.Source == "" {
		position.breakevenCosts = fallbackProtectionBreakevenCosts(position.entry, position.quantity)
		position.breakevenCostsKnown = true
	}
}

func validateProtectionUpdate(position managedPosition, decision *kernel.Decision) error {
	_, err := buildProtectionUpdatePlan(position, decision)
	return err
}

func buildProtectionUpdatePlan(position managedPosition, decision *kernel.Decision) (protectionUpdatePlan, error) {
	plan := protectionUpdatePlan{}
	ensureManagedPositionProtectionMetadata(&position)
	unchangedTakeProfit := normalizeProtectionUpdate(position, decision)
	if invalidProtectionPrice(decision.NewStopLoss) || invalidProtectionPrice(decision.NewTakeProfit) {
		return plan, fmt.Errorf("update_position protection prices must be finite absolute prices")
	}
	if decision.NewStopLoss <= 0 && decision.NewTakeProfit <= 0 {
		if unchangedTakeProfit {
			return plan, nil
		}
		return plan, fmt.Errorf("update_position requires new_stop_loss or new_take_profit")
	}
	long := strings.EqualFold(position.side, "long")
	profitable := (long && position.current > position.entry) || (!long && position.current < position.entry)
	if decision.NewStopLoss > 0 {
		if position.stopLossState != ProtectionPresent || position.stopLoss <= 0 {
			return plan, fmt.Errorf("cannot tighten stop: state=%s requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f", position.stopLossState, decision.NewStopLoss, position.entry, position.current, position.stopLoss)
		}
		if long {
			if decision.NewStopLoss >= position.current {
				return plan, fmt.Errorf("long stop must stay below current price/current mark: requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f", decision.NewStopLoss, position.entry, position.current, position.stopLoss)
			}
			if decision.NewStopLoss <= position.stopLoss {
				return plan, fmt.Errorf("long stop may only tighten: requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f", decision.NewStopLoss, position.entry, position.current, position.stopLoss)
			}
		} else {
			if decision.NewStopLoss <= position.current {
				return plan, fmt.Errorf("short stop must stay above current price/current mark: requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f", decision.NewStopLoss, position.entry, position.current, position.stopLoss)
			}
			if decision.NewStopLoss >= position.stopLoss {
				return plan, fmt.Errorf("short stop may only tighten: requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f", decision.NewStopLoss, position.entry, position.current, position.stopLoss)
			}
		}
		if profitable {
			boundary, err := feeInclusiveBreakevenBoundary(position)
			if err != nil {
				return plan, fmt.Errorf("profitable stop fee boundary unavailable: requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f: %w", decision.NewStopLoss, position.entry, position.current, position.stopLoss, err)
			}
			if err := normalizeStopToFeeBoundary(&position, decision, boundary); err != nil {
				return plan, err
			}
		}
	}
	if decision.NewTakeProfit <= 0 {
		return plan, nil
	}
	switch position.takeProfitState {
	case ProtectionUnavailable:
		if decision.NewStopLoss <= 0 {
			return plan, fmt.Errorf("take-profit state unavailable for %s: %s", position.symbol, position.protectionErr)
		}
		plan.degradedReason = fmt.Sprintf("take-profit snapshot unavailable; applied independently valid stop only: %s", position.protectionErr)
		decision.NewTakeProfit = 0
		return plan, nil
	case ProtectionAmbiguous:
		return plan, fmt.Errorf("take-profit state ambiguous for %s; refusing to modify or guess an active target", position.symbol)
	case ProtectionConfirmedAbsent:
		if long && decision.NewTakeProfit <= position.current {
			return plan, fmt.Errorf("initial long take profit %.8f must stay above current mark %.8f", decision.NewTakeProfit, position.current)
		}
		if !long && decision.NewTakeProfit >= position.current {
			return plan, fmt.Errorf("initial short take profit %.8f must stay below current mark %.8f", decision.NewTakeProfit, position.current)
		}
		plan.initialTakeProfit = true
		return plan, nil
	case ProtectionPresent:
		// Continue into extension-only checks below.
	default:
		return plan, fmt.Errorf("unknown take-profit state %q", position.takeProfitState)
	}
	if decision.NewStopLoss <= 0 {
		return plan, fmt.Errorf("extending take profit requires a tightened new_stop_loss in the same decision")
	}
	if !profitable || decision.Confidence < 80 {
		return plan, fmt.Errorf("take-profit extension requires an already profitable position and confidence >= 80")
	}
	targetDistance := math.Abs(position.takeProfit - position.entry)
	if targetDistance <= 0 {
		return plan, fmt.Errorf("current take-profit price is invalid: %.8f", position.takeProfit)
	}
	progress := math.Abs(position.current-position.entry) / targetDistance
	if progress < 0.55 {
		return plan, fmt.Errorf("take-profit extension is premature: %.0f%% of the current target path completed, need at least 55%%", progress*100)
	}
	maxStep := math.Min(targetDistance*0.5, position.current*0.03)
	if long {
		if decision.NewTakeProfit <= math.Max(position.current, position.takeProfit) {
			return plan, fmt.Errorf("long take profit may only extend beyond current target %.4f", position.takeProfit)
		}
		if decision.NewTakeProfit > position.takeProfit+maxStep {
			return plan, fmt.Errorf("long take-profit extension is too large; maximum next target %.4f", position.takeProfit+maxStep)
		}
	} else {
		if decision.NewTakeProfit >= math.Min(position.current, position.takeProfit) {
			return plan, fmt.Errorf("short take profit may only extend below current target %.4f", position.takeProfit)
		}
		if decision.NewTakeProfit < position.takeProfit-maxStep {
			return plan, fmt.Errorf("short take-profit extension is too large; minimum next target %.4f", position.takeProfit-maxStep)
		}
	}
	return plan, nil
}

func invalidProtectionPrice(price float64) bool {
	return math.IsNaN(price) || math.IsInf(price, 0)
}

func normalizeStopToFeeBoundary(position *managedPosition, decision *kernel.Decision, boundary float64) error {
	long := strings.EqualFold(position.side, "long")
	unsafeDistance := boundary - decision.NewStopLoss
	if !long {
		unsafeDistance = decision.NewStopLoss - boundary
	}
	if unsafeDistance <= 1e-12 {
		return nil
	}
	errorMessage := func() error {
		return fmt.Errorf("profitable %s stop must lock breakeven plus fees (fee-inclusive): requested=%.8f entry=%.8f current_mark=%.8f old_stop=%.8f fee_boundary=%.8f source=%s", position.side, decision.NewStopLoss, position.entry, position.current, position.stopLoss, boundary, position.breakevenCosts.Source)
	}
	if position.priceTick <= 0 || math.IsNaN(position.priceTick) || math.IsInf(position.priceTick, 0) {
		return errorMessage()
	}
	tolerance := math.Max(position.priceTick, math.Abs(position.entry)*protectionNormalizationTolerance)
	if unsafeDistance > tolerance+1e-12 {
		return errorMessage()
	}
	normalized := math.Ceil(boundary/position.priceTick-1e-9) * position.priceTick
	if !long {
		normalized = math.Floor(boundary/position.priceTick+1e-9) * position.priceTick
	}
	if normalized <= 0 || math.IsNaN(normalized) || math.IsInf(normalized, 0) {
		return errorMessage()
	}
	if long && !(position.stopLoss < normalized && normalized < position.current) {
		return errorMessage()
	}
	if !long && !(position.current < normalized && normalized < position.stopLoss) {
		return errorMessage()
	}
	decision.NewStopLoss = normalized
	return nil
}

// normalizeProtectionUpdate removes a target that falls on the same exchange
// tick as the confirmed active target. Unknown or ambiguous target state is
// never normalized into a no-op.
func normalizeProtectionUpdate(position managedPosition, decision *kernel.Decision) bool {
	if decision.NewTakeProfit <= 0 || position.takeProfitState != ProtectionPresent || position.takeProfit <= 0 {
		return false
	}
	sameTick := decision.NewTakeProfit == position.takeProfit
	if position.priceTick > 0 {
		sameTick = math.Round(decision.NewTakeProfit/position.priceTick) == math.Round(position.takeProfit/position.priceTick)
	}
	if sameTick {
		decision.NewTakeProfit = 0
		return true
	}
	return false
}

func (at *AutoTrader) replaceStopLoss(position managedPosition, positionSide string, requested float64) error {
	if modifier, ok := at.trader.(protectionOrderModifier); ok {
		if position.stopLossOrderID == "" {
			return fmt.Errorf("cannot atomically modify stop loss for %s: confirmed order ID is unavailable", position.symbol)
		}
		if err := modifier.ModifyProtectionOrder(position.symbol, position.stopLossOrderID, "stop_loss", positionSide, position.quantity, requested); err != nil {
			return err
		}
		return at.verifyProtectionLevel(position, true, requested)
	}
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
	return at.verifyProtectionLevel(position, true, requested)
}

func (at *AutoTrader) replaceTakeProfit(position managedPosition, positionSide string, requested float64) error {
	if modifier, ok := at.trader.(protectionOrderModifier); ok {
		if position.takeProfitOrderID == "" {
			return fmt.Errorf("cannot atomically modify take profit for %s: confirmed order ID is unavailable", position.symbol)
		}
		if err := modifier.ModifyProtectionOrder(position.symbol, position.takeProfitOrderID, "take_profit", positionSide, position.quantity, requested); err != nil {
			return err
		}
		return at.verifyProtectionLevel(position, false, requested)
	}
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
	return at.verifyProtectionLevel(position, false, requested)
}

func (at *AutoTrader) setInitialTakeProfit(position managedPosition, positionSide string, requested float64) error {
	if err := at.trader.SetTakeProfit(position.symbol, positionSide, position.quantity, requested); err != nil {
		return fmt.Errorf("set initial take profit for %s: %w", position.symbol, err)
	}
	return at.verifyProtectionLevel(position, false, requested)
}

func (at *AutoTrader) verifyProtectionLevel(position managedPosition, stopLoss bool, expected float64) error {
	provider, ok := at.trader.(ProtectionSnapshotProvider)
	if !ok {
		return nil
	}
	snapshot, err := provider.GetProtectionSnapshot(position.symbol, position.side)
	level := snapshot.TakeProfit
	label := "take profit"
	if stopLoss {
		level = snapshot.StopLoss
		label = "stop loss"
	}
	if err != nil && level.Status != ProtectionPresent {
		return fmt.Errorf("verify final protection snapshot for %s: %w", position.symbol, err)
	}
	if level.Status != ProtectionPresent || level.Price <= 0 {
		return fmt.Errorf("verify final %s for %s: state=%s price=%.8f", label, position.symbol, level.Status, level.Price)
	}
	if !pricesOnSameTick(level.Price, expected, position.priceTick) {
		return fmt.Errorf("verify final %s for %s: expected=%.8f actual=%.8f tick=%.8f", label, position.symbol, expected, level.Price, position.priceTick)
	}
	return nil
}

func pricesOnSameTick(left, right, tick float64) bool {
	if tick > 0 && !math.IsNaN(tick) && !math.IsInf(tick, 0) {
		return math.Round(left/tick) == math.Round(right/tick)
	}
	return math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
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

// marginPnLStopPrice converts a margin/position PnL threshold into an exchange
// trigger price. thresholdPct is the leveraged return on margin, e.g. -20 at
// 20x means a 1% adverse price move. Fees, slippage, and funding are handled by
// the existing execution/risk layers; the trigger itself represents the
// configured margin-PnL boundary before exchange tick-size normalization.
func marginPnLStopPrice(action string, entryPrice float64, leverage int, thresholdPct float64) (float64, error) {
	if entryPrice <= 0 || leverage <= 0 {
		return 0, fmt.Errorf("margin-PnL stop conversion requires positive entry price and leverage")
	}
	if thresholdPct >= 0 {
		return 0, fmt.Errorf("stop-loss threshold must be negative, got %.4f", thresholdPct)
	}
	priceMove := (thresholdPct / 100) / float64(leverage)
	switch action {
	case "open_long":
		return entryPrice * (1 + priceMove), nil
	case "open_short":
		return entryPrice * (1 - priceMove), nil
	default:
		return 0, fmt.Errorf("unsupported opening action %q for margin-PnL stop conversion", action)
	}
}

// marginPnLTakeProfitPrice converts a positive Margin/Position PnL target into
// the absolute exchange trigger price. Decision.TakeProfit and
// Decision.NewTakeProfit always carry this resulting price; a percentage must
// never be sent to an exchange or PaperBroker as if it were a price.
func marginPnLTakeProfitPrice(action string, entryPrice float64, leverage int, targetPct float64) (float64, error) {
	if entryPrice <= 0 || leverage <= 0 {
		return 0, fmt.Errorf("margin-PnL take-profit conversion requires positive entry price and leverage")
	}
	if targetPct <= 0 {
		return 0, fmt.Errorf("take-profit threshold must be positive, got %.4f", targetPct)
	}
	priceMove := (targetPct / 100) / float64(leverage)
	switch action {
	case "open_long":
		return entryPrice * (1 + priceMove), nil
	case "open_short":
		return entryPrice * (1 - priceMove), nil
	default:
		return 0, fmt.Errorf("unsupported opening action %q for margin-PnL take-profit conversion", action)
	}
}

// clampStopLossToMarginRisk tightens a model-provided stop to the configured
// margin/position PnL loss boundary, never loosening an already tighter stop.
func clampStopLossToMarginRisk(action string, entryPrice float64, leverage int, stopLoss float64, thresholdPct float64) (float64, bool, error) {
	limit, err := marginPnLStopPrice(action, entryPrice, leverage, thresholdPct)
	if err != nil {
		return 0, false, err
	}
	if stopLoss <= 0 {
		return limit, true, nil
	}
	switch action {
	case "open_long":
		if stopLoss < limit {
			return limit, true, nil
		}
	case "open_short":
		if stopLoss > limit {
			return limit, true, nil
		}
	}
	return stopLoss, false, nil
}

func calculateMaximumAffordableNotional(availableMarginBudget float64, leverage int) float64 {
	if availableMarginBudget <= 0 || leverage <= 0 {
		return 0
	}
	marginFactor := marginOverheadFactor/float64(leverage) + takerFeeRate
	return availableMarginBudget / marginFactor
}

// normalizeStopLossAtEntry applies the unified Margin/Position PnL stop boundary
// immediately before execution, after the effective leverage and current entry
// price are known. Existing tighter model stops are preserved.
//
// The normalization is intentionally execution-time: the exchange may clamp
// leverage before this function runs, and the trigger price must use that final
// leverage rather than the model's original request.
func (at *AutoTrader) normalizeStopLossAtEntry(decision *kernel.Decision, entryPrice float64) error {
	if decision == nil || (decision.Action != "open_long" && decision.Action != "open_short") {
		return nil
	}
	stop, changed, err := clampStopLossToMarginRisk(decision.Action, entryPrice, decision.Leverage, decision.StopLoss, unifiedMarginStopLossPct)
	if err != nil {
		return err
	}
	if changed {
		logger.Infof("  ⚠️ [RISK CONTROL] tightened %s stop to %.8f for %.1f%% Margin/Position PnL at %dx (Price PnL boundary %.4f%%)", decision.Symbol, stop, unifiedMarginStopLossPct, decision.Leverage, unifiedMarginStopLossPct/float64(decision.Leverage))
		decision.StopLoss = stop
	}
	return nil
}

// validateOpenProtection rejects malformed protective levels before an entry
// order is sent. The model must provide a stop and target on the profitable
// side of the entry for both directions; this check is independent of
// risk_usd so a missing risk budget cannot bypass directional safety.
func validateOpenProtection(action string, entryPrice, stopLoss, takeProfit float64) error {
	if action != "open_long" && action != "open_short" {
		return nil
	}
	if !isFinitePositive(entryPrice) {
		return fmt.Errorf("%s requires a positive current entry price, got %.8f", action, entryPrice)
	}
	if !isFinitePositive(stopLoss) || !isFinitePositive(takeProfit) {
		return fmt.Errorf("%s requires positive stop loss and take profit prices, got stop %.8f take profit %.8f", action, stopLoss, takeProfit)
	}

	switch action {
	case "open_long":
		if stopLoss >= entryPrice {
			return fmt.Errorf("long stop loss %.8f must be below current entry price %.8f", stopLoss, entryPrice)
		}
		if takeProfit <= entryPrice {
			return fmt.Errorf("long take profit %.8f must be above current entry price %.8f", takeProfit, entryPrice)
		}
	case "open_short":
		if stopLoss <= entryPrice {
			return fmt.Errorf("short stop loss %.8f must be above current entry price %.8f", stopLoss, entryPrice)
		}
		if takeProfit >= entryPrice {
			return fmt.Errorf("short take profit %.8f must be below current entry price %.8f", takeProfit, entryPrice)
		}
	}
	return nil
}

// validatePaperExitPrices guards the PaperBroker's lower-level Trader API. It
// accepts optional protections for funding/ledger tests, but any supplied TP
// or SL must already be an absolute price on the correct side of entry.
func validatePaperExitPrices(action string, entryPrice, stopLoss, takeProfit float64) error {
	if !isFinitePositive(entryPrice) {
		return fmt.Errorf("%s requires a positive entry price", action)
	}
	if (stopLoss != 0 && !isFinitePositive(stopLoss)) || (takeProfit != 0 && !isFinitePositive(takeProfit)) {
		return fmt.Errorf("%s requires supplied stop-loss/take-profit values to be finite positive absolute prices", action)
	}
	switch action {
	case "open_long":
		if stopLoss > 0 && stopLoss >= entryPrice {
			return fmt.Errorf("paper long stop-loss price %.8f must be below entry price %.8f", stopLoss, entryPrice)
		}
		if takeProfit > 0 && takeProfit <= entryPrice {
			return fmt.Errorf("paper long take-profit price %.8f must be above entry price %.8f; PnL percentages must be converted before PaperBroker execution", takeProfit, entryPrice)
		}
	case "open_short":
		if stopLoss > 0 && stopLoss <= entryPrice {
			return fmt.Errorf("paper short stop-loss price %.8f must be above entry price %.8f", stopLoss, entryPrice)
		}
		if takeProfit > 0 && takeProfit >= entryPrice {
			return fmt.Errorf("paper short take-profit price %.8f must be below entry price %.8f; PnL percentages must be converted before PaperBroker execution", takeProfit, entryPrice)
		}
	}
	return nil
}

// validatePaperUpdateExitPrices validates protections against the current mark
// rather than the entry price. An already profitable position may move its stop
// beyond entry, but the stop/target must remain on the safe side of the live
// price so the update cannot immediately cross the market.
func validatePaperUpdateExitPrices(action string, currentPrice, stopLoss, takeProfit float64) error {
	if !isFinitePositive(currentPrice) {
		return fmt.Errorf("%s requires a positive current price", action)
	}
	if (stopLoss != 0 && !isFinitePositive(stopLoss)) || (takeProfit != 0 && !isFinitePositive(takeProfit)) {
		return fmt.Errorf("%s requires supplied stop-loss/take-profit values to be finite positive absolute prices", action)
	}
	switch action {
	case "open_long":
		if stopLoss > 0 && stopLoss >= currentPrice {
			return fmt.Errorf("paper long stop-loss price %.8f must be below current price %.8f", stopLoss, currentPrice)
		}
		if takeProfit > 0 && takeProfit <= currentPrice {
			return fmt.Errorf("paper long take-profit price %.8f must be above current price %.8f", takeProfit, currentPrice)
		}
	case "open_short":
		if stopLoss > 0 && stopLoss <= currentPrice {
			return fmt.Errorf("paper short stop-loss price %.8f must be above current price %.8f", stopLoss, currentPrice)
		}
		if takeProfit > 0 && takeProfit >= currentPrice {
			return fmt.Errorf("paper short take-profit price %.8f must be below current price %.8f", takeProfit, currentPrice)
		}
	}
	return nil
}

func isFinitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
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

func (at *AutoTrader) validateOpenMarket(symbol string) (*market.MarketAvailability, error) {
	if at == nil {
		return nil, nil
	}
	if at.marketDataProvider != nil {
		availability, err := at.marketDataProvider.ValidateMarketAvailability(symbol)
		if err != nil {
			return nil, fmt.Errorf("refusing to open %s while %s public market data is unavailable: %w", symbol, at.exchange, err)
		}
		return availability, nil
	}
	if at.executionMode != ExecutionModePaper && !strings.EqualFold(at.exchange, "binance") {
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

func (at *AutoTrader) requireFreshExecutionPrice(symbol string) (float64, error) {
	provider := at.marketDataProvider
	if provider == nil {
		var err error
		provider, err = market.NewMarketDataProvider(at.exchange)
		if err != nil {
			return 0, err
		}
	}
	price, err := provider.GetCurrentPriceFresh(symbol)
	if err != nil {
		return 0, &market.MarketDataUnavailableError{Exchange: provider.Exchange(), Symbol: symbol, Capability: market.CapabilityPrice, Cause: err}
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, &market.MarketDataUnavailableError{Exchange: provider.Exchange(), Symbol: symbol, Capability: market.CapabilityPrice, Cause: fmt.Errorf("invalid execution price %.8f", price)}
	}
	return price, nil
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

	availability, err := at.validateOpenMarket(decision.Symbol)
	if err != nil {
		return err
	}
	currentPrice := 0.0
	if availability != nil {
		currentPrice = availability.Price
	} else {
		currentPrice, err = at.requireFreshExecutionPrice(decision.Symbol)
		if err != nil {
			return fmt.Errorf("failed to get fresh execution price for %s: %w", decision.Symbol, err)
		}
	}
	if err := at.normalizeStopLossAtEntry(decision, currentPrice); err != nil {
		return err
	}
	if err := validateOpenProtection(decision.Action, currentPrice, decision.StopLoss, decision.TakeProfit); err != nil {
		return err
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

	if provider, ok := at.trader.(leverageLimitProvider); ok {
		if err := clampDecisionToExchangeLeverageLimit(decision, provider); err != nil {
			return err
		}
	}
	if err := at.enforceOpenRiskBudget(decision, currentPrice); err != nil {
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
	quantity := actualPositionSize / currentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = currentPrice

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
	at.recordAndConfirmOrder(order, decision.Symbol, "open_long", quantity, currentPrice, decision.Leverage, 0)

	// Record position opening time
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// Set stop loss and take profit. Return exchange errors to the decision
	// loop; protection failures are not an instruction to place another trade.
	return at.setOpeningProtection(decision, "LONG", quantity)
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

	availability, err := at.validateOpenMarket(decision.Symbol)
	if err != nil {
		return err
	}
	currentPrice := 0.0
	if availability != nil {
		currentPrice = availability.Price
	} else {
		currentPrice, err = at.requireFreshExecutionPrice(decision.Symbol)
		if err != nil {
			return fmt.Errorf("failed to get fresh execution price for %s: %w", decision.Symbol, err)
		}
	}
	if err := at.normalizeStopLossAtEntry(decision, currentPrice); err != nil {
		return err
	}
	if err := validateOpenProtection(decision.Action, currentPrice, decision.StopLoss, decision.TakeProfit); err != nil {
		return err
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

	if provider, ok := at.trader.(leverageLimitProvider); ok {
		if err := clampDecisionToExchangeLeverageLimit(decision, provider); err != nil {
			return err
		}
	}
	if err := at.enforceOpenRiskBudget(decision, currentPrice); err != nil {
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
	quantity := actualPositionSize / currentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = currentPrice

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
	at.recordAndConfirmOrder(order, decision.Symbol, "open_short", quantity, currentPrice, decision.Leverage, 0)

	// Record position opening time
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// Set stop loss and take profit. Return exchange errors to the decision
	// loop; protection failures are not an instruction to place another trade.
	return at.setOpeningProtection(decision, "SHORT", quantity)
}

// setOpeningProtection applies both protection orders after a market open and
// reports any exchange rejection without issuing an unrelated close order.
func (at *AutoTrader) setOpeningProtection(decision *kernel.Decision, positionSide string, quantity float64) error {
	var protectionErrs []error
	if err := at.trader.SetStopLoss(decision.Symbol, positionSide, quantity, decision.StopLoss); err != nil {
		logger.Errorf("  ❌ Failed to set stop loss: %v", err)
		protectionErrs = append(protectionErrs, fmt.Errorf("stop loss: %w", err))
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, positionSide, quantity, decision.TakeProfit); err != nil {
		logger.Errorf("  ❌ Failed to set take-profit protection order: %v", err)
		protectionErrs = append(protectionErrs, fmt.Errorf("take profit: %w", err))
	}
	if len(protectionErrs) > 0 {
		return fmt.Errorf("opening protection failed for %s: %w", decision.Symbol, errors.Join(protectionErrs...))
	}
	return nil
}

// executeCloseLongWithRecord executes close long position and records detailed information
func (at *AutoTrader) executeCloseLongWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 Close long: %s", decision.Symbol)

	currentPrice, err := at.requireFreshExecutionPrice(decision.Symbol)
	if err != nil {
		return fmt.Errorf("failed to get fresh execution price for %s: %w", decision.Symbol, err)
	}
	actionRecord.Price = currentPrice

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
	at.recordAndConfirmOrder(order, decision.Symbol, "close_long", quantity, currentPrice, 0, entryPrice)

	logger.Infof("  ✓ Position closed successfully")
	return nil
}

// executeCloseShortWithRecord executes close short position and records detailed information
func (at *AutoTrader) executeCloseShortWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 Close short: %s", decision.Symbol)

	currentPrice, err := at.requireFreshExecutionPrice(decision.Symbol)
	if err != nil {
		return fmt.Errorf("failed to get fresh execution price for %s: %w", decision.Symbol, err)
	}
	actionRecord.Price = currentPrice

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
	at.recordAndConfirmOrder(order, decision.Symbol, "close_short", quantity, currentPrice, 0, entryPrice)

	logger.Infof("  ✓ Position closed successfully")
	return nil
}
