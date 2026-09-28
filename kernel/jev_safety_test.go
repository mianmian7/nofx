package kernel

import (
	"fmt"
	"math"
	"nofx/market"
	"nofx/mcp/provider"
	"nofx/store"
	"testing"
	"time"
)

func safetyJevResponse() *provider.JevResponse {
	return &provider.JevResponse{Answers: map[string]provider.JevAnswer{
		"market_regime": {Choice: "strong_trend_up", Confidence: .9},
		"entry_signal":  {Choice: "open_long", Confidence: .85, Probabilities: map[string]float64{"open_long": .85, "wait": .1, "open_short": .05}},
	}}
}
func TestJevSafetyRejectsInvalidOpeningEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*provider.JevResponse)
	}{
		{"chosen_action_not_winner", func(r *provider.JevResponse) {
			a := r.Answers["entry_signal"]
			a.Choice = "open_short"
			r.Answers["entry_signal"] = a
			r.Answers["market_regime"] = provider.JevAnswer{Choice: "strong_trend_down"}
		}},
		{"extreme_volatility", func(r *provider.JevResponse) {
			r.Answers["market_regime"] = provider.JevAnswer{Choice: "extreme_volatility"}
		}},
		{"missing_regime", func(r *provider.JevResponse) { delete(r.Answers, "market_regime") }},
		{"opposed_regime", func(r *provider.JevResponse) {
			r.Answers["market_regime"] = provider.JevAnswer{Choice: "strong_trend_down"}
		}},
		{"nan_probability", func(r *provider.JevResponse) { r.Answers["entry_signal"].Probabilities["open_long"] = math.NaN() }},
		{"missing_chosen_probability", func(r *provider.JevResponse) { delete(r.Answers["entry_signal"].Probabilities, "open_long") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := safetyJevResponse()
			tc.change(r)
			d, e := EvaluateJevSignal(r, &JevMarketFeature{Symbol: "SOLUSDT", CurrentPrice: 100, ATR14: 1}, 100, 100, store.RiskControlConfig{MaxLeverage: 3}, DefaultDeterministicRiskConfig())
			if e == nil && d != nil && (d.Action == "open_long" || d.Action == "open_short") {
				t.Fatalf("unsafe evidence opened: %+v", d)
			}
		})
	}
}
func TestJevSafetyMacroUnavailableBlocksOnlyEntry(t *testing.T) {
	for i, m := range []*MacroBias{nil, {Bias: "bullish_only", Confidence: 90, ValidUntil: time.Now().Add(-time.Minute)}, {Bias: "garbage", Confidence: 90, ValidUntil: time.Now().Add(time.Hour)}} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/held=%v", i, held), func(t *testing.T) {
				f := &JevMarketFeature{Symbol: "SOLUSDT", CurrentPrice: 100, ATR14: 1, HasPosition: held, PositionSide: "long"}
				r := safetyJevResponse()
				r.Answers["position_action"] = provider.JevAnswer{Choice: "exit_loss", Confidence: .9}
				d, e := EvaluateJevSignalWithMacro(r, f, 100, 100, store.RiskControlConfig{MaxLeverage: 3}, DefaultDeterministicRiskConfig(), m)
				want := "wait"
				if held {
					want = "close_long"
				}
				if e != nil || d == nil || d.Action != want {
					t.Fatalf("want %s: %v %+v", want, e, d)
				}
			})
		}
	}
}
func TestJevSafetySizingHonorsRiskAndMinimum(t *testing.T) {
	risk := store.RiskControlConfig{MaxLeverage: 3, PaperMaxTradeRiskRatio: .005, MinRiskRewardRatio: 2.5, MinPositionSize: 12}
	for _, action := range []string{"open_long", "open_short"} {
		t.Run(action, func(t *testing.T) {
			d, e := CalculateATRParams("SOLUSDT", action, 100, 1, 100, 100, risk, DefaultDeterministicRiskConfig(), 80)
			if e != nil || d == nil || d.Action != action || d.PositionSizeUSD <= 0 || math.IsNaN(d.PositionSizeUSD) {
				t.Fatalf("valid setup must open: %v %+v", e, d)
			}
			stopDistance := math.Abs(100 - d.StopLoss)
			actualRisk := d.PositionSizeUSD/100*stopDistance + d.PositionSizeUSD*.002
			if actualRisk > .5+1e-9 || d.RiskUSD > .5+1e-9 {
				t.Fatalf("final executable risk exceeded: %v %+v", actualRisk, d)
			}
			if math.Abs(d.TakeProfit-100)/stopDistance < 2.5-1e-6 {
				t.Fatalf("RR ignored: %+v", d)
			}
		})
	}
	for _, tc := range []struct {
		name, symbol   string
		atr, available float64
	}{{"minimum", "BTCUSDT", 10, 100}, {"zero_balance", "SOLUSDT", 1, 0}, {"missing_atr", "SOLUSDT", 0, 100}} {
		t.Run(tc.name, func(t *testing.T) {
			d, e := CalculateATRParams(tc.symbol, "open_long", 100, tc.atr, 100, tc.available, risk, DefaultDeterministicRiskConfig(), 80)
			if e == nil && (d == nil || d.Action != "wait") {
				t.Fatalf("must not open: %+v", d)
			}
		})
	}
}
func TestJevSafetyFeaturesUseConfiguredTimeframe(t *testing.T) {
	data := &market.Data{CurrentPrice: 100, CurrentEMA20: 110, TimeframeData: map[string]*market.TimeframeSeriesData{
		"5m":  {EMA20Values: []float64{98}, EMA50Values: []float64{99}, ATR14: 1},
		"15m": {EMA20Values: []float64{97}, EMA50Values: []float64{96}, ATR14: 2},
		"1h":  {EMA20Values: []float64{110}, EMA50Values: []float64{105}, ATR14: 5}}}
	for _, tf := range []string{"5m", "15m"} {
		t.Run(tf, func(t *testing.T) {
			f := extractJevFeature("SOLUSDT", data, nil, tf)
			s := data.TimeframeData[tf]
			if f.EMA20 != s.EMA20Values[0] || f.ATR14 != s.ATR14 {
				t.Fatalf("mixed timeframe: %+v", f)
			}
		})
	}
	if f := extractJevFeature("SOLUSDT", data, nil, "4h"); f.ATR14 != 0 {
		t.Fatal("missing configured timeframe silently fell back")
	}
}
func TestJevSafetyRoundingKeepsValidObservedSetups(t *testing.T) {
	for _, v := range [][2]float64{{121.693, .589428571428573}, {84371.1, 236.4642857142847}} {
		for _, action := range []string{"open_long", "open_short"} {
			d, e := CalculateATRParams("SOLUSDT", action, v[0], v[1], 100, 100, store.RiskControlConfig{MaxLeverage: 3, PaperMaxTradeRiskRatio: .005, MinRiskRewardRatio: 2.5, MinPositionSize: 12}, DefaultDeterministicRiskConfig(), 80)
			if e != nil || d.Action != action {
				t.Fatalf("rounding vetoed valid setup: %v %+v", e, d)
			}
			if math.Abs(d.TakeProfit-v[0])/math.Abs(v[0]-d.StopLoss) < 2.5-1e-10 {
				t.Fatalf("rounded RR violated: %+v", d)
			}
		}
	}
}

