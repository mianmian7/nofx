package trader

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"nofx/kernel"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
)

type controlledCycleAIClient struct {
	entered             chan struct{}
	release             chan struct{}
	cycleIDs            chan string
	callID              string // Written before entered closes; read after the worker exits.
	waitForCancellation bool
	response            string
	enterOnce           atomic.Bool
}

func (client *controlledCycleAIClient) SetAPIKey(string, string, string) {}
func (client *controlledCycleAIClient) SetTimeout(time.Duration)         {}
func (client *controlledCycleAIClient) CallWithMessages(string, string) (string, error) {
	return "", errors.New("unexpected non-context AI call")
}
func (client *controlledCycleAIClient) CallWithRequest(*mcp.Request) (string, error) {
	return "", errors.New("unexpected request AI call")
}
func (client *controlledCycleAIClient) CallWithRequestStream(*mcp.Request, func(string)) (string, error) {
	return "", errors.New("unexpected stream AI call")
}
func (client *controlledCycleAIClient) CallWithRequestFull(*mcp.Request) (*mcp.LLMResponse, error) {
	return nil, errors.New("unexpected full AI call")
}
func (client *controlledCycleAIClient) CallWithMessagesWithMetadataContext(ctx context.Context, metadata mcp.CallMetadata, _, _ string) (string, error) {
	if client.enterOnce.CompareAndSwap(false, true) {
		client.callID = metadata.CallID
		close(client.entered)
	}
	client.cycleIDs <- metadata.CycleID
	if client.waitForCancellation {
		<-ctx.Done()
		return "", ctx.Err()
	}
	<-client.release
	return client.response, nil
}

func newFullCycleTimeoutTrader(t *testing.T, aiClient mcp.AIClient) (*AutoTrader, *PaperBroker, *store.Store) {
	t.Helper()
	mainStore, err := store.New(t.TempDir() + "/cycle.db")
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, fixedPaperPriceSource{"BTCUSDT": 100})
	if err != nil {
		t.Fatal(err)
	}
	strategy := store.GetDefaultStrategyConfig("en")
	strategy.CoinSource.SourceType = "static"
	strategy.CoinSource.StaticCoins = []string{"BTCUSDT"}
	at := &AutoTrader{
		id:             "full-cycle-timeout",
		name:           "Full Cycle Timeout",
		exchange:       "binance",
		executionMode:  ExecutionModePaper,
		paperBroker:    broker,
		trader:         broker,
		mcpClient:      aiClient,
		store:          mainStore,
		strategyEngine: kernel.NewStrategyEngine(&strategy),
		initialBalance: 1_000,
		isRunning:      true,
		startTime:      time.Now(),
		config: AutoTraderConfig{
			ScanInterval:   5 * time.Millisecond,
			CycleTimeout:   5 * time.Millisecond,
			StrategyConfig: &strategy,
		},
	}
	at.tradingContextBuilder = func(context.Context) (*kernel.Context, error) {
		return &kernel.Context{
			TraderID: at.id,
			Account: kernel.AccountInfo{
				TotalEquity: 1_000, AvailableBalance: 1_000,
			},
			CandidateCoins: []kernel.CandidateCoin{{Symbol: "BTCUSDT"}},
			MarketDataMap: map[string]*market.Data{
				"BTCUSDT": {Symbol: "BTCUSDT", CurrentPrice: 100},
			},
		}, nil
	}
	return at, broker, mainStore
}

func receiveCycleID(t *testing.T, cycleIDs <-chan string) string {
	t.Helper()
	select {
	case cycleID := <-cycleIDs:
		return cycleID
	case <-time.After(time.Second):
		t.Fatal("cycle ID was not captured")
		return ""
	}
}

func waitForCycleWorkerExit(t *testing.T, at *AutoTrader) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for at.cycleInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if at.cycleInFlight.Load() {
		t.Fatal("cycle worker did not exit")
	}
}

