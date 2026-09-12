package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/kernel"
	"nofx/store"
)

// Account risk caps are opt-in Paper policy. They do not silently change live
// adapters or grid execution. Zero preserves the old low-level broker contract.
func paperStrategyAccountRisk(strategy *store.StrategyConfig) (trade, portfolio, minimum float64) {
	if strategy == nil || strategy.StrategyType == "grid_trading" {
		return
	}
	risk := strategy.RiskControl
	return risk.PaperMaxTradeRiskRatio, risk.PaperMaxPortfolioRiskRatio, risk.MinPositionSize
}

func validatePaperAccountRiskPolicy(trade, portfolio, minimum float64) error {
	for name, value := range map[string]float64{"trade": trade, "portfolio": portfolio} {
		if !isFinitePaperNumber(value) || value < 0 || value > 1 {
			return fmt.Errorf("paper %s risk ratio must be finite and in [0,1]", name)
		}
	}
	if !isFinitePaperNumber(minimum) || minimum < 0 {
		return fmt.Errorf("paper minimum entry notional must be finite and nonnegative")
	}
	return nil
}

func (b *PaperBroker) setAccountRiskPolicy(trade, portfolio, minimum float64) error {
	if err := validatePaperAccountRiskPolicy(trade, portfolio, minimum); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config.MaxTradeRiskRatio = trade
	b.config.MaxPortfolioRiskRatio = portfolio
	b.config.MinEntryNotional = minimum
	return nil
}

func (b *PaperBroker) hasAccountRiskPolicyLocked() bool {
	return b.config.MaxTradeRiskRatio > 0 || b.config.MaxPortfolioRiskRatio > 0
}

func (b *PaperBroker) entryRiskPolicyEnabledLocked() bool {
	return b.config.MinRiskRewardRatio > 0 || b.hasAccountRiskPolicyLocked()
}

// Conservative cost reserve shared with existing risk_usd sizing, with a floor
// of 20 bps and an allowance for configured entry/exit fees and adverse slippage.
func (b *PaperBroker) accountRiskCostRatioLocked() float64 {
	return math.Max(2*takerFeeRate, (math.Max(b.config.MakerFeeBPS, b.config.TakerFeeBPS)+b.config.TakerFeeBPS+2*b.config.SlippageBPS)/10_000)
}

func paperPositionRisk(side string, entry, stop, quantity, costRatio float64) (float64, error) {
	if !isFinitePositive(entry) || !isFinitePositive(stop) || !isFinitePositive(quantity) || !isFinitePositive(costRatio) {
		return 0, fmt.Errorf("paper account risk requires valid quantity, entry, stop and costs")
	}
	loss := entry - stop
	switch side {
	case "long":
	case "short":
		loss = stop - entry
	default:
		return 0, fmt.Errorf("paper account risk: invalid side %q", side)
	}
	// Do not offset another position's risk with an expected profit at a trailed
	// stop. Gaps can prevent that expected profit from being realized.
	risk := (math.Max(0, loss) + entry*costRatio) * quantity
	if !isFinitePaperNumber(risk) {
		return 0, fmt.Errorf("paper account risk overflow")
	}
	return risk, nil
}

