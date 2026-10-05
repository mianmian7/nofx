package kernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nofx/market"
	"nofx/mcp"
	"nofx/mcp/provider"
	"nofx/store"
)

// Exercise the production decision path and final client serialization, not
// only the intermediate feature map. No external network or orders are used.
func captureJevPriceContextRequest(t *testing.T, enhanced, macro bool, ema20, ema50 float64) []byte {
	t.Helper()
	captured := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured <- raw
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"answers":{"market_regime":{"choice":"ranging_chop"},"entry_signal":{"choice":"wait","confidence":0.8}}}`)
	}))
	defer server.Close()
	client := &provider.JevClient{Client: &mcp.Client{Model: "fixture", BaseURL: server.URL, HTTPClient: server.Client()}}
	config := store.StrategyConfig{RiskControl: store.RiskControlConfig{MaxPositions: 2, MaxLeverage: 3, MinConfidence: 75, MinRiskRewardRatio: 2.5}}
	// Unknown fields are ignored by the old implementation: this allows the
	// regression to fail behaviorally before the opt-in field is implemented.
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"jev_price_context":%t,"klines":{"primary_timeframe":"15m"}}`, enhanced)), &config.Indicators); err != nil {
		t.Fatal(err)
	}
	engine := &StrategyEngine{config: &config}
	data := &market.Data{CurrentPrice: 100, CurrentEMA20: 700, PriceChange1h: 1, PriceChange4h: 3, TimeframeData: map[string]*market.TimeframeSeriesData{
		"5m":  {EMA20Values: []float64{900}, EMA50Values: []float64{850}, ATR14: 99},
		"15m": {EMA20Values: []float64{ema20}, EMA50Values: []float64{ema50}, ATR14: 2, RSI14Values: []float64{60}, MACDValues: []float64{2}, BOLLUpper: []float64{120}, BOLLLower: []float64{80}},
	}}
	kCtx := &Context{Account: AccountInfo{TotalEquity: 100, AvailableBalance: 100}, CandidateCoins: []CandidateCoin{{Symbol: "TESTUSDT"}}, MarketDataMap: map[string]*market.Data{"TESTUSDT": data}}
	var err error
	if macro {
		_, err = GetJevDecisionWithMacroContext(context.Background(), kCtx, client, engine, &MacroBias{Bias: "neutral", Confidence: 80})
	} else {
		_, err = GetJevDecisionWithStrategyContext(context.Background(), kCtx, client, engine)
	}
	if err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-captured:
		return raw
	default:
		t.Fatal("decision path sent no Jev request")
		return nil
	}
}

func TestJevPriceContextFinalRequest(t *testing.T) {
	for _, macro := range []bool{false, true} {
		t.Run(fmt.Sprintf("macro_%t", macro), func(t *testing.T) {
			above := captureJevPriceContextRequest(t, true, macro, 90, 85)
			below := captureJevPriceContextRequest(t, true, macro, 110, 105)
			if bytes.Equal(above, below) {
				t.Error("opposite price/EMA relationships produced identical final requests")
			}
			var req struct {
				State     map[string]interface{}          `json:"state"`
				Questions map[string]provider.JevQuestion `json:"questions"`
			}
			if err := json.Unmarshal(above, &req); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]interface{}{"symbol": "TESTUSDT", "current_price": float64(100), "ema_20": float64(90), "ema_50": float64(85), "indicator_timeframe": "15m"} {
				if req.State[key] != want {
					t.Errorf("state[%s]=%v, want %v", key, req.State[key], want)
				}
			}
			if !strings.Contains(req.Questions["market_regime"].Instructions, "ema_20") || !strings.Contains(req.Questions["market_regime"].Instructions, "ema_50") {
				t.Error("regime question does not explicitly use the supplied EMA levels")
			}
			if _, exists := req.State["price_change_4h_pct"]; exists {
				t.Error("EMA candidate must not silently add another independent feature")
			}
		})
	}
}

func TestJevPriceContextMissingEMA(t *testing.T) {
	cases := []struct {
		name   string
		series *market.TimeframeSeriesData
	}{
		{"missing_configured_period", nil},
		{"empty_arrays", &market.TimeframeSeriesData{}},
		{"non_finite_levels", &market.TimeframeSeriesData{EMA20Values: []float64{math.NaN()}, EMA50Values: []float64{math.Inf(1)}}},
		{"non_positive_levels", &market.TimeframeSeriesData{EMA20Values: []float64{0}, EMA50Values: []float64{-1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := &market.Data{CurrentPrice: 100, CurrentEMA20: 700, TimeframeData: map[string]*market.TimeframeSeriesData{
				"5m":  {EMA20Values: []float64{900}, EMA50Values: []float64{850}},
				"15m": tc.series,
			}}
			feat := extractJevFeature("TESTUSDT", data, nil, "15m")
			engine := &StrategyEngine{config: &store.StrategyConfig{Indicators: store.IndicatorConfig{JevPriceContext: true}}}
			state, questions := engine.buildJevStateForStrategy(feat)
			raw, err := json.Marshal(provider.JevRequest{State: state, Questions: questions})
			if err != nil {
				t.Fatalf("unavailable levels must remain serializable: %v", err)
			}
			var wire struct {
				State map[string]interface{} `json:"state"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"ema_20", "ema_50"} {
				value, exists := wire.State[key]
				if !exists || value != nil {
					t.Errorf("%s=%v, want explicit JSON null", key, value)
				}
			}
			if wire.State["indicator_timeframe"] == "5m" {
				t.Error("missing configured period silently fell back to 5m")
			}
			if !strings.Contains(questions["market_regime"].Instructions, "unavailable") {
				t.Error("missing-context instruction absent")
			}
		})
	}
}

func TestJevPriceContextDoesNotChangeRiskOrExitQuestions(t *testing.T) {
	feat := &JevMarketFeature{Symbol: "TESTUSDT", CurrentPrice: 100, EMA20: 90, EMA50: 85, IndicatorTimeframe: "15m", ATR14: 2, ATRPercent: 2, HasPosition: true, PositionSide: "long"}
	_, legacy := BuildJevStateAndQuestions(feat)
	engine := &StrategyEngine{config: &store.StrategyConfig{Indicators: store.IndicatorConfig{JevPriceContext: true}}}
	before := *feat
	_, enhanced := engine.buildJevStateForStrategy(feat)
	if *feat != before {
		t.Error("request enhancement mutated risk features")
	}
	oldQuestion, _ := json.Marshal(legacy["position_action"])
	newQuestion, _ := json.Marshal(enhanced["position_action"])
	if !bytes.Equal(oldQuestion, newQuestion) {
		t.Error("information candidate changed exit policy")
	}
}

func TestJevPriceContextLegacyRequest(t *testing.T) {
	pure := captureJevPriceContextRequest(t, false, false, 90, 85)
	macro := captureJevPriceContextRequest(t, false, true, 90, 85)
	changedLevels := captureJevPriceContextRequest(t, false, false, 110, 105)
	if !bytes.Equal(pure, macro) || !bytes.Equal(pure, changedLevels) {
		t.Error("disabled candidate changed legacy requests")
	}
	// Freeze the actual pre-candidate wire representation, including questions.
	const legacySHA256 = "21c2ab7aa3f99ac462c25281cd0223eccc0e4553e3175adcb032393c1b354fab"
	if got := fmt.Sprintf("%x", sha256.Sum256(pure)); got != legacySHA256 {
		t.Errorf("legacy request fingerprint=%s, want %s", got, legacySHA256)
	}
}
