package store

import (
	"fmt"
	"math"
)

func (c *StrategyConfig) ValidateLocalSignals() error {
	if c == nil || c.CoinSource.SourceType != "local_signals" {
		return nil
	}
	if c.StrategyType != "ai_trading" && c.StrategyType != "" {
		return fmt.Errorf("local analysis requires an AI trading strategy")
	}
	s := c.CoinSource
	if math.IsNaN(s.LocalSignalMinScore) || math.IsInf(s.LocalSignalMinScore, 0) || s.LocalSignalMinScore < 35 || s.LocalSignalMinScore > 95 {
		return fmt.Errorf("local analysis minimum score must be between 35 and 95")
	}
	if s.LocalSignalLimit < 1 || s.LocalSignalLimit > MaxCandidateCoins {
		return fmt.Errorf("local analysis candidate count must be between 1 and %d", MaxCandidateCoins)
	}
	return nil
}
