package kernel

import (
	"fmt"
	"math"
	"nofx/market"
	"nofx/store"
	"time"
)

// DeterministicRiskConfig holds configuration parameters for algorithmic SL/TP and sizing
type DeterministicRiskConfig struct {
	ATRMultiplier   float64       `json:"atr_multiplier"`     // Multiplier for ATR stop distance (e.g. 1.8)
	RewardRiskRatio float64       `json:"reward_risk_ratio"`  // Target Reward to Risk ratio (e.g. 2.0)
	RiskPctPerTrade float64       `json:"risk_pct_per_trade"` // Max risk per trade as % of equity (e.g. 1.5)
	MaxLeverage     int           `json:"max_leverage"`       // Max leverage
	MinConfidence   int           `json:"min_confidence"`     // Minimum confidence required to open
	Cooldown        time.Duration `json:"cooldown"`           // Cooldown between conflicting trades
}

// DefaultDeterministicRiskConfig returns sensible defaults
func DefaultDeterministicRiskConfig() DeterministicRiskConfig {
	return DeterministicRiskConfig{
		ATRMultiplier:   1.8,
		RewardRiskRatio: 2.0,
		RiskPctPerTrade: 1.5,
		MaxLeverage:     10,
		MinConfidence:   60,
		Cooldown:        10 * time.Minute,
	}
}

// ExtractATR safely extracts ATR14 from market.Data across all timeframe layers
func ExtractATR(data *market.Data) float64 {
	if data == nil {
		return 0
	}

	// 1. Try timeframe data (5m, 15m, 1h, 3m)
	priorityTimeframes := []string{"5m", "15m", "3m", "1h"}
	for _, tf := range priorityTimeframes {
		if series, ok := data.TimeframeData[tf]; ok && series != nil && series.ATR14 > 0 {
			return series.ATR14
		}
	}

	// 2. Iterate any available timeframe series
	if data.TimeframeData != nil {
		for _, series := range data.TimeframeData {
			if series != nil && series.ATR14 > 0 {
				return series.ATR14
			}
		}
	}

	// 3. Try intraday series
	if data.IntradaySeries != nil && data.IntradaySeries.ATR14 > 0 {
		return data.IntradaySeries.ATR14
	}

	// 4. Try longer term context
	if data.LongerTermContext != nil && data.LongerTermContext.ATR14 > 0 {
		return data.LongerTermContext.ATR14
	}

	// 5. Fallback heuristic: 1.5% of current price
	if data.CurrentPrice > 0 {
		return data.CurrentPrice * 0.015
	}

	return 0
}

