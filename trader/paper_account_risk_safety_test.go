package trader

import (
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/market"
	"sync"
	"testing"
	"time"
)

func TestPaperAccountRiskPartialFillDoesNotInvalidateItsOwnReservation(t *testing.T) {
	now := time.Now().UTC()
	cfg := accountRiskConfig(t, true)
	cfg.Clock = func() time.Time { return now }
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	b, err := NewPaperBroker(cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	order, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 5, StopLoss: 95, TakeProfit: 115})
	if err != nil {
		t.Fatal(err)
	}
	depth := &market.BinanceDepthSnapshot{Bids: src.bids, Asks: src.asks}
	for i := 0; i < 2; i++ {
		if _, err = b.applyMakerFill(order.OrderID, order.Quantity/2, depth, now); err != nil {
			t.Fatalf("partial fill %d: %v", i, err)
		}
	}
	p := b.positions["MUUSDT:long"]
	risk, err := paperPositionRisk(p.Side, p.EntryPrice, p.StopLoss, p.Quantity, b.accountRiskCostRatioLocked())
	if err != nil {
		t.Fatal(err)
	}
	if risk > b.Snapshot().Equity*cfg.MaxTradeRiskRatio+1e-9 {
		t.Fatalf("filled risk %.9f exceeds post-fee equity budget", risk)
	}
}

func TestPaperAccountRiskRepriceRejectsAdverseRiskWithoutCharging(t *testing.T) {
	now := time.Now().UTC()
	cfg := accountRiskConfig(t, true)
	cfg.Clock = func() time.Time { return now }
	cfg.MakerMaxReprices = 2
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	b, err := NewPaperBroker(cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	order, err := b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 5, StopLoss: 95, TakeProfit: 115})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.expireOrRepriceMakerOrder(order.OrderID, &market.BinanceDepthSnapshot{Bids: [][]string{{"101", "10"}}, Asks: [][]string{{"102", "10"}}}, now.Add(time.Minute))
	if err == nil {
		t.Fatal("adverse reprice was not risk-rejected")
	}
	if b.Snapshot().Balance != 100 || len(b.RecentFills(10)) != 0 || len(b.PendingOrders()) != 0 {
		t.Fatal("reprice rejection changed financial ledger or left unsafe order")
	}
}

func TestPaperAccountRiskLegacyExposureBlocksNewEntriesButNotStopLoss(t *testing.T) {
	now := time.Now().UTC()
	cfg := accountRiskConfig(t, false)
	cfg.Clock = func() time.Time { return now }
	src := fixedPaperPriceSource{"MUUSDT": 100, "SOXLUSDT": 100}
	b, err := NewPaperBroker(cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	b.positions["MUUSDT:long"] = PaperPosition{Symbol: "MUUSDT", Side: "long", Quantity: 1, EntryPrice: 100, Leverage: 5, StopLoss: 95, TakeProfit: 115, EntryTime: now, InitialMargin: 20}
	b.marks["MUUSDT"] = 100
	b.markTimes["MUUSDT"] = now
	if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "SOXLUSDT", Action: "open_long", PositionSizeUSD: 30, Leverage: 5, StopLoss: 95, TakeProfit: 115}); err == nil {
		t.Fatal("legacy >2% exposure did not block new entry")
	}
	if b.positions["MUUSDT:long"].StopLoss != 95 {
		t.Fatal("new entry policy changed existing protection")
	}
	fills, err := b.ProcessPrice("MUUSDT", 94, now.Add(time.Second))
	if err != nil || len(fills) != 1 {
		t.Fatalf("risk policy blocked protective exit: %v %+v", err, fills)
	}
}

func TestPaperAccountRiskReservationsAreAtomicAcrossConcurrentEntries(t *testing.T) {
	cfg := accountRiskConfig(t, true)
	src := &fixedPaperMakerSource{price: 100, bids: [][]string{{"100", "10"}}, asks: [][]string{{"100", "10"}}}
	b, err := NewPaperBroker(cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = b.ExecuteDecision(&kernel.Decision{Symbol: fmt.Sprintf("TEST%dUSDT", i), Action: "open_long", PositionSizeUSD: 300, Leverage: 5, StopLoss: 95, TakeProfit: 115})
		}(i)
	}
	wg.Wait()
	orders := b.PendingOrders()
	total := 0.0
	for _, o := range orders {
		r, e := paperPositionRisk(o.Side, o.LimitPrice, o.StopLoss, o.RemainingQuantity, b.accountRiskCostRatioLocked())
		if e != nil {
			t.Fatal(e)
		}
		total += r
	}
	if len(orders) != 2 || total > 2+1e-9 {
		t.Fatalf("atomic reservations: %d orders risk %.9f", len(orders), total)
	}
}

func TestPaperAccountRiskInvalidPoliciesFailClosed(t *testing.T) {
	for _, value := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		cfg := accountRiskConfig(t, false)
		cfg.MaxTradeRiskRatio = value
		if _, err := NewPaperBroker(cfg, fixedPaperPriceSource{"MUUSDT": 100}); err == nil {
			t.Fatalf("accepted invalid policy %v", value)
		}
	}
}
