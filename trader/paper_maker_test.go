package trader

import (
	"path/filepath"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

type fixedPaperMakerSource struct {
	price float64
	bids  [][]string
	asks  [][]string
}

func (s *fixedPaperMakerSource) GetMarketPrice(string) (float64, error) {
	return s.price, nil
}

func (s *fixedPaperMakerSource) GetDepth(string, int) (*market.BinanceDepthSnapshot, error) {
	return &market.BinanceDepthSnapshot{Bids: s.bids, Asks: s.asks}, nil
}

func TestPaperMakerOpenCreatesPendingOrderWithoutChargingOrOpeningPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		MakerFirst:     true,
		MakerFeeBPS:    2,
		TakerFeeBPS:    5,
		MakerTimeout:   15 * time.Second,
		Clock:          func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	accepted, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if accepted.Status != "NEW" || !accepted.IsMaker || accepted.Price != 99.9 || accepted.Fee != 0 {
		t.Fatalf("accepted order = %#v", accepted)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.Fees != 0 || snapshot.PendingOrders != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	orders := broker.PendingOrders()
	if len(orders) != 1 || orders[0].Action != "open_long" || orders[0].LimitPrice != 99.9 || orders[0].Status != "NEW" {
		t.Fatalf("pending orders = %#v", orders)
	}
}

func TestPaperTakerOpenRespectsReservedMakerMargin(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		MakerFirst:     true,
		MakerFeeBPS:    2,
		TakerFeeBPS:    5,
		MakerTimeout:   15 * time.Second,
		Clock:          func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("maker open: %v", err)
	}

	// Use a different symbol and force the next order through the taker path.
	// The maker order reserves 300/3 = 100 USD of margin, leaving only 900 USD
	// before fees; the 2,700 USD taker request must therefore be rejected.
	broker.config.MakerFirst = false
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "ETHUSDT", Action: "open_long", PositionSizeUSD: 2_700, Leverage: 3,
	}); err == nil {
		t.Fatal("expected taker open to reject margin already reserved by maker order")
	}
}

func TestPaperMakerOrderDoesNotFillBeforeMarketCrossesItsLimit(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}

	if _, err := broker.RefreshMakerOrders(now.Add(5 * time.Second)); err != nil {
		t.Fatalf("RefreshMakerOrders: %v", err)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.PendingOrders != 1 || snapshot.Fees != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPaperMakerOrderPartiallyFillsAtRestingPriceWithMakerFee(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	accepted, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}

	source.asks = [][]string{{"99.80", "1.25"}}
	fills, err := broker.RefreshMakerOrders(now.Add(5 * time.Second))
	if err != nil {
		t.Fatalf("RefreshMakerOrders: %v", err)
	}
	if len(fills) != 1 || fills[0].Status != "PARTIALLY_FILLED" || !fills[0].IsMaker {
		t.Fatalf("fills = %#v", fills)
	}
	assertPaperFloat(t, "fill price", fills[0].Price, accepted.Price)
	assertPaperFloat(t, "fill quantity", fills[0].Quantity, 1.25)
	assertPaperFloat(t, "maker fee", fills[0].Fee, 99.9*1.25*2/10_000)

	positions, err := broker.GetPositions()
	if err != nil || len(positions) != 1 {
		t.Fatalf("positions = %#v, err=%v", positions, err)
	}
	assertPaperFloat(t, "position quantity", positions[0]["quantity"].(float64), 1.25)
	orders := broker.PendingOrders()
	if len(orders) != 1 || orders[0].Status != "PARTIALLY_FILLED" {
		t.Fatalf("pending orders = %#v", orders)
	}
	assertPaperFloat(t, "remaining quantity", orders[0].RemainingQuantity, accepted.Quantity-1.25)
}

