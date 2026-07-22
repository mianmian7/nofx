package kernel

import (
	"testing"

	"nofx/store"
)

func TestStaticBinanceTradFiCandidatesKeepUSDTContract(t *testing.T) {
	engine := &StrategyEngine{config: &store.StrategyConfig{
		CoinSource: store.CoinSourceConfig{
			SourceType:  "static",
			StaticCoins: []string{"MUUSDT", "SKHYNIXUSDT"},
		},
	}}

	candidates, err := engine.GetCandidateCoins()
	if err != nil {
		t.Fatalf("GetCandidateCoins: %v", err)
	}
	want := []string{"MUUSDT", "SKHYNIXUSDT"}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %+v, want %v", candidates, want)
	}
	for i, symbol := range want {
		if candidates[i].Symbol != symbol {
			t.Fatalf("candidate[%d] = %q, want %q", i, candidates[i].Symbol, symbol)
		}
		if len(candidates[i].Symbol) >= 4 && candidates[i].Symbol[:4] == "xyz:" {
			t.Fatalf("candidate[%d] leaked Hyperliquid prefix: %q", i, candidates[i].Symbol)
		}
	}
}