func TestRunAutomaticCycleTimesOutBlockedCycle(t *testing.T) {
	release := make(chan struct{})
	at := &AutoTrader{
		config: AutoTraderConfig{ScanInterval: 10 * time.Millisecond},
		cycleRunner: func() error {
			<-release
			return nil
		},
	}

	result := make(chan error, 1)
	go func() { result <- at.runAutomaticCycle() }()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("runAutomaticCycle error = %v, want timeout", err)
		}
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("runAutomaticCycle remained blocked after the cycle deadline")
	}
	close(release)

	// Verify that a timeout does NOT request stop on the trader
	at.runLifecycleMu.Lock()
	stopRequested := at.runStopRequested
	at.runLifecycleMu.Unlock()
	if stopRequested {
		t.Fatal("runAutomaticCycle requested stop after a single timeout, expected trader to remain running")
	}
}

func TestCycleTimeoutFinalizationIsClaimedOnce(t *testing.T) {
	ctx := context.WithValue(context.Background(), cycleFinalizationContextKey{}, &cycleFinalizationState{cycleID: "cycle-1"})
	at := &AutoTrader{}
	if !at.finalizeCycleTimeout(ctx, time.Second, nil, context.DeadlineExceeded) {
		t.Fatal("first timeout finalization should win")
	}
	if at.finalizeCycleTimeout(ctx, time.Second, nil, context.DeadlineExceeded) {
		t.Fatal("second timeout finalization must be ignored")
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}
}

func TestRunAutomaticCycleDoesNotOverlapDrainingLegacyWorker(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	at := &AutoTrader{
		config: AutoTraderConfig{ScanInterval: 5 * time.Millisecond},
		cycleRunner: func() error {
			if calls.Add(1) == 1 {
				<-release
			}
			return nil
		},
	}
	if err := at.runAutomaticCycle(); !errors.Is(err, ErrDecisionCycleTimeout) {
		close(release)
		t.Fatalf("first cycle error = %v, want timeout", err)
	}
	if err := at.runAutomaticCycle(); !errors.Is(err, ErrDecisionCycleInFlight) {
		close(release)
		t.Fatalf("overlapping cycle error = %v, want in-flight", err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for at.cycleInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if at.cycleInFlight.Load() {
		t.Fatal("legacy worker did not release in-flight guard")
	}
	if err := at.runAutomaticCycle(); err != nil {
		t.Fatalf("cycle after worker exit = %v, want success", err)
	}
}

func TestRunAutomaticCyclePersistsOneFinalTimeoutWithCycleID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&store.DecisionRecordDB{}); err != nil {
		t.Fatal(err)
	}
	mainStore, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	decisionStore := mainStore.Decision()
	at := &AutoTrader{
		id:     "cycle-timeout-trader",
		store:  mainStore,
		config: AutoTraderConfig{ScanInterval: 5 * time.Millisecond},
	}
	at.cycleRunnerWithContext = func(ctx context.Context) error {
		<-ctx.Done()
		at.finalizeCycleTimeout(ctx, 5*time.Millisecond, &store.DecisionRecord{
			TraderID:    at.id,
			CycleNumber: 1,
			Success:     false,
		}, ctx.Err())
		return ctx.Err()
	}

	if err := at.runAutomaticCycle(); !errors.Is(err, ErrDecisionCycleTimeout) {
		t.Fatalf("cycle error = %v, want timeout", err)
	}
	records, err := decisionStore.GetLatestRecords(at.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("timeout records = %d, want 1", len(records))
	}
	if records[0].CycleID == "" {
		t.Fatal("timeout record missing cycle ID")
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}
}

