package api

import "testing"

func TestIsSupportedTraderSymbol(t *testing.T) {
	tests := []struct {
		name     string
		exchange string
		symbol   string
		want     bool
	}{
		{name: "Binance USDT perp", exchange: "binance", symbol: "BTCUSDT", want: true},
		{name: "Binance USDT perp lowercase", exchange: "binance", symbol: "ethusdt", want: true},
		{name: "Hyperliquid xyz stock USDC pair rejected", exchange: "binance", symbol: "SMSN-USDC", want: false},
		{name: "Hyperliquid xyz commodity USDC pair rejected on other venue", exchange: "bybit", symbol: "GOLD-USDC", want: false},
		{name: "legacy internal xyz prefix rejected", exchange: "okx", symbol: "xyz:SMSN", want: false},
		{name: "empty slot ignored", exchange: "binance", symbol: "  ", want: true},
		{name: "bare stock rejected by Binance", exchange: "binance", symbol: "SMSN", want: false},
		{name: "non-USDT rejected by Binance", exchange: "binance", symbol: "BTCUSD", want: false},
		{name: "other venue keeps its native symbol format", exchange: "bybit", symbol: "BTCUSD", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSupportedTraderSymbol(tt.exchange, tt.symbol); got != tt.want {
				t.Fatalf("isSupportedTraderSymbol(%q, %q) = %v, want %v", tt.exchange, tt.symbol, got, tt.want)
			}
		})
	}
}
