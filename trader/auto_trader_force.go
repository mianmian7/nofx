package trader

import "nofx/kernel"

// ensureLongShortCoverage remains as a compatibility hook for the trader loop.
// Directional candidate forcing was removed with the remote candidate source.
func (at *AutoTrader) ensureLongShortCoverage(decisions []kernel.Decision, _ *kernel.Context, _ float64) []kernel.Decision {
	return decisions
}
