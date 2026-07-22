package store

import "testing"

func TestDefaultBinanceDynamicStrategyDoesNotEnablePaidData(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	assertBinanceDynamicDefault(t, cfg)
	ind := cfg.Indicators
	if ind.NofxOSAPIKey != "" {
		t.Fatalf("default should not include a NofxOS API key")
	}
	if ind.EnableQuantData || ind.EnableQuantOI || ind.EnableQuantNetflow || ind.EnableOIRanking || ind.EnableNetFlowRanking || ind.EnablePriceRanking {
		t.Fatalf("default Binance dynamic strategy must not enable NofxOS datasets: %+v", ind)
	}
	if !ind.EnableRawKlines {
		t.Fatalf("raw Binance klines must stay enabled")
	}
}

func TestBinanceDynamicDefaultSurvivesClampAndNormalize(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource.UseAI500 = true
	cfg.CoinSource.VergexLimit = 10
	cfg.CoinSource.VergexChain = "hyperliquid"
	cfg.ClampLimits()
	assertBinanceDynamicDefault(t, cfg)
	if cfg.CoinSource.UseAI500 {
		t.Fatalf("Binance dynamic strategy must clear stale AI500 flag: %+v", cfg.CoinSource)
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
	if cfg.CoinSource.VergexLimit != 0 || cfg.CoinSource.VergexMarketType != "" || cfg.CoinSource.VergexChain != "" {
		t.Fatalf("Binance dynamic default retained paid Vergex settings: %+v", cfg.CoinSource)
	}
}
