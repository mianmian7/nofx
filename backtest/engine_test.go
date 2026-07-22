package backtest

import (
	"context"
	"math"
	"nofx/market"
	"testing"
)

type providerFunc func(context.Context, Snapshot) (Decision, error)

func (f providerFunc) Decide(ctx context.Context, snapshot Snapshot) (Decision, error) {
	return f(ctx, snapshot)
}

func candle(openTime int64, open, high, low, close float64) market.Kline {
	return market.Kline{OpenTime: openTime, CloseTime: openTime + 59_999, Open: open, High: high, Low: low, Close: close}
}

func TestRunFillsDecisionAtNextOpen(t *testing.T) {
	candles := []market.Kline{
		candle(0, 100, 101, 99, 100),
		candle(60_000, 110, 116, 109, 115),
	}
	provider := providerFunc(func(_ context.Context, snapshot Snapshot) (Decision, error) {
		if snapshot.Index != 0 || len(snapshot.Candles) != 1 {
			t.Fatalf("unexpected snapshot: index=%d candles=%d", snapshot.Index, len(snapshot.Candles))
		}
		return Decision{Action: "open_long", Leverage: 1, PositionSizeUSD: 100}, nil
	})

	result, err := Run(context.Background(), Config{Symbol: "BTCUSDT", InitialBalance: 1000}, candles, provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trades) != 1 {
		t.Fatalf("trades=%d, want 1", len(result.Trades))
	}
	if result.Trades[0].EntryPrice != 110 {
		t.Fatalf("entry=%v, want next candle open 110", result.Trades[0].EntryPrice)
	}
	if result.Trades[0].ExitPrice != 115 {
		t.Fatalf("exit=%v, want final close 115", result.Trades[0].ExitPrice)
	}
}

func TestRunUsesConservativeStopWhenStopAndTargetBothTouched(t *testing.T) {
	candles := []market.Kline{
		candle(0, 100, 101, 99, 100),
		candle(60_000, 100, 120, 80, 105),
		candle(120_000, 105, 106, 104, 105),
	}
	provider := providerFunc(func(_ context.Context, snapshot Snapshot) (Decision, error) {
		if snapshot.Index == 0 {
			return Decision{Action: "open_long", Leverage: 1, PositionSizeUSD: 100, StopLoss: 90, TakeProfit: 110}, nil
		}
		return Decision{Action: "hold"}, nil
	})

	result, err := Run(context.Background(), Config{Symbol: "BTCUSDT", InitialBalance: 1000}, candles, provider)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Trades[0].ExitReason; got != "stop_loss" {
		t.Fatalf("exit reason=%q, want stop_loss", got)
	}
	if got := result.Trades[0].ExitPrice; got != 90 {
		t.Fatalf("exit=%v, want 90", got)
	}
}

func TestRunIncludesFeesAndAdverseSlippage(t *testing.T) {
	candles := []market.Kline{
		candle(0, 100, 101, 99, 100),
		candle(60_000, 100, 101, 99, 100),
	}
	provider := providerFunc(func(context.Context, Snapshot) (Decision, error) {
		return Decision{Action: "open_long", Leverage: 1, PositionSizeUSD: 100}, nil
	})

	result, err := Run(context.Background(), Config{
		Symbol: "BTCUSDT", InitialBalance: 1000, FeeBPS: 10, SlippageBPS: 10,
	}, candles, provider)
	if err != nil {
		t.Fatal(err)
	}
	trade := result.Trades[0]
	if trade.EntryPrice <= 100 || trade.ExitPrice >= 100 {
		t.Fatalf("fills are not adverse: entry=%v exit=%v", trade.EntryPrice, trade.ExitPrice)
	}
	if trade.Fees <= 0 || trade.NetPnL >= 0 {
		t.Fatalf("costs missing: fees=%v net=%v", trade.Fees, trade.NetPnL)
	}
	if math.Abs(result.FinalEquity-(1000+trade.NetPnL)) > 1e-9 {
		t.Fatalf("final equity=%v net=%v", result.FinalEquity, trade.NetPnL)
	}
}

func TestRunModelsLiquidationBeforeStop(t *testing.T) {
	candles := []market.Kline{
		candle(0, 100, 101, 99, 100),
		candle(60_000, 100, 102, 70, 75),
		candle(120_000, 75, 76, 74, 75),
	}
	provider := providerFunc(func(_ context.Context, snapshot Snapshot) (Decision, error) {
		if snapshot.Index == 0 {
			return Decision{Action: "open_long", Leverage: 5, PositionSizeUSD: 1000, StopLoss: 70}, nil
		}
		return Decision{Action: "hold"}, nil
	})

	result, err := Run(context.Background(), Config{
		Symbol: "BTCUSDT", InitialBalance: 1000, MaxLeverage: 5, MaxMarginUsage: 1,
	}, candles, provider)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Trades[0].ExitReason; got != "liquidation" {
		t.Fatalf("exit reason=%q, want liquidation", got)
	}
}

func TestRunRejectsInvalidExitLevelsForDirection(t *testing.T) {
	candles := []market.Kline{
		candle(0, 100, 101, 99, 100),
		candle(60_000, 100, 101, 99, 100),
	}
	provider := providerFunc(func(context.Context, Snapshot) (Decision, error) {
		return Decision{Action: "open_long", Leverage: 1, PositionSizeUSD: 100, StopLoss: 110, TakeProfit: 90}, nil
	})

	result, err := Run(context.Background(), Config{Symbol: "BTCUSDT", InitialBalance: 1000}, candles, provider)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Trades[0].ExitReason; got != "end_of_replay" {
		t.Fatalf("exit reason=%q, invalid levels should be ignored", got)
	}
}
