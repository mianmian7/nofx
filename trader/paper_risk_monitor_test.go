package trader

import (
	"errors"
	"sync"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
)

type failFirstCountingPriceSource struct {
	mu        sync.Mutex
	prices    map[string]float64
	calls     int
	failFirst bool
}

func (s *failFirstCountingPriceSource) GetMarketPrice(symbol string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failFirst {
		s.failFirst = false
		return 0, errors.New("temporary mark failure")
	}
	return s.prices[symbol], nil
}

type mutablePaperPriceSource struct {
	mu    sync.RWMutex
	price float64
	calls int
}

func (s *mutablePaperPriceSource) GetMarketPrice(string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.price, nil
}

func (s *mutablePaperPriceSource) Set(price float64) {
	s.mu.Lock()
	s.price = price
	s.mu.Unlock()
}

func (s *mutablePaperPriceSource) ResetCalls() {
	s.mu.Lock()
	s.calls = 0
	s.mu.Unlock()
}

func (s *mutablePaperPriceSource) Calls() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.calls
}

func TestPaperRiskMonitorClosesTakeProfitBetweenAIScanIntervals(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}

	strategy := store.GetDefaultStrategyConfig("en")
	strategy.CoinSource.SourceType = "static"
	strategy.CoinSource.StaticCoins = nil
	at := &AutoTrader{
		id: "paper-risk-monitor", name: "Paper Risk Monitor",
		executionMode: ExecutionModePaper,
		paperBroker:   broker, trader: broker,
		config: AutoTraderConfig{
			ScanInterval:             time.Hour,
			PaperRiskMonitorInterval: 5 * time.Millisecond,
		},
		strategyEngine:        kernel.NewStrategyEngine(&strategy, ""),
		initialBalance:        1_000,
		lastResetTime:         time.Now(),
		positionFirstSeenTime: make(map[string]int64),
		peakPnLCache:          make(map[string]float64),
	}
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool {
		return at.GetStatus()["call_count"].(int) == 1 && broker.Snapshot().OpenPositions == 1
	})

	prices.Set(111)
	waitForPaperCondition(t, time.Second, func() bool {
		return broker.Snapshot().OpenPositions == 0
	})
	if calls := at.GetStatus()["call_count"].(int); calls != 1 {
		t.Fatalf("AI cycle count = %d, want 1", calls)
	}
	at.Stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop promptly")
	}
}

func TestLiveModeDoesNotStartPaperRiskMonitor(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	prices.ResetCalls()

	at := newMonitorTestAutoTrader(ExecutionModeLive, broker, 2*time.Millisecond)
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool {
		return at.GetStatus()["call_count"].(int) == 1
	})
	time.Sleep(20 * time.Millisecond)
	if calls := prices.Calls(); calls != 0 {
		t.Fatalf("live mode paper price refresh calls = %d, want 0", calls)
	}
	at.Stop()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("live Run did not stop promptly")
	}
}

func TestRepeatedRunDoesNotStartAnotherPaperMonitor(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 5*time.Millisecond)
	firstDone := make(chan error, 1)
	go func() { firstDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool {
		return at.GetStatus()["call_count"].(int) == 1
	})

	secondDone := make(chan error, 1)
	go func() { secondDone <- at.Run() }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Run: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second Run did not return; duplicate runtime/monitor started")
	}
	if calls := at.GetStatus()["call_count"].(int); calls != 1 {
		t.Fatalf("AI cycle count after repeated Run = %d, want 1", calls)
	}
	at.Stop()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first Run did not stop promptly")
	}
}

func TestStopEndsPaperMonitorWhileTradingCycleIsBlocked(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	prices.ResetCalls()
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 2*time.Millisecond)
	cycleBlocked := make(chan struct{})
	at.cycleRunner = func() error {
		<-cycleBlocked
		return nil
	}
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool { return prices.Calls() > 0 })

	stopDone := make(chan struct{})
	go func() {
		at.Stop()
		close(stopDone)
	}()
	stoppedPromptly := false
	select {
	case <-stopDone:
		stoppedPromptly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(cycleBlocked)
	if !stoppedPromptly {
		select {
		case <-stopDone:
		case <-time.After(time.Second):
			t.Fatal("Stop remained blocked after trading cycle was released")
		}
		t.Fatal("Stop waited for a blocked trading/AI cycle instead of ending the Paper monitor promptly")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after blocked cycle was released")
	}
	callsAfterStop := prices.Calls()
	time.Sleep(10 * time.Millisecond)
	if calls := prices.Calls(); calls != callsAfterStop {
		t.Fatalf("Paper monitor continued after Stop: calls %d -> %d", callsAfterStop, calls)
	}
}

func TestPaperRefreshAttemptsEveryOpenSymbolAfterOnePriceError(t *testing.T) {
	prices := &failFirstCountingPriceSource{prices: map[string]float64{"MUUSDT": 100, "SNDKUSDT": 200}}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	for symbol, notional := range map[string]float64{"MUUSDT": 300, "SNDKUSDT": 300} {
		if _, err := broker.ExecuteDecision(&kernel.Decision{
			Symbol: symbol, Action: "open_long", PositionSizeUSD: notional, Leverage: 3,
		}); err != nil {
			t.Fatalf("open_long %s: %v", symbol, err)
		}
	}
	prices.mu.Lock()
	prices.calls = 0
	prices.failFirst = true
	prices.mu.Unlock()

	if err := broker.RefreshOpenPositions(); err == nil {
		t.Fatal("RefreshOpenPositions error = nil, want aggregated price error")
	}
	prices.mu.Lock()
	calls := prices.calls
	prices.mu.Unlock()
	if calls != 2 {
		t.Fatalf("price refresh calls = %d, want every 2 open symbols attempted", calls)
	}
}

func newMonitorTestAutoTrader(mode ExecutionMode, broker *PaperBroker, interval time.Duration) *AutoTrader {
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.CoinSource.SourceType = "static"
	strategy.CoinSource.StaticCoins = nil
	return &AutoTrader{
		id: "risk-monitor", name: "Risk Monitor",
		executionMode: mode, paperBroker: broker, trader: broker,
		config: AutoTraderConfig{
			ScanInterval: time.Hour, PaperRiskMonitorInterval: interval,
		},
		strategyEngine:        kernel.NewStrategyEngine(&strategy, ""),
		initialBalance:        1_000,
		lastResetTime:         time.Now(),
		positionFirstSeenTime: make(map[string]int64),
		peakPnLCache:          make(map[string]float64),
	}
}

func waitForPaperCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