func TestJevSafetyHigherTrendGatesOnlyEntries(t *testing.T) {
	for _, action := range []string{"open_long", "open_short", "close_long", "close_short", "hold"} {
		t.Run(action, func(t *testing.T) {
			d := &Decision{Action: action}
			gateJevEntryTrend(d, nil)
			want := action
			if action == "open_long" || action == "open_short" {
				want = "wait"
			}
			if d.Action != want {
				t.Fatal(d)
			}
		})
	}
	for _, action := range []string{"open_long", "open_short"} {
		t.Run("aligned/"+action, func(t *testing.T) {
			fast, slow := 99., 98.
			if action == "open_short" {
				fast, slow = 101, 102
			}
			m := &market.Data{CurrentPrice: 100, TimeframeData: map[string]*market.TimeframeSeriesData{}}
			for _, tf := range []string{"1h", "4h"} {
				m.TimeframeData[tf] = &market.TimeframeSeriesData{EMA20Values: []float64{fast}, EMA50Values: []float64{slow}}
			}
			d := &Decision{Action: action}
			gateJevEntryTrend(d, m)
			if d.Action != action {
				t.Fatal("aligned entry blocked", d)
			}
			m.TimeframeData["4h"].EMA50Values = []float64{fast}
			gateJevEntryTrend(d, m)
			if d.Action != "wait" {
				t.Fatal("flat higher trend allowed", d)
			}
		})
	}
}
