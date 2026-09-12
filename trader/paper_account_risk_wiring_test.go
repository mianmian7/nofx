package trader

import (
	"encoding/json"
	"nofx/kernel"
	"nofx/store"
	"path/filepath"
	"testing"
)

func TestPaperAccountRiskStrategyConstructorAndNestedJSON(t *testing.T) {
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.RiskControl.PaperMaxTradeRiskRatio = .01
	strategy.RiskControl.PaperMaxPortfolioRiskRatio = .02
	strategy.RiskControl.MinPositionSize = 12
	raw, err := json.Marshal(strategy)
	if err != nil {
		t.Fatal(err)
	}
	var restored store.StrategyConfig
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	at, err := NewAutoTrader(AutoTraderConfig{ID: "risk-wired", AIModel: "custom", Exchange: "binance", ExecutionMode: ExecutionModePaper, InitialBalance: 100, StrategyConfig: &restored}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if at.paperBroker.config.MaxTradeRiskRatio != .01 || at.paperBroker.config.MaxPortfolioRiskRatio != .02 || at.paperBroker.config.MinEntryNotional != 12 {
		t.Fatalf("policy not wired: %+v", at.paperBroker.config)
	}
	at.marketDataProvider = nil
	at.binanceMarketClient = nil
	at.paperBroker.prices = &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	at.paperBroker.fundingSource = nil
	at.paperBroker.config.MakerFirst = false
	at.paperBroker.config.SlippageBPS = 0
	if err = at.executeDecisionWithRecord(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 100, StopLoss: 95, TakeProfit: 120, RiskUSD: 20}, &store.DecisionAction{}); err != nil {
		t.Fatal(err)
	}
	p := at.paperBroker.positions["MUUSDT:long"]
	if p.Quantity*(p.EntryPrice-p.StopLoss)+p.Quantity*p.EntryPrice*.002 > 1 {
		t.Fatal("AutoTrader oversized AI intent bypassed account risk cap")
	}
}

func TestPaperAccountRiskReloadUpdatesAllLimitsTogether(t *testing.T) {
	cfg := accountRiskConfig(t, false)
	b, err := NewPaperBroker(cfg, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.setAccountRiskPolicy(.005, .01, 12); err != nil {
		t.Fatal(err)
	}
	if err = b.setAccountRiskPolicy(.5, -.1, 12); err == nil {
		t.Fatal("accepted invalid portfolio policy")
	}
	if b.config.MaxTradeRiskRatio != .005 || b.config.MaxPortfolioRiskRatio != .01 || b.config.MinEntryNotional != 12 {
		t.Fatal("failed reload partially updated policy")
	}
	// A 0.5 USDT risk allowance cannot support the existing 12 USDT minimum.
	if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 100, StopLoss: 95, TakeProfit: 120}); err == nil {
		t.Fatal("risk resize produced a sub-minimum entry")
	}
}

func TestPaperAccountRiskPersistedPendingIsRevalidatedAfterRestart(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "pending.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MakerFeeBPS: 2}
	old, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "policy-migration", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", Leverage: 3, PositionSizeUSD: 100, StopLoss: 95, TakeProfit: 120}); err != nil {
		t.Fatal(err)
	}
	cfg.MaxTradeRiskRatio = .01
	cfg.MaxPortfolioRiskRatio = .02
	cfg.MinEntryNotional = 12
	b, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "policy-migration", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.RefreshMakerOrders(b.now()); err == nil {
		t.Fatal("legacy oversized pending order filled after new policy activated")
	}
	if len(b.RecentFills(0)) != 0 || len(b.PendingOrders()) != 0 || b.Snapshot().Balance != 100 {
		t.Fatal("unsafe pending revalidation corrupted ledger or left order active")
	}
}
