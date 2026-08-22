package trader

import (
	"nofx/store"
	"strings"
	"testing"
)

func TestNewAutoTraderRejectsUnknownAIProvider(t *testing.T) {
	_, err := NewAutoTrader(AutoTraderConfig{
		AIModel:  "unknown-provider",
		Exchange: "binance",
	}, nil, "user")
	if err == nil || !strings.Contains(err.Error(), "unsupported AI provider") {
		t.Fatalf("error = %v, want unsupported AI provider", err)
	}
}

func TestNewAutoTraderRejectsIncompleteMarketDataProviderBeforeStart(t *testing.T) {
	strategy := store.GetDefaultStrategyConfig("en")
	_, err := NewAutoTrader(AutoTraderConfig{
		ID: "incomplete-feed", Name: "Incomplete feed", AIModel: "custom",
		Exchange: "bybit", ExecutionMode: ExecutionModePaper,
		InitialBalance: 10_000, StrategyConfig: &strategy,
	}, nil, "user")
	if err == nil || !strings.Contains(err.Error(), "without complete fresh market data") || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("error = %v, want startup rejection for incomplete fresh feed", err)
	}
}
