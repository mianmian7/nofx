package trader

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
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

type switchablePaperPriceSource struct {
	price float64
	err   error
}

func (s *switchablePaperPriceSource) GetMarketPrice(string) (float64, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.price, nil
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
		strategyEngine:        kernel.NewStrategyEngine(&strategy),
		initialBalance:        1_000,
		lastResetTime:         time.Now(),
		positionFirstSeenTime: make(map[string]int64),
		peakPnLCache:          make(map[string]float64),
	}
	defer at.Shutdown()
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

func TestPaperRiskMonitorContinuesAfterAutomaticTradingIsPaused(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 80, TakeProfit: 120,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}

	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 2*time.Millisecond)
	defer at.Shutdown()
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool { return prices.Calls() >= 3 })

	at.Stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not pause promptly")
	}

	priceCallsAtPause := prices.Calls()
	prices.Set(101)
	waitForPaperCondition(t, time.Second, func() bool {
		return prices.Calls() > priceCallsAtPause
	})
	if running := at.GetStatus()["is_running"].(bool); running {
		t.Fatal("AI decision loop should remain paused while marks continue refreshing")
	}
}

func TestPaperRiskMonitorSkipsIdleBroker(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if broker.HasActiveExecution() {
		t.Fatal("new paper broker should be idle")
	}

	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 2*time.Millisecond)
	at.stopMonitorCh = make(chan struct{})
	at.startPaperRiskMonitor()
	time.Sleep(20 * time.Millisecond)
	close(at.stopMonitorCh)
	at.monitorWg.Wait()

	if calls := prices.Calls(); calls != 0 {
		t.Fatalf("idle paper monitor price calls = %d, want 0", calls)
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
	defer at.Shutdown()
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
	defer at.Shutdown()
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

func TestShutdownEndsPaperMonitorWhileTradingCycleIsBlocked(t *testing.T) {
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
		at.Shutdown()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("Shutdown returned while the automatic decision cycle was still blocked")
	case <-time.After(100 * time.Millisecond):
	}
	close(cycleBlocked)
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown remained blocked after trading cycle was released")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after blocked cycle was released")
	}
	callsAfterShutdown := prices.Calls()
	time.Sleep(10 * time.Millisecond)
	if calls := prices.Calls(); calls != callsAfterShutdown {
		t.Fatalf("Paper monitor continued after Shutdown: calls %d -> %d", callsAfterShutdown, calls)
	}
}

func TestStopConcurrentWithBlockedCycleDoesNotLeaveReplacementRun(t *testing.T) {
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, time.Hour)
	defer at.Shutdown()

	cycleStarted := make(chan struct{})
	releaseCycle := make(chan struct{})
	var cycleCalls atomic.Int32
	replacementStarted := make(chan struct{}, 1)
	at.cycleRunner = func() error {
		if cycleCalls.Add(1) == 1 {
			close(cycleStarted)
			<-releaseCycle
			return nil
		}
		select {
		case replacementStarted <- struct{}{}:
		default:
		}
		return nil
	}

	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	select {
	case <-cycleStarted:
	case <-time.After(time.Second):
		t.Fatal("first automatic cycle did not start")
	}

	stopDone := make(chan struct{})
	go func() {
		at.Stop()
		close(stopDone)
	}()
	waitForPaperCondition(t, time.Second, func() bool {
		at.runLifecycleMu.Lock()
		defer at.runLifecycleMu.Unlock()
		return at.runStopRequested
	})

	attemptStarted := make(chan struct{})
	at.runAttemptHook = func() { close(attemptStarted) }
	attemptedRunDone := make(chan error, 1)
	go func() { attemptedRunDone <- at.Run() }()
	select {
	case <-attemptStarted:
	case <-time.After(time.Second):
		t.Fatal("attempted replacement Run did not start")
	}
	close(releaseCycle)

	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return promptly")
	}
	select {
	case err := <-attemptedRunDone:
		if err != nil {
			t.Fatalf("attempted replacement Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("attempted replacement Run did not return")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("original Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("original Run did not exit after its blocked cycle was released")
	}

	if calls := cycleCalls.Load(); calls != 1 {
		t.Fatalf("automatic cycle count = %d, want only the original cycle", calls)
	}
	select {
	case <-replacementStarted:
		t.Fatal("a replacement decision loop survived Stop")
	default:
	}
	if running := at.GetStatus()["is_running"].(bool); running {
		t.Fatal("trader remains running after Stop and the original cycle exited")
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

func TestPaperRefreshUsesFreshCachedMarkButFailsClosedAfterTTL(t *testing.T) {
	now := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	prices := &switchablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		Clock:          func() time.Time { return now },
		MarkStaleTTL:   30 * time.Second,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	prices.err = io.ErrUnexpectedEOF

	now = now.Add(10 * time.Second)
	err = broker.RefreshOpenPositions()
	if err == nil || !CanContinueWithCachedPaperMarks(err) {
		t.Fatalf("fresh-cache refresh error = %v, want non-fatal cached-mark warning", err)
	}

	now = now.Add(21 * time.Second)
	err = broker.RefreshOpenPositions()
	if err == nil || CanContinueWithCachedPaperMarks(err) {
		t.Fatalf("expired-cache refresh error = %v, want fail-closed error", err)
	}
}

func TestPaperRefreshUsesDefaultCachedMarkTTL(t *testing.T) {
	now := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	prices := &switchablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		Clock:          func() time.Time { return now },
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	prices.err = io.ErrUnexpectedEOF

	now = now.Add(89 * time.Second)
	err = broker.RefreshOpenPositions()
	if err == nil || !CanContinueWithCachedPaperMarks(err) {
		t.Fatalf("default-TTL fresh-cache refresh error = %v, want cached-mark warning", err)
	}

	now = now.Add(2 * time.Second)
	err = broker.RefreshOpenPositions()
	if err == nil || CanContinueWithCachedPaperMarks(err) {
		t.Fatalf("default-TTL expired-cache refresh error = %v, want fail-closed error", err)
	}
}

func TestPaperTradingContextContinuesOnTransientPriceFailureWithinTTL(t *testing.T) {
	now := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	prices := &switchablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		Clock:          func() time.Time { return now },
		MarkStaleTTL:   30 * time.Second,
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	prices.err = io.ErrUnexpectedEOF
	now = now.Add(10 * time.Second)
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, time.Hour)

	ctx, err := at.buildTradingContext()
	if err != nil {
		t.Fatalf("buildTradingContext should continue with fresh cached mark: %v", err)
	}
	if len(ctx.Positions) != 1 || ctx.Positions[0].MarkPrice != 100 {
		t.Fatalf("positions = %#v, want cached MUUSDT mark 100", ctx.Positions)
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
		strategyEngine:        kernel.NewStrategyEngine(&strategy),
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
