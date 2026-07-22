package trader

import "testing"

func TestValidateExecutionSymbolBinanceOnly(t *testing.T) {
	tests := []struct {
		name     string
		exchange string
		symbol   string
		wantErr  bool
	}{
		{name: "canonical Binance perp", exchange: "binance", symbol: "BTCUSDT"},
		{name: "case normalized", exchange: "BINANCE", symbol: "ethusdt"},
		{name: "reject xyz", exchange: "binance", symbol: "xyz:MU", wantErr: true},
		{name: "reject Hyperliquid USDC", exchange: "binance", symbol: "MU-USDC", wantErr: true},
		{name: "reject bare symbol", exchange: "binance", symbol: "MU", wantErr: true},
		{name: "preserve other venue", exchange: "aster", symbol: "BTCUSDT"},
		{name: "reject xyz on other venue", exchange: "aster", symbol: "xyz:MU", wantErr: true},
		{name: "reject disabled Hyperliquid", exchange: "hyperliquid", symbol: "BTCUSDT", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExecutionSymbol(tt.exchange, tt.symbol)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateExecutionSymbol(%q, %q) error = %v, wantErr %v", tt.exchange, tt.symbol, err, tt.wantErr)
			}
		})
	}
}
