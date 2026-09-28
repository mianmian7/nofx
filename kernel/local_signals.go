package kernel

import (
	"fmt"
	"nofx/signals"
	"nofx/store"
)

func (e *StrategyEngine) UsesLocalSignals() bool {
	return e != nil && e.config != nil && e.config.CoinSource.SourceType == "local_signals"
}

func (e *StrategyEngine) localSignalCandidates() ([]CandidateCoin, error) {
	if err := e.config.ValidateLocalSignals(); err != nil {
		return nil, err
	}
	source := e.config.CoinSource
	results, err := signals.Default().Candidates(e.Exchange(), source.LocalSignalMinScore, source.LocalSignalLimit)
	if err != nil {
		return nil, err
	}
	out := make([]CandidateCoin, 0, len(results))
	for _, row := range results {
		result := row
		out = append(out, CandidateCoin{Symbol: row.Symbol, Sources: []string{"local_signals"}, LocalSignal: &result})
	}
	return out, nil
}

func (e *StrategyEngine) ValidateLocalSignalOpen(symbol, action string) error {
	if !e.UsesLocalSignals() {
		return nil
	}
	if err := e.config.ValidateLocalSignals(); err != nil {
		return err
	}
	for _, excluded := range e.config.CoinSource.ExcludedCoins {
		if e.normalizeCandidateSymbol(excluded) == symbol {
			return fmt.Errorf("%s is excluded from this strategy", symbol)
		}
	}
	source := e.config.CoinSource
	return signals.Default().ValidateOpen(e.Exchange(), symbol, action, source.LocalSignalMinScore, source.LocalSignalLimit)
}

func formatLocalSignal(signal *store.LocalSignal) string {
	if signal == nil {
		return ""
	}
	return fmt.Sprintf("Local public-market analysis (%s, venue=%s, observed_ms=%d, closed_ms=%d): direction=%s; rule-agreement score=%.1f/100 (NOT win probability); confirmed=%t. Only open in this direction; independent position sizing and protective SL/TP remain mandatory.\n", signal.Version, signal.Exchange, signal.ObservedAt, signal.ClosedAt, signal.Direction, signal.Score, signal.Confirmed)
}
