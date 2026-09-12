package trader

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

type staticPriceSource struct {
	prices map[string]float64
	depths map[string]*market.DepthSnapshot
}

func (s *staticPriceSource) GetMarketPrice(symbol string) (float64, error) {
	return s.prices[symbol], nil
}

func (s *staticPriceSource) GetDepth(symbol string, limit int) (*market.DepthSnapshot, error) {
	if s.depths != nil {
		if d, ok := s.depths[symbol]; ok {
			return d, nil
		}
	}
	p := s.prices[symbol]
	if p <= 0 {
		p = 50000.0
	}
	return &market.DepthSnapshot{
		Bids: [][]string{{"49999.0", "100.0"}},
		Asks: [][]string{{"50001.0", "100.0"}},
	}, nil
}

type failingLedger struct {
	failSave bool
}

func (f *failingLedger) SavePaperState(traderID string, stateJSON []byte) error {
	if f.failSave {
		return errors.New("simulated disk I/O error on SavePaperState")
	}
	return nil
}

func (f *failingLedger) LoadPaperState(traderID string) ([]byte, bool, error) {
	return nil, false, nil
}

func (f *failingLedger) DeletePaperState(traderID string) error {
	return nil
}

func TestPaperBroker_PartialCloseLong_ScaleDownProtection(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"BTCUSDT": 50000.0,
		},
		depths: map[string]*market.DepthSnapshot{
			"BTCUSDT": {
				Bids: [][]string{{"49999.0", "10.0"}},
				Asks: [][]string{{"49990.0", "10.0"}},
			},
		},
	}

	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10000.0,
		MakerFirst:     true,
		SlippageBPS:    0,
		TakerFeeBPS:    5, // 0.05%
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	// 1. Open Long 1.0 BTC at 50,000 USDT with leverage 10
	// StopLoss at 48,000 and TakeProfit at 55,000
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 50000.0, // 1.0 BTC at 50,000 USDT
		StopLoss:        48000.0,
		TakeProfit:      55000.0,
	})
	if err != nil {
		t.Fatalf("open_long failed: %v", err)
	}

	// Fill open maker order
	if _, err := broker.RefreshMakerOrders(time.Now().UTC()); err != nil {
		t.Fatalf("fill open maker order: %v", err)
	}

	broker.mu.Lock()
	pos, exists := broker.positions["BTCUSDT:long"]
	if !exists {
		broker.mu.Unlock()
		t.Fatalf("long position not found after open_long")
	}
	initQty := pos.Quantity
	initMargin := pos.InitialMargin
	initEntryFee := pos.EntryFee
	broker.mu.Unlock()

	// Set TakeProfit order for long position (target price 55,000)
	if err := broker.SetTakeProfit("BTCUSDT", "long", initQty, 55000.0); err != nil {
		t.Fatalf("SetTakeProfit failed: %v", err)
	}

	broker.mu.Lock()
	var tpOrderID int64
	var foundTP bool
	for id, order := range broker.pendingOrders {
		if order.Symbol == "BTCUSDT" && order.Side == "long" && order.Action == "take_profit" && order.ReduceOnly {
			tpOrderID = id
			foundTP = true
			if math.Abs(order.Quantity-initQty) > 1e-6 {
				t.Errorf("expected TP order quantity %.4f, got %.4f", initQty, order.Quantity)
			}
			break
		}
	}
	broker.mu.Unlock()

	if !foundTP {
		t.Fatalf("expected resting take_profit order for long position")
	}

	// 2. Partial Close 50% via close_long with close_ratio = 0.5
	ratio := 0.5
	closeFill, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol:     "BTCUSDT",
		Action:     "close_long",
		CloseRatio: &ratio,
	})
	if err != nil {
		t.Fatalf("close_long 50%% failed: %v", err)
	}

	expectedCloseQty := initQty * 0.5
	if math.Abs(closeFill.Quantity-expectedCloseQty) > 1e-6 {
		t.Errorf("expected close fill quantity %.4f, got %.4f", expectedCloseQty, closeFill.Quantity)
	}

	// Fill maker close order
	prices.depths = map[string]*market.DepthSnapshot{
		"BTCUSDT": {
			Bids: [][]string{{"50010.0", "10.0"}},
			Asks: [][]string{{"50015.0", "10.0"}},
		},
	}
	if _, err := broker.RefreshMakerOrders(time.Now().UTC()); err != nil {
		t.Fatalf("fill maker close: %v", err)
	}

	// 3. Inspect remaining position state
	broker.mu.Lock()
	remPos, exists := broker.positions["BTCUSDT:long"]
	if !exists {
		broker.mu.Unlock()
		t.Fatalf("long position should still exist after 50%% partial close")
	}
	if math.Abs(remPos.Quantity-expectedCloseQty) > 1e-6 {
		t.Errorf("expected remaining position quantity %.4f, got %.4f", expectedCloseQty, remPos.Quantity)
	}
	if math.Abs(remPos.InitialMargin-(initMargin*0.5)) > 1e-4 {
		t.Errorf("expected remaining margin %.4f, got %.4f", initMargin*0.5, remPos.InitialMargin)
	}
	if math.Abs(remPos.EntryFee-(initEntryFee*0.5)) > 1e-4 {
		t.Errorf("expected remaining entry fee %.4f, got %.4f", initEntryFee*0.5, remPos.EntryFee)
	}

	// 4. Verify TakeProfit order scaled down mathematically
	tpOrder, tpExists := broker.pendingOrders[tpOrderID]
	if !tpExists {
		broker.mu.Unlock()
		t.Fatalf("take profit order should still exist after partial close")
	}
	if math.Abs(tpOrder.RemainingQuantity-expectedCloseQty) > 1e-6 {
		t.Errorf("expected TP remaining quantity %.4f, got %.4f", expectedCloseQty, tpOrder.RemainingQuantity)
	}
	if math.Abs(tpOrder.Quantity-expectedCloseQty) > 1e-6 {
		t.Errorf("expected TP total quantity %.4f, got %.4f", expectedCloseQty, tpOrder.Quantity)
	}
	broker.mu.Unlock()

	// 5. Test stop loss protection on the remaining 50% position via ProcessPrice
	// Price drops to 47,990 (below StopLoss 48,000)
	slFills, err := broker.ProcessPrice("BTCUSDT", 47990.0, time.Now().UTC())
	if err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	if len(slFills) == 0 {
		t.Fatalf("expected StopLoss fill on remaining position")
	}
	if math.Abs(slFills[0].Quantity-expectedCloseQty) > 1e-6 {
		t.Errorf("expected StopLoss executed quantity %.4f, got %.4f", expectedCloseQty, slFills[0].Quantity)
	}

	// Position must now be completely closed
	broker.mu.Lock()
	_, exists = broker.positions["BTCUSDT:long"]
	broker.mu.Unlock()
	if exists {
		t.Fatalf("position should be fully closed after StopLoss triggered")
	}
}

