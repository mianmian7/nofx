package trader

import (
	"nofx/kernel"
	"nofx/store"
	"testing"
)

func TestApplyAutopilotFullSizeOpenForClaw402(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "vergex_signal"
	// Existing saved strategies retain legacy notional sizing until explicitly migrated.
	cfg.RiskControl.PositionSizingMode = "notional_based"
	cfg.RiskControl.BTCETHMaxLeverage = 10
	cfg.RiskControl.AltcoinMaxLeverage = 10
	cfg.RiskControl.BTCETHMaxPositionValueRatio = 10
	cfg.RiskControl.AltcoinMaxPositionValueRatio = 10

	at := &AutoTrader{config: AutoTraderConfig{StrategyConfig: &cfg}}
	decision := &kernel.Decision{
		Symbol:          "xyz:INTC",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 12,
	}

	at.applyAutopilotFullSizeOpen(decision, 29.8)

	if decision.Leverage != 10 {
		t.Fatalf("expected leverage to be forced to 10x, got %dx", decision.Leverage)
	}
	if decision.PositionSizeUSD != 298 {
		t.Fatalf("expected position size to use full 10x notional 298, got %.2f", decision.PositionSizeUSD)
	}
}

func TestApplyAutopilotFullSizeOpenSkipsNonClaw402Strategies(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.RiskControl.BTCETHMaxLeverage = 10
	cfg.RiskControl.AltcoinMaxLeverage = 10

	at := &AutoTrader{config: AutoTraderConfig{StrategyConfig: &cfg}}
	decision := &kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 12,
	}

	at.applyAutopilotFullSizeOpen(decision, 29.8)

	if decision.Leverage != 3 || decision.PositionSizeUSD != 12 {
		t.Fatalf("non-Claw402 strategies should not be rewritten, got leverage=%d size=%.2f", decision.Leverage, decision.PositionSizeUSD)
	}
}

func TestApplyAutopilotFullSizeOpenSkipsFixedCapForMarginBasedSizing(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "vergex_signal"
	cfg.RiskControl.PositionSizingMode = "margin_based"
	cfg.RiskControl.BTCETHMaxLeverage = 50
	cfg.RiskControl.AltcoinMaxLeverage = 50
	cfg.RiskControl.BTCETHMaxMarginRatio = 0.25
	cfg.RiskControl.AltcoinMaxMarginRatio = 0.25

	at := &AutoTrader{config: AutoTraderConfig{StrategyConfig: &cfg}}
	decision := &kernel.Decision{
		Symbol:          "xyz:INTC",
		Action:          "open_long",
		Leverage:        8,
		PositionSizeUSD: 220,
	}

	at.applyAutopilotFullSizeOpen(decision, 94.87)

	if decision.Leverage != 8 || decision.PositionSizeUSD != 220 {
		t.Fatalf("margin-based sizing should preserve the AI decision until live available balance is checked, got leverage=%d size=%.2f", decision.Leverage, decision.PositionSizeUSD)
	}
}
