package kernel

import (
	"testing"

	"nofx/store"
)

func TestBinanceDynamicCandidatesUseLocalMarketSource(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "binance_dynamic"
	cfg.CoinSource.BinanceDynamicLimit = 2
	engine := NewStrategyEngine(&cfg)
	engine.binanceCandidates = func(limit int) ([]string, error) {
		if limit != 2 {
			t.Fatalf("limit = %d, want 2", limit)
		}
		return []string{"BTCUSDT", "ETHUSDT"}, nil
	}

	coins, err := engine.GetCandidateCoins()
	if err != nil {
		t.Fatalf("GetCandidateCoins returned error: %v", err)
	}
	if len(coins) != 2 || coins[0].Symbol != "BTCUSDT" || coins[1].Symbol != "ETHUSDT" {
		t.Fatalf("coins = %#v", coins)
	}
	for _, coin := range coins {
		if len(coin.Sources) != 1 || coin.Sources[0] != "binance_dynamic" {
			t.Fatalf("unexpected sources for %#v", coin)
		}
	}
}

func TestBinanceDynamicDoesNotInitializeClaw402FromEnvironment(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "test-wallet-key-that-must-not-be-read")
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "binance_dynamic"

	engine := NewStrategyEngine(&cfg)
	if engine.vergexClient != nil {
		t.Fatal("Binance dynamic strategy initialized a Vergex client")
	}
}
