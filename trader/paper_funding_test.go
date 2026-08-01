package trader

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

type fixedPaperFundingSource struct {
	events    map[string][]market.FundingEvent
	snapshots map[string]*market.FundingSnapshot
	errors    map[string]error
}

type countingPaperFundingSource struct{ calls atomic.Int64 }

type scheduledPaperFundingSource struct {
	snapshotCalls atomic.Int64
	historyCalls  atomic.Int64
	snapshot      *market.FundingSnapshot
	snapshotErr   error
	events        []market.FundingEvent
	historyErr    error
}

func (s *scheduledPaperFundingSource) GetFundingSnapshot(string) (*market.FundingSnapshot, error) {
	s.snapshotCalls.Add(1)
	if s.snapshotErr != nil {
		return nil, s.snapshotErr
	}
	return s.snapshot, nil
}

func (s *scheduledPaperFundingSource) GetFundingHistory(string, int64, int64) ([]market.FundingEvent, error) {
	s.historyCalls.Add(1)
	if s.historyErr != nil {
		return nil, s.historyErr
	}
	return append([]market.FundingEvent(nil), s.events...), nil
}

func (s *countingPaperFundingSource) GetFundingHistory(string, int64, int64) ([]market.FundingEvent, error) {
	s.calls.Add(1)
	return nil, nil
}

func (s *countingPaperFundingSource) GetFundingSnapshot(string) (*market.FundingSnapshot, error) {
	s.calls.Add(1)
	return nil, nil
}

func TestFiveSecondRiskTicksDoNotRepeatFundingBeforeNextFundingTime(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", MarkPrice: 100, IndexPrice: 100,
		NextFundingTime: now.Add(4 * time.Hour).UnixMilli(),
	}}
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 2*time.Millisecond)
	defer at.Shutdown()
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool { return prices.Calls() >= 5 })
	at.Stop()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	if got := funding.snapshotCalls.Load(); got != 1 {
		t.Fatalf("funding snapshot calls = %d, want startup-only 1", got)
	}
	if got := funding.historyCalls.Load(); got != 1 {
		t.Fatalf("funding history calls = %d, want startup catch-up only 1", got)
	}
}

func TestFundingTicksRefreshSnapshotButDeferHistoryBeforeNextFundingTime(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", MarkPrice: 100, IndexPrice: 100,
		NextFundingTime: now.Add(4 * time.Hour).UnixMilli(),
	}}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, time.Hour)
	at.config.PaperFundingMonitorInterval = 2 * time.Millisecond
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool { return funding.snapshotCalls.Load() >= 4 })
	at.Stop()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	if got := funding.historyCalls.Load(); got != 1 {
		t.Fatalf("funding history calls = %d, want startup catch-up only 1", got)
	}
}

func TestDueFundingHistoryFailureRetriesNextFundingTickAndSettlesOnce(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	now := entryTime
	dueTime := entryTime.Add(time.Hour)
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", MarkPrice: 100, IndexPrice: 100, NextFundingTime: dueTime.UnixMilli(),
	}}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.CatchUpFunding(now); err != nil {
		t.Fatalf("startup catch-up: %v", err)
	}

	now = dueTime.Add(time.Minute)
	funding.events = []market.FundingEvent{{
		Symbol: "MUUSDT", Rate: 0.001, FundingTime: dueTime.UnixMilli(), MarkPrice: 100,
	}}
	funding.historyErr = errors.New("temporary history failure")
	if _, err := broker.PollFunding(now); err == nil {
		t.Fatal("due poll error = nil, want history failure")
	}
	if payments := broker.RecentFundingPayments(10); len(payments) != 0 {
		t.Fatalf("failed history was applied: %#v", payments)
	}

	funding.historyErr = nil
	funding.snapshot.NextFundingTime = dueTime.Add(4 * time.Hour).UnixMilli()
	if _, err := broker.PollFunding(now.Add(time.Minute)); err != nil {
		t.Fatalf("retry poll: %v", err)
	}
	if payments := broker.RecentFundingPayments(10); len(payments) != 1 {
		t.Fatalf("retry payments = %#v, want exactly one", payments)
	}
	if _, err := broker.PollFunding(now.Add(2 * time.Minute)); err != nil {
		t.Fatalf("post-settlement poll: %v", err)
	}
	if got := funding.historyCalls.Load(); got != 3 {
		t.Fatalf("history calls = %d, want startup + failed due + retry", got)
	}
	assertPaperFloat(t, "wallet after retry", broker.Snapshot().Balance, 999)
}

