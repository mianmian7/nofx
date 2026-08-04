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

func TestMarginPnLStopPriceConvertsLossThresholdForBothDirections(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		leverage int
		want     float64
	}{
		{name: "long 20x", action: "open_long", leverage: 20, want: 99},
		{name: "short 20x", action: "open_short", leverage: 20, want: 101},
		{name: "long 10x", action: "open_long", leverage: 10, want: 98},
		{name: "short 10x", action: "open_short", leverage: 10, want: 102},
		{name: "long 50x", action: "open_long", leverage: 50, want: 99.6},
		{name: "short 50x", action: "open_short", leverage: 50, want: 100.4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := marginPnLStopPrice(tt.action, 100, tt.leverage, unifiedMarginStopLossPct)
			if err != nil {
				t.Fatalf("margin-PnL stop price: %v", err)
			}
			if math.Abs(got-tt.want) > 0.000001 {
				t.Fatalf("stop = %.6f, want %.6f", got, tt.want)
			}
		})
	}
}

func TestClampStopLossToMarginRiskDoesNotLoosenTighterStops(t *testing.T) {
	longStop, changed, err := clampStopLossToMarginRisk("open_long", 100, 20, 95, unifiedMarginStopLossPct)
	if err != nil {
		t.Fatalf("clamp long stop: %v", err)
	}
	if !changed || math.Abs(longStop-99) > 0.000001 {
		t.Fatalf("long stop = %.6f changed=%v, want 99.000000 true", longStop, changed)
	}

	longTighter, changed, err := clampStopLossToMarginRisk("open_long", 100, 20, 99.5, unifiedMarginStopLossPct)
	if err != nil {
		t.Fatalf("preserve tighter long stop: %v", err)
	}
	if changed || math.Abs(longTighter-99.5) > 0.000001 {
		t.Fatalf("tighter long stop = %.6f changed=%v, want 99.500000 false", longTighter, changed)
	}

	shortStop, changed, err := clampStopLossToMarginRisk("open_short", 100, 20, 105, unifiedMarginStopLossPct)
	if err != nil {
		t.Fatalf("clamp short stop: %v", err)
	}
	if !changed || math.Abs(shortStop-101) > 0.000001 {
		t.Fatalf("short stop = %.6f changed=%v, want 101.000000 true", shortStop, changed)
	}

	shortTighter, changed, err := clampStopLossToMarginRisk("open_short", 100, 20, 100.5, unifiedMarginStopLossPct)
	if err != nil {
		t.Fatalf("preserve tighter short stop: %v", err)
	}
	if changed || math.Abs(shortTighter-100.5) > 0.000001 {
		t.Fatalf("tighter short stop = %.6f changed=%v, want 100.500000 false", shortTighter, changed)
	}
}

func TestUnifiedMarginStopStaysBeforePaperLiquidation(t *testing.T) {
	for _, leverage := range []int{10, 20, 50} {
		longStop, err := marginPnLStopPrice("open_long", 100, leverage, unifiedMarginStopLossPct)
		if err != nil {
			t.Fatalf("long %dx stop conversion: %v", leverage, err)
		}
		longLiquidation := paperLiquidationPrice("long", 100, leverage, 0.005)
		if longStop <= longLiquidation {
			t.Fatalf("long %dx stop %.4f must remain above liquidation %.4f", leverage, longStop, longLiquidation)
		}

		shortStop, err := marginPnLStopPrice("open_short", 100, leverage, unifiedMarginStopLossPct)
		if err != nil {
			t.Fatalf("short %dx stop conversion: %v", leverage, err)
		}
		shortLiquidation := paperLiquidationPrice("short", 100, leverage, 0.005)
		if shortStop >= shortLiquidation {
			t.Fatalf("short %dx stop %.4f must remain below liquidation %.4f", leverage, shortStop, shortLiquidation)
		}
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
