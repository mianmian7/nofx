package manager

import (
	"testing"

	"nofx/crypto"
	"nofx/store"
)

func TestResolveTraderDataWalletKeyRequiresExplicitPaidSource(t *testing.T) {
	model := &store.AIModel{
		Provider: "claw402",
		APIKey:   crypto.EncryptedString("test-wallet-key"),
	}

	for _, source := range []string{"binance_dynamic", "static", "hyper_all", "hyper_main", "hyper_rank"} {
		cfg := store.GetDefaultStrategyConfig("zh")
		cfg.CoinSource.SourceType = source
		if got := resolveTraderDataWalletKey(nil, "user", model, &cfg); got != "" {
			t.Fatalf("source %s resolved a paid data wallet", source)
		}
	}

	paid := store.GetDefaultStrategyConfig("zh")
	paid.CoinSource.SourceType = "vergex_signal"
	if got := resolveTraderDataWalletKey(nil, "user", model, &paid); got != "test-wallet-key" {
		t.Fatalf("explicit paid source wallet = %q", got)
	}
}

func TestStrategyNeedsPaidDataWalletRespectsUserNofxOSKey(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "ai500"
	if !strategyNeedsPaidDataWallet(&cfg) {
		t.Fatal("AI500 without a user NofxOS key should require an explicit data wallet")
	}
	cfg.Indicators.NofxOSAPIKey = "user-provided-key"
	if strategyNeedsPaidDataWallet(&cfg) {
		t.Fatal("AI500 with a user NofxOS key should not borrow a Claw402 wallet")
	}
}
