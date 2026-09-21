package kernel

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"nofx/store"
)

func testRiskConfig() store.RiskControlConfig {
	return store.RiskControlConfig{
		PositionSizingMode:           "margin_based",
		MaxLeverage:                  8,
		MinPositionSize:              12,
		MinRiskRewardRatio:           1,
		BTCETHMaxPositionValueRatio:  1,
		AltcoinMaxPositionValueRatio: 1,
	}
}

func TestEffectiveMinPositionSizeMatchesPromptAndValidator(t *testing.T) {
	risk := testRiskConfig()
	if got := effectiveMinPositionSize("SOLUSDT", risk); got != 12 {
		t.Fatalf("SOL minimum = %.0f, want 12", got)
	}
	if got := effectiveMinPositionSize("BTCUSDT", risk); got != 60 {
		t.Fatalf("BTC minimum = %.0f, want 60", got)
	}
	risk.MinPositionSize = 75
	if got := effectiveMinPositionSize("ETHUSDT", risk); got != 75 {
		t.Fatalf("ETH configured minimum = %.0f, want 75", got)
	}
}

func TestBuildSystemPromptDisplaysEffectiveMinimums(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.RiskControl.MinPositionSize = 12
	cfg.CoinSource.SourceType = "dynamic"
	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(100, "balanced", 100)
	for _, want := range []string{
		"Minimum opening notional (Altcoin/Stock): ≥12 USDT",
		"Minimum opening notional (BTC/ETH): ≥60 USDT",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}

	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}
	prompt = NewStrategyEngine(&cfg).BuildSystemPrompt(100, "balanced", 5)
	if !strings.Contains(prompt, "Minimum opening notional (BTCUSDT): ≥60 USDT") {
		t.Fatalf("single BTC prompt missing effective minimum: %s", prompt)
	}
	if !strings.Contains(prompt, "output wait; never increase the order") {
		t.Fatal("small-account prompt must instruct wait instead of inflating the order")
	}
}

func TestDecisionUnmarshalAcceptsIntegralFloatAndRejectsFractionalLeverage(t *testing.T) {
	var decision Decision
	if err := json.Unmarshal([]byte(`{"symbol":"BTCUSDT","action":"open_long","leverage":3.0,"reasoning":"ok"}`), &decision); err != nil {
		t.Fatalf("3.0 should be accepted: %v", err)
	}
	if decision.Leverage != 3 {
		t.Fatalf("leverage = %d, want 3", decision.Leverage)
	}
	if err := json.Unmarshal([]byte(`{"symbol":"BTCUSDT","action":"open_long","leverage":1.5,"reasoning":"bad"}`), &decision); err == nil || !strings.Contains(err.Error(), "must be an integer") {
		t.Fatalf("fractional leverage error = %v", err)
	}
}

func TestDecisionUnmarshalLeveragePreservesExactNumericValue(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"3", 3}, {"3.0", 3}, {"3e0", 3}, {"30e-1", 3}, {"0.03e2", 3},
		{strconv.Itoa(maxInt), maxInt}, {strconv.Itoa(-maxInt - 1), -maxInt - 1},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var decision Decision
			if err := json.Unmarshal([]byte(`{"leverage":`+tc.raw+`}`), &decision); err != nil || decision.Leverage != tc.want {
				t.Fatalf("leverage %s: value=%d err=%v, want %d", tc.raw, decision.Leverage, err, tc.want)
			}
		})
	}
	for _, raw := range []string{"3.0000000000000001", "2.9999999999999999", "30000000000000001e-16", "1e-400", "1.5", "3e-1", strconv.Itoa(maxInt) + "0", `"3"`} {
		t.Run("reject_"+raw, func(t *testing.T) {
			var decision Decision
			if err := json.Unmarshal([]byte(`{"leverage":`+raw+`}`), &decision); err == nil {
				t.Fatalf("leverage %s was silently accepted as %d", raw, decision.Leverage)
			}
		})
	}
}

