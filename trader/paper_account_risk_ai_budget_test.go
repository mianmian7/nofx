package trader

import (
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

func TestPaperAccountRiskRetainsSmallerAIBudgetAcrossRestartAndReprice(t *testing.T) {
	now := time.Now().UTC()
	cfg := accountRiskConfig(t, true)
	cfg.Clock = func() time.Time { return now }
	cfg.MakerMaxReprices = 2
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	st, err := store.New(filepath.Join(t.TempDir(), "ai-budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, err := NewPersistentPaperBroker(cfg, src, st.Paper(), "ai-budget", nil)
	if err != nil {
		t.Fatal(err)
	}
	order, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 5, StopLoss: 95, TakeProfit: 115, RiskUSD: .8})
	if err != nil {
		t.Fatal(err)
	}
	b, err = NewPersistentPaperBroker(cfg, src, st.Paper(), "ai-budget", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.pendingOrders[order.OrderID].RiskUSD != .8 {
		t.Fatal("restart lost smaller AI risk budget")
	}
	// This reprice is below the account's 1 USD risk cap but above AI's 0.8 USD.
	_, err = b.expireOrRepriceMakerOrder(order.OrderID, &market.BinanceDepthSnapshot{Bids: [][]string{{"100.5", "10"}}, Asks: [][]string{{"101", "10"}}}, now.Add(time.Minute))
	if err == nil {
		t.Fatal("reprice silently raised AI's smaller risk budget")
	}
	if len(b.PendingOrders()) != 0 || len(b.RecentFills(0)) != 0 || b.Snapshot().Balance != 100 {
		t.Fatal("reprice rejection did not preserve account")
	}
}
