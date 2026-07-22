package backtest

import (
	"context"
	"fmt"
	"math"
	"nofx/market"
)

// TrendConfig defines a deterministic Donchian-breakout strategy. It exists
// for reproducible research and as a benchmark for AI replays; it does not
// imply future profitability.
type TrendConfig struct {
	FastEMAPeriod   int     `json:"fast_ema_period"`
	SlowEMAPeriod   int     `json:"slow_ema_period"`
	BreakoutPeriod  int     `json:"breakout_period"`
	ATRPeriod       int     `json:"atr_period"`
	ATRStopMultiple float64 `json:"atr_stop_multiple"`
	RewardRiskRatio float64 `json:"reward_risk_ratio"`
	RiskPerTradePct float64 `json:"risk_per_trade_pct"`
	MaxExposure     float64 `json:"max_exposure"`
	Leverage        int     `json:"leverage"`
	AllowShort      bool    `json:"allow_short"`
}

func DefaultTrendConfig() TrendConfig {
	return TrendConfig{
		FastEMAPeriod:   50,
		SlowEMAPeriod:   200,
		BreakoutPeriod:  100,
		ATRPeriod:       20,
		ATRStopMultiple: 3,
		RewardRiskRatio: 4,
		RiskPerTradePct: 1,
		MaxExposure:     1,
		Leverage:        1,
		AllowShort:      true,
	}
}

type TrendProvider struct {
	config TrendConfig
}

func NewTrendProvider(config TrendConfig) (*TrendProvider, error) {
	if config.FastEMAPeriod <= 1 || config.SlowEMAPeriod <= config.FastEMAPeriod {
		return nil, fmt.Errorf("slow EMA must be greater than fast EMA")
	}
	if config.BreakoutPeriod < 2 || config.ATRPeriod < 2 {
		return nil, fmt.Errorf("breakout and ATR periods must be at least 2")
	}
	if config.ATRStopMultiple <= 0 || config.RewardRiskRatio <= 0 {
		return nil, fmt.Errorf("stop multiple and reward/risk ratio must be positive")
	}
	if config.RiskPerTradePct <= 0 || config.RiskPerTradePct > 5 {
		return nil, fmt.Errorf("risk per trade must be in (0, 5]")
	}
	if config.MaxExposure <= 0 || config.MaxExposure > 3 {
		return nil, fmt.Errorf("max exposure must be in (0, 3]")
	}
	if config.Leverage <= 0 || config.Leverage > 3 {
		return nil, fmt.Errorf("trend benchmark leverage must be between 1 and 3")
	}
	return &TrendProvider{config: config}, nil
}

func (p *TrendProvider) Decide(_ context.Context, snapshot Snapshot) (Decision, error) {
	candles := snapshot.Candles
	minimum := maxInt(p.config.SlowEMAPeriod, p.config.BreakoutPeriod+1, p.config.ATRPeriod+1)
	if len(candles) < minimum {
		return Decision{Action: "hold", Reasoning: "warming up deterministic trend indicators"}, nil
	}

	closes := make([]float64, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}
	fast := ema(closes, p.config.FastEMAPeriod)
	slow := ema(closes, p.config.SlowEMAPeriod)
	current := candles[len(candles)-1].Close
	atrValue := atr(candles, p.config.ATRPeriod)
	if current <= 0 || atrValue <= 0 {
		return Decision{Action: "hold", Reasoning: "invalid price or ATR"}, nil
	}

	if snapshot.Position != nil {
		switch snapshot.Position.Side {
		case "long":
			if current < slow || fast < slow {
				return Decision{Action: "close_long", Reasoning: "close fell below the slow EMA trend filter"}, nil
			}
		case "short":
			if current > slow || fast > slow {
				return Decision{Action: "close_short", Reasoning: "close rose above the slow EMA trend filter"}, nil
			}
		}
		return Decision{Action: "hold", Reasoning: "existing trend remains intact"}, nil
	}

	start := len(candles) - 1 - p.config.BreakoutPeriod
	highest := candles[start].High
	lowest := candles[start].Low
	for i := start + 1; i < len(candles)-1; i++ {
		highest = math.Max(highest, candles[i].High)
		lowest = math.Min(lowest, candles[i].Low)
	}
	stopDistance := atrValue * p.config.ATRStopMultiple
	riskFraction := stopDistance / current
	if riskFraction <= 0 {
		return Decision{Action: "hold", Reasoning: "invalid volatility risk fraction"}, nil
	}
	riskBudget := snapshot.Equity * p.config.RiskPerTradePct / 100
	notional := math.Min(riskBudget/riskFraction, snapshot.Equity*p.config.MaxExposure)
	if notional <= 0 {
		return Decision{Action: "hold", Reasoning: "no available risk budget"}, nil
	}

	if current > highest && fast > slow {
		return Decision{
			Action:          "open_long",
			Leverage:        p.config.Leverage,
			PositionSizeUSD: notional,
			StopLoss:        current - stopDistance,
			TakeProfit:      current + stopDistance*p.config.RewardRiskRatio,
			Confidence:      80,
			Reasoning:       "Donchian upside breakout aligned with EMA trend",
		}, nil
	}
	if p.config.AllowShort && current < lowest && fast < slow {
		return Decision{
			Action:          "open_short",
			Leverage:        p.config.Leverage,
			PositionSizeUSD: notional,
			StopLoss:        current + stopDistance,
			TakeProfit:      current - stopDistance*p.config.RewardRiskRatio,
			Confidence:      80,
			Reasoning:       "Donchian downside breakout aligned with EMA trend",
		}, nil
	}
	return Decision{Action: "hold", Reasoning: "no trend-aligned breakout"}, nil
}

func ema(values []float64, period int) float64 {
	start := len(values) - period
	if start < 0 {
		start = 0
	}
	result := values[start]
	multiplier := 2.0 / float64(period+1)
	for i := start + 1; i < len(values); i++ {
		result = (values[i]-result)*multiplier + result
	}
	return result
}

func atr(candles []market.Kline, period int) float64 {
	start := len(candles) - period
	if start < 1 {
		start = 1
	}
	total := 0.0
	count := 0
	for i := start; i < len(candles); i++ {
		previousClose := candles[i-1].Close
		trueRange := math.Max(candles[i].High-candles[i].Low,
			math.Max(math.Abs(candles[i].High-previousClose), math.Abs(candles[i].Low-previousClose)))
		total += trueRange
		count++
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func maxInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value > result {
			result = value
		}
	}
	return result
}
