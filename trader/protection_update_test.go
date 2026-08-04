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
	side             string
	entryPrice       float64
	markPrice        float64
	stopLoss         float64
	takeProfit       float64
}

func (t *protectionUpdateTrader) GetPositions() ([]map[string]interface{}, error) {
	side := t.side
	if side == "" {
		side = "long"
	}
	entryPrice := t.entryPrice
	if entryPrice == 0 {
		entryPrice = 100
	}
	markPrice := t.markPrice
	if markPrice == 0 {
		markPrice = 106
	}
	stopLoss := t.stopLoss
	if stopLoss == 0 {
		stopLoss = 90
	}
	takeProfit := t.takeProfit
	if takeProfit == 0 {
		takeProfit = 110
	}
	return []map[string]interface{}{{
		"symbol":      "SKHYUSDT",
		"side":        side,
		"positionAmt": 1.0,
		"entryPrice":  entryPrice,
		"markPrice":   markPrice,
		"stop_loss":   stopLoss,
		"take_profit": takeProfit,
	}}, nil
}

func (t *protectionUpdateTrader) GetMarketPrice(string) (float64, error) {
	if t.markPrice != 0 {
		return t.markPrice, nil
	}
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

func TestValidateProtectionUpdateTreatsUnchangedTakeProfitAsNoOp(t *testing.T) {
	tests := []struct {
		name string
		side string
		stop float64
		tp   float64
	}{
		{name: "long", side: "long", stop: 103, tp: 110},
		{name: "short", side: "short", stop: 142, tp: 135},
		{name: "short target only", side: "short", stop: 0, tp: 135},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := managedPosition{
				side: tt.side, entry: 150, current: 141.5, stopLoss: 145, takeProfit: tt.tp,
			}
			if tt.side == "long" {
				position.entry = 100
				position.current = 106
				position.stopLoss = 90
			}
			decision := &kernel.Decision{
				Action: "update_position", NewStopLoss: tt.stop, NewTakeProfit: tt.tp, Confidence: 85,
			}
			if err := validateProtectionUpdate(position, decision); err != nil {
				t.Fatalf("unchanged take profit rejected: %v", err)
			}
			if decision.NewTakeProfit != 0 {
				t.Fatalf("unchanged take profit was not normalized: %v", decision.NewTakeProfit)
			}
		})
	}
}

func TestLiveShortUpdatePositionTightensStopWithoutChangingTakeProfit(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{
		side: "short", entryPrice: 150, markPrice: 141.5, stopLoss: 145, takeProfit: 135,
	}
	at := &AutoTrader{exchange: "binance", trader: exchangeTrader}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 142, NewTakeProfit: 135, Confidence: 85,
	}
	actionRecord := &store.DecisionAction{}

	if err := at.executeDecisionWithRecord(decision, actionRecord); err != nil {
		t.Fatalf("short stop-only update rejected: %v", err)
	}
	if !actionRecord.Success {
		t.Fatal("short stop-only update was not marked successful")
	}
	if got, want := exchangeTrader.stopLossPrices, []float64{142}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss prices = %v, want %v", got, want)
	}
	if len(exchangeTrader.takeProfitPrices) != 0 {
		t.Fatalf("take-profit replacement calls = %v, want none", exchangeTrader.takeProfitPrices)
	}
}

func TestLiveLongUpdatePositionTightensStopWithoutChangingTakeProfit(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{}
	at := &AutoTrader{exchange: "binance", trader: exchangeTrader}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 103, NewTakeProfit: 110, Confidence: 85,
	}
	actionRecord := &store.DecisionAction{}

	if err := at.executeDecisionWithRecord(decision, actionRecord); err != nil {
		t.Fatalf("long stop-only update rejected: %v", err)
	}
	if !actionRecord.Success {
		t.Fatal("long stop-only update was not marked successful")
	}
	if got, want := exchangeTrader.stopLossPrices, []float64{103}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss prices = %v, want %v", got, want)
	}
	if len(exchangeTrader.takeProfitPrices) != 0 {
		t.Fatalf("take-profit replacement calls = %v, want none", exchangeTrader.takeProfitPrices)
	}
}

func TestExecuteUpdatePositionClassifiesProtectionRejection(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{
		side: "short", entryPrice: 150, markPrice: 141.5, stopLoss: 145, takeProfit: 135,
	}
	at := &AutoTrader{exchange: "binance", trader: exchangeTrader}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 141.1, NewTakeProfit: 135, Confidence: 90,
	}

	err := at.executeDecisionWithRecord(decision, &store.DecisionAction{})
	if err == nil || !isProtectionUpdateRejected(err) {
		t.Fatalf("error = %v, want classified protection rejection", err)
	}
	if !strings.Contains(err.Error(), "must stay above current price") {
		t.Fatalf("error = %v, want preserved rejection reason", err)
	}
}

