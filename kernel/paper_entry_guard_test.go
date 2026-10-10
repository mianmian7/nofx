package kernel

import (
	"encoding/json"
	"math"
	"nofx/market"
	"nofx/store"
	"strings"
	"testing"
	"time"
)

func entryGuardFixture(long bool, now time.Time) *market.Data {
	d := &market.Data{Symbol: "TESTUSDT", CurrentPrice: 98, ClosedIndicatorsAt: now, TimeframeData: map[string]*market.TimeframeSeriesData{}}
	for tf, duration := range map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour} {
		at := now.Truncate(duration).Add(-duration)
		f := &market.TimeframeSeriesData{Timeframe: tf, EMA20Values: []float64{110}, EMA50Values: []float64{120}, Klines: []market.KlineBar{{Time: at.Add(-duration).UnixMilli(), Open: 101, High: 104, Low: 98, Close: 100}, {Time: at.UnixMilli(), Open: 101, High: 103, Low: 97, Close: 99}}}
		if long {
			f.EMA20Values[0] = 90
			f.EMA50Values[0] = 80
			for i := range f.Klines {
				b := &f.Klines[i]
				b.Open = 200 - b.Open
				b.Close = 200 - b.Close
				b.High, b.Low = 200-b.Low, 200-b.High
			}
		}
		d.TimeframeData[tf] = f
	}
	if long {
		d.CurrentPrice = 102
	}
	return d
}

func TestPaperEntryGuardSymmetricAndFailClosed(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 7, 0, 0, time.UTC)
	for _, long := range []bool{false, true} {
		action, stop := "open_short", 105.0
		if long {
			action, stop = "open_long", 95
		}
		t.Run(action, func(t *testing.T) {
			d := entryGuardFixture(long, now)
			e, err := BuildPaperEntryEvidence(d, action, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Validate(d.Symbol, action, d.CurrentPrice, stop, now); err != nil {
				t.Fatal(err)
			}
			for name, mutate := range map[string]func(*market.Data){
				"wrong higher trend":          func(d *market.Data) { d.TimeframeData["1h"].EMA20Values[0] = d.TimeframeData["1h"].EMA50Values[0] },
				"no directional confirmation": func(d *market.Data) { f := d.TimeframeData["15m"]; f.Klines[1].Close = f.Klines[1].Open },
				"forming bar":                 func(d *market.Data) { d.TimeframeData["15m"].Klines[1].Time += 900000 },
				"stale 4h":                    func(d *market.Data) { d.TimeframeData["4h"].Klines[1].Time -= 4 * 3600000 },
				"gap":                         func(d *market.Data) { d.TimeframeData["15m"].Klines[0].Time -= 900000 },
				"missing tf":                  func(d *market.Data) { delete(d.TimeframeData, "1h") },
				"NaN":                         func(d *market.Data) { d.TimeframeData["1h"].EMA20Values[0] = math.NaN() },
				"bad OHLC":                    func(d *market.Data) { d.TimeframeData["15m"].Klines[1].Low = 999 },
				"no completion proof":         func(d *market.Data) { d.ClosedIndicatorsAt = time.Time{} },
			} {
				t.Run(name, func(t *testing.T) {
					d := entryGuardFixture(long, now)
					mutate(d)
					if _, err := BuildPaperEntryEvidence(d, action, now); err == nil {
						t.Fatal("invalid observation passed")
					}
				})
			}
			if _, err := BuildPaperEntryEvidence(d, action, now.Truncate(15*time.Minute).Add(15*time.Minute)); err == nil {
				t.Fatal("late response passed candle boundary")
			}
			if err := e.Validate("OTHERUSDT", action, d.CurrentPrice, stop, now); err == nil {
				t.Fatal("evidence transferred to another symbol")
			}
			if err := e.Validate(d.Symbol, action, e.TrendBoundary, stop, now); err == nil {
				t.Fatal("fresh price trend break allowed")
			}
			if err := e.Validate(d.Symbol, action, d.CurrentPrice, e.Invalidation, now); err == nil {
				t.Fatal("stop inside/on invalidation allowed")
			}
			if err := e.Validate(d.Symbol, action, d.CurrentPrice, stop, e.ValidUntil); err == nil {
				t.Fatal("expired evidence allowed")
			}
			extended := *e
			extended.ValidUntil = extended.ValidUntil.Add(time.Minute)
			if err := extended.Validate(d.Symbol, action, d.CurrentPrice, stop, now); err == nil {
				t.Fatal("renewed evidence allowed")
			}
		})
	}
}

func TestPaperEntryEvidenceCannotComeFromModelJSON(t *testing.T) {
	var d Decision
	if err := json.Unmarshal([]byte(`{"action":"open_short","symbol":"TESTUSDT","paper_entry_evidence":{"symbol":"TESTUSDT"},"PaperEntryEvidence":{"symbol":"TESTUSDT"}}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.PaperEntryEvidence != nil {
		t.Fatal("model supplied server evidence")
	}
}

func TestPaperEntryTypedStatePreservesExitsAndOptOut(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&cfg)
	now := time.Now().UTC()
	for _, enabled := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			cfg.PaperEntryGuard = enabled
			engine = NewStrategyEngine(&cfg)
			state, questions := BuildJevStateAndQuestions(&JevMarketFeature{Symbol: "TESTUSDT", HasPosition: held})
			before, _ := json.Marshal(questions["position_action"])
			engine.addPaperEntryState(state, questions, entryGuardFixture(false, now), now)
			_, present := state.(map[string]interface{})["entry_eligibility"]
			if present != (enabled && !held) {
				t.Fatalf("typed eligibility enabled=%t held=%t present=%t", enabled, held, present)
			}
			after, _ := json.Marshal(questions["position_action"])
			if string(before) != string(after) {
				t.Fatal("exit question changed")
			}
		}
	}
}

func TestPaperEntryGuidanceIsOptIn(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	ctx := &Context{CandidateCoins: []CandidateCoin{{Symbol: "TESTUSDT"}}, MarketDataMap: map[string]*market.Data{"TESTUSDT": entryGuardFixture(false, time.Now().UTC())}}
	engine := NewStrategyEngine(&cfg)
	var b strings.Builder
	engine.writePaperEntryGuidance(&b, ctx)
	if b.Len() != 0 {
		t.Fatal("opt-out prompt changed")
	}
	cfg.PaperEntryGuard = true
	engine = NewStrategyEngine(&cfg)
	engine.writePaperEntryGuidance(&b, ctx)
	for _, want := range []string{"Paper Entry Evidence Contract", "minimum confirmation present", "stop must be strictly above 104", "exits and protective updates are not gated"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q: %s", want, b.String())
		}
	}
}