func TestRiskExitAtFundingBoundarySettlesFundingBeforeClosing(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	now := entryTime
	dueTime := entryTime.Add(time.Hour)
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", MarkPrice: 100, IndexPrice: 100, NextFundingTime: dueTime.UnixMilli(),
	}}
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.CatchUpFunding(now); err != nil {
		t.Fatalf("startup catch-up: %v", err)
	}

	now = dueTime
	funding.events = []market.FundingEvent{{
		Symbol: "MUUSDT", Rate: 0.001, FundingTime: dueTime.UnixMilli(), MarkPrice: 110,
	}}
	prices.Set(110)
	if err := broker.RefreshOpenPositions(); err != nil {
		t.Fatalf("boundary risk refresh: %v", err)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 {
		t.Fatalf("open positions = %d, want TP close", snapshot.OpenPositions)
	}
	payments := broker.RecentFundingPayments(10)
	if len(payments) != 1 || payments[0].FundingTime != dueTime.UnixMilli() {
		t.Fatalf("funding payments = %#v, want due event before close", payments)
	}
	if fills := broker.RecentFills(10); len(fills) < 2 || fills[len(fills)-1].Action != "take_profit" {
		t.Fatalf("fills = %#v, want take_profit after funding", fills)
	}
	if got := funding.historyCalls.Load(); got != 2 {
		t.Fatalf("history calls = %d, want startup + boundary settlement", got)
	}
}

func TestRiskExitBeforeNextFundingTimeMakesNoFundingRequest(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", MarkPrice: 100, IndexPrice: 100,
		NextFundingTime: entryTime.Add(4 * time.Hour).UnixMilli(),
	}}
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.CatchUpFunding(now); err != nil {
		t.Fatalf("startup catch-up: %v", err)
	}
	now = entryTime.Add(time.Hour)
	prices.Set(110)
	if err := broker.RefreshOpenPositions(); err != nil {
		t.Fatalf("pre-funding risk refresh: %v", err)
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 {
		t.Fatalf("open positions = %d, want TP close", snapshot.OpenPositions)
	}
	if got := funding.snapshotCalls.Load(); got != 1 {
		t.Fatalf("snapshot calls = %d, want startup-only 1", got)
	}
	if got := funding.historyCalls.Load(); got != 1 {
		t.Fatalf("history calls = %d, want startup-only 1", got)
	}
}

func TestFundingSnapshotFailureRetainsPersistedStatusWithoutEarlyHistory(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper-funding-status-stale.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	nextTime := now.Add(4 * time.Hour).UnixMilli()
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", Rate: 0.0002, MarkPrice: 101, IndexPrice: 100.9,
		NextFundingTime: nextTime,
	}}
	config := PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}
	broker, err := NewPersistentPaperBroker(config, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "stale-status", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.CatchUpFunding(now); err != nil {
		t.Fatalf("startup catch-up: %v", err)
	}
	funding.snapshotErr = errors.New("temporary snapshot failure")
	now = now.Add(time.Minute)
	if _, err := broker.PollFunding(now); err == nil {
		t.Fatal("snapshot failure poll error = nil")
	}
	if got := funding.historyCalls.Load(); got != 1 {
		t.Fatalf("history calls = %d, want no early retry before nextFundingTime", got)
	}

	restored, err := NewPersistentPaperBroker(config, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "stale-status", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("restore broker: %v", err)
	}
	statuses := restored.FundingStatuses()
	if len(statuses) != 1 {
		t.Fatalf("restored statuses = %#v", statuses)
	}
	status := statuses[0]
	if status.NextFundingTime != nextTime || status.Rate != 0.0002 || !status.Stale || status.Warning == "" {
		t.Fatalf("restored stale status = %#v", status)
	}
}

