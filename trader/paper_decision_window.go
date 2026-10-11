package trader

import "time"

const (
	paperDecisionWindow    = 15 * time.Minute
	paperDecisionLead      = time.Minute
	paperCandleCloseSettle = time.Second
)

// paperDecisionWindowDelay avoids repeatedly starting data collection seconds
// before every 15m close. It is scheduling headroom, not an entry filter or a
// guarantee that a slow model will finish in time. The original evidence expiry
// and fresh-price checks still apply to every opening action and pending fill.
func (at *AutoTrader) paperDecisionWindowDelay(now time.Time) time.Duration {
	if at.executionMode != ExecutionModePaper || !paperEntryGuardEnabled(at.config.StrategyConfig) {
		return 0
	}
	start := now.UTC().Truncate(paperDecisionWindow)
	if ready := start.Add(paperCandleCloseSettle); now.Before(ready) {
		return ready.Sub(now)
	}
	remaining := start.Add(paperDecisionWindow).Sub(now)
	if remaining <= paperDecisionLead {
		return remaining + paperCandleCloseSettle
	}
	return 0
}

// Wait before creating the cycle deadline or gathering any market data. The
// runtime stop channel cancels this wait; independent Paper risk/price monitors
// and explicit order/protection APIs do not use this scheduling path.
func (at *AutoTrader) waitForPaperDecisionWindow(now time.Time) bool {
	delay := at.paperDecisionWindowDelay(now)
	if delay == 0 {
		return true
	}
	at.runLifecycleMu.Lock()
	stopCh := at.runStopCh
	at.runLifecycleMu.Unlock()
	at.logInfof("⏳ Deferring Paper decision %.1fs to collect a fresh 15m candle window", delay.Seconds())
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stopCh:
		return false
	case <-timer.C:
		// A simultaneous stop must not start another decision.
		select {
		case <-stopCh:
			return false
		default:
			return true
		}
	}
}
