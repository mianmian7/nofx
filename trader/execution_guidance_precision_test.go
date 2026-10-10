package trader

import (
	"math"
	"regexp"
	"strconv"
	"testing"

	"nofx/kernel"
	"nofx/store"
)

// Exercise the full advisory serialization -> model echo -> execution path,
// not only the unrounded in-memory boundary.
func TestBreakevenPromptRoundTripPassesUnchangedCosts(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, entry := range []float64{180.31, 85957, 0.00000813} {
			t.Run(side+strconv.FormatFloat(entry, 'g', -1, 64), func(t *testing.T) {
				current, stop := entry*1.05, entry*.95
				if side == "short" {
					current, stop = entry*.95, entry*1.05
				}
				position := managedPosition{symbol: "TESTUSDT", side: side, entry: entry, current: current, quantity: 2, stopLoss: stop, stopLossState: ProtectionPresent, breakevenCostsKnown: true, breakevenCosts: protectionBreakevenCosts{EntryFeeQuote: entry * 2 * .0002, ExitFeeRate: .0004, SlippageRate: .0002, ProfitBufferRate: .0001, Source: "paper actual costs"}}
				cfg := store.GetDefaultStrategyConfig("en")
				cfg.ExecutionGuidance = true
				at := &AutoTrader{executionMode: ExecutionModePaper, config: AutoTraderConfig{StrategyConfig: &cfg}}
				ctx := &kernel.Context{Positions: []kernel.PositionInfo{{Symbol: "TESTUSDT", Side: side, EntryPrice: entry, MarkPrice: current, Quantity: 2}}}
				at.addExecutionGuidance(ctx, []managedPosition{position})
				prompt := kernel.NewStrategyEngine(&cfg).BuildUserPrompt(ctx)
				m := regexp.MustCompile(`Fee-inclusive breakeven stop boundary: ([^ ]+)`).FindStringSubmatch(prompt)
				if len(m) != 2 {
					t.Fatalf("boundary missing: %s", prompt)
				}
				echoed, err := strconv.ParseFloat(m[1], 64)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := feeInclusiveBreakevenBoundary(position)
				if err != nil {
					t.Fatal(err)
				}
				if math.Float64bits(echoed) != math.Float64bits(ctx.Positions[0].FeeInclusiveBreakevenStop) {
					t.Errorf("serialization moved boundary: %.17g -> %.17g (actual %.17g)", ctx.Positions[0].FeeInclusiveBreakevenStop, echoed, actual)
				}
				if err := validateProtectionUpdate(position, &kernel.Decision{NewStopLoss: echoed}); err != nil {
					t.Errorf("echoed prompt rejected: %v", err)
				}
			})
		}
	}
}