func TestFundingHistoryUsesBoundedFallbackWhenNextFundingTimeUnavailable(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	funding := &scheduledPaperFundingSource{snapshot: &market.FundingSnapshot{
		Symbol: "MUUSDT", MarkPrice: 100, IndexPrice: 100,
	}}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
		FundingHistoryFallbackInterval: 5 * time.Minute,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.CatchUpFunding(now); err != nil {
		t.Fatalf("startup catch-up: %v", err)
	}
	if _, err := broker.PollFunding(now.Add(4 * time.Minute)); err != nil {
		t.Fatalf("early fallback poll: %v", err)
	}
	if got := funding.historyCalls.Load(); got != 1 {
		t.Fatalf("history calls before fallback = %d, want 1", got)
	}
	if _, err := broker.PollFunding(now.Add(5 * time.Minute)); err != nil {
		t.Fatalf("due fallback poll: %v", err)
	}
	if got := funding.historyCalls.Load(); got != 2 {
		t.Fatalf("history calls at fallback = %d, want 2", got)
	}
}

func (s *fixedPaperFundingSource) GetFundingHistory(symbol string, _, _ int64) ([]market.FundingEvent, error) {
	if err := s.errors[symbol]; err != nil {
		return nil, err
	}
	return append([]market.FundingEvent(nil), s.events[symbol]...), nil
}

func (s *fixedPaperFundingSource) GetFundingSnapshot(symbol string) (*market.FundingSnapshot, error) {
	if err := s.errors[symbol]; err != nil {
		return nil, err
	}
	return s.snapshots[symbol], nil
}

func TestPaperFundingPositiveRateDebitsLongWithoutChangingCommissionFees(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{
			"MUUSDT": {{Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100}},
		},
		snapshots: map[string]*market.FundingSnapshot{},
		errors:    map[string]error{},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("SettleFunding: %v", err)
	}

	snapshot := broker.Snapshot()
	assertPaperFloat(t, "wallet", snapshot.Balance, 999)
	assertPaperFloat(t, "funding net", snapshot.FundingNet, -1)
	assertPaperFloat(t, "funding paid", snapshot.FundingPaid, 1)
	assertPaperFloat(t, "funding received", snapshot.FundingReceived, 0)
	assertPaperFloat(t, "commission fees", snapshot.Fees, 0)
	payments := broker.RecentFundingPayments(10)
	if len(payments) != 1 || payments[0].WalletDelta != -1 || payments[0].Side != "long" {
		t.Fatalf("payments = %#v", payments)
	}
}

func TestPaperFundingDirectionAndRateSigns(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		rate      float64
		wantDelta float64
	}{
		{name: "positive short receives", action: "open_short", rate: 0.001, wantDelta: 1},
		{name: "negative long receives", action: "open_long", rate: -0.001, wantDelta: 1},
		{name: "negative short pays", action: "open_short", rate: -0.001, wantDelta: -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
			now := entryTime
			funding := &fixedPaperFundingSource{
				events: map[string][]market.FundingEvent{
					"MUUSDT": {{Symbol: "MUUSDT", Rate: tc.rate, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100}},
				}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
			}
			broker, err := NewPaperBroker(PaperBrokerConfig{
				InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
			}, fixedPaperPriceSource{"MUUSDT": 100})
			if err != nil {
				t.Fatalf("NewPaperBroker: %v", err)
			}
			if _, err := broker.ExecuteDecision(&kernel.Decision{
				Symbol: "MUUSDT", Action: tc.action, PositionSizeUSD: 1_000, Leverage: 2,
			}); err != nil {
				t.Fatalf("%s: %v", tc.action, err)
			}
			now = entryTime.Add(2 * time.Hour)
			if err := broker.SettleFunding(now); err != nil {
				t.Fatalf("SettleFunding: %v", err)
			}
			assertPaperFloat(t, "wallet delta", broker.Snapshot().Balance-1_000, tc.wantDelta)
		})
	}
}

