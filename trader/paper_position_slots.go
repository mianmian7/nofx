package trader

import (
	"fmt"
	"nofx/store"
)

// Low-level brokers and grid strategies retain their existing contract. AI
// Paper entries reserve a slot until canceled or fully closed, including while
// resting. No separate counter can drift away from the persisted ledger.
func paperStrategyMaxPositions(strategy *store.StrategyConfig) int {
	if strategy == nil || strategy.StrategyType == "grid_trading" {
		return 0
	}
	return strategy.RiskControl.MaxPositions
}

func paperStrategyMaxDrawdown(strategy *store.StrategyConfig) float64 {
	if strategy == nil || strategy.StrategyType == "grid_trading" {
		return 0
	}
	return strategy.RiskControl.PaperMaxDrawdownRatio
}

func (b *PaperBroker) setEntrySafetyLimits(maximum int, drawdown float64) error {
	if maximum < 0 || !isFinitePaperNumber(drawdown) || drawdown < 0 || drawdown > 1 {
		return fmt.Errorf("invalid Paper entry safety limits")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config.MaxPositions = maximum
	b.config.MaxDrawdownRatio = drawdown
	return nil
}

// pendingID is nonzero only for revalidation of an already reserved order.
// Repricing/partial fills retain their own slot; lower limits never force exits.
func (b *PaperBroker) validateEntrySlotLocked(symbol, side string, pendingID int64) error {
	if err := b.validateHourlyOpenLimitLocked(pendingID); err != nil {
		return err
	}
	if b.config.MaxDrawdownRatio > 0 {
		dd := b.maxDrawdown
		if b.peakEquity > 0 {
			dd = max(dd, (b.peakEquity-b.snapshotLocked().Equity)/b.peakEquity*100)
		}
		if dd >= b.config.MaxDrawdownRatio*100 {
			return fmt.Errorf("paper drawdown entry circuit breaker: %.2f%% reached %.2f%%; protective exits remain enabled", dd, b.config.MaxDrawdownRatio*100)
		}
	}
	if b.config.MaxPositions <= 0 {
		return nil
	}
	key := symbol + ":" + side
	slots := make(map[string]bool)
	for k, p := range b.positions {
		if p.Quantity > paperQuantityDust {
			slots[k] = true
		}
	}
	if pendingID == 0 && slots[key] {
		return fmt.Errorf("paper scale-in is disabled for %s", key)
	}
	for id, o := range b.pendingOrders {
		if o.ReduceOnly || o.RemainingQuantity <= paperQuantityDust {
			continue
		}
		k := o.Symbol + ":" + o.Side
		if pendingID == 0 && k == key {
			return fmt.Errorf("paper entry already reserved for %s (order %d)", key, id)
		}
		slots[k] = true
	}
	slots[key] = true
	if len(slots) > b.config.MaxPositions {
		return fmt.Errorf("paper position slots exhausted: %d positions/reservations exceed limit %d", len(slots), b.config.MaxPositions)
	}
	return nil
}