func TestRealCycleWorkerFinalizesCooperativeAITimeoutOnce(t *testing.T) {
	aiClient := &controlledCycleAIClient{
		entered:             make(chan struct{}),
		release:             make(chan struct{}),
		cycleIDs:            make(chan string, 1),
		waitForCancellation: true,
	}
	at, _, mainStore := newFullCycleTimeoutTrader(t, aiClient)
	at.config.CycleTimeout = 100 * time.Millisecond
	result := make(chan error, 1)
	go func() { result <- at.runAutomaticCycle() }()
	select {
	case <-aiClient.entered:
	case <-time.After(time.Second):
		t.Fatal("cooperative AI call was not reached")
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrDecisionCycleTimeout) {
			t.Fatalf("cycle error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cooperative AI cycle did not finish")
	}
	waitForCycleWorkerExit(t, at)
	cycleID := receiveCycleID(t, aiClient.cycleIDs)
	records, err := mainStore.Decision().GetLatestRecords(at.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].CycleID != cycleID {
		t.Fatalf("timeout records = %+v, captured cycle ID=%q", records, cycleID)
	}
	if aiClient.callID == "" || records[0].CallID != aiClient.callID {
		t.Fatalf("timeout call ID=%q, actual logical call ID=%q", records[0].CallID, aiClient.callID)
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}
}

func TestLateSuccessFixtureIsValidProductionOpenDecision(t *testing.T) {
	releaseAI := make(chan struct{})
	close(releaseAI)
	aiClient := &controlledCycleAIClient{
		entered:  make(chan struct{}),
		release:  releaseAI,
		cycleIDs: make(chan string, 1),
		response: `[{"symbol":"BTCUSDT","action":"open_long","leverage":3,"position_size_usd":60,"stop_loss":90,"take_profit":120,"reasoning":"valid"}]`,
	}
	at, _, _ := newFullCycleTimeoutTrader(t, aiClient)
	ctx, err := at.tradingContextBuilder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := kernel.GetFullDecisionWithStrategyContext(context.Background(), ctx, aiClient, at.strategyEngine, "balanced")
	if err != nil {
		t.Fatalf("fixture decision failed production validation: %v", err)
	}
	if len(decision.Decisions) != 1 || decision.Decisions[0].Action != "open_long" {
		t.Fatalf("fixture decision = %+v", decision.Decisions)
	}
}