// CalculateATRParams calculates exact numeric SL, TP, and position size based on ATR and risk rules.
func CalculateATRParams(
	symbol string,
	action string,
	currentPrice float64,
	atr float64,
	accountEquity float64,
	availableBalance float64,
	riskConfig store.RiskControlConfig,
	detConfig DeterministicRiskConfig,
	confidence int,
) (*Decision, error) {
	if math.IsNaN(currentPrice) || math.IsInf(currentPrice, 0) || currentPrice <= 0 {
		return nil, fmt.Errorf("current price must be positive, got %.4f", currentPrice)
	}
	if math.IsNaN(atr) || math.IsInf(atr, 0) || atr <= 0 {
		return &Decision{Symbol: symbol, Action: "wait", Reasoning: "observed ATR is unavailable"}, nil
	}
	if math.IsNaN(accountEquity) || math.IsInf(accountEquity, 0) || accountEquity <= 0 {
		return nil, fmt.Errorf("account equity must be positive, got %.2f", accountEquity)
	}
	if math.IsNaN(availableBalance) || math.IsInf(availableBalance, 0) || availableBalance <= 0 {
		return &Decision{Symbol: symbol, Action: "wait", Reasoning: "no available balance"}, nil
	}

	maxLeverage := riskConfig.MaxLeverage
	if maxLeverage <= 0 {
		maxLeverage = detConfig.MaxLeverage
	}
	if maxLeverage <= 0 {
		maxLeverage = 10
	}

	atrMult := detConfig.ATRMultiplier
	if atrMult <= 0 {
		atrMult = 1.8
	}
	// Scale ATR slightly if confidence is very high
	if confidence >= 85 {
		atrMult *= 1.1
	}

	rrr := detConfig.RewardRiskRatio
	if rrr <= 0 {
		rrr = 2.0
	}
	rrr = math.Max(rrr, riskConfig.MinRiskRewardRatio)

	riskPct := detConfig.RiskPctPerTrade
	if riskPct <= 0 {
		riskPct = 1.5
	}

	stopDistance := atr * atrMult
	var stopLoss, takeProfit float64

	switch action {
	case "open_long":
		stopLoss = currentPrice - stopDistance
		takeProfit = currentPrice + (stopDistance * rrr)
		if stopLoss <= 0 {
			stopLoss = currentPrice * 0.95
		}
		if !(stopLoss < currentPrice && currentPrice < takeProfit) {
			return nil, fmt.Errorf("open_long invariant failed: stopLoss(%.4f) < currentPrice(%.4f) < takeProfit(%.4f)",
				stopLoss, currentPrice, takeProfit)
		}

	case "open_short":
		stopLoss = currentPrice + stopDistance
		takeProfit = currentPrice - (stopDistance * rrr)
		if takeProfit <= 0 {
			takeProfit = currentPrice * 0.5
		}
		if !(takeProfit < currentPrice && currentPrice < stopLoss) {
			return nil, fmt.Errorf("open_short invariant failed: takeProfit(%.4f) < currentPrice(%.4f) < stopLoss(%.4f)",
				takeProfit, currentPrice, stopLoss)
		}

	default:
		return nil, fmt.Errorf("unsupported opening action: %s", action)
	}

	// Sizing calculation:
	// maxRiskUSD = accountEquity * (riskPct / 100)
	// lossFraction = stopDistance / currentPrice
	// positionSizeUSD = maxRiskUSD / lossFraction
	maxRiskUSD := accountEquity * (riskPct / 100.0)
	if riskConfig.PaperMaxTradeRiskRatio > 0 {
		maxRiskUSD = math.Min(maxRiskUSD, accountEquity*riskConfig.PaperMaxTradeRiskRatio)
	}
	lossFraction := stopDistance / currentPrice
	if lossFraction <= 0 {
		lossFraction = 0.01
	}
	// Include conservative two-sided fees/slippage instead of sizing to raw SL alone.
	targetPositionSizeUSD := maxRiskUSD / (lossFraction + 0.002)

	// Minimum position size requirement (BTC/ETH >= 60, Altcoins >= 12 or config)
	minPos := effectiveMinPositionSize(symbol, riskConfig)
	if targetPositionSizeUSD < minPos {
		return &Decision{Symbol: symbol, Action: "wait", Reasoning: "risk-sized notional is below minimum; risk must not be rounded up"}, nil
	}

	// Apply margin budget clamp
	// Notional cannot exceed availableBalance * maxLeverage * 0.9 (safety buffer)
	marginBudget := availableBalance
	if riskConfig.MaxMarginUsage > 0 {
		marginBudget = math.Min(marginBudget, accountEquity*riskConfig.MaxMarginUsage)
	}
	maxMarginNotional := marginBudget * float64(maxLeverage) * 0.9
	if maxMarginNotional < minPos {
		return &Decision{
			Symbol:     symbol,
			Action:     "wait",
			Confidence: confidence,
			Reasoning:  fmt.Sprintf("insufficient available margin for %s min position (requires $%.2f notional, max affordable $%.2f)", symbol, minPos, maxMarginNotional),
		}, nil
	}
	if targetPositionSizeUSD > maxMarginNotional {
		targetPositionSizeUSD = maxMarginNotional
	}

	// Clamp by RiskControl limits if configured
	isMajor := symbol == "BTCUSDT" || symbol == "ETHUSDT" || market.IsXyzDexAsset(symbol)
	maxNotional := riskConfig.MaxPositionNotional(accountEquity, maxLeverage, isMajor)
	if !riskConfig.IsMarginBased() && maxNotional > 0 && minPos > maxNotional {
		return &Decision{
			Symbol:     symbol,
			Action:     "wait",
			Confidence: confidence,
			Reasoning:  fmt.Sprintf("account equity ($%.2f) too small for %s min position ($%.2f exceeds max allowed position $%.2f)", accountEquity, symbol, minPos, maxNotional),
		}, nil
	}
	if maxNotional > 0 && targetPositionSizeUSD > maxNotional {
		targetPositionSizeUSD = maxNotional
	}

	// Re-verify min position size
	if targetPositionSizeUSD < minPos {
		return &Decision{Symbol: symbol, Action: "wait", Reasoning: "affordable notional is below minimum"}, nil
	}

	// Recompute final risk after price rounding; never round quantity/risk up.
	stopLoss = roundPriceByMagnitude(stopLoss)
	// Derive the target from the rounded stop, then round outward by at most
	// one display tick. Nearest rounding otherwise rejects valid ATR setups
	// nondeterministically just below the exact configured RR boundary.
	step := priceStepByMagnitude(takeProfit)
	if action == "open_long" {
		takeProfit = math.Ceil((currentPrice+(currentPrice-stopLoss)*rrr)/step) * step
	} else {
		takeProfit = math.Floor((currentPrice-(stopLoss-currentPrice)*rrr)/step) * step
	}
	finalRisk, finalReward := currentPrice-stopLoss, takeProfit-currentPrice
	if action == "open_short" {
		finalRisk, finalReward = stopLoss-currentPrice, currentPrice-takeProfit
	}
	if finalRisk <= 0 || finalReward <= 0 || finalReward/finalRisk+1e-6 < rrr {
		return &Decision{Symbol: symbol, Action: "wait", Reasoning: "rounded protection does not meet risk/reward"}, nil
	}
	targetPositionSizeUSD = math.Min(targetPositionSizeUSD, maxRiskUSD/(finalRisk/currentPrice+0.002))
	targetPositionSizeUSD = math.Floor(targetPositionSizeUSD*100) / 100
	if targetPositionSizeUSD < minPos {
		return &Decision{Symbol: symbol, Action: "wait", Reasoning: "rounded risk-sized notional below minimum"}, nil
	}

	reasoning := fmt.Sprintf("Jev System 1: %s (conf=%d%%) | ATR(14)=%.4f dist=%.4f (%.2f%%) | SL=%.4f TP=%.4f (RRR=%.1f) | Risk=$%.2f Size=$%.2f",
		action, confidence, atr, stopDistance, lossFraction*100, stopLoss, takeProfit, rrr, maxRiskUSD, targetPositionSizeUSD)

	return &Decision{
		Symbol:          symbol,
		Action:          action,
		Leverage:        maxLeverage,
		PositionSizeUSD: targetPositionSizeUSD,
		StopLoss:        stopLoss,
		TakeProfit:      takeProfit,
		Confidence:      confidence,
		RiskUSD:         maxRiskUSD,
		Reasoning:       reasoning,
	}, nil
}

