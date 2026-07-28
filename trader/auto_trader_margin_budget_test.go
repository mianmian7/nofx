package trader

import (
	"math"
	"nofx/kernel"
	"nofx/store"
	"testing"
)

func TestCalculateRemainingStrategyMarginIncludesPositionsAndOpenOrders(t *testing.T) {
	remainingMargin := calculateRemainingStrategyMargin(100, 0.5, 20, 5)
	if remainingMargin != 25 {
		t.Fatalf("remaining margin = %.2f, want 25.00", remainingMargin)
	}
}

func TestCalculateRiskLimitedNotionalIncludesStopDistanceAndFees(t *testing.T) {
	maximumNotional, enabled, err := calculateRiskLimitedNotional(
		"open_long",
		100,
		98,
		5,
	)
	if err != nil {
		t.Fatalf("calculate risk-limited notional: %v", err)
	}
	if !enabled {
		t.Fatal("risk limit should be enabled for positive risk_usd")
	}
	wantedNotional := 5 / 0.022
	if math.Abs(maximumNotional-wantedNotional) > 0.000001 {
		t.Fatalf("maximum notional = %.6f, want %.6f", maximumNotional, wantedNotional)
	}
}

func TestCalculateRiskLimitedNotionalRejectsStopOnWrongSide(t *testing.T) {
	_, _, err := calculateRiskLimitedNotional("open_short", 100, 99, 5)
	if err == nil {
		t.Fatal("short stop below entry should be rejected")
	}
}

func TestCalculateRiskLimitedNotionalIgnoresMissingRiskBudget(t *testing.T) {
	maximumNotional, enabled, err := calculateRiskLimitedNotional("open_long", 100, 98, 0)
	if err != nil {
		t.Fatalf("zero risk_usd should preserve compatibility: %v", err)
	}
	if enabled || maximumNotional != 0 {
		t.Fatalf("zero risk_usd returned enabled=%v maximum=%.2f, want disabled", enabled, maximumNotional)
	}
}

func TestCalculateRemainingStrategyMarginStopsAtConfiguredLimit(t *testing.T) {
	remainingMargin := calculateRemainingStrategyMargin(100, 0.5, 45, 5)
	if remainingMargin != 0 {
		t.Fatalf("remaining margin = %.2f, want 0.00", remainingMargin)
	}
}

func TestMaximumAffordableNotionalMakesMarginProfilesEffective(t *testing.T) {
	balancedMarginBudget := calculateRemainingStrategyMargin(100, 0.5, 0, 0)
	balancedMaximumNotional := calculateMaximumAffordableNotional(balancedMarginBudget, 10)
	if balancedMaximumNotional >= 500 {
		t.Fatalf("balanced 50%% margin profile allowed %.2f USDT at 10x, want less than 500", balancedMaximumNotional)
	}

	aggressiveMarginBudget := calculateRemainingStrategyMargin(100, 0.8, 0, 0)
	aggressiveMaximumNotional := calculateMaximumAffordableNotional(aggressiveMarginBudget, 8)
	if aggressiveMaximumNotional <= 500 {
		t.Fatalf("aggressive 80%% margin profile allowed %.2f USDT at 8x, want more than 500", aggressiveMaximumNotional)
	}
}

func TestEstimatePositionMarginUsesReportedAndDerivedValues(t *testing.T) {
	positions := []map[string]interface{}{
		{
			"initial_margin": float64(12),
		},
		{
			"markPrice":   float64(100),
			"positionAmt": float64(-2),
			"leverage":    float64(10),
		},
	}

	estimatedMargin := estimatePositionMargin(positions)
	if math.Abs(estimatedMargin-32) > 0.000001 {
		t.Fatalf("estimated margin = %.6f, want 32.000000", estimatedMargin)
	}
}

func TestExtractAccountMarginUsageDoesNotDoubleCountOpenOrders(t *testing.T) {
	positionMargin, openOrderMargin := extractAccountMarginUsage(
		map[string]interface{}{
			"totalInitialMargin":          float64(30),
			"totalOpenOrderInitialMargin": float64(10),
		},
		nil,
	)
	if positionMargin != 20 || openOrderMargin != 10 {
		t.Fatalf(
			"margin usage = positions %.2f + orders %.2f, want 20.00 + 10.00",
			positionMargin,
			openOrderMargin,
		)
	}
}

