package trader

import (
	"testing"
	"time"
)

func TestReconstructPaperPerformanceRecognizesDirectionalMakerExits(t *testing.T) {
	baseTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	fills := []PaperFill{
		{OrderID: 1, Symbol: "BTCUSDT", Action: "open_short", Side: "short", Price: 100, Quantity: 1, Fee: 0.1, Time: baseTime},
		{OrderID: 2, Symbol: "BTCUSDT", Action: "take_profit_short", Price: 90, Quantity: 1, Fee: 0.05, RealizedPnL: 9.85, Status: "FILLED", IsMaker: true, Time: baseTime.Add(time.Minute)},
		{OrderID: 3, Symbol: "ETHUSDT", Action: "open_long", Side: "long", Price: 50, Quantity: 2, Fee: 0.1, Time: baseTime.Add(2 * time.Minute)},
		{OrderID: 4, Symbol: "ETHUSDT", Action: "stop_loss_long", Price: 45, Quantity: 2, Fee: 0.05, RealizedPnL: -10.15, Status: "FILLED", IsMaker: true, Time: baseTime.Add(3 * time.Minute)},
	}

	performance := reconstructPaperPerformance(fills, 100)

	if performance.TotalTrades != 2 {
		t.Fatalf("expected both directional exits to be counted, got %d trades", performance.TotalTrades)
	}
	if len(performance.ClosedTrades) != 2 {
		t.Fatalf("expected two closed-trade records, got %d", len(performance.ClosedTrades))
	}
	if performance.ClosedTrades[1].CloseReason != "take_profit_short" || performance.ClosedTrades[1].Side != "short" {
		t.Fatalf("short take-profit was reconstructed incorrectly: %#v", performance.ClosedTrades[1])
	}
	if performance.ClosedTrades[0].CloseReason != "stop_loss_long" || performance.ClosedTrades[0].Side != "long" {
		t.Fatalf("long stop-loss was reconstructed incorrectly: %#v", performance.ClosedTrades[0])
	}
}