func TestPaperMakerEntryRepricesOnceThenCancelsWithoutFee(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second, MakerMaxReprices: 1,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	first, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}

	source.bids = [][]string{{"99.80", "5"}}
	if _, err := broker.RefreshMakerOrders(now.Add(16 * time.Second)); err != nil {
		t.Fatalf("first timeout: %v", err)
	}
	orders := broker.PendingOrders()
	if len(orders) != 1 || orders[0].OrderID == first.OrderID || orders[0].LimitPrice != 99.8 || orders[0].RepriceCount != 1 {
		t.Fatalf("repriced orders = %#v", orders)
	}

	source.bids = [][]string{{"99.70", "5"}}
	if _, err := broker.RefreshMakerOrders(now.Add(32 * time.Second)); err != nil {
		t.Fatalf("second timeout: %v", err)
	}
	if orders := broker.PendingOrders(); len(orders) != 0 {
		t.Fatalf("orders after max reprices = %#v", orders)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.Fees != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPaperMakerOpenFillCreatesRestingReduceOnlyTakeProfit(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	accepted, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.asks = [][]string{{"99.80", "10"}}
	fills, err := broker.RefreshMakerOrders(now.Add(5 * time.Second))
	if err != nil {
		t.Fatalf("RefreshMakerOrders: %v", err)
	}
	if len(fills) != 1 || fills[0].Status != "FILLED" {
		t.Fatalf("fills = %#v", fills)
	}
	orders := broker.PendingOrders()
	if len(orders) != 1 || orders[0].Action != "take_profit" || !orders[0].ReduceOnly || orders[0].LimitPrice != 110 {
		t.Fatalf("take-profit orders = %#v", orders)
	}
	assertPaperFloat(t, "take-profit quantity", orders[0].Quantity, accepted.Quantity)
}

func TestPaperMakerTakeProfitClosesWithMakerFee(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	accepted, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, TakeProfit: 110,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.asks = [][]string{{"99.80", "10"}}
	if _, err := broker.RefreshMakerOrders(now.Add(5 * time.Second)); err != nil {
		t.Fatalf("entry fill: %v", err)
	}

	source.bids = [][]string{{"110.10", "10"}}
	source.asks = [][]string{{"110.20", "10"}}
	fills, err := broker.RefreshMakerOrders(now.Add(10 * time.Second))
	if err != nil {
		t.Fatalf("take-profit fill: %v", err)
	}
	if len(fills) != 1 || fills[0].Action != "take_profit" || !fills[0].IsMaker || fills[0].Status != "FILLED" {
		t.Fatalf("fills = %#v", fills)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.PendingOrders != 0 || snapshot.ClosedTrades != 1 || snapshot.TakerFees != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	} else {
		wantMakerFees := accepted.Price*accepted.Quantity*2/10_000 + 110*accepted.Quantity*2/10_000
		assertPaperFloat(t, "maker fees", snapshot.MakerFees, wantMakerFees)
	}
}

func TestPaperMakerStopLossUsesImmediateTakerExitAndCancelsTakeProfit(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, SlippageBPS: 2, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.asks = [][]string{{"99.80", "10"}}
	if _, err := broker.RefreshMakerOrders(now.Add(5 * time.Second)); err != nil {
		t.Fatalf("entry fill: %v", err)
	}

	exits, err := broker.ProcessPrice("MUUSDT", 89, now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	if len(exits) != 1 || exits[0].Action != "stop_loss" || exits[0].IsMaker {
		t.Fatalf("exits = %#v", exits)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.PendingOrders != 0 || snapshot.TakerFees <= 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPaperMakerTakeProfitIsNotConvertedIntoSyntheticMarketExit(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.asks = [][]string{{"99.80", "10"}}
	if _, err := broker.RefreshMakerOrders(now.Add(5 * time.Second)); err != nil {
		t.Fatalf("entry fill: %v", err)
	}

	exits, err := broker.ProcessPrice("MUUSDT", 111, now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	if len(exits) != 0 || broker.Snapshot().OpenPositions != 1 {
		t.Fatalf("market exits = %#v, snapshot=%#v", exits, broker.Snapshot())
	}
}

func TestPaperMakerOrdinaryCloseCreatesReduceOnlyPendingOrder(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, TakerFeeBPS: 5,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("market setup open_long: %v", err)
	}
	broker.config.MakerFirst = true
	broker.config.MakerFeeBPS = 2
	broker.config.MakerTimeout = 15 * time.Second

	accepted, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"})
	if err != nil {
		t.Fatalf("close_long: %v", err)
	}
	if accepted.Status != "NEW" || !accepted.IsMaker || accepted.Price != 100.1 || accepted.Fee != 0 {
		t.Fatalf("accepted close = %#v", accepted)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 1 || snapshot.PendingOrders != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	orders := broker.PendingOrders()
	if len(orders) != 1 || !orders[0].ReduceOnly || orders[0].Action != "close_long" {
		t.Fatalf("pending orders = %#v", orders)
	}
}

func TestPaperMakerOrdinaryCloseFallsBackToTakerAfterTimeout(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, TakerFeeBPS: 5, SlippageBPS: 2,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("market setup open_long: %v", err)
	}
	broker.config.MakerFirst = true
	broker.config.MakerFeeBPS = 2
	broker.config.MakerTimeout = 15 * time.Second
	broker.config.MakerMaxReprices = 0
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatalf("close_long: %v", err)
	}

	fills, err := broker.RefreshMakerOrders(now.Add(16 * time.Second))
	if err != nil {
		t.Fatalf("RefreshMakerOrders: %v", err)
	}
	if len(fills) != 1 || fills[0].Action != "close_long" || fills[0].IsMaker || fills[0].Status != "FILLED" {
		t.Fatalf("fallback fills = %#v", fills)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.PendingOrders != 0 || snapshot.TakerFees <= 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPaperMakerPendingAndPartialFillSurviveRestart(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	st, err := store.New(filepath.Join(t.TempDir(), "paper-maker.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	config := PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}
	first, err := NewPersistentPaperBroker(config, source, st.Paper(), "maker-restart")
	if err != nil {
		t.Fatalf("first broker: %v", err)
	}
	if _, err := first.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}

	restored, err := NewPersistentPaperBroker(config, source, st.Paper(), "maker-restart")
	if err != nil {
		t.Fatalf("restored broker: %v", err)
	}
	if orders := restored.PendingOrders(); len(orders) != 1 || restored.Snapshot().Fees != 0 {
		t.Fatalf("restored pending state = %#v, snapshot=%#v", orders, restored.Snapshot())
	}
	source.asks = [][]string{{"99.80", "1.25"}}
	if _, err := restored.RefreshMakerOrders(now.Add(5 * time.Second)); err != nil {
		t.Fatalf("partial fill after restart: %v", err)
	}

	again, err := NewPersistentPaperBroker(config, source, st.Paper(), "maker-restart")
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if snapshot := again.Snapshot(); snapshot.OpenPositions != 1 || snapshot.PendingOrders != 1 || snapshot.MakerFees <= 0 {
		t.Fatalf("second restored snapshot = %#v", snapshot)
	}
	if fills := again.RecentFills(10); len(fills) != 1 || !fills[0].IsMaker {
		t.Fatalf("restored fills = %#v", fills)
	}
}

func TestPaperMakerPartialFillsReconstructOneClosedTrade(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	accepted, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, TakeProfit: 110,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.asks = [][]string{{"99.80", "1"}}
	if _, err := broker.RefreshMakerOrders(now.Add(time.Second)); err != nil {
		t.Fatalf("first entry fill: %v", err)
	}
	source.asks = [][]string{{"99.80", "10"}}
	if _, err := broker.RefreshMakerOrders(now.Add(2 * time.Second)); err != nil {
		t.Fatalf("second entry fill: %v", err)
	}
	source.bids = [][]string{{"110.10", "1"}}
	if _, err := broker.RefreshMakerOrders(now.Add(3 * time.Second)); err != nil {
		t.Fatalf("first exit fill: %v", err)
	}
	source.bids = [][]string{{"110.10", "10"}}
	if _, err := broker.RefreshMakerOrders(now.Add(4 * time.Second)); err != nil {
		t.Fatalf("second exit fill: %v", err)
	}

	performance := broker.Performance()
	if performance.TotalTrades != 1 || len(performance.ClosedTrades) != 1 {
		t.Fatalf("performance = %#v", performance)
	}
	trade := performance.ClosedTrades[0]
	assertPaperFloat(t, "closed quantity", trade.Quantity, accepted.Quantity)
	assertPaperFloat(t, "closed fees", trade.Fee, performance.TotalFees)
	assertPaperFloat(t, "maker fee split", performance.MakerFees, performance.TotalFees)
	assertPaperFloat(t, "taker fee split", performance.TakerFees, 0)
}

func TestPaperMakerRepriceAndCancelRemainAuditable(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		MakerTimeout: 15 * time.Second, MakerMaxReprices: 1,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.bids = [][]string{{"99.80", "5"}}
	if _, err := broker.RefreshMakerOrders(now.Add(16 * time.Second)); err != nil {
		t.Fatalf("reprice: %v", err)
	}
	source.bids = [][]string{{"99.70", "5"}}
	if _, err := broker.RefreshMakerOrders(now.Add(32 * time.Second)); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	events := broker.RecentOrderEvents(10)
	if len(events) != 4 || events[0].Status != "NEW" || events[1].Reason != "reprice" || events[2].Status != "NEW" || events[3].Reason != "max_reprices" {
		t.Fatalf("events = %#v", events)
	}
}

func TestPaperMakerRejectsDuplicateEntryWhileOrderIsPending(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		MakerTimeout: 15 * time.Second, Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	decision := &kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3}
	if _, err := broker.ExecuteDecision(decision); err != nil {
		t.Fatalf("first open_long: %v", err)
	}
	if _, err := broker.ExecuteDecision(decision); err == nil {
		t.Fatal("duplicate maker entry should be rejected while the first order is pending")
	}
	if got := len(broker.PendingOrders()); got != 1 {
		t.Fatalf("pending orders = %d, want 1", got)
	}
}

func TestPaperMakerStopLossCancelsUnfilledEntryRemainder(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "5"}},
		asks:  [][]string{{"100.10", "5"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 95, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	source.asks = [][]string{{"99.80", "1"}}
	if _, err := broker.RefreshMakerOrders(now.Add(time.Second)); err != nil {
		t.Fatalf("partial entry fill: %v", err)
	}
	if _, err := broker.ProcessPrice("MUUSDT", 94, now.Add(2*time.Second)); err != nil {
		t.Fatalf("stop loss: %v", err)
	}
	if got := len(broker.PendingOrders()); got != 0 {
		t.Fatalf("pending orders after stop loss = %d, want 0", got)
	}
}

func TestPaperMakerRestoreCreatesTakeProfitForLegacyOpenPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	st, err := store.New(filepath.Join(t.TempDir(), "paper-maker-migration.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	legacy, err := NewPersistentPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, TakerFeeBPS: 5, Clock: func() time.Time { return now },
	}, source, st.Paper(), "legacy-maker-migration")
	if err != nil {
		t.Fatalf("legacy broker: %v", err)
	}
	if _, err := legacy.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 95, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("legacy open_long: %v", err)
	}

	restored, err := NewPersistentPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now.Add(time.Minute) },
	}, source, st.Paper(), "legacy-maker-migration")
	if err != nil {
		t.Fatalf("restored broker: %v", err)
	}
	orders := restored.PendingOrders()
	if len(orders) != 1 || !orders[0].ReduceOnly || orders[0].Action != "take_profit" || orders[0].LimitPrice != 110 {
		t.Fatalf("restored take-profit orders = %#v", orders)
	}
}

