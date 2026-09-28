package trader

import (
	"nofx/kernel"
	"nofx/store"
	"testing"
)

func TestPaperPositionSlotsReservePendingEntries(t *testing.T) {
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.RiskControl.MaxPositions = 1
	at, err := NewAutoTrader(AutoTraderConfig{ID: "slot-test", AIModel: "custom", Exchange: "binance", ExecutionMode: ExecutionModePaper, InitialBalance: 100, StrategyConfig: &strategy}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	at.marketDataProvider = nil
	at.binanceMarketClient = nil
	b := at.paperBroker
	b.prices = &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"101", "10"}}}
	b.fundingSource = nil
	b.config.MakerFirst = true
	b.config.MinRiskRewardRatio = 0
	open := func(sym string) error {
		_, err := b.ExecuteDecision(&kernel.Decision{Symbol: sym, Action: "open_long", Leverage: 3, PositionSizeUSD: 20, StopLoss: 95, TakeProfit: 115})
		return err
	}
	if err := open("SOLUSDT"); err != nil {
		t.Fatal(err)
	}
	if err := open("ETHUSDT"); err == nil {
		t.Fatal("second resting entry bypassed max_positions=1; both can subsequently fill")
	}
	if len(b.PendingOrders()) != 1 {
		t.Fatalf("rejection must preserve first order: %+v", b.PendingOrders())
	}
}
