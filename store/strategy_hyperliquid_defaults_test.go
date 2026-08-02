package store

import "testing"

func TestDefaultBinanceDynamicStrategyUsesPublicMarketData(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	assertBinanceDynamicDefault(t, cfg)
	ind := cfg.Indicators
	if !ind.EnableRawKlines {
		t.Fatalf("raw Binance klines must stay enabled")
	}
}

func TestBinanceDynamicDefaultSurvivesClampAndNormalize(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource.HyperRankLimit = 10
	cfg.ClampLimits()
	assertBinanceDynamicDefault(t, cfg)
	if cfg.CoinSource.HyperRankLimit != 0 {
		t.Fatalf("Binance dynamic strategy must clear unrelated Hyperliquid ranking settings: %+v", cfg.CoinSource)
	}
}

func TestEmptyLegacyCoinSourceMigratesToBinanceDynamic(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource = CoinSourceConfig{}
	cfg.NormalizeProductSchema()
	if cfg.CoinSource.SourceType != "binance_dynamic" {
		t.Fatalf("legacy empty source inferred %q, want binance_dynamic", cfg.CoinSource.SourceType)
	}
}

func assertBinanceDynamicDefault(t *testing.T, cfg StrategyConfig) {
	t.Helper()
	if cfg.CoinSource.SourceType != "binance_dynamic" || cfg.CoinSource.BinanceDynamicLimit != MaxCandidateCoins {
		t.Fatalf("coin source = %+v, want Binance local dynamic top %d", cfg.CoinSource, MaxCandidateCoins)
	}
	if cfg.CoinSource.HyperRankLimit != 0 || cfg.CoinSource.HyperRankCategory != "" || cfg.CoinSource.HyperRankDirection != "" {
		t.Fatalf("Binance dynamic default retained Hyperliquid ranking settings: %+v", cfg.CoinSource)
	}
}
