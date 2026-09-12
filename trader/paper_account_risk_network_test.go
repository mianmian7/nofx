package trader

import (
	"nofx/kernel"
	"nofx/store"
	"os"
	"testing"
)

// Opt-in bounded integration check for 生产环境. It constructs only credential-free
// public market-data providers and ephemeral Paper brokers: no Store, no trader
// Run(), no AI call, no live exchange writer, and no persistent account mutations.
func TestPaperAccountRiskPublicMarketIntegration(t *testing.T) {
	if os.Getenv("NOFX_PAPER_POLICY_NETWORK") != "1" {
		t.Skip("set NOFX_PAPER_POLICY_NETWORK=1 for public-data-only Paper validation")
	}
	for _, exchange := range []string{"binance", "bitget", "okx"} {
		t.Run(exchange, func(t *testing.T) {
			cfg := store.GetDefaultStrategyConfig("en")
			cfg.RiskControl.PaperMaxTradeRiskRatio = .01
			cfg.RiskControl.PaperMaxPortfolioRiskRatio = .02
			cfg.RiskControl.MinPositionSize = 12
			at, err := NewAutoTrader(AutoTraderConfig{ID: "isolated-paper-policy-probe", AIModel: "custom", Exchange: exchange, ExecutionMode: ExecutionModePaper, InitialBalance: 100, StrategyConfig: &cfg}, nil, "isolated-verification")
			if err != nil {
				t.Fatal(err)
			}
			if at.trader != at.paperBroker || at.store != nil {
				t.Fatal("probe unexpectedly constructed a live writer or persistent store")
			}
			price, err := at.paperBroker.prices.GetMarketPrice("BTCUSDT")
			if err != nil || price <= 0 {
				t.Fatalf("public price: %.8f %v", price, err)
			}
			for _, maker := range []bool{false, true} {
				bcfg := at.paperBroker.config
				bcfg.MakerFirst = maker
				b, err := NewPaperBroker(bcfg, at.paperBroker.prices)
				if err != nil {
					t.Fatal(err)
				}
				fill, err := b.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 300, RiskUSD: 300, StopLoss: price * .95, TakeProfit: price * 1.15})
				if err != nil {
					t.Fatalf("maker=%v: %v", maker, err)
				}
				stop := price * .95
				entry := fill.Price
				quantity := fill.Quantity
				if maker {
					orders := b.PendingOrders()
					if len(orders) != 1 {
						t.Fatal("missing risk-bounded pending order")
					}
					entry, quantity, stop = orders[0].LimitPrice, orders[0].RemainingQuantity, orders[0].StopLoss
				}
				risk, err := paperPositionRisk("long", entry, stop, quantity, b.accountRiskCostRatioLocked())
				if err != nil {
					t.Fatal(err)
				}
				if risk > b.Snapshot().Equity*.01+1e-8 || quantity*entry >= 300 {
					t.Fatalf("oversized live-quote Paper intent not capped: maker=%v risk=%.8f", maker, risk)
				}
				t.Logf("public=%s maker=%v price=%.4f risk=%.6f equity=%.6f persistent=false live_writer=false", exchange, maker, entry, risk, b.Snapshot().Equity)
			}
		})
	}
}