func TestPaperBroker_PartialCloseShort_ScaleDownProtection(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"ETHUSDT": 3000.0,
		},
	}

	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10000.0,
		SlippageBPS:    0,
		TakerFeeBPS:    5,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	// 1. Open Short 10.0 ETH at 3,000 USDT (30,000 notional)
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:          "ETHUSDT",
		Action:          "open_short",
		Leverage:        5,
		PositionSizeUSD: 30000.0,
		StopLoss:        3200.0,
		TakeProfit:      2700.0,
	})
	if err != nil {
		t.Fatalf("open_short failed: %v", err)
	}

	broker.mu.Lock()
	pos, exists := broker.positions["ETHUSDT:short"]
	if !exists {
		broker.mu.Unlock()
		t.Fatalf("short position not found after open_short")
	}
	initQty := pos.Quantity
	broker.mu.Unlock()

	// 2. Partial close 40% via CloseShortPartial
	ratio := 0.4
	res, err := broker.CloseShortPartial("ETHUSDT", ratio)
	if err != nil {
		t.Fatalf("CloseShortPartial: %v", err)
	}
	execQty, _ := res["executedQty"].(float64)
	expectedCloseQty := initQty * ratio
	if math.Abs(execQty-expectedCloseQty) > 1e-6 {
		t.Errorf("expected close qty %.4f, got %.4f", expectedCloseQty, execQty)
	}

	// 3. Verify remaining quantity is 60%
	broker.mu.Lock()
	remPos, exists := broker.positions["ETHUSDT:short"]
	if !exists {
		broker.mu.Unlock()
		t.Fatalf("short position should exist")
	}
	expectedRemQty := initQty * 0.6
	if math.Abs(remPos.Quantity-expectedRemQty) > 1e-6 {
		t.Errorf("expected remainder %.4f, got %.4f", expectedRemQty, remPos.Quantity)
	}
	broker.mu.Unlock()
}

