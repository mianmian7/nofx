package market

import (
	"context"
	"testing"
	"time"
)

type closedObservationProvider struct {
	MarketDataProvider
	bars  map[string][]Kline
	calls int
}

func (p *closedObservationProvider) Exchange() string                { return "binance" }
func (p *closedObservationProvider) NormalizeSymbol(s string) string { return s }
func (p *closedObservationProvider) GetKlinesFresh(s, tf string, n int) ([]Kline, error) {
	p.calls++
	return append([]Kline(nil), p.bars[tf]...), nil
}
func (p *closedObservationProvider) GetOpenInterest(s string) (*OIData, error) {
	return &OIData{Latest: 20000000, NotionalUSD: 20000000}, nil
}
func (p *closedObservationProvider) GetFundingSnapshot(s string) (*FundingSnapshot, error) {
	return &FundingSnapshot{}, nil
}
func observationBars(boundary time.Time, period time.Duration) []Kline {
	out := make([]Kline, 61)
	for i := range out {
		start := boundary.Add(time.Duration(i-60) * period)
		price := 100 + float64(i)/10
		if i == 60 {
			price = 10000
		}
		out[i] = Kline{OpenTime: start.UnixMilli(), CloseTime: start.Add(period).UnixMilli() - 1, Open: price, High: price + 1, Low: price - 1, Close: price, Volume: 100}
	}
	return out
}

func TestClosedObservationPreservesPriceAndLegacy(t *testing.T) {
	boundary := time.Now().UTC().Truncate(15 * time.Minute)
	cutoff := boundary.Add(time.Minute)
	p := &closedObservationProvider{bars: map[string][]Kline{"15m": observationBars(boundary, 15*time.Minute), "1h": observationBars(boundary.Truncate(time.Hour), time.Hour)}}
	legacy, err := fetchWithTimeframesFromProvider(context.Background(), p, "TESTUSDT", []string{"15m", "1h"}, "15m", 30)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := fetchWithTimeframesFromProviderMode(context.Background(), p, "TESTUSDT", []string{"15m", "1h"}, "15m", 30, true, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CurrentPrice != 10000 || candidate.CurrentPrice != legacy.CurrentPrice {
		t.Fatal("candidate changed live raw price")
	}
	if candidate.CurrentEMA20 != calculateEMA(p.bars["15m"][:60], 20) {
		t.Fatal("candidate EMA contains forming extreme")
	}
	if candidate.CurrentEMA20 == legacy.CurrentEMA20 {
		t.Fatal("indicator observation unchanged despite forming extreme")
	}
	if legacy.ClosedIndicatorsAt.IsZero() == false || !candidate.ClosedIndicatorsAt.Equal(cutoff) {
		t.Fatal("mode diagnostics incorrect")
	}
	for tf, series := range candidate.TimeframeData {
		last := series.Klines[len(series.Klines)-1]
		if last.Close == 10000 {
			t.Errorf("forming bar leaked into %s", tf)
		}
		if candidate.FreshnessProofs[tf].SourceTime.UnixMilli() != p.bars[tf][60].OpenTime {
			t.Error("candidate mislabeled closed data as transport source time")
		}
	}
	candidate.TimeframeData["15m"].Klines[0].Close = -1
	if legacy.TimeframeData["15m"].Klines[0].Close == -1 || p.bars["15m"][30].Close == -1 {
		t.Fatal("candidate mutated legacy/provider input")
	}
}

func TestClosedObservationBoundaryAndInvalidHistory(t *testing.T) {
	boundary := time.Now().UTC().Truncate(15 * time.Minute)
	raw := observationBars(boundary, 15*time.Minute)
	for _, offset := range []time.Duration{-time.Millisecond, 0, time.Millisecond, time.Minute} {
		closed, err := completedIndicatorKlines(raw, boundary.Add(offset))
		if offset < 0 {
			if err == nil {
				t.Error("snapshot beyond cutoff accepted")
			}
			continue
		}
		if err != nil || len(closed) != 60 {
			t.Errorf("boundary offset %v: len=%d, err=%v", offset, len(closed), err)
		}
	}
	if _, err := completedIndicatorKlines(raw[20:], boundary.Add(time.Minute)); err == nil {
		t.Error("insufficient completed history accepted")
	}
	gap := append([]Kline(nil), raw[:59]...)
	gap = append(gap, raw[60])
	if _, err := completedIndicatorKlines(gap, boundary.Add(time.Minute)); err == nil {
		t.Error("latest completed candle missing but accepted")
	}
	unordered := append([]Kline(nil), raw...)
	unordered[58], unordered[59] = unordered[59], unordered[58]
	if _, err := completedIndicatorKlines(unordered, boundary.Add(time.Minute)); err == nil {
		t.Error("unordered input accepted")
	}
}

func TestClosedObservationBypassesLegacyCache(t *testing.T) {
	ResetMarketDataCache()
	t.Cleanup(ResetMarketDataCache)
	boundary := time.Now().UTC().Truncate(15 * time.Minute)
	p := &closedObservationProvider{bars: map[string][]Kline{"15m": observationBars(boundary, 15*time.Minute)}}
	old, err := GetWithTimeframesForProviderContext(context.Background(), p, "TESTUSDT", []string{"15m"}, "15m", 30)
	if err != nil {
		t.Fatal(err)
	}
	count := p.calls
	for i := 0; i < 2; i++ {
		if _, err := GetWithClosedTimeframesForProviderContext(context.Background(), p, "TESTUSDT", []string{"15m"}, "15m", 30); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls != count+2 {
		t.Fatal("candidate reused pre-boundary aggregate cache")
	}
	again, err := GetWithTimeframesForProviderContext(context.Background(), p, "TESTUSDT", []string{"15m"}, "15m", 30)
	if err != nil {
		t.Fatal(err)
	}
	if p.calls != count+2 || again.CurrentEMA20 != old.CurrentEMA20 || !again.ClosedIndicatorsAt.IsZero() {
		t.Fatal("candidate polluted legacy cache")
	}
}
