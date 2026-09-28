package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/store"
)

// Closing and protective actions never depend on the analysis service. In
// particular, a held position is not an exemption for a new opening request.
func (at *AutoTrader) validateLocalSignalOpening(d *kernel.Decision) error {
	if d.Action != "open_long" && d.Action != "open_short" {
		return nil
	}
	if at.strategyEngine == nil {
		if at.config.StrategyConfig != nil && at.config.StrategyConfig.CoinSource.SourceType == "local_signals" {
			return fmt.Errorf("local analysis strategy engine is unavailable")
		}
		return nil
	}
	return at.strategyEngine.ValidateLocalSignalOpen(d.Symbol, d.Action)
}

func localSignalEntryValidator(config *store.StrategyConfig, exchange string) func(string, string) error {
	if config == nil || config.CoinSource.SourceType != "local_signals" {
		return nil
	}
	copyConfig := *config
	copyConfig.CoinSource.ExcludedCoins = append([]string(nil), config.CoinSource.ExcludedCoins...)
	engine := kernel.NewStrategyEngineForExchange(&copyConfig, exchange)
	return engine.ValidateLocalSignalOpen
}

func (b *PaperBroker) setLocalSignalEntryValidator(config *store.StrategyConfig, exchange string) {
	validator := localSignalEntryValidator(config, exchange)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config.EntryValidator = validator
}