func TestPaperBroker_SequentialScaleOutAndPerformanceReconstruct(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"BTCUSDT": 50000.0,
		},
	}

	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10000.0,
		SlippageBPS:    0,
		TakerFeeBPS:    0,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	// 1. Open 1.0 BTC
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 50000.0,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}

	// Price goes up to 55,000: first scale-out 50% (0.5 BTC)
	prices.prices["BTCUSDT"] = 55000.0
	ratio := 0.5
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:     "BTCUSDT",
		Action:     "close_long",
		CloseRatio: &ratio,
	})
	if err != nil {
		t.Fatalf("first close: %v", err)
	}

	// Price goes up to 60,000: final full close (omitted CloseRatio -> 1.0)
	prices.prices["BTCUSDT"] = 60000.0
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol: "BTCUSDT",
		Action: "close_long",
	})
	if err != nil {
		t.Fatalf("second close: %v", err)
	}

	// Reconstruct performance
	perf := broker.Performance()
	if len(perf.ClosedTrades) != 2 {
		t.Fatalf("expected 2 closed trades from 2-step scale out, got %d", len(perf.ClosedTrades))
	}

	// Most recent trade is ClosedTrades[0]
	trade2 := perf.ClosedTrades[0]
	trade1 := perf.ClosedTrades[1]

	if math.Abs(trade1.Quantity-0.5) > 1e-6 || math.Abs(trade2.Quantity-0.5) > 1e-6 {
		t.Errorf("expected both trades 0.5, got trade1=%.4f trade2=%.4f", trade1.Quantity, trade2.Quantity)
	}
	if math.Abs(trade1.ExitPrice-55000.0) > 1e-4 {
		t.Errorf("trade1 exit price: expected 55000, got %.2f", trade1.ExitPrice)
	}
	if math.Abs(trade2.ExitPrice-60000.0) > 1e-4 {
		t.Errorf("trade2 exit price: expected 60000, got %.2f", trade2.ExitPrice)
	}

	// Profit check: (55000 - 50000)*0.5 = 2500; (60000 - 50000)*0.5 = 5000; total = 7500
	totalRealized := trade1.RealizedPnL + trade2.RealizedPnL
	if math.Abs(totalRealized-7500.0) > 1e-4 {
		t.Errorf("expected total realized PnL 7500, got %.2f", totalRealized)
	}
}

func TestPaperBroker_PersistenceRollbackOnFailure(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"BTCUSDT": 50000.0,
		},
	}

	ledger := &failingLedger{failSave: false}
	broker, err := NewPersistentPaperBroker(PaperBrokerConfig{
		InitialBalance: 10000.0,
		SlippageBPS:    0,
		TakerFeeBPS:    5,
	}, prices, ledger, "trader_rollback_test", nil)
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker: %v", err)
	}

	// 1. Open long
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 50000.0,
		StopLoss:        48000.0,
		TakeProfit:      55000.0,
	})
	if err != nil {
		t.Fatalf("open_long failed: %v", err)
	}

	// 2. Arm ledger to fail save
	ledger.failSave = true

	// 3. Attempt partial close 50%
	ratio := 0.5
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:     "BTCUSDT",
		Action:     "close_long",
		CloseRatio: &ratio,
	})
	if err == nil {
		t.Fatalf("expected error when SavePaperState fails")
	}

	// 4. Verify in-memory state was 100% restored and not half-deducted
	broker.mu.Lock()
	pos, exists := broker.positions["BTCUSDT:long"]
	if !exists {
		broker.mu.Unlock()
		t.Fatalf("position must exist after rollback")
	}
	if math.Abs(pos.Quantity-1.0) > 1e-6 {
		broker.mu.Unlock()
		t.Fatalf("position quantity must remain 1.0 after rollback, got %.4f", pos.Quantity)
	}
	if len(broker.fills) != 1 {
		broker.mu.Unlock()
		t.Fatalf("fills slice must be rolled back to 1 entry, got %d", len(broker.fills))
	}
	broker.mu.Unlock()
}

