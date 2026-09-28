package trader

import (
	"fmt"
	"nofx/store"
	"time"
)

func paperStrategyMaxOpensPerHour(strategy *store.StrategyConfig) int {
	if strategy == nil || strategy.StrategyType == "grid_trading" {
		return 0
	}
	return strategy.RiskControl.EffectiveTradeThrottle().MaxOpensPerHour
}

func (b *PaperBroker) setHourlyOpenLimit(maximum int) error {
	if maximum < 0 {
		return fmt.Errorf("invalid Paper hourly opening limit")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config.MaxOpensPerHour = maximum
	return nil
}

func (b *PaperBroker) countRecentOpenEntries(since time.Time) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.countOpenEntriesLocked(since, 0)
}

// Count logical openings, not individual fills or replacement order IDs. Fills
// and replacement events are retained in the persisted Paper ledger (unlike the
// separate live order table). A resting entry reserves one place regardless of
// its age; an entirely unfilled cancellation releases it. A partially filled
// entry remains counted from its FIRST fill, even after cancellation or close.
// excludeOrder allows the already-reserved entry to reprice/continue filling.
func (b *PaperBroker) countOpenEntriesLocked(since time.Time, excludeOrder int64) int {
	parents := make(map[int64]int64)
	for _, event := range b.orderEvents {
		// Native order IDs increase across replacements. Ignore malformed links
		// conservatively: missing lineage can overcount, never merge unrelated IDs.
		if isOpenAction(event.Action) && event.OrderID > 0 && event.ReplacementOrderID > event.OrderID {
			parents[event.ReplacementOrderID] = event.OrderID
		}
	}
	root := func(id int64) int64 {
		for parents[id] > 0 && parents[id] < id {
			id = parents[id]
		}
		return id
	}
	first := make(map[int64]time.Time)
	coveredPosition := make(map[string]bool)
	for _, fill := range b.fills {
		if !isOpenAction(fill.Action) || fill.Quantity <= paperQuantityDust {
			continue
		}
		id := root(fill.OrderID)
		if at, ok := first[id]; !ok || fill.Time.Before(at) {
			first[id] = fill.Time
		}
		side := fill.Side
		if side == "" {
			side = "long"
			if fill.Action == "open_short" {
				side = "short"
			}
		}
		key := fill.Symbol + ":" + side
		if position, ok := b.positions[key]; ok && !fill.Time.Before(position.EntryTime) {
			coveredPosition[key] = true
		}
	}
	occupied := make(map[int64]bool)
	for id, at := range first {
		if at.IsZero() || at.After(since) {
			occupied[id] = true
		}
	}
	for id, order := range b.pendingOrders {
		if !order.ReduceOnly && isOpenAction(order.Action) && order.RemainingQuantity > paperQuantityDust {
			occupied[root(id)] = true
		}
	}
	if excludeOrder > 0 {
		delete(occupied, root(excludeOrder))
	}
	count := len(occupied)
	// Some legacy ledgers retain a position but lack its entry fill. Do not
	// silently treat that recent opening as zero. This fallback never merges
	// unrelated openings or relaxes an uncertain restored reservation.
	for key, position := range b.positions {
		if position.Quantity > paperQuantityDust && !coveredPosition[key] && (position.EntryTime.IsZero() || position.EntryTime.After(since)) {
			count++
		}
	}
	return count
}

// Called under the broker mutex for NEW admissions, repricing and delayed fills.
// Exits and protection updates never enter this entry-only gate.
func (b *PaperBroker) validateHourlyOpenLimitLocked(pendingID int64) error {
	if b.config.MaxOpensPerHour <= 0 {
		return nil
	}
	count := b.countOpenEntriesLocked(b.now().Add(-time.Hour), pendingID)
	if count >= b.config.MaxOpensPerHour {
		return fmt.Errorf("paper trade throttle: %d openings/reservations in the last hour; max is %d", count, b.config.MaxOpensPerHour)
	}
	return nil
}
