package trader

import (
	"testing"

	"nofx/kernel"
	"nofx/store"
)

func baseForceTrader() *AutoTrader {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.RiskControl.MaxPositions = 5
	cfg.RiskControl.MaxLeverage = 10
	cfg.RiskControl.AltcoinMaxPositionValueRatio = 10
	at := &AutoTrader{config: AutoTraderConfig{StrategyConfig: &cfg}}
	at.strategyEngine = kernel.NewStrategyEngine(&cfg) // empty ranking cache
	return at
}

func TestEnsureLongShortCoverageSafeModeSkips(t *testing.T) {
	at := baseForceTrader()
	at.safeMode = true
	out := at.ensureLongShortCoverage(nil, &kernel.Context{}, 100)
	if len(out) != 0 {
		t.Fatalf("safe mode must not force opens, got %d", len(out))
	}
}

func TestEnsureLongShortCoveragePreservesDecisions(t *testing.T) {
	at := baseForceTrader()
	in := []kernel.Decision{{Action: "open_long", Symbol: "BTCUSDT"}}
	out := at.ensureLongShortCoverage(in, &kernel.Context{}, 100)
	if len(out) != len(in) || out[0].Symbol != in[0].Symbol {
		t.Fatalf("compatibility hook changed decisions: got %#v", out)
	}
}

func TestEnsureLongShortCoverageBothPresentNoop(t *testing.T) {
	at := baseForceTrader()
	in := []kernel.Decision{
		{Action: "open_long", Symbol: "xyz:AAPL"},
		{Action: "open_short", Symbol: "BTC"},
	}
	out := at.ensureLongShortCoverage(in, &kernel.Context{}, 100)
	if len(out) != 2 {
		t.Fatalf("both directions already present -> no force, got %d", len(out))
	}
}

func TestEnsureLongShortCoverageNoCandidatesNoForce(t *testing.T) {
	at := baseForceTrader() // empty ranking cache -> no directional candidates
	out := at.ensureLongShortCoverage(nil, &kernel.Context{}, 100)
	if len(out) != 0 {
		t.Fatalf("no candidates available -> nothing to force, got %d", len(out))
	}
}