func TestPaperFundingAppliesOnlyEventsStrictlyAfterEntryAndNotAfterNow(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {
			{Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(-time.Millisecond).UnixMilli(), MarkPrice: 100},
			{Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.UnixMilli(), MarkPrice: 100},
			{Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100},
			{Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(3 * time.Hour).UnixMilli(), MarkPrice: 100},
		}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("SettleFunding: %v", err)
	}
	if payments := broker.RecentFundingPayments(10); len(payments) != 1 || payments[0].FundingTime != entryTime.Add(time.Hour).UnixMilli() {
		t.Fatalf("payments = %#v", payments)
	}
}

func TestPaperFundingRepeatedPollIsIdempotent(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("first SettleFunding: %v", err)
	}
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("second SettleFunding: %v", err)
	}
	assertPaperFloat(t, "wallet", broker.Snapshot().Balance, 999)
	if payments := broker.RecentFundingPayments(10); len(payments) != 1 {
		t.Fatalf("payments = %#v", payments)
	}
}

func TestPaperFundingPaymentAndLastEventSurviveRestart(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper-funding.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	config := PaperBrokerConfig{InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now }}
	first, err := NewPersistentPaperBroker(config, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "funding-restart", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("first broker: %v", err)
	}
	if _, err := first.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := first.SettleFunding(now); err != nil {
		t.Fatalf("first SettleFunding: %v", err)
	}

	restored, err := NewPersistentPaperBroker(config, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "funding-restart", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("restored broker: %v", err)
	}
	if err := restored.SettleFunding(now); err != nil {
		t.Fatalf("restored SettleFunding: %v", err)
	}
	assertPaperFloat(t, "restored wallet", restored.Snapshot().Balance, 999)
	if payments := restored.RecentFundingPayments(10); len(payments) != 1 {
		t.Fatalf("restored payments = %#v", payments)
	}
}

func TestPaperFundingRestartCatchesUpMissedSettlement(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper-funding-catchup.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	config := PaperBrokerConfig{InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now }}
	first, err := NewPersistentPaperBroker(config, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "funding-catchup", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("first broker: %v", err)
	}
	if _, err := first.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2}); err != nil {
		t.Fatalf("open_long: %v", err)
	}

	now = entryTime.Add(2 * time.Hour)
	restored, err := NewPersistentPaperBroker(config, fixedPaperPriceSource{"MUUSDT": 100}, st.Paper(), "funding-catchup", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("restored broker: %v", err)
	}
	if err := restored.SettleFunding(now); err != nil {
		t.Fatalf("catch-up SettleFunding: %v", err)
	}
	assertPaperFloat(t, "catch-up wallet", restored.Snapshot().Balance, 999)
	if payments := restored.RecentFundingPayments(10); len(payments) != 1 {
		t.Fatalf("catch-up payments = %#v", payments)
	}
}

func TestPaperFundingInvalidMarkIsRetriedAndNotMarkedApplied(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	eventTime := entryTime.Add(time.Hour).UnixMilli()
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: eventTime, MarkPrice: 0,
		}}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err == nil {
		t.Fatal("invalid mark settlement error = nil")
	}
	if payments := broker.RecentFundingPayments(10); len(payments) != 0 {
		t.Fatalf("invalid event was applied: %#v", payments)
	}
	funding.events["MUUSDT"][0].MarkPrice = 100
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("retry SettleFunding: %v", err)
	}
	assertPaperFloat(t, "retried wallet", broker.Snapshot().Balance, 999)
}

func TestPaperFundingOneSymbolFailureDoesNotBlockOthers(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"SNDKUSDT": {{
			Symbol: "SNDKUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}}, snapshots: map[string]*market.FundingSnapshot{},
		errors: map[string]error{"MUUSDT": errors.New("temporary funding history failure")},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 2_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100, "SNDKUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	for _, symbol := range []string{"MUUSDT", "SNDKUSDT"} {
		if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: symbol, Action: "open_long", PositionSizeUSD: 500, Leverage: 2}); err != nil {
			t.Fatalf("open %s: %v", symbol, err)
		}
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err == nil {
		t.Fatal("SettleFunding error = nil, want MU failure")
	}
	payments := broker.RecentFundingPayments(10)
	if len(payments) != 1 || payments[0].Symbol != "SNDKUSDT" {
		t.Fatalf("payments = %#v", payments)
	}
}