func TestPaperBroker_ExecutionValidation_RejectsInvalidRatios(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"BTCUSDT": 50000.0,
		},
	}
	broker, _ := NewPaperBroker(PaperBrokerConfig{InitialBalance: 10000}, prices)
	broker.ExecuteDecision(&kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 50000.0,
	})

	invalidValues := []float64{-0.5, 0.0, 1.5, math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, val := range invalidValues {
		// Direct partial method
		if _, err := broker.CloseLongPartial("BTCUSDT", val); err == nil {
			t.Errorf("CloseLongPartial should reject invalid ratio %v", val)
		}
		// ExecuteDecision with ratio pointer
		r := val
		if _, err := broker.ExecuteDecision(&kernel.Decision{
			Symbol:     "BTCUSDT",
			Action:     "close_long",
			CloseRatio: &r,
		}); err == nil {
			t.Errorf("ExecuteDecision should reject invalid ratio %v", val)
		}
	}

	// Ensure position quantity is still intact 1.0
	broker.mu.Lock()
	pos := broker.positions["BTCUSDT:long"]
	if math.Abs(pos.Quantity-1.0) > 1e-6 {
		t.Errorf("position quantity was corrupted, got %.4f", pos.Quantity)
	}
	broker.mu.Unlock()
}

type dummyLiveTraderWithoutPartialClose struct{}

func (d *dummyLiveTraderWithoutPartialClose) GetBalance() (map[string]interface{}, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) GetPositions() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"symbol": "BTCUSDT", "side": "long", "positionAmt": 1.0, "entryPrice": 50000.0},
		{"symbol": "ETHUSDT", "side": "short", "positionAmt": -10.0, "entryPrice": 3000.0},
	}, nil
}
func (d *dummyLiveTraderWithoutPartialClose) OpenLong(string, float64, int) (map[string]interface{}, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) OpenShort(string, float64, int) (map[string]interface{}, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) CloseLong(string, float64) (map[string]interface{}, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) CloseShort(string, float64) (map[string]interface{}, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) SetLeverage(string, int) error            { return nil }
func (d *dummyLiveTraderWithoutPartialClose) SetMarginMode(string, bool) error        { return nil }
func (d *dummyLiveTraderWithoutPartialClose) GetMarketPrice(string) (float64, error) { return 50000.0, nil }
func (d *dummyLiveTraderWithoutPartialClose) SetStopLoss(string, string, float64, float64) error {
	return nil
}
func (d *dummyLiveTraderWithoutPartialClose) SetTakeProfit(string, string, float64, float64) error {
	return nil
}
func (d *dummyLiveTraderWithoutPartialClose) CancelStopLossOrders(string) error   { return nil }
func (d *dummyLiveTraderWithoutPartialClose) CancelTakeProfitOrders(string) error { return nil }
func (d *dummyLiveTraderWithoutPartialClose) CancelAllOrders(string) error        { return nil }
func (d *dummyLiveTraderWithoutPartialClose) CancelOrder(string, string) error    { return nil }
func (d *dummyLiveTraderWithoutPartialClose) CancelStopOrders(string) error       { return nil }
func (d *dummyLiveTraderWithoutPartialClose) FormatQuantity(string, float64) (string, error) {
	return "1", nil
}
func (d *dummyLiveTraderWithoutPartialClose) GetOrderStatus(string, string) (map[string]interface{}, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) GetClosedPnL(time.Time, int) ([]ClosedPnLRecord, error) {
	return nil, nil
}
func (d *dummyLiveTraderWithoutPartialClose) GetOpenOrders(string) ([]OpenOrder, error) {
	return nil, nil
}

type countingMarketDataProvider struct {
	market.MarketDataProvider
	callCount int
}

func (c *countingMarketDataProvider) GetCurrentPriceFresh(symbol string) (float64, error) {
	c.callCount++
	return 50000.0, nil
}