func priceStepByMagnitude(price float64) float64 {
	switch {
	case price >= 1000:
		return .01
	case price >= 1:
		return .0001
	case price >= .01:
		return .000001
	default:
		return .00000001
	}
}

func roundPriceByMagnitude(price float64) float64 {
	step := priceStepByMagnitude(price)
	return math.Round(price/step) * step
}

// AntiWhipsawFilter evaluates whether a candidate trade should be rejected due to
// regime chop, low probability separation, or rapid flipping.
func AntiWhipsawFilter(
	marketRegime string,
	actionProb float64,
	secondProb float64,
	minConfidence int,
) (approved bool, reason string) {
	// 1. Regime filter: Range-bound chop blocks opening
	if marketRegime != "strong_trend_up" && marketRegime != "strong_trend_down" {
		return false, "market regime is missing or unsafe; opening blocked by anti-whipsaw filter"
	}
	if math.IsNaN(actionProb) || math.IsInf(actionProb, 0) || math.IsNaN(secondProb) || math.IsInf(secondProb, 0) || actionProb < 0 || actionProb > 1 || secondProb < 0 || secondProb > 1 {
		return false, "invalid action probabilities"
	}

	// 2. Minimum confidence threshold (default 60% or configured minConfidence)
	confThreshold := 0.60
	if minConfidence > 0 {
		confThreshold = float64(minConfidence) / 100.0
		if confThreshold < 0.50 {
			confThreshold = 0.50
		}
	}
	if actionProb < confThreshold {
		return false, fmt.Sprintf("action probability %.2f is below threshold %.2f", actionProb, confThreshold)
	}

	// 3. Probability separation: Clear winning decision required (at least 10% separation)
	if (actionProb - secondProb) < 0.10 {
		return false, fmt.Sprintf("insufficient probability separation (top=%.2f, second=%.2f, diff=%.2f < 0.10)",
			actionProb, secondProb, actionProb-secondProb)
	}

	return true, ""
}
