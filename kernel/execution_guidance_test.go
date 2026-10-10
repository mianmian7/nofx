package kernel

import (
	"strings"
	"testing"

	"nofx/market"
	"nofx/store"
)

func TestExecutionGuidanceIsOptInAndDoesNotChangeBaselinePrompt(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	e := NewStrategyEngineForExchange(&cfg, "binance")
	ctx := &Context{Account: AccountInfo{TotalEquity: 100, AvailableBalance: 80}, CandidateCoins: []CandidateCoin{{Symbol: "BTCUSDT"}}, Positions: []PositionInfo{{Symbol: "ETHUSDT", Side: "long", EntryPrice: 100, MarkPrice: 105, Quantity: 1}}}
	beforeUser := e.BuildUserPrompt(ctx)
	beforeSystem := e.BuildSystemPrompt(100, "balanced", 80)
	ctx.OpeningRestrictions = map[string]string{"BTCUSDT": "trade throttle: wait 20m before re-entry"}
	ctx.Positions[0].FeeInclusiveBreakevenStop = 100.18
	ctx.Positions[0].BreakevenCostSource = "paper ledger actual costs"
	if after := e.BuildUserPrompt(ctx); after != beforeUser {
		t.Fatalf("new advisory fields changed opt-out user prompt:\nbefore=%s\nafter=%s", beforeUser, after)
	}
	// The existing data dictionary iterates maps; compare the stable contract
	// after that dictionary, rather than asserting its incidental line order.
	_, beforeContract, _ := strings.Cut(beforeSystem, "---\n\n")
	_, afterContract, _ := strings.Cut(e.BuildSystemPrompt(100, "balanced", 80), "---\n\n")
	if beforeContract != afterContract || strings.Contains(afterContract, "Execution-Aware Decision Contract") {
		t.Fatal("new advisory fields changed opt-out system contract")
	}
	cfg.ExecutionGuidance = true
	e = NewStrategyEngineForExchange(&cfg, "binance")
	user := e.BuildUserPrompt(ctx)
	for _, wanted := range []string{"new opening blocked", "wait 20m", "BTCUSDT minimum opening notional: 60.00", "100.18", "paper ledger actual costs", "Inside current mark boundary: true"} {
		if !strings.Contains(user, wanted) {
			t.Errorf("guidance missing %q", wanted)
		}
	}
	system := e.BuildSystemPrompt(100, "balanced", 80)
	for _, wanted := range []string{"JSON integers", "ONLY when managing an existing position", "not initial-entry stop requirements", "never loosen", "execution", "at most six bullets"} {
		if !strings.Contains(system, wanted) {
			t.Errorf("system guidance missing %q", wanted)
		}
	}
}

func TestPositionGuidanceLabelsUnavailableAndUnplaceableBoundary(t *testing.T) {
	for _, pos := range []PositionInfo{
		{Side: "long", MarkPrice: 100, FeeInclusiveBreakevenStop: 101},
		{Side: "short", MarkPrice: 100, FeeInclusiveBreakevenStop: 99},
	} {
		var out strings.Builder
		writePositionBreakevenGuidance(&out, pos)
		if !strings.Contains(out.String(), "Inside current mark boundary: false") {
			t.Fatal("unsafe current-price boundary was labeled placeable")
		}
	}
	var out strings.Builder
	writePositionBreakevenGuidance(&out, PositionInfo{})
	if !strings.Contains(out.String(), "unavailable; do not guess") {
		t.Fatal("missing costs turned into invented breakeven")
	}
}

func TestExecutionGuidanceCompactsOnlyIndicatorPresentation(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	e := NewStrategyEngineForExchange(&cfg, "binance")
	series := &market.TimeframeSeriesData{Timeframe: "15m"}
	for i := 1; i <= 30; i++ {
		series.EMA20Values = append(series.EMA20Values, float64(i))
		series.Klines = append(series.Klines, market.KlineBar{Time: int64(i) * 900000, Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 100})
	}
	before := e.formatIndicatorSeries(series.EMA20Values)
	cfg.ExecutionGuidance = true
	e = NewStrategyEngineForExchange(&cfg, "binance")
	after := e.formatIndicatorSeries(series.EMA20Values)
	if len(after) >= len(before) || after != "[25.0000, 26.0000, 27.0000, 28.0000, 29.0000, 30.0000]" {
		t.Fatalf("indicator presentation not compacted correctly: %s", after)
	}
	var out strings.Builder
	e.formatTimeframeSeriesData(&out, series, cfg.Indicators)
	if strings.Count(out.String(), "100.00") != 30 || len(series.Klines) != 30 || len(series.EMA20Values) != 30 || series.EMA20Values[0] != 1 {
		t.Fatal("presentation changed raw candles or full indicator history")
	}
}

func TestParseDecisionBatchDoesNotDropValidOpenBesideFractionalWait(t *testing.T) {
	response := `<decision>[
{"symbol":"BTCUSDT","action":"open_long","leverage":3,"position_size_usd":60,"stop_loss":90,"take_profit":140,"confidence":85,"risk_usd":1},
{"symbol":"HYPEUSDT","action":"wait","confidence":63.4,"reasoning":"no entry setup"}
]</decision>`
	decision, err := parseFullDecisionResponse(response, 100, store.RiskControlConfig{PositionSizingMode: "margin_based", MaxLeverage: 3, MinPositionSize: 12, MinRiskRewardRatio: 2.5})
	if err != nil || decision.Degraded || len(decision.Decisions) != 2 || len(decision.RejectedDecisions) != 0 {
		t.Fatalf("valid open dropped beside decorative wait confidence: %+v, %v", decision, err)
	}
}
