package trader

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
)

// Minimum stop distance for NEW entries (paper and live):
//
//	minDist = max(NOFX_MIN_STOP_ATR_MULT * ATR14(15m), NOFX_MIN_STOP_PCT% * entry)
//
// A closer AI stop is widened (never rejected), the take-profit is kept, and
// the position size is scaled down so the USDT loss at the stop equals the
// originally planned loss. Risk/reward gates keep using the AI's original stop
// (Decision.RiskRewardStopLoss). Set both env values to 0 to disable.
const (
	defaultMinStopATRMult = 1.2
	defaultMinStopPct     = 0.5
	minStopATRTimeframe   = "15m"
	minStopATRPeriod      = 14
	minStopKlineLimit     = 60
)

type minStopConfig struct {
	ATRMult float64
	Pct     float64 // percent of entry price, e.g. 0.5 == 0.5%
}

func (c minStopConfig) enabled() bool { return c.ATRMult > 0 || c.Pct > 0 }

func envNonNegativeFloat(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		logger.Warnf("⚠️ invalid %s=%q, using default %.4g", key, raw, def)
		return def
	}
	return v
}

func loadMinStopConfig() minStopConfig {
	return minStopConfig{
		ATRMult: envNonNegativeFloat("NOFX_MIN_STOP_ATR_MULT", defaultMinStopATRMult),
		Pct:     envNonNegativeFloat("NOFX_MIN_STOP_PCT", defaultMinStopPct),
	}
}

// minStopDistance returns the required absolute price distance.
func (c minStopConfig) minStopDistance(entry, atr float64) float64 {
	d := 0.0
	if c.ATRMult > 0 && atr > 0 && !math.IsNaN(atr) && !math.IsInf(atr, 0) {
		d = c.ATRMult * atr
	}
	if c.Pct > 0 {
		d = math.Max(d, entry*c.Pct/100)
	}
	return d
}

type minStopResult struct {
	Widened      bool
	OriginalStop float64
	NewStop      float64
	OriginalSize float64
	NewSize      float64
	MinDistance  float64
	ATR          float64
}

// widenStopToMinimum is the pure core: it mutates decision in place.
// marginLimitPct is the hard Margin/Position PnL stop (negative, e.g. -20);
// a widened stop never goes beyond that hard boundary.
func widenStopToMinimum(decision *kernel.Decision, entry, atr float64, cfg minStopConfig, marginLimitPct float64) (minStopResult, error) {
	res := minStopResult{}
	if decision == nil || (decision.Action != "open_long" && decision.Action != "open_short") || !cfg.enabled() {
		return res, nil
	}
	if !isFinitePositive(entry) || !isFinitePositive(decision.StopLoss) {
		return res, nil // validateOpenProtection reports malformed input
	}
	cur := math.Abs(entry - decision.StopLoss)
	minDist := cfg.minStopDistance(entry, atr)
	res.ATR, res.MinDistance = atr, minDist
	if cur <= 0 || minDist <= 0 || cur+1e-12*entry >= minDist {
		return res, nil
	}
	newStop := entry - minDist
	if decision.Action == "open_short" {
		newStop = entry + minDist
	}
	if decision.Leverage > 0 {
		clamped, _, err := clampStopLossToMarginRisk(decision.Action, entry, decision.Leverage, newStop, marginLimitPct)
		if err != nil {
			return res, err
		}
		newStop = clamped
	}
	if !isFinitePositive(newStop) {
		return res, fmt.Errorf("min stop distance produced invalid stop %.8f for %s", newStop, decision.Symbol)
	}
	newDist := math.Abs(entry - newStop)
	if newDist <= cur {
		return res, nil
	}
	if decision.RiskRewardStopLoss <= 0 {
		decision.RiskRewardStopLoss = decision.StopLoss
	}
	res.Widened = true
	res.OriginalStop, res.NewStop = decision.StopLoss, newStop
	res.OriginalSize = decision.PositionSizeUSD
	res.NewSize = decision.PositionSizeUSD * cur / newDist
	decision.StopLoss = newStop
	decision.PositionSizeUSD = res.NewSize
	return res, nil
}

func (at *AutoTrader) minStopATR(symbol string) (atr float64, err error) {
	// A market-data failure (even a provider panic) must only degrade the
	// floor to the percentage rule, never abort the entry pipeline.
	defer func() {
		if r := recover(); r != nil {
			atr, err = 0, fmt.Errorf("ATR provider panic: %v", r)
		}
	}()
	provider := at.marketDataProvider
	if provider == nil {
		provider, err = market.NewMarketDataProvider(at.exchange)
		if err != nil {
			return 0, err
		}
	}
	klines, err := provider.GetKlines(symbol, minStopATRTimeframe, minStopKlineLimit)
	if err != nil {
		return 0, err
	}
	atr = market.ExportCalculateATR(klines, minStopATRPeriod)
	if !isFinitePositive(atr) {
		return 0, fmt.Errorf("insufficient %s klines for ATR%d (%d)", minStopATRTimeframe, minStopATRPeriod, len(klines))
	}
	return atr, nil
}

// applyMinStopDistance enforces the minimum stop distance on a new entry.
// ATR unavailability degrades to the percentage floor instead of blocking.
func (at *AutoTrader) applyMinStopDistance(decision *kernel.Decision, entry float64) error {
	cfg := loadMinStopConfig()
	if decision == nil || (decision.Action != "open_long" && decision.Action != "open_short") || !cfg.enabled() {
		return nil
	}
	atr := 0.0
	if cfg.ATRMult > 0 {
		v, err := at.minStopATR(decision.Symbol)
		if err != nil {
			at.logWarnf("⚠️ [MIN STOP] %s ATR(%s,%d) unavailable, using %.2f%% floor only: %v", decision.Symbol, minStopATRTimeframe, minStopATRPeriod, cfg.Pct, err)
		} else {
			atr = v
		}
	}
	res, err := widenStopToMinimum(decision, entry, atr, cfg, unifiedMarginStopLossPct)
	if err != nil {
		return err
	}
	if res.Widened {
		at.logInfof("🛡️ [MIN STOP] %s %s widened stop %.8f -> %.8f (entry %.8f, min dist %.8f = max(%.2f×ATR %.8f, %.2f%%)); TP kept %.8f; size %.2f -> %.2f USDT (risk at stop preserved); R:R gate uses original stop",
			decision.Symbol, decision.Action, res.OriginalStop, res.NewStop, entry, res.MinDistance, cfg.ATRMult, res.ATR, cfg.Pct, decision.TakeProfit, res.OriginalSize, res.NewSize)
	}
	return nil
}

// riskRewardStop returns the stop used for risk/reward gates.
func riskRewardStop(original, effective float64) float64 {
	if original > 0 {
		return original
	}
	return effective
}
