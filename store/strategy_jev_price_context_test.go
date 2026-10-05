package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestJevPriceContextStoredStrategyRoundTrip(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "strategy.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Strategy{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := NewStrategyStore(db)
	for _, jev := range []bool{false, true} {
		for _, closed := range []bool{false, true} {
			t.Run(fmt.Sprintf("jev_%t_closed_%t", jev, closed), func(t *testing.T) {
				cfg := GetDefaultStrategyConfig("en")
				if cfg.Indicators.JevPriceContext || cfg.Indicators.Klines.ClosedOnly {
					t.Fatal("experiment enabled by default")
				}
				cfg, err = MergeStrategyConfig(cfg, map[string]any{"ai_config": map[string]any{"indicators": map[string]any{"jev_price_context": jev, "klines": map[string]any{"closed_only": closed}}}})
				if err != nil {
					t.Fatal(err)
				}
				cfg.ClampLimits()
				st := &Strategy{ID: fmt.Sprintf("fixture-%t-%t", jev, closed), UserID: "fixture-user", Name: "fixture"}
				if err := st.SetConfig(&cfg); err != nil {
					t.Fatal(err)
				}
				if err := s.Create(st); err != nil {
					t.Fatal(err)
				}
				copyID := st.ID + "-copy"
				if err := s.Duplicate(st.UserID, st.ID, copyID, "copy"); err != nil {
					t.Fatal(err)
				}
				loaded, err := s.Get(st.UserID, copyID)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := loaded.ParseConfig()
				if err != nil {
					t.Fatal(err)
				}
				if parsed.Indicators.JevPriceContext != jev || parsed.Indicators.Klines.ClosedOnly != closed {
					t.Fatal("copied flags differ")
				}
				// Exercise a real value change, including true->false; a no-op
				// update would incorrectly pass an unchanged-readback check.
				parsed.Indicators.JevPriceContext = !jev
				parsed.Indicators.Klines.ClosedOnly = !closed
				if err := loaded.SetConfig(parsed); err != nil {
					t.Fatal(err)
				}
				if err := s.Update(loaded); err != nil {
					t.Fatal(err)
				}
				updated, err := s.Get(st.UserID, copyID)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err = updated.ParseConfig()
				if err != nil {
					t.Fatal(err)
				}
				if parsed.Indicators.JevPriceContext == jev || parsed.Indicators.Klines.ClosedOnly == closed {
					t.Fatal("changed flags were not persisted")
				}
			})
		}
	}
}

func TestJevPriceContextLegacyDefaults(t *testing.T) {
	for _, raw := range []string{
		`{"indicators":{"klines":{"primary_timeframe":"15m"}}}`,
		`{"ai_config":{"indicators":{"jev_price_context":false,"klines":{"closed_only":false,"primary_timeframe":"15m"}},"risk_control":{"max_positions":2,"max_leverage":3}}}`,
	} {
		var cfg StrategyConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Indicators.JevPriceContext || cfg.Indicators.Klines.ClosedOnly {
			t.Fatal("legacy/false config enabled candidate")
		}
	}
}
