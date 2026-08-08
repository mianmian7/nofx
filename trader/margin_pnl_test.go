package trader

import (
	"math"
	"testing"
	"time"

	"nofx/kernel"
)

func TestMarginPnLTakeProfitPriceConvertsLongAndShortAtFinalLeverage(t *testing.T) {
	for _, tc := range []struct {
		leverage int
		long     float64
		short    float64
	}{
		{leverage: 10, long: 104, short: 96},
		{leverage: 20, long: 102, short: 98},
		{leverage: 50, long: 100.8, short: 99.2},
	} {
		long, err := marginPnLTakeProfitPrice("open_long", 100, tc.leverage, 40)
		if err != nil {
			t.Fatalf("long %dx conversion: %v", tc.leverage, err)
		}
		short, err := marginPnLTakeProfitPrice("open_short", 100, tc.leverage, 40)
		if err != nil {
			t.Fatalf("short %dx conversion: %v", tc.leverage, err)
		}
		if math.Abs(long-tc.long) > 1e-9 || math.Abs(short-tc.short) > 1e-9 {
			t.Fatalf("%dx +40%% Margin/Position PnL prices = long %.8f short %.8f, want %.8f/%.8f", tc.leverage, long, short, tc.long, tc.short)
		}
	}
}

func TestMarginPnLStopPriceConvertsLongAndShortAtFinalLeverage(t *testing.T) {
	for _, tc := range []struct {
		leverage int
		long     float64
		short    float64
	}{
		{leverage: 10, long: 98, short: 102},
		{leverage: 20, long: 99, short: 101},
		{leverage: 50, long: 99.6, short: 100.4},
	} {
		long, err := marginPnLStopPrice("open_long", 100, tc.leverage, -20)
		if err != nil {
			t.Fatalf("long %dx conversion: %v", tc.leverage, err)
		}
		short, err := marginPnLStopPrice("open_short", 100, tc.leverage, -20)
		if err != nil {
			t.Fatalf("short %dx conversion: %v", tc.leverage, err)
		}
		if math.Abs(long-tc.long) > 1e-9 || math.Abs(short-tc.short) > 1e-9 {
			t.Fatalf("%dx -20%% Margin/Position PnL prices = long %.8f short %.8f, want %.8f/%.8f", tc.leverage, long, short, tc.long, tc.short)
		}
	}
}

func TestPositionInitialMarginUsesEntryNotMarkWhenProviderOmitsMargin(t *testing.T) {
	position := map[string]interface{}{
		"entryPrice":  100.0,
		"markPrice":   110.0,
		"positionAmt": 1.0,
		"leverage":    10.0,
	}
	if got := positionInitialMargin(position, 100, 110, 1, 10); math.Abs(got-10) > 1e-9 {
		t.Fatalf("initial margin = %.8f, want 10.00000000", got)
	}
	if got := calculatePnLPercentage(10, positionInitialMargin(position, 100, 110, 1, 10)); math.Abs(got-100) > 1e-9 {
		t.Fatalf("Margin/Position PnL = %.8f%%, want 100.00000000%%", got)
	}
}

func TestPositionInitialMarginPrefersProviderInitialMarginAndSeparatesPricePnL(t *testing.T) {
	position := map[string]interface{}{
		"initial_margin": 25.0,
		"margin_used":    15.0,
	}
	if got := positionInitialMargin(position, 100, 110, 1, 10); got != 25 {
		t.Fatalf("provider initial margin = %.8f, want 25.00000000", got)
	}
	if got := calculatePricePnLPct(100, 110, "long"); math.Abs(got-10) > 1e-9 {
		t.Fatalf("long Price PnL = %.8f%%, want +10%%", got)
	}
	if got := calculatePricePnLPct(100, 110, "short"); math.Abs(got+10) > 1e-9 {
		t.Fatalf("short Price PnL = %.8f%%, want -10%%", got)
	}
}

func TestPaperBrokerAcceptsOnlyAbsoluteTakeProfitPrice(t *testing.T) {
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 20,
		StopLoss: 99, TakeProfit: 40, // +40 is a PnL threshold, not a valid price
	}); err == nil {
		t.Fatal("PaperBroker accepted a Margin/Position PnL percentage as a take-profit price")
	}
	if broker.Snapshot().OpenPositions != 0 {
		t.Fatal("invalid take-profit mutated the paper position")
	}

	target, err := marginPnLTakeProfitPrice("open_long", 100, 20, 40)
	if err != nil {
		t.Fatalf("convert target: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 20,
		StopLoss: 99, TakeProfit: target,
	}); err != nil {
		t.Fatalf("PaperBroker rejected converted absolute target %.4f: %v", target, err)
	}
	if exits, err := broker.ProcessPrice("MUUSDT", target, time.Now().UTC()); err != nil || len(exits) != 1 || exits[0].Action != "take_profit" {
		t.Fatalf("converted target exit = %#v, err=%v", exits, err)
	}
}

func TestPaperBrokerRejectsNonFiniteAndWrongSideProtectionWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		stop   float64
		target float64
	}{
		{name: "long NaN stop", action: "open_long", stop: math.NaN(), target: 105},
		{name: "short positive infinity target", action: "open_short", stop: 110, target: math.Inf(1)},
		{name: "short wrong-side take-profit price", action: "open_short", stop: 110, target: 105},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, fixedPaperPriceSource{"MUUSDT": 100})
			if err != nil {
				t.Fatalf("NewPaperBroker: %v", err)
			}
			if _, err := broker.ExecuteDecision(&kernel.Decision{
				Symbol: "MUUSDT", Action: tc.action, PositionSizeUSD: 300, Leverage: 10,
				StopLoss: tc.stop, TakeProfit: tc.target,
			}); err == nil {
				t.Fatal("PaperBroker accepted invalid protection")
			}
			if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.Balance != 1_000 {
				t.Fatalf("invalid protection mutated paper account: %#v", snapshot)
			}
		})
	}
}
