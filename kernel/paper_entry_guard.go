package kernel

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/market"
	"nofx/mcp/provider"
)

// PaperEntryEvidence is generated from the observed completed candles, never
// decoded from model JSON. A resting entry carries the original evidence and
// expiry; repricing cannot renew the setup. This is not a profitability score.
type PaperEntryEvidence struct {
	Symbol        string    `json:"symbol"`
	Action        string    `json:"action"`
	ObservedAt    time.Time `json:"observed_at"`
	ValidUntil    time.Time `json:"valid_until"`
	TrendBoundary float64   `json:"trend_boundary"`
	Invalidation  float64   `json:"invalidation"`
}

func positiveEntryNumber(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

// BuildPaperEntryEvidence checks an intentionally small, symmetric contract:
// both higher timeframes agree numerically, and the latest completed 15m bar
// confirms movement in that direction. The AI must still justify a genuine
// pullback/retest and a target; this necessary condition is not that full thesis.
func BuildPaperEntryEvidence(data *market.Data, action string, now time.Time) (*PaperEntryEvidence, error) {
	if action != "open_long" && action != "open_short" {
		return nil, fmt.Errorf("entry guard requires an opening action")
	}
	if data == nil || data.Symbol == "" || data.ClosedIndicatorsAt.IsZero() || !positiveEntryNumber(data.CurrentPrice) {
		return nil, fmt.Errorf("entry guard: completed-candle observation is unavailable")
	}
	observed := data.ClosedIndicatorsAt.UTC()
	if observed.After(now) {
		return nil, fmt.Errorf("entry guard: observation is in the future")
	}
	expiry := observed.Truncate(15 * time.Minute).Add(15 * time.Minute)
	if !now.Before(expiry) {
		return nil, fmt.Errorf("entry guard: completed-candle window expired; wait for a fresh setup")
	}
	long := action == "open_long"
	boundary := math.Inf(1)
	if long {
		boundary = 0
	}
	for _, tf := range []struct {
		name     string
		duration time.Duration
	}{{"15m", 15 * time.Minute}, {"1h", time.Hour}, {"4h", 4 * time.Hour}} {
		f := data.TimeframeData[tf.name]
		if f == nil || len(f.Klines) < 2 || len(f.EMA20Values) == 0 || len(f.EMA50Values) == 0 {
			return nil, fmt.Errorf("entry guard: missing %s candles/EMAs", tf.name)
		}
		last, previous := f.Klines[len(f.Klines)-1], f.Klines[len(f.Klines)-2]
		expected := observed.Truncate(tf.duration).Add(-tf.duration).UnixMilli()
		if last.Time != expected || previous.Time != last.Time-tf.duration.Milliseconds() {
			return nil, fmt.Errorf("entry guard: %s candles are stale, forming, or nonconsecutive", tf.name)
		}
		for _, bar := range []market.KlineBar{previous, last} {
			if !positiveEntryNumber(bar.Open) || !positiveEntryNumber(bar.High) || !positiveEntryNumber(bar.Low) || !positiveEntryNumber(bar.Close) || bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) {
				return nil, fmt.Errorf("entry guard: invalid %s OHLC", tf.name)
			}
		}
		fast, slow := f.EMA20Values[len(f.EMA20Values)-1], f.EMA50Values[len(f.EMA50Values)-1]
		if !positiveEntryNumber(fast) || !positiveEntryNumber(slow) {
			return nil, fmt.Errorf("entry guard: invalid %s EMA", tf.name)
		}
		if tf.name == "15m" {
			continue
		}
		aligned := data.CurrentPrice < fast && fast < slow
		if long {
			aligned = data.CurrentPrice > fast && fast > slow
		}
		if !aligned {
			return nil, fmt.Errorf("entry guard: %s does not align with %s (price=%g EMA20=%g EMA50=%g)", tf.name, action, data.CurrentPrice, fast, slow)
		}
		if long {
			boundary = math.Max(boundary, fast)
		} else {
			boundary = math.Min(boundary, fast)
		}
	}
	f := data.TimeframeData["15m"]
	last, prev := f.Klines[len(f.Klines)-1], f.Klines[len(f.Klines)-2]
	confirmed := last.Close < last.Open && last.Close < prev.Close
	invalidation := math.Max(last.High, prev.High)
	if long {
		confirmed = last.Close > last.Open && last.Close > prev.Close
		invalidation = math.Min(last.Low, prev.Low)
	}
	if !confirmed {
		return nil, fmt.Errorf("entry guard: latest completed 15m candle has not confirmed %s; trend score alone is insufficient", action)
	}
	return &PaperEntryEvidence{Symbol: data.Symbol, Action: action, ObservedAt: observed, ValidUntil: expiry, TrendBoundary: boundary, Invalidation: invalidation}, nil
}

