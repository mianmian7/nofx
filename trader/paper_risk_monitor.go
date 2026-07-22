package trader

import "time"

func (at *AutoTrader) startPaperRiskMonitor() {
	if at.executionMode != ExecutionModePaper || at.paperBroker == nil {
		return
	}
	interval := at.config.PaperRiskMonitorInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		at.logInfof("🛡️ Paper risk/mark monitor started (interval %v, AI-independent)", interval)
		for {
			select {
			case <-ticker.C:
				now := at.paperBroker.now()
				if fills, err := at.paperBroker.RefreshMakerOrders(now); err != nil {
					at.logWarnf("⚠️ Paper maker-order refresh failed: %v", err)
				} else if len(fills) > 0 {
					at.logInfof("🧾 Paper maker-order refresh produced %d fill(s)", len(fills))
				}
				before := at.paperBroker.Snapshot().OpenPositions
				if err := at.paperBroker.RefreshOpenPositions(); err != nil {
					at.logWarnf("⚠️ Paper risk/mark refresh failed: %v", err)
				}
				after := at.paperBroker.Snapshot().OpenPositions
				if after < before {
					at.logInfof("🛡️ Paper risk/mark monitor closed %d position(s)", before-after)
				}
			case <-at.stopMonitorCh:
				at.logInfof("⏹ Paper risk/mark monitor stopped")
				return
			}
		}
	}()
}

func (at *AutoTrader) runPaperFundingStartupCatchup() {
	if at.executionMode != ExecutionModePaper || at.paperBroker == nil {
		return
	}
	result, err := at.paperBroker.CatchUpFunding(at.paperBroker.now())
	if err != nil {
		at.logWarnf("⚠️ Paper funding startup catch-up failed: %v", err)
		return
	}
	at.logInfof("💸 Paper funding startup catch-up complete (snapshots=%d, history=%d, payments=%d)", result.SnapshotRequests, result.HistoryRequests, result.PaymentsApplied)
}

func (at *AutoTrader) startPaperFundingMonitor() {
	if at.executionMode != ExecutionModePaper || at.paperBroker == nil {
		return
	}
	interval := at.config.PaperFundingMonitorInterval
	if interval <= 0 {
		interval = time.Minute
	}
	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		at.logInfof("💸 Paper funding monitor started (interval %v, risk-independent)", interval)
		for {
			select {
			case <-ticker.C:
				result, err := at.paperBroker.PollFunding(at.paperBroker.now())
				if err != nil {
					at.logWarnf("⚠️ Paper funding settlement failed: %v", err)
				} else {
					at.logInfof("💸 Paper funding poll complete (snapshots=%d, history=%d, payments=%d)", result.SnapshotRequests, result.HistoryRequests, result.PaymentsApplied)
				}
			case <-at.stopMonitorCh:
				at.logInfof("⏹ Paper funding monitor stopped")
				return
			}
		}
	}()
}
