package kernel

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

// ============================================================================
// Decision Validation
// ============================================================================

func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64) error {
	risk := store.RiskControlConfig{PositionSizingMode: "notional_based", BTCETHMaxLeverage: btcEthLeverage, AltcoinMaxLeverage: altcoinLeverage, BTCETHMaxPositionValueRatio: btcEthPosRatio, AltcoinMaxPositionValueRatio: altcoinPosRatio, MinPositionSize: 12, MinRiskRewardRatio: 3}
	return validateDecisionsWithRisk(decisions, accountEquity, risk)
}

func validateDecisionsWithRisk(decisions []Decision, accountEquity float64, risk store.RiskControlConfig) error {
	for i := range decisions {
		if err := validateDecisionWithRisk(&decisions[i], accountEquity, risk); err != nil {
			return fmt.Errorf("decision #%d validation failed: %w", i+1, err)
		}
	}
	return nil
}

func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64) error {
	risk := store.RiskControlConfig{PositionSizingMode: "notional_based", BTCETHMaxLeverage: btcEthLeverage, AltcoinMaxLeverage: altcoinLeverage, BTCETHMaxPositionValueRatio: btcEthPosRatio, AltcoinMaxPositionValueRatio: altcoinPosRatio, MinPositionSize: 12, MinRiskRewardRatio: 3}
	return validateDecisionWithRisk(d, accountEquity, risk)
}

func validateDecisionWithRisk(d *Decision, accountEquity float64, risk store.RiskControlConfig) error {
	validActions := map[string]bool{
		"open_long":   true,
		"open_short":  true,
		"close_long":  true,
		"close_short": true,
		"hold":        true,
		"wait":        true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("invalid action: %s", d.Action)
	}

	if d.Action == "open_long" || d.Action == "open_short" {
		// Asset tiering for validation:
		//   - BTC/ETH crypto perps use the BTC/ETH tier (typically 5x equity).
		//   - Hyperliquid XYZ assets (US equities, commodities, forex) are
		//     also treated as the higher tier — they are not crypto altcoins
		//     and the user's quick-trade flow shows them at the higher cap,
		//     so the validator must match.
		//   - Everything else is altcoin (1x equity by default).
		maxLeverage := risk.AltcoinMaxLeverage
		posRatio := risk.AltcoinMaxPositionValueRatio
		isMajor := d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" || market.IsXyzDexAsset(d.Symbol)
		if isMajor {
			maxLeverage = risk.BTCETHMaxLeverage
			posRatio = risk.BTCETHMaxPositionValueRatio
		}

		if d.Leverage <= 0 {
			return fmt.Errorf("leverage must be greater than 0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			logger.Infof("⚠️  [Leverage Fallback] %s leverage exceeded (%dx > %dx), auto-adjusting to limit %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage
		}
		maxPositionValue := risk.MaxPositionNotional(accountEquity, d.Leverage, isMajor)
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("position size must be greater than 0: %.2f", d.PositionSizeUSD)
		}

		minPositionSizeGeneral := risk.MinPositionSize
		if minPositionSizeGeneral <= 0 {
			minPositionSizeGeneral = 12
		}
		minPositionSizeBTCETH := math.Max(60, minPositionSizeGeneral)

		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			if d.PositionSizeUSD < minPositionSizeBTCETH {
				return fmt.Errorf("%s opening amount too small (%.2f USDT), must be ≥%.2f USDT", d.Symbol, d.PositionSizeUSD, minPositionSizeBTCETH)
			}
		} else {
			if d.PositionSizeUSD < minPositionSizeGeneral {
				return fmt.Errorf("opening amount too small (%.2f USDT), must be ≥%.2f USDT", d.PositionSizeUSD, minPositionSizeGeneral)
			}
		}

		// The prompt displays whole-USDT limits. Accept the displayed rounded
		// boundary so an AI decision of 20 is not rejected when the exact cap is
		// 19.63 due to account decimals.
		tolerance := math.Max(maxPositionValue*0.01, 0.5)
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			switch {
			case d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT":
				return fmt.Errorf("BTC/ETH single coin position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", maxPositionValue, posRatio, d.PositionSizeUSD)
			case market.IsXyzDexAsset(d.Symbol):
				return fmt.Errorf("%s position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", d.Symbol, maxPositionValue, posRatio, d.PositionSizeUSD)
			default:
				return fmt.Errorf("altcoin single coin position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", maxPositionValue, posRatio, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("stop loss and take profit must be greater than 0")
		}

		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("for long positions, stop loss price must be less than take profit price")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("for short positions, stop loss price must be greater than take profit price")
			}
		}

		var entryPrice float64
		if d.Action == "open_long" {
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2
		} else {
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		minRiskRewardRatio := risk.MinRiskRewardRatio
		if minRiskRewardRatio <= 0 {
			minRiskRewardRatio = 3
		}
		if riskRewardRatio < minRiskRewardRatio {
			return fmt.Errorf("risk/reward ratio too low (%.2f:1), must be ≥%.1f:1 [risk: %.2f%% reward: %.2f%%] [stop loss: %.2f take profit: %.2f]",
				riskRewardRatio, minRiskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}
