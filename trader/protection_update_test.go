package trader

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"nofx/kernel"
	"nofx/store"
)

type protectionUpdateTrader struct {
	Trader
	stopLossPrices   []float64
	takeProfitPrices []float64
}

func (t *protectionUpdateTrader) GetPositions() ([]map[string]interface{}, error) {
	return []map[string]interface{}{{
		"symbol":      "SKHYUSDT",
		"side":        "long",
		"positionAmt": 1.0,
		"entryPrice":  100.0,
		"markPrice":   106.0,
		"stop_loss":   90.0,
		"take_profit": 110.0,
	}}, nil
}

func (t *protectionUpdateTrader) GetMarketPrice(string) (float64, error) {
	return 106, nil
}

func (t *protectionUpdateTrader) CancelStopLossOrders(string) error {
	return nil
}

func (t *protectionUpdateTrader) SetStopLoss(_ string, _ string, _ float64, price float64) error {
	t.stopLossPrices = append(t.stopLossPrices, price)
	return nil
}

func (t *protectionUpdateTrader) CancelTakeProfitOrders(string) error {
	return nil
}

func (t *protectionUpdateTrader) SetTakeProfit(_ string, _ string, _ float64, price float64) error {
	t.takeProfitPrices = append(t.takeProfitPrices, price)
	if price == 113 {
		return fmt.Errorf("injected take-profit failure")
	}
	return nil
}

func TestValidateProtectionUpdateAllowsProtectedRunawayExtension(t *testing.T) {
	position := managedPosition{
		symbol: "SKHYUSDT", side: "long", quantity: 1,
		entry: 130.47, current: 135.40, stopLoss: 127.10, takeProfit: 138.20,
	}
	decision := &kernel.Decision{
		Action: "update_position", NewStopLoss: 133.80, NewTakeProfit: 141.00, Confidence: 82,
	}
	if err := validateProtectionUpdate(position, decision); err != nil {
		t.Fatalf("protected runaway extension rejected: %v", err)
	}
}

func TestValidateProtectionUpdateRejectsUnprotectedTargetChasing(t *testing.T) {
	position := managedPosition{
		symbol: "SKHYUSDT", side: "long", quantity: 1,
		entry: 130.47, current: 135.40, stopLoss: 127.10, takeProfit: 138.20,
	}
	decision := &kernel.Decision{
		Action: "update_position", NewTakeProfit: 141.00, Confidence: 90,
	}
	err := validateProtectionUpdate(position, decision)
	if err == nil || !strings.Contains(err.Error(), "tightened new_stop_loss") {
		t.Fatalf("error = %v, want paired stop requirement", err)
	}
}

func TestPaperUpdatePositionReplacesProtection(t *testing.T) {
	prices := fixedPaperPriceSource{"SKHYUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	at := &AutoTrader{
		executionMode: ExecutionModePaper,
		exchange:      "binance",
		trader:        broker,
		paperBroker:   broker,
	}
	open := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 300, StopLoss: 90, TakeProfit: 110,
	}
	if err := at.executeDecisionWithRecord(open, &store.DecisionAction{}); err != nil {
		t.Fatalf("open: %v", err)
	}

	prices["SKHYUSDT"] = 106
	update := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 101, NewTakeProfit: 113, Confidence: 85,
	}
	if err := at.executeDecisionWithRecord(update, &store.DecisionAction{}); err != nil {
		t.Fatalf("update protection: %v", err)
	}

	positions, err := broker.GetPositions()
	if err != nil || len(positions) != 1 {
		t.Fatalf("positions = %#v, err=%v", positions, err)
	}
	if got := floatFromPosition(positions[0], "stop_loss"); got != 101 {
		t.Fatalf("stop loss = %v, want 101", got)
	}
	if got := floatFromPosition(positions[0], "take_profit"); got != 113 {
		t.Fatalf("take profit = %v, want 113", got)
	}
	orders, err := broker.GetOpenOrders("SKHYUSDT")
	if err != nil || len(orders) != 1 || orders[0].Price != 113 {
		t.Fatalf("open orders = %#v, err=%v; want replacement target 113", orders, err)
	}
}

func TestLiveUpdatePositionRestoresStopLossWhenTakeProfitReplacementFails(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{}
	at := &AutoTrader{
		exchange: "binance",
		trader:   exchangeTrader,
	}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 101, NewTakeProfit: 113, Confidence: 85,
	}
	actionRecord := &store.DecisionAction{}

	err := at.executeDecisionWithRecord(decision, actionRecord)
	if err == nil || !strings.Contains(err.Error(), "old stop 90.0000 restored") {
		t.Fatalf("error = %v, want restored-stop failure result", err)
	}
	if actionRecord.Success {
		t.Fatal("action record unexpectedly marked successful")
	}
	if got, want := exchangeTrader.stopLossPrices, []float64{101, 90}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss prices = %v, want %v", got, want)
	}
	if got, want := exchangeTrader.takeProfitPrices, []float64{113, 110}; !slices.Equal(got, want) {
		t.Fatalf("take-profit prices = %v, want %v", got, want)
	}
}
