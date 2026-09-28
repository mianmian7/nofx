package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalSignalConfigRoundTripAndBounds(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "local_signals"
	cfg.CoinSource.LocalSignalMinScore = 72
	cfg.CoinSource.LocalSignalLimit = 4
	cfg.ClampLimits()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got StrategyConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	got.ClampLimits()
	if got.CoinSource.SourceType != "local_signals" || got.CoinSource.LocalSignalMinScore != 72 || got.CoinSource.LocalSignalLimit != 4 {
		t.Fatalf("lost config %+v", got.CoinSource)
	}
	if err := got.ValidateLocalSignals(); err != nil {
		t.Fatal(err)
	}
	got.CoinSource.LocalSignalMinScore = 0
	got.ClampLimits()
	if err := got.ValidateLocalSignals(); err == nil {
		t.Fatal("explicit invalid score silently replaced by a default")
	}
	got.CoinSource.LocalSignalMinScore = 99
	if err := got.ValidateLocalSignals(); err == nil {
		t.Fatal("bad score accepted")
	}
	got.CoinSource.LocalSignalMinScore = 60
	got.CoinSource.LocalSignalLimit = 11
	if err := got.ValidateLocalSignals(); err == nil {
		t.Fatal("bad count accepted")
	}
	got.CoinSource.LocalSignalLimit = 5
	got.StrategyType = "grid_trading"
	if err := got.ValidateLocalSignals(); err == nil {
		t.Fatal("grid bypass accepted")
	}
}

func TestLocalSignalHistoryRetentionIsIsolated(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "analysis.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for _, exchange := range []string{"binance", "okx"} {
		for _, at := range []time.Time{now, now.AddDate(0, 0, -31)} {
			row := LocalSignal{Exchange: exchange, Symbol: "BTCUSDT", ClosedAt: at.UnixMilli(), Version: "test"}
			if err := st.LocalSignal().Save(&LocalSignalScan{Exchange: exchange, ObservedAt: at.UnixMilli(), Results: []LocalSignal{row}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.LocalSignal().Prune(now); err != nil {
		t.Fatal(err)
	}
	history, err := st.LocalSignal().History("binance", "BTCUSDT", 96)
	if err != nil || len(history) != 1 || history[0].Exchange != "binance" {
		t.Fatalf("bad isolated history %+v %v", history, err)
	}
}
