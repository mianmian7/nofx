package trader

import (
	"encoding/json"
	"math"
	"nofx/kernel"
	"nofx/market"
	"testing"
	"time"
)

func accountRiskConfig(t *testing.T, maker bool) PaperBrokerConfig {
	t.Helper()
	var cfg PaperBrokerConfig
	// Decode so the regression demonstrates ignored risk settings on the old broker.
	if err := json.Unmarshal([]byte(`{"InitialBalance":100,"MaxTradeRiskRatio":0.01,"MaxPortfolioRiskRatio":0.02,"MinEntryNotional":12,"MakerFeeBPS":2,"TakerFeeBPS":5,"SlippageBPS":2}`), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.MakerFirst = maker
	return cfg
}

func TestPaperAccountRiskCapsOversizedAndMissingAIRiskBudgets(t *testing.T) {
	for _, maker := range []bool{false, true} {
		for _, aiRisk := range []float64{0, 8, 300} {
			for _, side := range []string{"long", "short"} {
				cfg := accountRiskConfig(t, maker)
				src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
				b, err := NewPaperBroker(cfg, src)
				if err != nil {
					t.Fatal(err)
				}
				stop, target := 95.0, 115.0
				if side == "short" {
					stop, target = 105, 85
				}
				f, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_" + side, PositionSizeUSD: 300, Leverage: 8, StopLoss: stop, TakeProfit: target, RiskUSD: aiRisk})
				if err != nil {
					t.Fatal(err)
				}
				risk := (math.Abs(f.Price-stop) + f.Price*0.002) * f.Quantity
				if risk > 1.00000001 {
					t.Fatalf("maker=%v side=%s AI risk=%v: accepted account loss %.8f > 1 USDT", maker, side, aiRisk, risk)
				}
			}
		}
	}
}

func TestPaperAccountRiskIncludesRestingEntriesAndRejectsSubMinimum(t *testing.T) {
	cfg := accountRiskConfig(t, true)
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	b, err := NewPaperBroker(cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"MUUSDT", "SOXLUSDT"} {
		if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: symbol, Action: "open_long", PositionSizeUSD: 30, Leverage: 5, StopLoss: 95, TakeProfit: 115}); err != nil {
			t.Fatal(err)
		}
	}
	before := b.Snapshot()
	if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "SNDKUSDT", Action: "open_long", PositionSizeUSD: 30, Leverage: 5, StopLoss: 95, TakeProfit: 115}); err == nil {
		t.Fatal("third pending entry exceeded 2% portfolio risk")
	}
	after := b.Snapshot()
	if after.PendingOrders != before.PendingOrders || after.Balance != before.Balance {
		t.Fatal("rejected entry changed ledger")
	}
}

func TestPaperAccountRiskRechecksOldPendingOrderBeforeFill(t *testing.T) {
	cfg := accountRiskConfig(t, true)
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	b, err := NewPaperBroker(cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	// Existing pre-migration order: the new policy must reject it at actual fill,
	// not just limit new proposals. No positions or protection exits are removed.
	now := time.Now().UTC()
	b.pendingOrders[1] = PaperPendingOrder{OrderID: 1, Symbol: "MUUSDT", Action: "open_long", Side: "long", LimitPrice: 100, Quantity: 3, RemainingQuantity: 3, PositionSizeUSD: 300, Leverage: 5, StopLoss: 95, TakeProfit: 115, Status: "NEW", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	b.nextID = 2
	_, err = b.applyMakerFill(1, 3, &market.BinanceDepthSnapshot{Bids: src.bids, Asks: src.asks}, now)
	if err == nil {
		t.Fatal("old oversized maker order filled under new risk cap")
	}
	if len(b.RecentFills(10)) != 0 || b.Snapshot().OpenPositions != 0 || b.Snapshot().Fees != 0 {
		t.Fatal("risk-rejected pending order mutated financial ledger")
	}
	if len(b.PendingOrders()) != 0 {
		t.Fatal("unsafe pending remainder was not cancelled")
	}
}
