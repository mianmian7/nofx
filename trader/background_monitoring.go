package trader

import (
	"time"

	"nofx/logger"
	"nofx/trader/aster"
	"nofx/trader/binance"
	"nofx/trader/bitget"
	"nofx/trader/bybit"
	"nofx/trader/gate"
	"nofx/trader/hyperliquid"
	"nofx/trader/kucoin"
	"nofx/trader/lighter"
	"nofx/trader/okx"
)

// StartBackgroundMonitoring starts AI-independent position, risk, funding, and
// exchange synchronization. It is safe to call repeatedly and also runs while
// automatic AI decisions are paused.
func (at *AutoTrader) StartBackgroundMonitoring() {
	if at == nil {
		return
	}

	at.monitorLifecycleMu.Lock()
	defer at.monitorLifecycleMu.Unlock()
	if at.monitorsStarted || at.monitorsStopped {
		return
	}
	if at.stopMonitorCh == nil {
		at.stopMonitorCh = make(chan struct{})
	}
	at.monitorsStarted = true

	at.startDrawdownMonitor()
	at.runPaperFundingStartupCatchup()
	at.startPaperFundingMonitor()
	at.startPaperRiskMonitor()
	at.startExchangeOrderSync()
}

func (at *AutoTrader) startExchangeOrderSync() {
	if at.store == nil {
		return
	}

	switch at.exchange {
	case "lighter":
		if exchangeTrader, ok := at.trader.(*lighter.LighterTraderV2); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "hyperliquid":
		if exchangeTrader, ok := at.trader.(*hyperliquid.HyperliquidTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "bybit":
		if exchangeTrader, ok := at.trader.(*bybit.BybitTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "okx":
		if exchangeTrader, ok := at.trader.(*okx.OKXTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "bitget":
		if exchangeTrader, ok := at.trader.(*bitget.BitgetTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "aster":
		if exchangeTrader, ok := at.trader.(*aster.AsterTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "binance":
		if exchangeTrader, ok := at.trader.(*binance.FuturesTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "gate":
		if exchangeTrader, ok := at.trader.(*gate.GateTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	case "kucoin":
		if exchangeTrader, ok := at.trader.(*kucoin.KuCoinTrader); ok {
			exchangeTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second, at.stopMonitorCh)
		}
	default:
		return
	}

	at.logInfof("🔄 %s order+position sync enabled (every 30s, continues while paused)", at.exchange)
}

// Shutdown permanently stops both automatic decisions and all background
// monitoring for an instance that is being removed or during service exit.
func (at *AutoTrader) Shutdown() {
	if at == nil {
		return
	}

	at.Stop()
	at.monitorLifecycleMu.Lock()
	if at.monitorsStopped {
		at.monitorLifecycleMu.Unlock()
		return
	}
	at.monitorsStopped = true
	monitorStopCh := at.stopMonitorCh
	monitorsStarted := at.monitorsStarted
	at.monitorLifecycleMu.Unlock()

	if monitorsStarted && monitorStopCh != nil {
		close(monitorStopCh)
		at.monitorWg.Wait()
	}
	logger.Info("⏹ Automatic trader background monitoring stopped")
}