func TestPaperMakerShortTakeProfitRestsAsBuyAndFillsFromAsks(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	source := &fixedPaperMakerSource{
		price: 100,
		bids:  [][]string{{"99.90", "10"}},
		asks:  [][]string{{"100.10", "10"}},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, MakerFirst: true, MakerFeeBPS: 2,
		TakerFeeBPS: 5, MakerTimeout: 15 * time.Second,
		Clock: func() time.Time { return now },
	}, source)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_short", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 105, TakeProfit: 90,
	}); err != nil {
		t.Fatalf("open_short: %v", err)
	}
	source.bids = [][]string{{"100.20", "10"}}
	if _, err := broker.RefreshMakerOrders(now.Add(time.Second)); err != nil {
		t.Fatalf("entry fill: %v", err)
	}
	orders := broker.PendingOrders()
	if len(orders) != 1 || orders[0].Action != "take_profit_short" {
		t.Fatalf("short take-profit order = %#v", orders)
	}
	source.asks = [][]string{{"89.90", "10"}}
	fills, err := broker.RefreshMakerOrders(now.Add(2 * time.Second))
	if err != nil {
		t.Fatalf("take-profit fill: %v", err)
	}
	if len(fills) != 1 || fills[0].Action != "take_profit_short" || broker.Snapshot().OpenPositions != 0 {
		t.Fatalf("short take-profit fills = %#v, snapshot=%#v", fills, broker.Snapshot())
	}
}
