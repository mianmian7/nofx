package trader

import (
	"nofx/kernel"
	"testing"
)

// filterFixture builds the AutoTrader + Context used by the universe filter
// tests. Candidates are OKX-style USDT pairs plus one Hyperliquid instrument
// ("xyz:QNT") so the base-level fallback path is exercised too.
func filterFixture(exchange string) (*AutoTrader, *kernel.Context) {
	return &AutoTrader{exchange: exchange}, &kernel.Context{
		CandidateCoins: []kernel.CandidateCoin{
			{Symbol: "BTCUSDT"},
			{Symbol: "ETHUSDT"},
			{Symbol: "xyz:QNT"},
		},
	}
}

func keptSymbolActions(decisions []kernel.Decision) map[string]string {
	kept := make(map[string]string, len(decisions))
	for _, d := range decisions {
		kept[d.Symbol] = d.Action
	}
	return kept
}

func TestFilterDecisionsToStrategyUniverseKeepsGlobalWaitSentinels(t *testing.T) {
	// The engine's safe fallback and the AI both signal a global wait with an
	// "ALL" or empty symbol. These must survive the filter untouched (the raw
	// value is checked before exchange normalization, which would otherwise
	// rewrite "ALL" -> "ALLUSDT" and "" -> "USDT" and drop the decision with a
	// warning on every cycle).
	for _, exchange := range []string{"okx", "binance", "hyperliquid"} {
		t.Run(exchange, func(t *testing.T) {
			at, ctx := filterFixture(exchange)
			decisions := []kernel.Decision{
				{Symbol: "ALL", Action: "wait", Reasoning: "model did not emit structured JSON"},
				{Symbol: "", Action: "wait"},
				{Symbol: "ALL", Action: "hold"},
			}

			got := at.filterDecisionsToStrategyUniverse(decisions, ctx)

			if len(got) != len(decisions) {
				t.Fatalf("expected %d wait sentinels to pass through, got %d: %+v", len(decisions), len(got), got)
			}
			for i, d := range got {
				if d.Symbol != decisions[i].Symbol || d.Action != decisions[i].Action {
					t.Errorf("sentinel %d rewritten: got %s %s, want %s %s",
						i, d.Symbol, d.Action, decisions[i].Symbol, decisions[i].Action)
				}
			}
		})
	}
}

func TestFilterDecisionsToStrategyUniverseDropsTradingSentinelsAndForeignSymbols(t *testing.T) {
	at, ctx := filterFixture("okx")

	got := at.filterDecisionsToStrategyUniverse([]kernel.Decision{
		// Sentinel symbol combined with a trading action is malformed AI
		// output and must still be dropped, never executed.
		{Symbol: "ALL", Action: "open_long"},
		{Symbol: "", Action: "open_short"},
		{Symbol: "ALL", Action: "close_long"},
		// Genuinely out-of-universe symbols stay dropped as before.
		{Symbol: "SHIBUSDT", Action: "open_long"},
		{Symbol: "DOGEUSDT", Action: "close_short"},
	}, ctx)

	if len(got) != 0 {
		t.Fatalf("expected all malformed/foreign decisions dropped, got %d: %+v", len(got), got)
	}
}

func TestFilterDecisionsToStrategyUniverseKeepsCandidatesAndCanonicalizesBases(t *testing.T) {
	at, ctx := filterFixture("okx")

	got := at.filterDecisionsToStrategyUniverse([]kernel.Decision{
		{Symbol: "BTCUSDT", Action: "open_long"},
		{Symbol: "ETHUSDT", Action: "open_short"},
		// Base-level fallback: "QNTUSDC" resolves to candidate "xyz:QNT" and
		// is rewritten to the canonical form for downstream order placement.
		{Symbol: "QNTUSDC", Action: "open_long"},
	}, ctx)

	want := map[string]string{
		"BTCUSDT": "open_long",
		"ETHUSDT": "open_short",
		"xyz:QNT": "open_long",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d kept decisions, got %d: %+v", len(want), len(got), got)
	}
	for symbol, action := range keptSymbolActions(got) {
		if wantAction, ok := want[symbol]; !ok || wantAction != action {
			t.Errorf("unexpected kept decision %s %s; want %v", symbol, action, want)
		}
	}
}
