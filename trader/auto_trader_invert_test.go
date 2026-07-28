package trader

import (
	"nofx/kernel"
	"testing"
)

func TestInvertDecisions(t *testing.T) {
	decisions := []kernel.Decision{
		{
			Symbol:     "BTCUSDT",
			Action:     "open_long",
			StopLoss:   90000,
			TakeProfit: 100000,
			Reasoning:  "Bullish momentum",
		},
		{
			Symbol:     "ETHUSDT",
			Action:     "open_short",
			StopLoss:   3500,
			TakeProfit: 3000,
			Reasoning:  "Bearish divergence",
		},
		{
			Symbol:    "SOLUSDT",
			Action:    "close_long",
			Reasoning: "Taking profit",
		},
		{
			Symbol:    "DOGEUSDT",
			Action:    "close_short",
			Reasoning: "Stopping loss",
		},
		{
			Symbol:    "BNBUSDT",
			Action:    "hold",
			Reasoning: "No signal",
		},
	}

	inverted := invertDecisions(decisions)

	if len(inverted) != 5 {
		t.Fatalf("expected 5 decisions, got %d", len(inverted))
	}

	// 1. open_long -> open_short
	if inverted[0].Action != "open_short" {
		t.Errorf("expected open_short, got %s", inverted[0].Action)
	}
	if inverted[0].StopLoss != 100000 || inverted[0].TakeProfit != 90000 {
		t.Errorf("expected swapped SL/TP (SL:100000, TP:90000), got SL:%.0f, TP:%.0f", inverted[0].StopLoss, inverted[0].TakeProfit)
	}

	// 2. open_short -> open_long
	if inverted[1].Action != "open_long" {
		t.Errorf("expected open_long, got %s", inverted[1].Action)
	}
	if inverted[1].StopLoss != 3000 || inverted[1].TakeProfit != 3500 {
		t.Errorf("expected swapped SL/TP (SL:3000, TP:3500), got SL:%.0f, TP:%.0f", inverted[1].StopLoss, inverted[1].TakeProfit)
	}

	// Exits must continue to target the actual positions shown to the AI.
	if inverted[2].Action != "close_long" {
		t.Errorf("expected close_long to remain unchanged, got %s", inverted[2].Action)
	}

	if inverted[3].Action != "close_short" {
		t.Errorf("expected close_short to remain unchanged, got %s", inverted[3].Action)
	}

	// 5. hold -> hold
	if inverted[4].Action != "hold" {
		t.Errorf("expected hold, got %s", inverted[4].Action)
	}
}

func TestInvertDecisionsPreservesSingleProtectivePrice(t *testing.T) {
	decisions := []kernel.Decision{
		{Symbol: "BTCUSDT", Action: "open_long", StopLoss: 90},
		{Symbol: "ETHUSDT", Action: "open_short", TakeProfit: 80},
	}

	inverted := invertDecisions(decisions)

	if inverted[0].Action != "open_short" || inverted[0].StopLoss != 0 || inverted[0].TakeProfit != 90 {
		t.Fatalf("long stop should become short take-profit, got %#v", inverted[0])
	}
	if inverted[1].Action != "open_long" || inverted[1].StopLoss != 80 || inverted[1].TakeProfit != 0 {
		t.Fatalf("short take-profit should become long stop, got %#v", inverted[1])
	}
}