// Validate rechecks fresh price, structural stop and the ORIGINAL window at
// submission and every partial/pending fill. It never performs network I/O.
func (e *PaperEntryEvidence) Validate(symbol, action string, price, stop float64, now time.Time) error {
	if e == nil {
		return fmt.Errorf("entry guard: server-generated setup evidence is required")
	}
	if e.Symbol != symbol || e.Action != action || (action != "open_long" && action != "open_short") {
		return fmt.Errorf("entry guard: evidence does not match this symbol/direction")
	}
	if e.ObservedAt.IsZero() || e.ObservedAt.After(now) || !e.ValidUntil.Equal(e.ObservedAt.Truncate(15*time.Minute).Add(15*time.Minute)) || !now.Before(e.ValidUntil) {
		return fmt.Errorf("entry guard: setup expired or invalid; repricing cannot renew it")
	}
	if !positiveEntryNumber(price) || !positiveEntryNumber(stop) || !positiveEntryNumber(e.TrendBoundary) || !positiveEntryNumber(e.Invalidation) {
		return fmt.Errorf("entry guard: invalid price/stop/evidence")
	}
	if action == "open_long" {
		if price <= math.Max(e.TrendBoundary, e.Invalidation) || stop >= e.Invalidation {
			return fmt.Errorf("entry guard: long price must remain above trend/setup and stop below invalidation %g", e.Invalidation)
		}
	} else {
		if price >= math.Min(e.TrendBoundary, e.Invalidation) || stop <= e.Invalidation {
			return fmt.Errorf("entry guard: short price must remain below trend/setup and stop above invalidation %g", e.Invalidation)
		}
	}
	return nil
}

// Jev's typed questions do not load the LLM custom prompt. Expose the same
// factual eligibility to that route without changing its exit question.
func (e *StrategyEngine) addPaperEntryState(state interface{}, questions map[string]provider.JevQuestion, data *market.Data, now time.Time) {
	if !e.config.PaperEntryGuard {
		return
	}
	question, opening := questions["entry_signal"]
	values, ok := state.(map[string]interface{})
	if !opening || !ok {
		return
	}
	eligibility := map[string]string{}
	for _, action := range []string{"open_long", "open_short"} {
		evidence, err := BuildPaperEntryEvidence(data, action, now)
		if err != nil {
			eligibility[action] = err.Error()
		} else {
			eligibility[action] = fmt.Sprintf("minimum confirmation present; structural invalidation=%g; expires=%s; independently justify setup and target", evidence.Invalidation, evidence.ValidUntil.Format(time.RFC3339))
		}
	}
	values["entry_eligibility"] = eligibility
	question.Instructions += " Only consider directions with minimum confirmation present in entry_eligibility. Confirmation is necessary, not sufficient; wait without a genuine setup. Existing-position exits are independent."
	questions["entry_signal"] = question
}

func (e *StrategyEngine) writePaperEntryGuidance(sb *strings.Builder, ctx *Context) {
	if !e.config.PaperEntryGuard {
		return
	}
	sb.WriteString("\n## Paper Entry Evidence Contract\nBoth completed 1h/4h EMA20/EMA50 and price must agree numerically. The latest completed 15m candle must close in the proposed direction relative to both its open and the previous close. This is only a minimum confirmation, not a complete entry thesis. A genuine pullback/retest, plausible target before opposing structure and cost-adjusted reward/risk remain mandatory. Do not invent a target or reinterpret conflicting numbers. Evidence expires at the next 15m close; exits and protective updates are not gated.\n")
	for _, coin := range ctx.CandidateCoins {
		for _, action := range []string{"open_long", "open_short"} {
			data := ctx.MarketDataMap[coin.Symbol]
			now := time.Now().UTC()
			// BuildUserPrompt reflects the same observation used for the model. The
			// broker independently revalidates wall time when the response is executed.
			evidence, err := BuildPaperEntryEvidence(data, action, now)
			if err != nil {
				fmt.Fprintf(sb, "- %s %s: %v.\n", coin.Symbol, action, err)
				continue
			}
			relation := "below"
			if action == "open_short" {
				relation = "above"
			}
			fmt.Fprintf(sb, "- %s %s: minimum confirmation present; structural stop must be strictly %s %.17g; setup expires %s UTC. Still wait if no evidenced target or complete setup.\n", coin.Symbol, action, relation, evidence.Invalidation, evidence.ValidUntil.UTC().Format("15:04:05"))
		}
	}
}