func TestExtractAccountMarginUsagePrefersExplicitPositionMargin(t *testing.T) {
	positionMargin, openOrderMargin := extractAccountMarginUsage(
		map[string]interface{}{
			"totalInitialMargin":          float64(30),
			"totalPositionInitialMargin":  float64(20),
			"totalOpenOrderInitialMargin": float64(10),
		},
		nil,
	)
	if positionMargin != 20 || openOrderMargin != 10 {
		t.Fatalf(
			"explicit margin usage = positions %.2f + orders %.2f, want 20.00 + 10.00",
			positionMargin,
			openOrderMargin,
		)
	}
}

func TestPaperOpenRiskBudgetEnforcesBalancedMarginLimit(t *testing.T) {
	strategyConfig := store.GetDefaultStrategyConfig("en")
	strategyConfig.RiskControl.PositionSizingMode = "margin_based"
	strategyConfig.RiskControl.MaxMarginUsage = 0.5
	strategyConfig.RiskControl.MinPositionSize = 12

	paperBroker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100}, fixedPaperPriceSource{"SPYUSDT": 100})
	if err != nil {
		t.Fatalf("create paper broker: %v", err)
	}
	autoTrader := &AutoTrader{
		executionMode: ExecutionModePaper,
		paperBroker:   paperBroker,
		config: AutoTraderConfig{
			StrategyConfig: &strategyConfig,
		},
	}
	decision := &kernel.Decision{
		Symbol:          "SPYUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 950,
	}

	if err := autoTrader.enforceOpenRiskBudget(decision); err != nil {
		t.Fatalf("enforce paper risk budget: %v", err)
	}
	if decision.PositionSizeUSD >= 500 || decision.PositionSizeUSD <= 470 {
		t.Fatalf("adjusted notional = %.2f, want a fee-buffered value between 470 and 500", decision.PositionSizeUSD)
	}
}

func TestPaperOpenRiskBudgetPreservesAggressivePositionWithinLimit(t *testing.T) {
	strategyConfig := store.GetDefaultStrategyConfig("en")
	strategyConfig.RiskControl.PositionSizingMode = "margin_based"
	strategyConfig.RiskControl.MaxMarginUsage = 0.8
	strategyConfig.RiskControl.MinPositionSize = 12

	paperBroker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100}, fixedPaperPriceSource{"SAMSUNGUSDT": 100})
	if err != nil {
		t.Fatalf("create paper broker: %v", err)
	}
	autoTrader := &AutoTrader{
		executionMode: ExecutionModePaper,
		paperBroker:   paperBroker,
		config: AutoTraderConfig{
			StrategyConfig: &strategyConfig,
		},
	}
	decision := &kernel.Decision{
		Symbol:          "SAMSUNGUSDT",
		Action:          "open_long",
		Leverage:        8,
		PositionSizeUSD: 500,
	}

	if err := autoTrader.enforceOpenRiskBudget(decision); err != nil {
		t.Fatalf("enforce paper risk budget: %v", err)
	}
	if decision.PositionSizeUSD != 500 {
		t.Fatalf("adjusted notional = %.2f, want unchanged 500.00", decision.PositionSizeUSD)
	}
}

func TestPaperOpenRiskBudgetCapsPositionByRiskUSD(t *testing.T) {
	strategyConfig := store.GetDefaultStrategyConfig("en")
	strategyConfig.RiskControl.PositionSizingMode = "margin_based"
	strategyConfig.RiskControl.MaxMarginUsage = 1
	strategyConfig.RiskControl.MinPositionSize = 12

	paperBroker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100}, fixedPaperPriceSource{"SPYUSDT": 100})
	if err != nil {
		t.Fatalf("create paper broker: %v", err)
	}
	autoTrader := &AutoTrader{
		executionMode: ExecutionModePaper,
		paperBroker:   paperBroker,
		config: AutoTraderConfig{
			StrategyConfig: &strategyConfig,
		},
	}
	decision := &kernel.Decision{
		Symbol:          "SPYUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 500,
		StopLoss:        98,
		RiskUSD:         5,
	}

	if err := autoTrader.enforceOpenRiskBudget(decision); err != nil {
		t.Fatalf("enforce paper risk budget: %v", err)
	}
	wantedNotional := 5 / 0.022
	if math.Abs(decision.PositionSizeUSD-wantedNotional) > 0.000001 {
		t.Fatalf("adjusted notional = %.6f, want risk-capped %.6f", decision.PositionSizeUSD, wantedNotional)
	}
}
