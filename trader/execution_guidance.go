package trader

import (
	"math"
	"nofx/kernel"
	"strings"
)

// addExecutionGuidance exposes the same costs and throttle reasons used by
// execution. It is advisory, opt-in and Paper-only; execution always rechecks.
func (at *AutoTrader) addExecutionGuidance(ctx *kernel.Context, positions []managedPosition) {
	if ctx == nil || at.executionMode != ExecutionModePaper || at.config.StrategyConfig == nil || !at.config.StrategyConfig.ExecutionGuidance {
		return
	}
	for i := range ctx.Positions {
		info := &ctx.Positions[i]
		for _, position := range positions {
			if position.symbol != info.Symbol || !strings.EqualFold(position.side, info.Side) {
				continue
			}
			boundary, err := feeInclusiveBreakevenBoundary(position)
			if err != nil {
				info.BreakevenCostSource = "unavailable; execution must obtain valid costs"
				break
			}
			if position.priceTick > 0 {
				if strings.EqualFold(position.side, "long") {
					boundary = math.Ceil(boundary/position.priceTick-1e-9) * position.priceTick
				} else {
					boundary = math.Floor(boundary/position.priceTick+1e-9) * position.priceTick
				}
			}
			if boundary > 0 && !math.IsNaN(boundary) && !math.IsInf(boundary, 0) {
				info.FeeInclusiveBreakevenStop = boundary
				info.BreakevenCostSource = position.breakevenCosts.Source
			}
			break
		}
	}
	ctx.OpeningRestrictions = make(map[string]string)
	for _, coin := range ctx.CandidateCoins {
		reason := at.tradeThrottleReason(kernel.Decision{Symbol: coin.Symbol, Action: "open_long"}, ctx, 0)
		if reason != "" {
			ctx.OpeningRestrictions[coin.Symbol] = reason
		}
	}
}
