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
				before := at.paperBroker.Snapshot().OpenPositions
				if err := at.paperBroker.RefreshOpenPositions(); err != nil {
					at.logWarnf("⚠️ Paper risk/mark refresh failed: %v", err)
					continue
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
