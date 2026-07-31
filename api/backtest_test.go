package api

import (
	"nofx/store"
	"testing"
	"time"
)

func TestValidateHistoricalReplaySupportRejectsLiveOnlySources(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	if err := validateHistoricalReplaySupport(&cfg); err != nil {
		t.Fatalf("local default should be replayable: %v", err)
	}

	cfg.CoinSource.SourceType = "vergex_signal"
	if err := validateHistoricalReplaySupport(&cfg); err == nil {
		t.Fatal("expected paid live ranking source to be rejected")
	}

	cfg.CoinSource.SourceType = "static"
	cfg.Indicators.EnableFundingRate = true
	if err := validateHistoricalReplaySupport(&cfg); err == nil {
		t.Fatal("expected live-only funding data to be rejected")
	}
}

func TestValidateBacktestRangeRejectsOversizedReplayBeforeFetch(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := validateBacktestRange("1m", start, start.Add(501*time.Minute), "ai_replay"); err == nil {
		t.Fatal("expected AI replay range over 500 candles to be rejected")
	}
	if err := validateBacktestRange("1m", start, start.Add(20_001*time.Minute), "trend_v1"); err == nil {
		t.Fatal("expected trend replay range over 20000 candles to be rejected")
	}
	if err := validateBacktestRange("1m", start, start.Add(500*time.Minute), "ai_replay"); err != nil {
		t.Fatalf("valid boundary range rejected: %v", err)
	}
}

func TestBacktestJobStorePrunesTerminalJobs(t *testing.T) {
	store := newBacktestJobStore()
	old := time.Now().UTC().Add(-backtestJobRetention - time.Minute)
	if !store.put(&backtestJob{ID: "old", Status: "completed", CreatedAt: old, UpdatedAt: old}) {
		t.Fatal("failed to add old job")
	}
	if !store.put(&backtestJob{ID: "new", Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}) {
		t.Fatal("failed to add new job")
	}
	if got := store.get("old"); got != nil {
		t.Fatal("expected expired terminal job to be pruned")
	}
}
