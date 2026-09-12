package trader

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const paperQuantityDust = 1e-12

// lastFullClose reads the actual persisted fill history, not the live order
// synchronization table. It does not create a second ledger or new persistent
// state. A consistent full-history replay makes partial fills/retries and
// restart behavior identical. Unreconciled history fails closed for this
// symbol instead of fabricating a close time or silently skipping cooldown.
func (b *PaperBroker) lastFullClose(exchange, symbol string) (time.Time, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	matches := func(s string) bool { return normalizedDecisionSymbol(exchange, s) == symbol }
	for key, p := range b.quarantinedPositions {
		// The quarantined payload itself may have lost its symbol; the
		// persisted position key still identifies the affected instrument.
		keySymbol := key
		if i := strings.LastIndex(key, ":"); i >= 0 {
			keySymbol = key[:i]
		}
		if matches(p.Symbol) || matches(keySymbol) {
			return time.Time{}, fmt.Errorf("Paper %s has quarantined position history", symbol)
		}
	}
	quantities := map[string]float64{"long": 0, "short": 0}
	terminal := map[string]float64{"long": 0, "short": 0}
	for _, p := range b.positions {
		if !matches(p.Symbol) {
			continue
		}
		side := strings.ToLower(p.Side)
		if (side != "long" && side != "short") || !isFinitePositive(p.Quantity) {
			return time.Time{}, fmt.Errorf("Paper %s has invalid current position quantities", symbol)
		}
		terminal[side] += p.Quantity
	}
	var closedAt, previousTime time.Time
	seen := make(map[PaperFill]bool)
	for _, f := range b.fills {
		if !matches(f.Symbol) {
			continue
		}
		if !isFinitePositive(f.Quantity) || f.Time.IsZero() || (!previousTime.IsZero() && f.Time.Before(previousTime)) || seen[f] {
			return time.Time{}, fmt.Errorf("Paper %s fill history is invalid or ambiguous", symbol)
		}
		seen[f], previousTime = true, f.Time
		side := strings.ToLower(f.Side)
		opening := false
		switch f.Action {
		case "open_long", "open_short":
			opening = true
			if side == "" {
				side = strings.TrimPrefix(f.Action, "open_")
			}
		case "close_long", "close_short":
			if side == "" {
				side = strings.TrimPrefix(f.Action, "close_")
			}
		case "take_profit_short":
			if side == "" {
				side = "short"
			}
		case "stop_loss", "liquidation", "take_profit":
			// Older fills sometimes lack side. Infer only if exactly one
			// side is open; never guess across a hedge or a missing prefix.
			if side == "" {
				if quantities["long"] > paperQuantityDust && quantities["short"] == 0 {
					side = "long"
				}
				if quantities["short"] > paperQuantityDust && quantities["long"] == 0 {
					side = "short"
				}
			}
		default:
			return time.Time{}, fmt.Errorf("Paper %s has an unknown fill action", symbol)
		}
		if side != "long" && side != "short" {
			return time.Time{}, fmt.Errorf("Paper %s fill position side is ambiguous", symbol)
		}
		expectedSide := ""
		switch f.Action {
		case "open_long", "close_long":
			expectedSide = "long"
		case "open_short", "close_short", "take_profit_short":
			expectedSide = "short"
		}
		if expectedSide != "" && side != expectedSide {
			return time.Time{}, fmt.Errorf("Paper %s fill action conflicts with position side", symbol)
		}
		if opening {
			quantities[side] += f.Quantity
		} else {
			if quantities[side] <= 0 {
				return time.Time{}, fmt.Errorf("Paper %s close has no preceding open quantity", symbol)
			}
			remaining := quantities[side] - f.Quantity
			if remaining < -paperQuantityDust {
				return time.Time{}, fmt.Errorf("Paper %s fill history is missing opening quantity", symbol)
			}
			if math.Abs(remaining) <= paperQuantityDust {
				remaining = 0
			}
			quantities[side] = remaining
			if quantities["long"] == 0 && quantities["short"] == 0 {
				closedAt = f.Time
			}
		}
	}
	for side, quantity := range quantities {
		if !isFinitePaperNumber(quantity) || math.Abs(quantity-terminal[side]) > paperQuantityDust {
			return time.Time{}, fmt.Errorf("Paper %s fill history does not reconcile with current positions", symbol)
		}
	}
	return closedAt, nil
}

func (at *AutoTrader) paperReentryReason(symbol string, cooldown time.Duration) string {
	if at.paperBroker == nil {
		return "trade throttle: Paper history unavailable; refusing new entry"
	}
	closedAt, err := at.paperBroker.lastFullClose(at.exchange, symbol)
	if err != nil {
		return fmt.Sprintf("trade throttle: cannot verify Paper re-entry: %v", err)
	}
	if closedAt.IsZero() {
		return ""
	}
	age := at.paperBroker.now().Sub(closedAt)
	if age < 0 {
		return "trade throttle: Paper close timestamp is in the future; refusing new entry"
	}
	if age < cooldown {
		return fmt.Sprintf("trade throttle: %s was closed %s ago; wait %s before re-entry", symbol, roundDuration(age), roundDuration(cooldown-age))
	}
	return ""
}
