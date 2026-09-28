package trader

import (
	"nofx/kernel"
	"nofx/store"
	"path/filepath"
	"strings"
	"testing"
)

// Paper execution persists its own ledger, not the live trader_orders table.
func TestPaperHourlyThrottleUsesPersistentExecutionLedger(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := PaperBrokerConfig{InitialBalance: 100}
	broker, err := NewPersistentPaperBroker(cfg, slotSource(), st.Paper(), "hourly", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slotOpen(broker, "BTCUSDT"); err != nil {
		t.Fatal(err)
	}
	broker, err = NewPersistentPaperBroker(cfg, slotSource(), st.Paper(), "hourly", nil)
	if err != nil {
		t.Fatal(err)
	}
	throttle := store.DefaultTradeThrottleConfig()
	throttle.MaxOpensPerHour = 1
	at := &AutoTrader{id: "hourly", exchange: "binance", executionMode: ExecutionModePaper, paperBroker: broker, store: st,
		config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{RiskControl: store.RiskControlConfig{TradeThrottle: &throttle}}}}
	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "ETHUSDT", Action: "open_long"}, &kernel.Context{}, 0)
	if !strings.Contains(reason, "last hour") {
		t.Fatalf("second Paper entry bypassed hourly limit after restart: reason=%q", reason)
	}
}
