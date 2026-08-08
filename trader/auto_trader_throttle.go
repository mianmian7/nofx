package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

func isOpenAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "open_long", "open_short":
		return true
	default:
		return false
	}
}

func isCloseAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "close_long", "close_short":
		return true
	default:
		return false
	}
}

func normalizedDecisionSymbol(exchange, symbol string) string {
	return market.NormalizeForExchange(exchange, strings.TrimSpace(symbol))
}

func (at *AutoTrader) effectiveTradeThrottle() store.TradeThrottleConfig {
	if at == nil || at.config.StrategyConfig == nil {
		return store.DefaultTradeThrottleConfig()
	}
	return at.config.StrategyConfig.RiskControl.EffectiveTradeThrottle()
}

func (at *AutoTrader) tradeThrottleReason(decision kernel.Decision, ctx *kernel.Context, opensQueuedThisCycle int) string {
	if ctx == nil {
		return ""
	}
	throttle := at.effectiveTradeThrottle()

	// Only opening frequency is throttled. Close decisions are owned by the AI:
	// the exchange-side -20% Margin/Position PnL hard stop caps downside, so a
	// holding-time / PnL-band gate would only override the model's context-aware
	// position management.
	switch {
	case isOpenAction(decision.Action):
		return at.openThrottleReason(decision, ctx, opensQueuedThisCycle, throttle)
	default:
		return ""
	}
}

func (at *AutoTrader) openThrottleReason(decision kernel.Decision, ctx *kernel.Context, opensQueuedThisCycle int, throttle store.TradeThrottleConfig) string {
	symbol := normalizedDecisionSymbol(at.exchange, decision.Symbol)
	if symbol == "" {
		return ""
	}

	if opensQueuedThisCycle >= throttle.MaxOpensPerCycle {
		return fmt.Sprintf("trade throttle: only %d new position may be opened per cycle", throttle.MaxOpensPerCycle)
	}

	if pos := findAnyContextPosition(at.exchange, ctx, symbol); pos != nil {
		return fmt.Sprintf("trade throttle: %s already has an open %s position; manage or close it before opening another side", symbol, pos.Side)
	}

	openCount, err := at.countRecentOpenOrders(time.Now().Add(-1 * time.Hour))
	if err != nil {
		at.logWarnf("⚠️ Trade throttle could not read recent open orders: %v", err)
	} else if openCount >= throttle.MaxOpensPerHour {
		return fmt.Sprintf("trade throttle: %d open order already executed in the last hour; max is %d", openCount, throttle.MaxOpensPerHour)
	}

	reentryCooldown := time.Duration(throttle.ReentryCooldownMinutes) * time.Minute
	if order := at.findRecentCloseOrder(symbol, time.Now().Add(-reentryCooldown)); order != nil {
		age := time.Since(time.UnixMilli(order.CreatedAt))
		remaining := reentryCooldown - age
		if remaining < 0 {
			remaining = 0
		}
		return fmt.Sprintf("trade throttle: %s was closed %s ago; wait %s before re-entry", symbol, roundDuration(age), roundDuration(remaining))
	}

	return ""
}

func findAnyContextPosition(exchange string, ctx *kernel.Context, symbol string) *kernel.PositionInfo {
	if ctx == nil {
		return nil
	}
	for i := range ctx.Positions {
		pos := &ctx.Positions[i]
		if normalizedDecisionSymbol(exchange, pos.Symbol) == symbol {
			return pos
		}
	}
	return nil
}

func (at *AutoTrader) recentOrders(limit int) ([]*store.TraderOrder, error) {
	if at == nil || at.store == nil {
		return nil, nil
	}
	return at.store.Order().GetTraderOrders(at.id, limit)
}

func (at *AutoTrader) countRecentOpenOrders(since time.Time) (int, error) {
	orders, err := at.recentOrders(100)
	if err != nil {
		return 0, err
	}
	sinceMs := since.UTC().UnixMilli()
	count := 0
	for _, order := range orders {
		if order == nil || order.CreatedAt < sinceMs || isCanceledOrder(order) {
			continue
		}
		if isOpenAction(order.OrderAction) {
			count++
		}
	}
	return count, nil
}

func (at *AutoTrader) findRecentCloseOrder(symbol string, since time.Time) *store.TraderOrder {
	orders, err := at.recentOrders(100)
	if err != nil {
		at.logWarnf("⚠️ Trade throttle could not read recent close orders: %v", err)
		return nil
	}
	sinceMs := since.UTC().UnixMilli()
	for _, order := range orders {
		if order == nil || order.CreatedAt < sinceMs || isCanceledOrder(order) {
			continue
		}
		if normalizedDecisionSymbol(at.exchange, order.Symbol) == symbol && isCloseAction(order.OrderAction) {
			return order
		}
	}
	return nil
}

func isCanceledOrder(order *store.TraderOrder) bool {
	status := strings.ToUpper(strings.TrimSpace(order.Status))
	return status == "CANCELED" || status == "CANCELLED" || status == "REJECTED" || status == "EXPIRED"
}

func roundDuration(d time.Duration) string {
	if d < time.Minute {
		return "0m"
	}
	return d.Round(time.Minute).String()
}