func TestAutoTrader_LiveCapabilityGateRejectsBothSides(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	liveTrader := &dummyLiveTraderWithoutPartialClose{}
	countingMarket := &countingMarketDataProvider{}

	at := &AutoTrader{
		id:                 "trader_live_test",
		exchange:           "binance",
		executionMode:      ExecutionModeLive,
		trader:             liveTrader,
		store:              st,
		marketDataProvider: countingMarket,
	}

	ratio := 0.5

	// 1. Long side rejection without network calls
	err = at.executeCloseLongWithRecord(&kernel.Decision{
		Symbol:     "BTCUSDT",
		Action:     "close_long",
		CloseRatio: &ratio,
	}, &store.DecisionAction{})
	if err == nil {
		t.Fatalf("expected live long partial close to be rejected")
	}
	if countingMarket.callCount != 0 {
		t.Fatalf("capability gate must be evaluated before fetching price; callCount = %d", countingMarket.callCount)
	}

	// 2. Short side rejection without network calls
	err = at.executeCloseShortWithRecord(&kernel.Decision{
		Symbol:     "ETHUSDT",
		Action:     "close_short",
		CloseRatio: &ratio,
	}, &store.DecisionAction{})
	if err == nil {
		t.Fatalf("expected live short partial close to be rejected")
	}
	if countingMarket.callCount != 0 {
		t.Fatalf("capability gate must be evaluated before fetching price; callCount = %d", countingMarket.callCount)
	}
}

func TestPaperBroker_MakerFirst_PartialCloseLong_ScaleDownProtection(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"BTCUSDT": 50000.0,
		},
		depths: map[string]*market.DepthSnapshot{
			"BTCUSDT": {
				Bids: [][]string{{"49999.0", "10.0"}},
				Asks: [][]string{{"50001.0", "10.0"}},
			},
		},
	}

	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10000.0,
		MakerFirst:     true,
		MakerFeeBPS:    2,
		TakerFeeBPS:    5,
		MakerTimeout:   15 * time.Second,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	// 1. Open long 1.0 BTC
	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 50000.0,
		StopLoss:        48000.0,
		TakeProfit:      55000.0,
	})
	if err != nil {
		t.Fatalf("open_long failed: %v", err)
	}

	// In MakerFirst mode, open_long places a resting maker order. Refresh with favorable ask depth to fill it.
	prices.depths = map[string]*market.DepthSnapshot{
		"BTCUSDT": {
			Bids: [][]string{{"49999.0", "10.0"}},
			Asks: [][]string{{"49990.0", "10.0"}},
		},
	}
	openFills, err := broker.RefreshMakerOrders(time.Now().UTC())
	if err != nil || len(openFills) == 0 {
		t.Fatalf("failed to fill open maker order: fills=%v, err=%v", openFills, err)
	}

	broker.mu.Lock()
	initPos := broker.positions["BTCUSDT:long"]
	initQty := initPos.Quantity
	broker.mu.Unlock()

	// 2. Request partial close: close_ratio = 0.5 under MakerFirst
	ratio := 0.5
	fill, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol:     "BTCUSDT",
		Action:     "close_long",
		CloseRatio: &ratio,
	})
	if err != nil {
		t.Fatalf("partial close failed: %v", err)
	}

	expectedHalf := initQty * 0.5
	if math.Abs(fill.Quantity-expectedHalf) > 1e-6 {
		t.Errorf("expected resting Maker order quantity %.4f, got %.4f", expectedHalf, fill.Quantity)
	}
	if !fill.IsMaker {
		t.Errorf("expected order to be Maker order")
	}

	// 3. Simulate maker order execution via RefreshMakerOrders
	prices.depths = map[string]*market.DepthSnapshot{
		"BTCUSDT": {
			Bids: [][]string{{"50010.0", "10.0"}},
			Asks: [][]string{{"50015.0", "10.0"}},
		},
	}
	makerFills, err := broker.RefreshMakerOrders(time.Now().UTC())
	if err != nil {
		t.Fatalf("RefreshMakerOrders: %v", err)
	}
	if len(makerFills) == 0 {
		t.Fatalf("expected maker order to fill")
	}

	broker.mu.Lock()
	defer broker.mu.Unlock()

	// 4. Verify position was NOT deleted: 50% remainder must still exist!
	remPos, exists := broker.positions["BTCUSDT:long"]
	if !exists {
		t.Fatalf("position must NOT be deleted when 50%% maker order fills")
	}
	if math.Abs(remPos.Quantity-expectedHalf) > 1e-6 {
		t.Errorf("expected remaining position quantity %.4f, got %.4f", expectedHalf, remPos.Quantity)
	}
}

