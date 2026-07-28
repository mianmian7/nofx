package trader

import (
	"testing"

	"nofx/kernel"
	"nofx/store"
)

func baseForceTrader() *AutoTrader {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "vergex_signal"
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

func TestEnsureLongShortCoverageNonVergexSkips(t *testing.T) {
	at := baseForceTrader()
	at.config.StrategyConfig.CoinSource.SourceType = "static"
	out := at.ensureLongShortCoverage(nil, &kernel.Context{}, 100)
	if len(out) != 0 {
		t.Fatalf("non-vergex source must not force opens, got %d", len(out))
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

func TestDirectionalCandidatesForSignalModeMirrorsForcedCoverage(t *testing.T) {
	bullish := []kernel.DirectionalCandidate{{Symbol: "BTCUSDT", Score: 1.2}}
	bearish := []kernel.DirectionalCandidate{{Symbol: "ETHUSDT", Score: -0.9}}

	longCandidates, shortCandidates := directionalCandidatesForSignalMode(bullish, bearish, false)
	if longCandidates[0].Symbol != "BTCUSDT" || shortCandidates[0].Symbol != "ETHUSDT" {
		t.Fatalf("normal candidates were unexpectedly swapped: long=%v short=%v", longCandidates, shortCandidates)
	}

	longCandidates, shortCandidates = directionalCandidatesForSignalMode(bullish, bearish, true)
	if longCandidates[0].Symbol != "ETHUSDT" || shortCandidates[0].Symbol != "BTCUSDT" {
		t.Fatalf("inverse candidates were not swapped: long=%v short=%v", longCandidates, shortCandidates)
	}
}