// Called under the broker lock. Reservations include the entire unfilled entry
// remainder, not only the next partial fill; reprices exclude their own old
// reservation but retain any already-filled position exposure.
func (b *PaperBroker) maximumEntryNotionalLocked(symbol, action string, entry, stop, aiRisk float64, excludedOrderID int64) (float64, error) {
	if !isFinitePaperNumber(aiRisk) || aiRisk < 0 {
		return 0, fmt.Errorf("paper account risk requires finite nonnegative risk_usd")
	}
	if !isFinitePositive(entry) || !isFinitePositive(stop) || (action != "open_long" && action != "open_short") || (action == "open_long" && stop >= entry) || (action == "open_short" && stop <= entry) {
		return 0, fmt.Errorf("paper entry risk requires a directionally valid entry and stop")
	}
	equity := b.snapshotLocked().Equity
	if !isFinitePositive(equity) {
		return 0, fmt.Errorf("paper account risk requires positive finite equity")
	}
	costRatio := b.accountRiskCostRatioLocked()
	side := strings.TrimPrefix(action, "open_")
	used, samePosition := 0.0, 0.0
	now := b.now()
	for _, p := range b.positions {
		if !isFinitePositive(b.marks[p.Symbol]) || b.markTimes[p.Symbol].IsZero() || now.Sub(b.markTimes[p.Symbol]) > 2*time.Minute || b.markTimes[p.Symbol].Sub(now) > time.Minute {
			return 0, fmt.Errorf("paper account risk: fresh held-position mark required for %s", p.Symbol)
		}
		risk, err := paperPositionRisk(p.Side, p.EntryPrice, p.StopLoss, p.Quantity, costRatio)
		if err != nil {
			return 0, fmt.Errorf("paper position %s risk unavailable: %w", p.Symbol, err)
		}
		used += risk
		if p.Symbol == symbol && p.Side == side {
			samePosition += risk
		}
	}
	for id, p := range b.pendingOrders {
		if id == excludedOrderID || p.ReduceOnly {
			continue
		}
		risk, err := paperPositionRisk(p.Side, p.LimitPrice, p.StopLoss, p.RemainingQuantity, costRatio)
		if err != nil {
			return 0, fmt.Errorf("paper pending order %d risk unavailable: %w", id, err)
		}
		used += risk
		if p.Symbol == symbol && p.Side == side {
			samePosition += risk
		}
	}
	budget := math.Inf(1)
	if b.config.MaxTradeRiskRatio > 0 {
		budget = equity*b.config.MaxTradeRiskRatio - samePosition
	}
	if b.config.MaxPortfolioRiskRatio > 0 {
		budget = math.Min(budget, equity*b.config.MaxPortfolioRiskRatio-used)
	}
	if aiRisk > 0 {
		budget = math.Min(budget, aiRisk-samePosition)
	}
	if !isFinitePaperNumber(budget) || budget <= 0 {
		return 0, fmt.Errorf("paper account stop-risk budget exhausted (equity %.4f, reserved risk %.4f)", equity, used)
	}
	unitRisk, err := paperPositionRisk(side, entry, stop, 1/entry, costRatio)
	if err != nil {
		return 0, err
	}
	// Reserve the equity reduction from entry fees AND adverse entry slippage.
	// Short-entry loss / filled notional is slip/(1-slip), the conservative
	// bound for both sides. This also prevents our own partial fill from
	// invalidating an otherwise unchanged pending reservation.
	slip := b.config.SlippageBPS / 10_000
	if !isFinitePaperNumber(slip) || slip < 0 || slip >= 1 {
		return 0, fmt.Errorf("paper account risk requires slippage in [0,10000) bps")
	}
	futureEquityCost := math.Max(0, math.Max(b.config.MakerFeeBPS, b.config.TakerFeeBPS))/10_000 + slip/(1-slip)
	denominator := unitRisk + math.Max(b.config.MaxTradeRiskRatio, b.config.MaxPortfolioRiskRatio)*futureEquityCost
	maximum := budget / denominator
	if !isFinitePositive(maximum) {
		return 0, fmt.Errorf("paper account risk produced invalid notional")
	}
	return maximum, nil
}

func (b *PaperBroker) limitEntryNotionalLocked(decision *kernel.Decision, entry float64) (float64, error) {
	requested := decision.PositionSizeUSD
	if !b.hasAccountRiskPolicyLocked() {
		return requested, nil
	}
	maximum, err := b.maximumEntryNotionalLocked(decision.Symbol, decision.Action, entry, decision.StopLoss, decision.RiskUSD, 0)
	if err != nil {
		return 0, err
	}
	if !isFinitePositive(requested) || !isFinitePaperNumber(decision.RiskUSD) || decision.RiskUSD < 0 {
		return 0, fmt.Errorf("paper account risk requires finite positive notional and nonnegative risk_usd")
	}
	limited := math.Min(requested, maximum)
	if limited+1e-9 < b.config.MinEntryNotional {
		return 0, fmt.Errorf("paper risk-capped notional %.4f is below minimum %.4f; wait instead of exceeding risk", limited, b.config.MinEntryNotional)
	}
	return limited, nil
}

func (b *PaperBroker) validatePendingAccountRiskLocked(order PaperPendingOrder, entry float64) error {
	if !b.hasAccountRiskPolicyLocked() {
		return nil
	}
	maximum, err := b.maximumEntryNotionalLocked(order.Symbol, order.Action, entry, order.StopLoss, order.RiskUSD, order.OrderID)
	if err != nil {
		return err
	}
	notional := order.RemainingQuantity * entry
	if !isFinitePositive(notional) || notional > maximum+1e-9*math.Max(1, maximum) {
		return fmt.Errorf("paper pending entry stop-risk exceeds account budget: notional %.4f > %.4f", notional, maximum)
	}
	return nil
}
