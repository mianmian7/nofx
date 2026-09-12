package trader

import (
	"fmt"
	"math"
	"time"

	"nofx/store"
)

// Only AI Paper strategies opt into mandatory entry RR. Low-level ledger
// brokers and grid orders retain their existing optional-protection contract.
func paperStrategyMinRiskReward(strategy *store.StrategyConfig) float64 {
	if strategy == nil || strategy.StrategyType == "grid_trading" {
		return 0
	}
	if strategy.RiskControl.MinRiskRewardRatio <= 0 {
		return 3 // same effective minimum as the decision validator
	}
	return strategy.RiskControl.MinRiskRewardRatio
}

// Strategy reloads must reach background maker fills without racing them.
func (b *PaperBroker) setMinRiskRewardRatio(minimum float64) error {
	if !isFinitePaperNumber(minimum) || minimum < 0 {
		return fmt.Errorf("paper minimum risk/reward must be finite and nonnegative")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config.MinRiskRewardRatio = minimum
	return nil
}

// Called with b.mu held and with the actual proposed execution price, before
// changing positions, fees, pending orders or fills. Never used for exits.
func (b *PaperBroker) validateEntryRiskRewardLocked(action string, entry, stop, target float64) error {
	if err := validatePaperExitPrices(action, entry, stop, target); err != nil {
		return err
	}
	minimum := b.config.MinRiskRewardRatio
	if minimum == 0 {
		return nil
	}
	if err := validateOpenProtection(action, entry, stop, target); err != nil {
		return err
	}
	risk, reward := entry-stop, target-entry
	if action == "open_short" {
		risk, reward = stop-entry, entry-target
	}
	ratio := reward / risk
	if !isFinitePaperNumber(ratio) || ratio+1e-12*math.Max(1, minimum) < minimum {
		return fmt.Errorf("paper entry risk/reward %.4f:1 is below required %.4f:1 at execution price %.8f (stop %.8f, target %.8f)", ratio, minimum, entry, stop, target)
	}
	return nil
}

// Existing positions retain their own protections during additional maker
// fills, while the pending order still carries its original levels. If those
// levels changed after a partial fill, cancel the remainder rather than using
// stale RR inputs or recreating the old take-profit when the entry completes.
func (b *PaperBroker) validatePendingEntryRiskLocked(order PaperPendingOrder, entry float64) error {
	if p, ok := b.positions[order.Symbol+":"+order.Side]; ok && p.Quantity > paperQuantityDust {
		samePrice := func(a, b float64) bool {
			tolerance := 1e-12 * math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
			return isFinitePaperNumber(a) && isFinitePaperNumber(b) && math.Abs(a-b) <= tolerance
		}
		if !samePrice(p.StopLoss, order.StopLoss) || !samePrice(p.TakeProfit, order.TakeProfit) {
			return fmt.Errorf("paper pending entry protection changed after partial fill; canceling remainder for %s %s", order.Symbol, order.Side)
		}
	}
	if err := b.validateEntryRiskRewardLocked(order.Action, entry, order.StopLoss, order.TakeProfit); err != nil {
		return err
	}
	return b.validatePendingAccountRiskLocked(order, entry)
}

// A policy rejection is recoverable within a maker refresh only once the
// invalid order's cancellation has been persisted successfully.
type paperEntryRejectedError struct{ cause error }

func (e *paperEntryRejectedError) Error() string { return e.cause.Error() }
func (e *paperEntryRejectedError) Unwrap() error { return e.cause }

// Cancel only the unfilled entry remainder. A partial position and its
// protections remain untouched; failed persistence restores the pending order
// so that the next attempt can revalidate/retry instead of losing its record.
func (b *PaperBroker) rejectPendingEntryLocked(order PaperPendingOrder, at time.Time, cause error) error {
	snapshot := b.captureStateLocked()
	delete(b.pendingOrders, order.OrderID)
	order.UpdatedAt = at
	b.recordOrderEventLocked(order, "CANCELED", "entry_risk_rejected", 0, 0)
	if err := b.persistLocked(); err != nil {
		b.restoreStateLocked(snapshot)
		return fmt.Errorf("%w; could not persist entry cancellation: %v", cause, err)
	}
	return &paperEntryRejectedError{cause: cause}
}
