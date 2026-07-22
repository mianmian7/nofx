package trader

import (
	"strings"
	"testing"
)

func TestNewAutoTraderRejectsUnknownAIProvider(t *testing.T) {
	_, err := NewAutoTrader(AutoTraderConfig{
		AIModel: "unknown-provider",
		Exchange: "binance",
	}, nil, "user")
	if err == nil || !strings.Contains(err.Error(), "unsupported AI provider") {
		t.Fatalf("error = %v, want unsupported AI provider", err)
	}
}
