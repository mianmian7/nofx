package trader

import (
	"math"
	"strings"
	"testing"

	"nofx/kernel"
	"nofx/store"
)

func TestPaperExecutionGuidanceReusesValidatedBreakevenBoundary(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		t.Run(side, func(t *testing.T) {
			current, oldStop, target := 105.0, 90.0, 110.0
			if side == "short" {
				current, oldStop, target = 95, 110, 90
			}
			position := managedPosition{symbol: "BTCUSDT", side: side, entry: 100, current: current, quantity: 2, priceTick: 0.01,
				stopLoss: oldStop, takeProfit: target, stopLossState: ProtectionPresent, takeProfitState: ProtectionPresent,
				breakevenCostsKnown: true, breakevenCosts: protectionBreakevenCosts{EntryFeeQuote: 0.04, ExitFeeRate: 0.0005, FundingCostQuote: 0.10, SlippageRate: 0.0005, ProfitBufferRate: 0.0001, Source: "paper actual costs"}}
			at := &AutoTrader{executionMode: ExecutionModePaper, config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{ExecutionGuidance: true}}}
			ctx := &kernel.Context{Positions: []kernel.PositionInfo{{Symbol: "BTCUSDT", Side: side, EntryPrice: 100, MarkPrice: current, Quantity: 2}}}
			at.addExecutionGuidance(ctx, []managedPosition{position})
			boundary, err := feeInclusiveBreakevenBoundary(position)
			if err != nil {
				t.Fatal(err)
			}
			want := math.Ceil(boundary/0.01-1e-9) * 0.01
			if side == "short" {
				want = math.Floor(boundary/0.01+1e-9) * 0.01
			}
			// Independent hand-calculated expectations for these quote costs.
			handCalculated := 100.19
			if side == "short" {
				handCalculated = 99.82
			}
			if math.Abs(want-handCalculated) > 1e-9 {
				t.Fatalf("boundary %.8f differs from independent expectation %.8f", want, handCalculated)
			}
			if ctx.Positions[0].FeeInclusiveBreakevenStop != want || ctx.Positions[0].BreakevenCostSource != "paper actual costs" {
				t.Fatalf("guidance differs from execution calculation: %+v, want %.8f", ctx.Positions[0], want)
			}
			decision := &kernel.Decision{NewStopLoss: ctx.Positions[0].FeeInclusiveBreakevenStop}
			if err := validateProtectionUpdate(position, decision); err != nil {
				t.Fatalf("execution rejects supplied advisory boundary: %v", err)
			}
		})
	}
}

func TestExecutionGuidanceDoesNotRunOnLiveOrOptOut(t *testing.T) {
	for _, test := range []struct {
		mode    ExecutionMode
		enabled bool
	}{{ExecutionModeLive, true}, {ExecutionModePaper, false}} {
		at := &AutoTrader{executionMode: test.mode, config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{ExecutionGuidance: test.enabled}}}
		ctx := &kernel.Context{Positions: []kernel.PositionInfo{{Symbol: "BTCUSDT", Side: "long"}}}
		at.addExecutionGuidance(ctx, []managedPosition{{symbol: "BTCUSDT", side: "long", entry: 100, quantity: 1}})
		if ctx.OpeningRestrictions != nil || ctx.Positions[0].BreakevenCostSource != "" {
			t.Fatal("guidance leaked into a live or opt-out strategy")
		}
	}
}

func TestPaperExecutionGuidanceReportsHeldSymbolAndMissingCosts(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100}, fixedPaperPriceSource{"BTCUSDT": 100})
	if err != nil {
		t.Fatal(err)
	}
	at := &AutoTrader{exchange: "binance", executionMode: ExecutionModePaper, paperBroker: broker, config: AutoTraderConfig{StrategyConfig: &store.StrategyConfig{ExecutionGuidance: true}}}
	ctx := &kernel.Context{Positions: []kernel.PositionInfo{{Symbol: "BTCUSDT", Side: "long"}}, CandidateCoins: []kernel.CandidateCoin{{Symbol: "BTCUSDT"}}}
	at.addExecutionGuidance(ctx, []managedPosition{{symbol: "BTCUSDT", side: "long", entry: 100, quantity: 1}})
	if !strings.Contains(ctx.OpeningRestrictions["BTCUSDT"], "already has an open long position") || ctx.Positions[0].FeeInclusiveBreakevenStop != 0 || !strings.Contains(ctx.Positions[0].BreakevenCostSource, "unavailable") {
		t.Fatalf("incorrect constraint/cost guidance: %+v", ctx)
	}
}
