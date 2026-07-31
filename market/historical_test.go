package market

import (
	"context"
	"testing"
	"time"
)

func TestFilterClosedKlinesRemovesFormingCandle(t *testing.T) {
	cutoff := time.UnixMilli(1500)
	input := []Kline{
		{OpenTime: 0, CloseTime: 999},
		{OpenTime: 1000, CloseTime: 1999},
	}
	closed := FilterClosedKlines(input, cutoff)
	if len(closed) != 1 || closed[0].CloseTime != 999 {
		t.Fatalf("closed=%+v, want only the completed candle", closed)
	}
}

func TestGetKlinesRangeContextRejectsOversizedRangeBeforeNetwork(t *testing.T) {
	start := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if _, err := GetKlinesRangeContext(context.Background(), "BTCUSDT", "1m", start, start.Add(501*time.Minute), 500); err == nil {
		t.Fatal("expected oversized historical range to be rejected before fetching")
	}
}
