package store

import "fmt"

// ValidatePaperEntryGuard rejects an unusable opt-in configuration before a
// trader starts/reloads, rather than silently producing an endless wait loop.
func (c *StrategyConfig) ValidatePaperEntryGuard() error {
	if c == nil || !c.PaperEntryGuard {
		return nil
	}
	if c.StrategyType == "grid_trading" {
		return fmt.Errorf("paper entry guard is only supported for AI trading")
	}
	if !c.Indicators.Klines.ClosedOnly || c.Indicators.Klines.PrimaryTimeframe != "15m" {
		return fmt.Errorf("paper entry guard requires completed candles and a 15m primary timeframe")
	}
	enabled := map[string]bool{}
	for _, tf := range c.Indicators.Klines.SelectedTimeframes {
		enabled[tf] = true
	}
	for _, tf := range []string{"15m", "1h", "4h"} {
		if !enabled[tf] {
			return fmt.Errorf("paper entry guard requires %s data", tf)
		}
	}
	return nil
}
