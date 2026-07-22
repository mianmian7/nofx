package api

import (
	"nofx/store"
	"testing"
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
