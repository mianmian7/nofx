package kernel

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func (e *StrategyEngine) writeExecutionSystemGuidance(sb *strings.Builder) {
	if !e.config.ExecutionGuidance {
		return
	}
	sb.WriteString("\n# Execution-Aware Decision Contract\n")
	sb.WriteString("- leverage and confidence must be JSON integers (e.g. 3 and 78, never 3.31 or 78.5). Omit leverage and sizing fields on wait/hold.\n")
	sb.WriteString("- Opening restrictions in the current input are advisory snapshots of the backend's actual throttle. Do not resubmit an opening that is currently blocked. They never block closes or protective updates. The backend will recheck all constraints at execution.\n")
	sb.WriteString("- Fee-inclusive breakeven stops apply ONLY when managing an existing position. They are not initial-entry stop requirements. Use the supplied boundary, not the entry price or a guessed fee rate. Keep a more protective existing stop; never loosen it to this boundary. A long stop must remain below current mark and a short stop above current mark. If the boundary cannot be placed safely now, hold the existing stop rather than fabricate a safe breakeven update.\n")
	sb.WriteString("- Calculate entry reward/risk from the latest current_price and actual stop/target distances, with entry/exit costs. Do not infer an entry price from stop and target, or move a target outside evidenced structure merely to satisfy the ratio. BTC/ETH require at least 60 USDT notional even when the general minimum is lower.\n")
	sb.WriteString("- Completed-candle indicators establish trend and structure; current_price/current mark remain fresh execution prices. Do not substitute the last completed close for the current quote.\n")
	sb.WriteString("- Give a concise evidence summary (at most six bullets) and then the decision JSON. Do not repeat every indicator, debate multiple hypothetical strategies, or optimize today's trade to recover historical losses.\n")
}

func (e *StrategyEngine) writeExecutionUserGuidance(sb *strings.Builder, ctx *Context) {
	if !e.config.ExecutionGuidance {
		return
	}
	sb.WriteString("## Current Execution Guidance\n")
	sb.WriteString("These are snapshots, not order permission; all execution checks remain mandatory.\n")
	blocked := make([]string, 0, len(ctx.OpeningRestrictions))
	for symbol := range ctx.OpeningRestrictions {
		blocked = append(blocked, symbol)
	}
	sort.Strings(blocked)
	for _, symbol := range blocked {
		sb.WriteString(fmt.Sprintf("- %s: new opening blocked: %s\n", symbol, ctx.OpeningRestrictions[symbol]))
	}
	for _, coin := range ctx.CandidateCoins {
		symbol := e.normalizeCandidateSymbol(coin.Symbol)
		sb.WriteString(fmt.Sprintf("- %s minimum opening notional: %.2f USDT.\n", symbol, effectiveMinPositionSize(symbol, e.config.RiskControl)))
	}
	sb.WriteString("\n")
}

// Presentation only: keep the indicator calculation history and raw candles
// intact while avoiding long redundant EMA/RSI arrays in the model input.
func (e *StrategyEngine) formatIndicatorSeries(values []float64) string {
	if e.config.ExecutionGuidance && len(values) > 6 {
		values = values[len(values)-6:]
	}
	if e.config.ExecutionGuidance {
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = e.formatPromptPrice(value, 4)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return formatFloatSlice(values)
}

// Small positive contracts and ATRs must not disappear as 0.0000. Opt-out
// strategies retain their presentation, including independent controls.
func (e *StrategyEngine) formatPromptPrice(value float64, decimals int) string {
	if e.config.ExecutionGuidance && value != 0 && math.Abs(value) < 0.1 {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

func writePositionBreakevenGuidance(sb *strings.Builder, pos PositionInfo) {
	if pos.FeeInclusiveBreakevenStop <= 0 {
		sb.WriteString("   Fee-inclusive breakeven stop: unavailable; do not guess its price.\n")
		return
	}
	placeable := pos.Side == "long" && pos.FeeInclusiveBreakevenStop < pos.MarkPrice || pos.Side == "short" && pos.FeeInclusiveBreakevenStop > pos.MarkPrice
	sb.WriteString(fmt.Sprintf("   Fee-inclusive breakeven stop boundary: %.17g | Source: %s | Inside current mark boundary: %t. Preserve any more protective existing stop; execution revalidates costs, mark and tick.\n", pos.FeeInclusiveBreakevenStop, pos.BreakevenCostSource, placeable))
}
