package market

import (
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
