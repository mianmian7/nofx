// Package signals computes explainable market observations without paid data.
// Its scores are rule agreement, never calibrated probabilities or win rates.
package signals

import (
	"fmt"
	"math"
	"nofx/market"
	"nofx/store"
	"sort"
	"time"
)

const Version = "local-trend-1"

var Timeframes = []string{"15m", "1h", "4h"}
var durations = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour}

func finite(v float64) bool   { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func clamp(v float64) float64 { return math.Max(-1, math.Min(1, v)) }

// closedBars validates venue timestamps and values before using any indicators.
// A candle at the current interval boundary is still open and is excluded.
func closedBars(input []market.Kline, tf string, now time.Time) ([]market.Kline, error) {
	d, ok := durations[tf]
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %s", tf)
	}
	step := d.Milliseconds()
	bars := append([]market.Kline(nil), input...)
	sort.Slice(bars, func(i, j int) bool { return bars[i].OpenTime < bars[j].OpenTime })
	closed := make([]market.Kline, 0, len(bars))
	for _, b := range bars {
		if b.OpenTime <= 0 || b.OpenTime%step != 0 {
			return nil, fmt.Errorf("%s invalid candle timestamp", tf)
		}
		if b.OpenTime+step > now.UnixMilli() {
			continue
		}
		if len(closed) > 0 && closed[len(closed)-1].OpenTime == b.OpenTime {
			return nil, fmt.Errorf("%s duplicate candle", tf)
		}
		for _, v := range []float64{b.Open, b.High, b.Low, b.Close, b.Volume} {
			if !finite(v) {
				return nil, fmt.Errorf("%s non-finite candle", tf)
			}
		}
		if b.Low <= 0 || b.Close <= 0 || b.Volume < 0 || b.Low > math.Min(b.Open, b.Close) || b.High < math.Max(b.Open, b.Close) {
			return nil, fmt.Errorf("%s invalid OHLCV", tf)
		}
		b.CloseTime = b.OpenTime + step - 1
		closed = append(closed, b)
	}
	if len(closed) < 80 {
		return nil, fmt.Errorf("%s requires at least 80 closed candles", tf)
	}
	// Only the contiguous indicator window is required; older upstream history
	// cannot hide a gap or stale last observation in the actual calculation.
	if len(closed) > 180 {
		closed = closed[len(closed)-180:]
	}
	for i := 1; i < len(closed); i++ {
		if closed[i].OpenTime-closed[i-1].OpenTime != step {
			return nil, fmt.Errorf("%s candle gap", tf)
		}
	}
	expected := now.UnixMilli()/step*step - step
	if closed[len(closed)-1].OpenTime != expected {
		return nil, fmt.Errorf("%s latest closed candle is unavailable", tf)
	}
	return closed, nil
}

func frame(bars []market.Kline, tf string) store.LocalSignalFrame {
	last := bars[len(bars)-1]
	ema20 := market.ExportCalculateEMA(bars, 20)
	ema50 := market.ExportCalculateEMA(bars, 50)
	atr := market.ExportCalculateATR(bars, 14)
	rsi := market.ExportCalculateRSI(bars, 14)
	prior := bars[len(bars)-21 : len(bars)-1]
	var volume float64
	hi, lo := prior[0].High, prior[0].Low
	for _, b := range prior {
		volume += b.Volume
		hi = math.Max(hi, b.High)
		lo = math.Min(lo, b.Low)
	}
	ratio := 0.0
	if volume > 0 {
		ratio = last.Volume / (volume / 20)
	}
	trend, breakout := 0.0, 0.0
	if atr > 0 {
		trend = clamp((ema20 - ema50) / atr)
		if last.Close > hi {
			breakout = clamp((last.Close-hi)/atr + 0.5)
		}
		if last.Close < lo {
			breakout = clamp((last.Close-lo)/atr - 0.5)
		}
	}
	return store.LocalSignalFrame{Timeframe: tf, ClosedAt: last.CloseTime, Price: last.Close, EMA20: ema20, EMA50: ema50, RSI: rsi, ATRPercent: 100 * atr / last.Close, VolumeRatio: ratio, Trend: trend, Momentum: clamp((rsi - 50) / 25), Breakout: breakout}
}

func direction(frames []store.LocalSignalFrame) (string, float64) {
	weights := []float64{0.25, 0.35, 0.40}
	var total float64
	for i, f := range frames {
		total += weights[i] * (0.65*f.Trend + 0.25*f.Momentum + 0.10*f.Breakout)
	}
	score := math.Round(math.Abs(total)*1000) / 10
	if total >= 0.35 {
		return "long", score
	}
	if total <= -0.35 {
		return "short", score
	}
	return "wait", score
}

// Analyze confirms direction using two different 15m closes. Higher-timeframe
// observations in the confirmation calculation are cut off at that earlier
// close, avoiding a look-ahead when a 1h/4h candle has just closed.
func Analyze(exchange, symbol string, data map[string][]market.Kline, now time.Time) (store.LocalSignal, error) {
	result := store.LocalSignal{Exchange: exchange, Symbol: symbol, Version: Version, ObservedAt: now.UnixMilli(), Frames: []store.LocalSignalFrame{}, Warnings: []string{}}
	var previous []store.LocalSignalFrame
	cutoff := now.Truncate(15 * time.Minute).Add(-15 * time.Minute).UnixMilli()
	for _, tf := range Timeframes {
		bars, err := closedBars(data[tf], tf, now)
		if err != nil {
			return result, err
		}
		result.Frames = append(result.Frames, frame(bars, tf))
		end := len(bars)
		for end > 0 && bars[end-1].CloseTime >= cutoff {
			end--
		}
		if end < 60 {
			return result, fmt.Errorf("%s insufficient confirmation history", tf)
		}
		previous = append(previous, frame(bars[:end], tf))
	}
	result.ClosedAt = result.Frames[0].ClosedAt
	result.Direction, result.Score = direction(result.Frames)
	priorDirection, _ := direction(previous)
	result.Confirmed = result.Direction != "wait" && priorDirection == result.Direction
	// Extreme volatility and absent trading activity are explicit no-entry
	// states, not invitations to increase leverage or confidence.
	if result.Frames[0].ATRPercent > 5 || result.Frames[0].VolumeRatio == 0 {
		result.Warnings = append(result.Warnings, "extreme_volatility_or_no_volume")
		result.Confirmed = false
	}
	if !result.Confirmed {
		result.Direction = "wait"
	}
	return result, nil
}

func Fresh(result store.LocalSignal, now time.Time) bool {
	if result.Version != Version || len(result.Frames) != len(Timeframes) || result.ObservedAt <= 0 {
		return false
	}
	age := now.Sub(time.UnixMilli(result.ObservedAt))
	if age < 0 || age > 10*time.Minute {
		return false
	}
	for i, tf := range Timeframes {
		f := result.Frames[i]
		step := durations[tf].Milliseconds()
		// Allow the first two minutes of a new candle boundary for the bounded
		// collector to complete; never extend validity by re-reading old data.
		expected := now.Add(-2*time.Minute).UnixMilli()/step*step - 1
		if f.Timeframe != tf || f.ClosedAt < expected || f.ClosedAt >= now.UnixMilli() {
			return false
		}
	}
	return true
}