func TestPromptMinimumPreservesFractionalConfiguredAmount(t *testing.T) {
	for _, tc := range []struct {
		name, symbol, want string
		minimum            float64
	}{
		{"dynamic altcoin", "", "Minimum opening notional (Altcoin/Stock): ≥12.4 USDT", 12.4},
		{"dynamic major", "", "Minimum opening notional (BTC/ETH): ≥60.125 USDT", 60.125},
		{"single major", "BTCUSDT", "Minimum opening notional (BTCUSDT): ≥60.125 USDT", 60.125},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := store.GetDefaultStrategyConfig("en")
			cfg.RiskControl.MinPositionSize = tc.minimum
			cfg.CoinSource.SourceType = "dynamic"
			if tc.symbol != "" {
				cfg.CoinSource.SourceType = "static"
				cfg.CoinSource.StaticCoins = []string{tc.symbol}
			}
			prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(100, "balanced", 100)
			if !strings.Contains(prompt, tc.want) {
				t.Fatalf("prompt does not preserve the validator minimum: missing %q", tc.want)
			}
		})
	}
}

func TestParseDecisionBatchKeepsValidatedCloseWhenOpeningIsInvalid(t *testing.T) {
	raw := `[
		{"symbol":"SOLUSDT","action":"close_long","close_ratio":0.5,"reasoning":"reduce risk"},
		{"symbol":"BTCUSDT","action":"open_long","leverage":3,"position_size_usd":12,"stop_loss":90,"take_profit":120,"reasoning":"too small"}
	]`
	decision, err := parseFullDecisionResponse(raw, 100, testRiskConfig())
	if err != nil {
		t.Fatalf("validated close should survive degraded batch: %v", err)
	}
	if !decision.Degraded || len(decision.Decisions) != 1 || decision.Decisions[0].Action != "close_long" {
		t.Fatalf("degraded decision = %+v", decision)
	}
	if len(decision.RejectedDecisions) != 1 || decision.RejectedDecisions[0].Index != 1 {
		t.Fatalf("rejected decisions = %+v", decision.RejectedDecisions)
	}
}

func TestParseDecisionBatchKeepsCloseWhenSiblingHasFractionalLeverage(t *testing.T) {
	raw := `[
		{"symbol":"SOLUSDT","action":"close_long","close_ratio":0.5,"reasoning":"reduce risk"},
		{"symbol":"BTCUSDT","action":"open_long","leverage":1.5,"position_size_usd":60,"stop_loss":90,"take_profit":120,"reasoning":"invalid leverage"}
	]`
	decision, err := parseFullDecisionResponse(raw, 100, testRiskConfig())
	if err != nil {
		t.Fatalf("validated close should survive sibling parse failure: %v", err)
	}
	if !decision.Degraded || len(decision.Decisions) != 1 || decision.Decisions[0].Action != "close_long" {
		t.Fatalf("degraded decision = %+v", decision)
	}
	if len(decision.RejectedDecisions) != 1 || decision.RejectedDecisions[0].Index != 1 || !strings.Contains(decision.RejectedDecisions[0].Reason, "must be an integer") {
		t.Fatalf("rejected decisions = %+v", decision.RejectedDecisions)
	}
}

func TestParseDecisionBatchRejectsAllRiskIncreaseWhenSiblingInvalid(t *testing.T) {
	raw := `[
		{"symbol":"SOLUSDT","action":"open_long","leverage":3,"position_size_usd":20,"stop_loss":90,"take_profit":120,"reasoning":"valid alone"},
		{"symbol":"BTCUSDT","action":"open_long","leverage":3,"position_size_usd":12,"stop_loss":90,"take_profit":120,"reasoning":"too small"}
	]`
	decision, err := parseFullDecisionResponse(raw, 100, testRiskConfig())
	if err == nil {
		t.Fatal("batch with no safe close must fail")
	}
	if len(decision.Decisions) != 0 || len(decision.RejectedDecisions) != 2 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestMalformedJSONNeverProducesExecutableDecision(t *testing.T) {
	decision, err := parseFullDecisionResponse(`[{"symbol":"SOLUSDT","action":"close_long" <broken>}]`, 100, testRiskConfig())
	if err == nil {
		t.Fatal("malformed JSON must fail")
	}
	if decision == nil || len(decision.Decisions) != 0 {
		t.Fatalf("malformed JSON produced actions: %+v", decision)
	}
}