func TestValidateProtectionUpdateRejectsInvalidShortProtectionChanges(t *testing.T) {
	tests := []struct {
		name     string
		position managedPosition
		decision *kernel.Decision
		want     string
	}{
		{
			name:     "stop below current price",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 145, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 141.1, NewTakeProfit: 135, Confidence: 90},
			want:     "must stay above current price",
		},
		{
			name:     "profitable stop below fee-inclusive floor",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 151, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 149.9, NewTakeProfit: 135, Confidence: 90},
			want:     "breakeven plus fees",
		},
		{
			name:     "target extension too early",
			position: managedPosition{side: "short", entry: 150, current: 145, stopLoss: 148, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 146, NewTakeProfit: 130, Confidence: 90},
			want:     "premature",
		},
		{
			name:     "target extension wrong direction",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 145, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 143, NewTakeProfit: 140, Confidence: 90},
			want:     "may only extend below",
		},
		{
			name:     "target extension too large",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 145, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 143, NewTakeProfit: 130, Confidence: 90},
			want:     "too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtectionUpdate(tt.position, tt.decision)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateProtectionUpdateRejectsInvalidLongProtectionChanges(t *testing.T) {
	tests := []struct {
		name     string
		position managedPosition
		decision *kernel.Decision
		want     string
	}{
		{
			name:     "stop above current price",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 90, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 107, NewTakeProfit: 110, Confidence: 90},
			want:     "must stay below current price",
		},
		{
			name:     "profitable stop below fee-inclusive floor",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 99, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 100.05, NewTakeProfit: 110, Confidence: 90},
			want:     "breakeven plus fees",
		},
		{
			name:     "target extension too early",
			position: managedPosition{side: "long", entry: 100, current: 102, stopLoss: 95, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 101, NewTakeProfit: 115, Confidence: 90},
			want:     "premature",
		},
		{
			name:     "target extension wrong direction",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 90, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 103, NewTakeProfit: 105, Confidence: 90},
			want:     "may only extend beyond",
		},
		{
			name:     "target extension too large",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 90, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 103, NewTakeProfit: 116, Confidence: 90},
			want:     "too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtectionUpdate(tt.position, tt.decision)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
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

	prices["SKHYUSDT"] = 108
	secondUpdate := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 102, NewTakeProfit: 114, Confidence: 85,
	}
	if err := at.executeDecisionWithRecord(secondUpdate, &store.DecisionAction{}); err != nil {
		t.Fatalf("replace protection: %v", err)
	}

	positions, err := broker.GetPositions()
	if err != nil || len(positions) != 1 {
		t.Fatalf("positions = %#v, err=%v", positions, err)
	}
	if got := floatFromPosition(positions[0], "stop_loss"); got != 102 {
		t.Fatalf("stop loss = %v, want 102", got)
	}
	if got := floatFromPosition(positions[0], "take_profit"); got != 114 {
		t.Fatalf("take profit = %v, want 114", got)
	}
	orders, err := broker.GetOpenOrders("SKHYUSDT")
	if err != nil || len(orders) != 1 || orders[0].Price != 114 {
		t.Fatalf("open orders = %#v, err=%v; want replacement target 114", orders, err)
	}
	if events := broker.RecentOrderEvents(10); len(events) != 3 || events[1].Status != "CANCELED" || events[1].Reason != "protection_replaced" || events[2].Status != "NEW" || events[2].Reason != "protection_updated" {
		t.Fatalf("protection replacement events = %#v, want cancellation followed by replacement", events)
	}
}

func TestPaperUpdatePositionUsesShortTakeProfitProtectionOrder(t *testing.T) {
	prices := fixedPaperPriceSource{"SKHYUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "open_short", Leverage: 3,
		PositionSizeUSD: 300,
	}); err != nil {
		t.Fatalf("open short: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position", NewTakeProfit: 95,
	}); err != nil {
		t.Fatalf("set short take profit: %v", err)
	}
	orders, err := broker.GetOpenOrders("SKHYUSDT")
	if err != nil || len(orders) != 1 {
		t.Fatalf("short protection orders = %#v, err=%v", orders, err)
	}
	if orders[0].PositionSide != "SHORT" || orders[0].Side != "BUY" || orders[0].Price != 95 {
		t.Fatalf("short protection order = %#v, want BUY SHORT @ 95", orders[0])
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
