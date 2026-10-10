package store

import (
	"encoding/json"
	"testing"
)

func TestPaperEntryGuardRejectsUnusableConfiguration(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	cfg.PaperEntryGuard = true
	if err := cfg.ValidatePaperEntryGuard(); err == nil {
		t.Fatal("missing completed multi-timeframe data accepted")
	}
	cfg.Indicators.Klines.ClosedOnly = true
	cfg.Indicators.Klines.PrimaryTimeframe = "15m"
	cfg.Indicators.Klines.SelectedTimeframes = []string{"15m", "1h", "4h"}
	if err := cfg.ValidatePaperEntryGuard(); err != nil {
		t.Fatal(err)
	}
	cfg.StrategyType = "grid_trading"
	if err := cfg.ValidatePaperEntryGuard(); err == nil {
		t.Fatal("grid silently ignored guard")
	}
	cfg.PaperEntryGuard = false
	if err := cfg.ValidatePaperEntryGuard(); err != nil {
		t.Fatal("opt-out changed")
	}
}

func TestPaperEntryGuardConfigRoundTripAndOptOut(t *testing.T) {
	for _, input := range []string{`{"strategy_type":"ai_trading","ai_config":{"paper_entry_guard":true}}`, `{"paper_entry_guard":true}`} {
		var cfg StrategyConfig
		if err := json.Unmarshal([]byte(input), &cfg); err != nil {
			t.Fatal(err)
		}
		if !cfg.PaperEntryGuard {
			t.Fatal("guard setting lost")
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var next StrategyConfig
		if err = json.Unmarshal(data, &next); err != nil || !next.PaperEntryGuard {
			t.Fatalf("round trip lost entry guard: %s, %v", data, err)
		}
	}
	var cfg StrategyConfig
	if err := json.Unmarshal([]byte(`{"ai_config":{}}`), &cfg); err != nil || cfg.PaperEntryGuard {
		t.Fatal("default must remain disabled")
	}
	if err := json.Unmarshal([]byte(`{"paper_entry_guard":true,"ai_config":{"paper_entry_guard":false}}`), &cfg); err != nil || cfg.PaperEntryGuard {
		t.Fatal("nested explicit opt-out must win")
	}
}