func TestRealCycleWorkerDiscardsLateAISuccessAfterWatchdogTimeout(t *testing.T) {
	aiClient := &controlledCycleAIClient{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		cycleIDs: make(chan string, 1),
		response: `[{"symbol":"BTCUSDT","action":"open_long","leverage":3,"position_size_usd":60,"stop_loss":90,"take_profit":120,"reasoning":"late"}]`,
	}
	at, broker, mainStore := newFullCycleTimeoutTrader(t, aiClient)
	before := broker.Snapshot()
	result := make(chan error, 1)
	go func() { result <- at.runAutomaticCycle() }()

	select {
	case <-aiClient.entered:
	case <-time.After(time.Second):
		close(aiClient.release)
		t.Fatal("AI call was not reached")
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrDecisionCycleTimeout) {
			close(aiClient.release)
			t.Fatalf("cycle error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		close(aiClient.release)
		t.Fatal("watchdog did not return while AI ignored cancellation")
	}
	cycleID := receiveCycleID(t, aiClient.cycleIDs)
	close(aiClient.release)
	waitForCycleWorkerExit(t, at)

	records, err := mainStore.Decision().GetLatestRecords(at.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].CycleID != cycleID {
		t.Fatalf("late-success records = %+v, captured cycle ID=%q", records, cycleID)
	}
	if aiClient.callID == "" || records[0].CallID != aiClient.callID {
		t.Fatalf("watchdog call ID=%q, actual logical call ID=%q", records[0].CallID, aiClient.callID)
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("late success changed consecutiveAIFailures to %d, want 1", at.consecutiveAIFailures)
	}
	after := broker.Snapshot()
	if after.Balance != before.Balance || after.OpenPositions != before.OpenPositions || after.PendingOrders != before.PendingOrders {
		t.Fatalf("late AI decision mutated broker: before=%#v after=%#v", before, after)
	}
}

func TestRealCycleWorkerDiscardsLateContextBuilderFailure(t *testing.T) {
	aiClient := &controlledCycleAIClient{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		cycleIDs: make(chan string, 1),
	}
	at, _, mainStore := newFullCycleTimeoutTrader(t, aiClient)
	at.config.CycleTimeout = 20 * time.Millisecond
	builderEntered := make(chan struct{})
	releaseBuilder := make(chan struct{})
	cycleIDs := make(chan string, 1)
	at.tradingContextBuilder = func(ctx context.Context) (*kernel.Context, error) {
		cycleIDs <- cycleIDFromContext(ctx)
		close(builderEntered)
		<-releaseBuilder
		return nil, errors.New("late context failure")
	}
	result := make(chan error, 1)
	go func() { result <- at.runAutomaticCycle() }()
	select {
	case <-builderEntered:
	case <-time.After(time.Second):
		close(releaseBuilder)
		t.Fatal("context builder was not reached")
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrDecisionCycleTimeout) {
			close(releaseBuilder)
			t.Fatalf("cycle error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		close(releaseBuilder)
		t.Fatal("watchdog did not return while context builder ignored cancellation")
	}
	close(releaseBuilder)
	waitForCycleWorkerExit(t, at)

	records, err := mainStore.Decision().GetLatestRecords(at.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	cycleID := receiveCycleID(t, cycleIDs)
	if len(records) != 1 || records[0].CycleID != cycleID {
		t.Fatalf("late-builder records = %+v, captured cycle ID=%q", records, cycleID)
	}
	if records[0].CallID != "" || aiClient.enterOnce.Load() {
		t.Fatalf("pre-AI timeout must not invent a call ID or invoke AI: call ID=%q", records[0].CallID)
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}
}

func TestRealCycleExecutionCrossingDeadlineKeepsTimeoutTerminal(t *testing.T) {
	releaseAI := make(chan struct{})
	close(releaseAI)
	aiClient := &controlledCycleAIClient{
		entered:  make(chan struct{}),
		release:  releaseAI,
		cycleIDs: make(chan string, 1),
		response: `[{"symbol":"ALL","action":"wait","reasoning":"wait"}]`,
	}
	at, _, mainStore := newFullCycleTimeoutTrader(t, aiClient)
	at.config.CycleTimeout = 50 * time.Millisecond
	sleeperEntered := make(chan struct{})
	releaseSleeper := make(chan struct{})
	var sleeperOnce sync.Once
	at.sleeper = func(time.Duration) {
		sleeperOnce.Do(func() { close(sleeperEntered) })
		<-releaseSleeper
	}
	result := make(chan error, 1)
	go func() { result <- at.runAutomaticCycle() }()
	select {
	case <-sleeperEntered:
	case <-time.After(time.Second):
		close(releaseSleeper)
		t.Fatal("execution phase was not reached")
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrDecisionCycleTimeout) {
			close(releaseSleeper)
			t.Fatalf("cycle error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		close(releaseSleeper)
		t.Fatal("watchdog did not return while execution ignored cancellation")
	}
	cycleID := receiveCycleID(t, aiClient.cycleIDs)
	close(releaseSleeper)
	waitForCycleWorkerExit(t, at)

	records, err := mainStore.Decision().GetLatestRecords(at.id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].CycleID != cycleID || records[0].Success {
		t.Fatalf("execution-timeout records = %+v, captured cycle ID=%q", records, cycleID)
	}
	if aiClient.callID == "" || records[0].CallID != aiClient.callID {
		t.Fatalf("execution watchdog call ID=%q, actual logical call ID=%q", records[0].CallID, aiClient.callID)
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}
}

func TestRealCycleDegradedBatchExecutesNoOpeningAction(t *testing.T) {
	releaseAI := make(chan struct{})
	close(releaseAI)
	aiClient := &controlledCycleAIClient{
		entered:  make(chan struct{}),
		release:  releaseAI,
		cycleIDs: make(chan string, 1),
		response: `[
			{"symbol":"BTCUSDT","action":"close_long","close_ratio":0.5,"reasoning":"reduce"},
			{"symbol":"BTCUSDT","action":"open_long","leverage":1.5,"position_size_usd":60,"stop_loss":90,"take_profit":120,"reasoning":"invalid"}
		]`,
	}
	at, broker, mainStore := newFullCycleTimeoutTrader(t, aiClient)
	at.config.CycleTimeout = time.Second
	before := broker.Snapshot()
	if err := at.runAutomaticCycle(); err != nil {
		t.Fatalf("degraded cycle error = %v", err)
	}
	records, err := mainStore.Decision().GetLatestRecords(at.id, 1)
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	for _, action := range records[0].Decisions {
		if isOpenDecision(action.Action) {
			t.Fatalf("degraded batch executed opening action: %+v", action)
		}
	}
	after := broker.Snapshot()
	if after.Balance != before.Balance || after.OpenPositions != before.OpenPositions || after.PendingOrders != before.PendingOrders {
		t.Fatalf("degraded batch mutated broker with an opening: before=%#v after=%#v", before, after)
	}
}

func TestRunAutomaticCycleConsecutiveTimeoutsTriggerSafeMode(t *testing.T) {
	at := &AutoTrader{
		config: AutoTraderConfig{ScanInterval: 5 * time.Millisecond},
		cycleRunnerWithContext: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	if at.isSafeMode() {
		t.Fatal("expected safe mode to be disabled initially")
	}

	// 1st timeout
	_ = at.runAutomaticCycle()
	if at.isSafeMode() {
		t.Fatal("expected safe mode to remain disabled after 1 timeout")
	}
	if at.consecutiveAIFailures != 1 {
		t.Fatalf("consecutiveAIFailures = %d, want 1", at.consecutiveAIFailures)
	}

	// 2nd timeout
	_ = at.runAutomaticCycle()
	if at.isSafeMode() {
		t.Fatal("expected safe mode to remain disabled after 2 timeouts")
	}
	if at.consecutiveAIFailures != 2 {
		t.Fatalf("consecutiveAIFailures = %d, want 2", at.consecutiveAIFailures)
	}

	// 3rd timeout -> should trigger Safe Mode
	_ = at.runAutomaticCycle()
	if !at.isSafeMode() {
		t.Fatal("expected safe mode to be enabled after 3 consecutive timeouts")
	}
	if at.consecutiveAIFailures != 3 {
		t.Fatalf("consecutiveAIFailures = %d, want 3", at.consecutiveAIFailures)
	}
}

func TestRunContinuesAcrossCycleTimeout(t *testing.T) {
	var cycleCount atomic.Int32
	secondCycleReached := make(chan struct{})

	at := &AutoTrader{
		id:   "test-retry-trader",
		name: "Test Retry Trader",
		config: AutoTraderConfig{
			ScanInterval: 10 * time.Millisecond,
		},
		cycleRunnerWithContext: func(ctx context.Context) error {
			current := cycleCount.Add(1)
			if current == 1 {
				// First cycle: block until timeout
				<-ctx.Done()
				return ctx.Err()
			}
			// Second cycle reached successfully
			select {
			case <-secondCycleReached:
			default:
				close(secondCycleReached)
			}
			return nil
		},
	}

	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()

	select {
	case <-secondCycleReached:
		// Succeeded: the loop retried after the first cycle timed out!
	case <-time.After(1 * time.Second):
		t.Fatal("second cycle was not reached after first cycle timeout; loop stopped unexpectedly")
	}

	// Stop trader
	at.Stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned unexpected error on Stop: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not exit after Stop")
	}
}
