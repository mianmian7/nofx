package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/store"
	"time"
)

func paperEntryGuardEnabled(config *store.StrategyConfig) bool {
	return config != nil && config.StrategyType != "grid_trading" && config.PaperEntryGuard
}

func (at *AutoTrader) attachPaperEntryEvidence(decision *kernel.Decision, ctx *kernel.Context, now time.Time) error {
	if at.executionMode != ExecutionModePaper || !paperEntryGuardEnabled(at.config.StrategyConfig) || !isOpenAction(decision.Action) {
		return nil
	}
	decision.PaperEntryEvidence = nil
	if ctx == nil {
		return fmt.Errorf("entry guard: trading context is unavailable")
	}
	evidence, err := kernel.BuildPaperEntryEvidence(ctx.MarketDataMap[decision.Symbol], decision.Action, now)
	if err != nil {
		return err
	}
	if err = evidence.Validate(decision.Symbol, decision.Action, ctx.MarketDataMap[decision.Symbol].CurrentPrice, decision.StopLoss, now); err != nil {
		return err
	}
	decision.PaperEntryEvidence = evidence
	return nil
}

func (b *PaperBroker) validateEntryEvidenceLocked(evidence *kernel.PaperEntryEvidence, symbol, action string, price, stop float64, now time.Time) error {
	if !b.config.RequireEntryEvidence {
		return nil
	}
	return evidence.Validate(symbol, action, price, stop, now)
}

func copyEntryEvidence(e *kernel.PaperEntryEvidence) *kernel.PaperEntryEvidence {
	if e == nil {
		return nil
	}
	clone := *e
	return &clone
}
