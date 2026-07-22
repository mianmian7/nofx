package backtest

import (
	"context"
	"nofx/market"
	"testing"
)

func TestTrendProviderOpensOnlyAfterPriorRangeBreaks(t *testing.T) {
	config := DefaultTrendConfig()
	config.FastEMAPeriod = 3
	config.SlowEMAPeriod = 5
	config.BreakoutPeriod = 3
	config.ATRPeriod = 3
	provider, err := NewTrendProvider(config)
	if err != nil {
		t.Fatal(err)
	}
	candles := []market.Kline{
		{Open: 99, High: 100, Low: 98, Close: 99},
		{Open: 99, High: 101, Low: 98, Close: 100},
		{Open: 100, High: 102, Low: 99, Close: 101},
		{Open: 101, High: 103, Low: 100, Close: 102},
		{Open: 102, High: 104, Low: 101, Close: 103},
		{Open: 103, High: 108, Low: 102, Close: 107},
	}
	decision, err := provider.Decide(context.Background(), Snapshot{
		Candles: candles, Equity: 1000, Balance: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "open_long" {
		t.Fatalf("action=%q, want open_long", decision.Action)
	}
	if decision.StopLoss >= candles[len(candles)-1].Close || decision.TakeProfit <= candles[len(candles)-1].Close {
		t.Fatalf("invalid trend exits: %+v", decision)
	}
}
