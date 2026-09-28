package signals

import (
	"math"
	"nofx/market"
	"testing"
	"time"
)

func candles(now time.Time, rising bool) map[string][]market.Kline {
	out := make(map[string][]market.Kline)
	for _, tf := range Timeframes {
		d := durations[tf]
		for i := 0; i < 183; i++ {
			price := 100 + float64(i)*0.2
			if !rising {
				price = 200 - float64(i)*0.2
			}
			start := now.Truncate(d).Add(time.Duration(i-182) * d)
			out[tf] = append(out[tf], market.Kline{OpenTime: start.UnixMilli(), CloseTime: start.Add(d).UnixMilli() - 1, Open: price, High: price + 0.25, Low: price - 0.25, Close: price, Volume: 100})
		}
	}
	return out
}

func TestAnalyzeClosedCandlesAndDirections(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 4, 0, 0, time.UTC)
	for _, rising := range []bool{true, false} {
		data := candles(now, rising)
		got, err := Analyze("binance", "BTCUSDT", data, now)
		if err != nil {
			t.Fatal(err)
		}
		want := "long"
		if !rising {
			want = "short"
		}
		if got.Direction != want || !got.Confirmed || got.Score < 70 || !Fresh(got, now) {
			t.Fatalf("unexpected signal: %+v", got)
		}
		// An extreme current (unclosed) candle cannot change a recommendation.
		for _, tf := range Timeframes {
			data[tf][182].Close = 1e9
			data[tf][182].High = 1e9
		}
		unchanged, err := Analyze("binance", "BTCUSDT", data, now)
		if err != nil || unchanged.Score != got.Score || unchanged.Direction != got.Direction {
			t.Fatalf("open candle changed signal: %+v %v", unchanged, err)
		}
		if Fresh(got, now.Add(11*time.Minute)) {
			t.Fatal("old observation was fresh")
		}
		if Fresh(got, now.Add(-time.Minute)) {
			t.Fatal("future observation was fresh")
		}
	}
}

func TestAnalyzeRejectsBadOrMissingData(t *testing.T) {
	now := time.Now().UTC()
	for _, name := range []string{"gap", "duplicate", "nan", "invalid_price", "stale", "warmup"} {
		t.Run(name, func(t *testing.T) {
			data := candles(now, true)
			bars := data["1h"]
			switch name {
			case "gap":
				bars = append(bars[:100], bars[101:]...)
			case "duplicate":
				bars[100] = bars[99]
			case "nan":
				bars[100].Close = math.NaN()
			case "invalid_price":
				bars[100].Low = bars[100].High + 1
			case "stale":
				bars = bars[:len(bars)-2]
			case "warmup":
				bars = bars[len(bars)-30:]
			}
			data["1h"] = bars
			if _, err := Analyze("binance", "BTCUSDT", data, now); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestFreshChecksEachTimeframeNotJustFetchTime(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 4, 0, 0, time.UTC)
	got, err := Analyze("binance", "BTCUSDT", candles(now, true), now)
	if err != nil {
		t.Fatal(err)
	}
	got.Frames[2].ClosedAt -= int64(4 * time.Hour / time.Millisecond)
	if Fresh(got, now) {
		t.Fatal("stale 4h candle accepted with fresh fetch time")
	}
}