func TestPaperStatusExposesFundingPaymentsAndNextFundingTime(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	nextTime := entryTime.Add(4 * time.Hour).UnixMilli()
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}},
		snapshots: map[string]*market.FundingSnapshot{"MUUSDT": {
			Symbol: "MUUSDT", Rate: 0.0002, MarkPrice: 101, IndexPrice: 100.9, NextFundingTime: nextTime,
		}}, errors: map[string]error{},
	}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("SettleFunding: %v", err)
	}
	at := &AutoTrader{id: "paper-funding-status", executionMode: ExecutionModePaper, paperBroker: broker}
	status := at.GetStatus()
	statuses, ok := status["paper_funding_status"].([]PaperFundingStatus)
	if !ok || len(statuses) != 1 || statuses[0].NextFundingTime != nextTime || statuses[0].Rate != 0.0002 {
		t.Fatalf("paper_funding_status = %#v", status["paper_funding_status"])
	}
	payments, ok := status["paper_recent_funding"].([]PaperFundingPayment)
	if !ok || len(payments) != 1 {
		t.Fatalf("paper_recent_funding = %#v", status["paper_recent_funding"])
	}
}

func TestPaperMonitorSettlesFundingBeforeRiskExit(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	prices := &mutablePaperPriceSource{price: 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3, TakeProfit: 110,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	at := newMonitorTestAutoTrader(ExecutionModePaper, broker, 5*time.Millisecond)
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool { return at.GetStatus()["call_count"].(int) == 1 })
	now = entryTime.Add(2 * time.Hour)
	prices.Set(111)
	waitForPaperCondition(t, time.Second, func() bool { return broker.Snapshot().OpenPositions == 0 })
	at.Stop()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	if payments := broker.RecentFundingPayments(10); len(payments) != 1 {
		t.Fatalf("funding was not settled before exit: %#v", payments)
	}
}

func TestLiveModeNeverPollsPaperFunding(t *testing.T) {
	funding := &countingPaperFundingSource{}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	at := newMonitorTestAutoTrader(ExecutionModeLive, broker, 2*time.Millisecond)
	runDone := make(chan error, 1)
	go func() { runDone <- at.Run() }()
	waitForPaperCondition(t, time.Second, func() bool { return at.GetStatus()["call_count"].(int) == 1 })
	time.Sleep(20 * time.Millisecond)
	at.Stop()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("live Run did not stop")
	}
	if calls := funding.calls.Load(); calls != 0 {
		t.Fatalf("live funding calls = %d, want 0", calls)
	}
}

func TestPaperFundingChangesWalletButNotClosedTradePnLOrCommissionFees(t *testing.T) {
	entryTime := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	now := entryTime
	funding := &fixedPaperFundingSource{
		events: map[string][]market.FundingEvent{"MUUSDT": {{
			Symbol: "MUUSDT", Rate: 0.001, FundingTime: entryTime.Add(time.Hour).UnixMilli(), MarkPrice: 100,
		}}}, snapshots: map[string]*market.FundingSnapshot{}, errors: map[string]error{},
	}
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, FundingSource: funding, Clock: func() time.Time { return now },
	}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 2}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	now = entryTime.Add(2 * time.Hour)
	if err := broker.SettleFunding(now); err != nil {
		t.Fatalf("SettleFunding: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatalf("close_long: %v", err)
	}
	assertPaperFloat(t, "wallet funding delta", broker.Snapshot().Balance-1_000, -1)
	assertPaperFloat(t, "commission fees", broker.Snapshot().Fees, 0)
	assertPaperFloat(t, "trade-only closed pnl", broker.Performance().TotalPnL, 0)
}