func TestAutoTrader_ExecuteDecisionWithRecord_MakerFirst_PartialClose(t *testing.T) {
	prices := &staticPriceSource{
		prices: map[string]float64{
			"BTCUSDT": 50000.0,
		},
		depths: map[string]*market.DepthSnapshot{
			"BTCUSDT": {
				Bids: [][]string{{"49999.0", "10.0"}},
				Asks: [][]string{{"50001.0", "10.0"}},
			},
		},
	}

	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10000.0,
		MakerFirst:     true,
		MakerFeeBPS:    2,
		TakerFeeBPS:    5,
		MakerTimeout:   15 * time.Second,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	at := &AutoTrader{
		id:                 "trader_mf_test",
		exchange:           "binance",
		executionMode:      ExecutionModePaper,
		paperBroker:        broker,
		trader:             broker,
		store:              st,
		marketDataProvider: &dummyMarketDataProvider{price: 50000.0},
	}

	// 1. Open long
	action1 := &store.DecisionAction{}
	err = at.executeDecisionWithRecord(&kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 50000.0,
		StopLoss:        48000.0,
		TakeProfit:      55000.0,
	}, action1)
	if err != nil {
		t.Fatalf("open_long failed: %v", err)
	}

	// Fill open maker order
	prices.depths = map[string]*market.DepthSnapshot{
		"BTCUSDT": {
			Bids: [][]string{{"49999.0", "10.0"}},
			Asks: [][]string{{"49990.0", "10.0"}},
		},
	}
	openFills, err := broker.RefreshMakerOrders(time.Now().UTC())
	if err != nil || len(openFills) == 0 {
		t.Fatalf("failed to fill open maker order: fills=%v, err=%v", openFills, err)
	}

	broker.mu.Lock()
	initPos := broker.positions["BTCUSDT:long"]
	initQty := initPos.Quantity
	broker.mu.Unlock()

	// 2. End-to-end partial close 50%
	ratio := 0.5
	action2 := &store.DecisionAction{}
	err = at.executeDecisionWithRecord(&kernel.Decision{
		Symbol:     "BTCUSDT",
		Action:     "close_long",
		CloseRatio: &ratio,
	}, action2)
	if err != nil {
		t.Fatalf("partial close failed: %v", err)
	}

	// 3. Fill maker order
	prices.depths = map[string]*market.DepthSnapshot{
		"BTCUSDT": {
			Bids: [][]string{{"50010.0", "10.0"}},
			Asks: [][]string{{"50015.0", "10.0"}},
		},
	}
	_, err = broker.RefreshMakerOrders(time.Now().UTC())
	if err != nil {
		t.Fatalf("RefreshMakerOrders: %v", err)
	}

	broker.mu.Lock()
	defer broker.mu.Unlock()

	expectedHalf := initQty * 0.5
	pos, exists := broker.positions["BTCUSDT:long"]
	if !exists {
		t.Fatalf("remainder position must exist")
	}
	if math.Abs(pos.Quantity-expectedHalf) > 1e-6 {
		t.Errorf("expected position %.4f, got %.4f", expectedHalf, pos.Quantity)
	}
}

type dummyMarketDataProvider struct {
	market.MarketDataProvider
	price float64
}

func (d *dummyMarketDataProvider) Exchange() string                               { return "binance" }
func (d *dummyMarketDataProvider) NormalizeSymbol(s string) string               { return s }
func (d *dummyMarketDataProvider) Capabilities() market.MarketCapability         { return market.CapabilityPrice }
func (d *dummyMarketDataProvider) GetCurrentPrice(string) (float64, error)       { return d.price, nil }
func (d *dummyMarketDataProvider) GetCurrentPriceFresh(string) (float64, error)  { return d.price, nil }
func (d *dummyMarketDataProvider) ValidateMarketAvailability(string) (*market.MarketAvailability, error) {
	return &market.MarketAvailability{Symbol: "BTCUSDT", Price: d.price, CheckedAt: time.Now()}, nil
}
