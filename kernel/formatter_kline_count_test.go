package kernel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nofx/market"
)

func TestKlineFormattersIncludeFiftyConfiguredBars(t *testing.T) {
	const timeframe = "15m"
	klines := make([]market.KlineBar, 50)
	for i := range klines {
		klines[i] = market.KlineBar{
			Time:   time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).UnixMilli(),
			Open:   float64(i + 1),
			High:   float64(i + 2),
			Low:    float64(i),
			Close:  float64(i + 1),
			Volume: float64(100 + i),
		}
	}
	data := map[string]*market.TimeframeSeriesData{
		timeframe: {Timeframe: timeframe, Klines: klines},
	}

	for name, output := range map[string]string{
		"zh": formatKlineDataZH("TESTUSDT", data, []string{timeframe}),
		"en": formatKlineDataEN("TESTUSDT", data, []string{timeframe}),
	} {
		for i := 0; i < 50; i++ {
			stamp := time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format("01-02 15:04")
			if !strings.Contains(output, stamp) {
				t.Fatalf("%s formatter omitted bar %d (%s)\n%s", name, i, stamp, fmt.Sprintf("output length=%d", len(output)))
			}
		}
	}
}
