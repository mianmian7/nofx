package market

import (
	"errors"
	"strings"
	"testing"
)

type freshnessContractProvider struct {
	regularKlineCalls int
	freshKlineCalls   int
	oiErr             error
	fundingErr        error
}

func (provider *freshnessContractProvider) Exchange() string                     { return "contract-test" }
func (provider *freshnessContractProvider) NormalizeSymbol(symbol string) string { return symbol }
func (provider *freshnessContractProvider) Capabilities() MarketCapability {
	return TradingCapabilities
}
func (provider *freshnessContractProvider) GetCurrentPrice(string) (float64, error) { return 100, nil }
func (provider *freshnessContractProvider) GetCurrentPriceFresh(string) (float64, error) {
	return 100, nil
}
func (provider *freshnessContractProvider) GetKlines(string, string, int) ([]Kline, error) {
	provider.regularKlineCalls++
	return nil, errors.New("non-fresh K-line path must not be used")
}
func (provider *freshnessContractProvider) GetKlinesFresh(string, string, int) ([]Kline, error) {
	provider.freshKlineCalls++
	return []Kline{
		{OpenTime: 1, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1},
		{OpenTime: 2, Open: 100, High: 102, Low: 99, Close: 101, Volume: 1},
	}, nil
}
func (provider *freshnessContractProvider) GetDepth(string, int) (*DepthSnapshot, error) {
	return nil, errors.New("unused")
}
func (provider *freshnessContractProvider) GetDepthFresh(string, int) (*DepthSnapshot, error) {
	return nil, errors.New("unused")
}
func (provider *freshnessContractProvider) GetFundingSnapshot(string) (*FundingSnapshot, error) {
	if provider.fundingErr != nil {
		return nil, provider.fundingErr
	}
	return &FundingSnapshot{}, nil
}
func (provider *freshnessContractProvider) GetFundingHistory(string, int64, int64) ([]FundingEvent, error) {
	return nil, nil
}
func (provider *freshnessContractProvider) GetOpenInterest(string) (*OIData, error) {
	if provider.oiErr != nil {
		return nil, provider.oiErr
	}
	return &OIData{}, nil
}

func TestCompleteMarketContextRejectsMissingRequiredFields(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		provider   *freshnessContractProvider
		capability MarketCapability
	}{
		{name: "open interest", provider: &freshnessContractProvider{oiErr: errors.New("OI unavailable")}, capability: CapabilityOpenInterest},
		{name: "funding", provider: &freshnessContractProvider{fundingErr: errors.New("funding unavailable")}, capability: CapabilityFunding},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := GetWithTimeframesForProvider(testCase.provider, "BTCUSDT", []string{"1m"}, "1m", 2)
			var unavailable *MarketDataUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("error = %v, want MarketDataUnavailableError", err)
			}
			if unavailable.Capability != testCase.capability {
				t.Fatalf("capability = %s, want %s", unavailable.Capability, testCase.capability)
			}
		})
	}
}
func (provider *freshnessContractProvider) GetContractSpec(string) (*ContractSpec, error) {
	return &ContractSpec{}, nil
}
func (provider *freshnessContractProvider) ListPerpetualSymbols(int) ([]string, error) {
	return nil, nil
}
func (provider *freshnessContractProvider) ValidateMarketAvailability(string) (*MarketAvailability, error) {
	return &MarketAvailability{}, nil
}

func TestTradingMarketDataContractCannotSelectNonFreshKlines(t *testing.T) {
	provider := &freshnessContractProvider{}
	_, _ = GetWithTimeframesForProvider(provider, "BTCUSDT", []string{"1m"}, "1m", 2)
	if provider.regularKlineCalls != 0 {
		t.Fatalf("regular K-line calls = %d, want 0", provider.regularKlineCalls)
	}
	if provider.freshKlineCalls != 1 {
		t.Fatalf("fresh K-line calls = %d, want 1", provider.freshKlineCalls)
	}
}

func TestProviderCapabilityMatrixRejectsIncompleteTradingFeeds(t *testing.T) {
	for _, exchange := range []string{"binance", "okx", "bitget"} {
		provider, err := NewMarketDataProvider(exchange)
		if err != nil {
			t.Fatalf("NewMarketDataProvider(%s): %v", exchange, err)
		}
		if err := RequireMarketCapabilities(provider, TradingCapabilities); err != nil {
			t.Fatalf("%s complete trading capabilities rejected: %v", exchange, err)
		}
	}

	for _, exchange := range []string{"bybit", "gate", "kucoin", "hyperliquid", "aster"} {
		provider, err := NewMarketDataProvider(exchange)
		if err != nil {
			t.Fatalf("NewMarketDataProvider(%s): %v", exchange, err)
		}
		err = RequireMarketCapabilities(provider, TradingCapabilities)
		var capabilityErr *UnsupportedMarketCapabilityError
		if !errors.As(err, &capabilityErr) {
			t.Fatalf("%s capability error = %v, want UnsupportedMarketCapabilityError", exchange, err)
		}
		if !strings.Contains(err.Error(), exchange) {
			t.Fatalf("%s capability error lost venue identity: %v", exchange, err)
		}
	}
}
