package trader

import (
	"testing"

	"nofx/kernel"
	"nofx/store"
)

// panicExchangeWriter makes every real exchange mutation fail immediately.
// Read methods remain deliberately unavailable: a Paper cycle must use only
// its public market-data source and virtual ledger.
type panicExchangeWriter struct{ Trader }

type fixedPaperPriceSource map[string]float64

func (s fixedPaperPriceSource) GetMarketPrice(symbol string) (float64, error) {
	return s[symbol], nil
}

func (*panicExchangeWriter) OpenLong(string, float64, int) (map[string]interface{}, error) {
	panic("paper touched live OpenLong")
}
func (*panicExchangeWriter) OpenShort(string, float64, int) (map[string]interface{}, error) {
	panic("paper touched live OpenShort")
}
func (*panicExchangeWriter) CloseLong(string, float64) (map[string]interface{}, error) {
	panic("paper touched live CloseLong")
}
func (*panicExchangeWriter) CloseShort(string, float64) (map[string]interface{}, error) {
	panic("paper touched live CloseShort")
}
func (*panicExchangeWriter) SetLeverage(string, int) error {
	panic("paper touched live SetLeverage")
}
func (*panicExchangeWriter) SetMarginMode(string, bool) error {
	panic("paper touched live SetMarginMode")
}
func (*panicExchangeWriter) SetStopLoss(string, string, float64, float64) error {
	panic("paper touched live SetStopLoss")
}
func (*panicExchangeWriter) SetTakeProfit(string, string, float64, float64) error {
	panic("paper touched live SetTakeProfit")
}
func (*panicExchangeWriter) CancelStopLossOrders(string) error {
	panic("paper touched live CancelStopLossOrders")
}
func (*panicExchangeWriter) CancelTakeProfitOrders(string) error {
	panic("paper touched live CancelTakeProfitOrders")
}
func (*panicExchangeWriter) CancelAllOrders(string) error {
	panic("paper touched live CancelAllOrders")
}
func (*panicExchangeWriter) CancelStopOrders(string) error {
	panic("paper touched live CancelStopOrders")
}

func TestPaperDecisionCycleNeverTouchesLiveExchangeWriter(t *testing.T) {
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10_000,
		TakerFeeBPS:    5,
		SlippageBPS:    2,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	at := &AutoTrader{
		executionMode: ExecutionModePaper,
		exchange:      "binance",
		trader:        &panicExchangeWriter{},
		paperBroker:   broker,
	}
	action := &store.DecisionAction{}
	decision := &kernel.Decision{
		Symbol:          "MUUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 300,
		StopLoss:        90,
		TakeProfit:      120,
	}
	if err := at.executeDecisionWithRecord(decision, action); err != nil {
		t.Fatalf("paper open_long: %v", err)
	}

	positions, err := broker.GetPositions()
	if err != nil {
		t.Fatalf("GetPositions: %v", err)
	}
	if len(positions) != 1 || positions[0]["symbol"] != "MUUSDT" {
		t.Fatalf("paper positions = %#v, want one MUUSDT position", positions)
	}
	if action.OrderID == 0 || !action.Success {
		t.Fatalf("paper action record = %#v, want synthetic successful fill", action)
	}
}

func TestNewPaperAutoTraderNeedsNoExchangeCredentialsAndCreatesNoLiveWriter(t *testing.T) {
	strategy := store.GetDefaultStrategyConfig("en")
	at, err := NewAutoTrader(AutoTraderConfig{
		ID: "paper-no-credentials", Name: "Paper", AIModel: "custom",
		Exchange: "binance", ExecutionMode: ExecutionModePaper,
		InitialBalance: 10_000, StrategyConfig: &strategy,
	}, nil, "user")
	if err != nil {
		t.Fatalf("NewAutoTrader paper without exchange credentials: %v", err)
	}
	if at.paperBroker == nil || at.trader != at.paperBroker {
		t.Fatalf("paper runtime constructed unexpected writer: %#v", at.trader)
	}
}

var _ Trader = (*panicExchangeWriter)(nil)
