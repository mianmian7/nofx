package market

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MarketDataProvider is the exchange-neutral public market-data contract used
// by strategy analysis, paper trading, API handlers, and frontend diagnostics.
// Symbols exposed by this interface use NOFX's canonical form, for example
// BTCUSDT. Each provider converts that form to the venue-specific format.
type MarketDataProvider interface {
	Exchange() string
	NormalizeSymbol(symbol string) string

	GetCurrentPrice(symbol string) (float64, error)
	GetKlines(symbol, interval string, limit int) ([]Kline, error)
	GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error)
	GetDepth(symbol string, limit int) (*DepthSnapshot, error)
	GetFundingSnapshot(symbol string) (*FundingSnapshot, error)
	GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error)
	GetOpenInterest(symbol string) (*OIData, error)
	GetContractSpec(symbol string) (*ContractSpec, error)
	ListPerpetualSymbols(limit int) ([]string, error)
	ValidateMarketAvailability(symbol string) (*MarketAvailability, error)
}

// BinanceMarketDataProvider adapts the existing Binance APIClient to the
// exchange-neutral market-data interface. APIClient retains ownership of the
// Binance proxy, cache, fresh/stale, singleflight, and rate-limit behavior.
type BinanceMarketDataProvider struct {
	client *APIClient
}

func NewBinanceMarketDataProvider(client *APIClient) *BinanceMarketDataProvider {
	if client == nil {
		client = NewAPIClient()
	}
	return &BinanceMarketDataProvider{client: client}
}

func (provider *BinanceMarketDataProvider) Exchange() string { return "binance" }

func (provider *BinanceMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange("binance", symbol)
}

func (provider *BinanceMarketDataProvider) GetCurrentPrice(symbol string) (float64, error) {
	return provider.client.GetCurrentPrice(provider.NormalizeSymbol(symbol))
}

func (provider *BinanceMarketDataProvider) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	return provider.client.GetKlines(provider.NormalizeSymbol(symbol), interval, limit)
}

func (provider *BinanceMarketDataProvider) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return provider.client.GetKlinesFresh(provider.NormalizeSymbol(symbol), interval, limit)
}

func (provider *BinanceMarketDataProvider) GetDepth(symbol string, limit int) (*DepthSnapshot, error) {
	return provider.client.GetDepth(provider.NormalizeSymbol(symbol), limit)
}

func (provider *BinanceMarketDataProvider) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	return provider.client.GetFundingSnapshot(provider.NormalizeSymbol(symbol))
}

func (provider *BinanceMarketDataProvider) GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error) {
	return provider.client.GetFundingHistory(provider.NormalizeSymbol(symbol), startTime, endTime)
}

func (provider *BinanceMarketDataProvider) GetOpenInterest(symbol string) (*OIData, error) {
	return provider.client.GetOpenInterest(provider.NormalizeSymbol(symbol))
}

func (provider *BinanceMarketDataProvider) GetContractSpec(symbol string) (*ContractSpec, error) {
	return provider.client.GetContractSpec(provider.NormalizeSymbol(symbol))
}

func (provider *BinanceMarketDataProvider) ListPerpetualSymbols(limit int) ([]string, error) {
	return provider.client.GetBinanceDynamicSymbols(limit)
}

func (provider *BinanceMarketDataProvider) ValidateMarketAvailability(symbol string) (*MarketAvailability, error) {
	return provider.client.ValidateMarketAvailability(provider.NormalizeSymbol(symbol))
}

// coinAnkMarketDataProvider preserves support for exchanges that do not yet
// have a native provider in this package. It intentionally does not fall back
// to Binance: a successful response must identify the requested venue.
type coinAnkMarketDataProvider struct {
	exchange string
}

// unavailableMarketDataProvider preserves the requested exchange identity
// when provider construction fails. Returning a failing provider is safer than
// silently switching a trader to Binance and analyzing a different market.
type unavailableMarketDataProvider struct {
	exchange string
	err      error
}

var nativeProviderRegistry = struct {
	sync.Mutex
	providers map[string]MarketDataProvider
}{
	providers: make(map[string]MarketDataProvider),
}

func NewUnavailableMarketDataProvider(exchange string, err error) MarketDataProvider {
	if err == nil {
		err = fmt.Errorf("market-data provider is unavailable")
	}
	return &unavailableMarketDataProvider{
		exchange: strings.ToLower(strings.TrimSpace(exchange)),
		err:      err,
	}
}

func (provider *unavailableMarketDataProvider) Exchange() string { return provider.exchange }

func (provider *unavailableMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange(provider.exchange, symbol)
}

func (provider *unavailableMarketDataProvider) unavailableError() error {
	return fmt.Errorf("%s market-data provider is unavailable: %w", provider.exchange, provider.err)
}

func (provider *unavailableMarketDataProvider) GetCurrentPrice(string) (float64, error) {
	return 0, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetKlines(string, string, int) ([]Kline, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetKlinesFresh(string, string, int) ([]Kline, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetDepth(string, int) (*DepthSnapshot, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetFundingSnapshot(string) (*FundingSnapshot, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetFundingHistory(string, int64, int64) ([]FundingEvent, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetOpenInterest(string) (*OIData, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetContractSpec(string) (*ContractSpec, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) ListPerpetualSymbols(int) ([]string, error) {
	return nil, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) ValidateMarketAvailability(string) (*MarketAvailability, error) {
	return nil, provider.unavailableError()
}

func newCoinAnkMarketDataProvider(exchange string) *coinAnkMarketDataProvider {
	return &coinAnkMarketDataProvider{exchange: strings.ToLower(strings.TrimSpace(exchange))}
}

func (provider *coinAnkMarketDataProvider) Exchange() string { return provider.exchange }

func (provider *coinAnkMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange(provider.exchange, symbol)
}

func (provider *coinAnkMarketDataProvider) GetCurrentPrice(symbol string) (float64, error) {
	klines, err := provider.GetKlines(symbol, "1m", 1)
	if err != nil {
		return 0, err
	}
	if len(klines) == 0 || klines[len(klines)-1].Close <= 0 {
		return 0, fmt.Errorf("%s returned no current price for %s", provider.exchange, symbol)
	}
	return klines[len(klines)-1].Close, nil
}

func (provider *coinAnkMarketDataProvider) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	return getKlinesFromCoinAnk(provider.NormalizeSymbol(symbol), interval, provider.exchange, limit)
}

func (provider *coinAnkMarketDataProvider) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return getKlinesFromCoinAnkFresh(provider.NormalizeSymbol(symbol), interval, provider.exchange, limit)
}

func (provider *coinAnkMarketDataProvider) GetDepth(string, int) (*DepthSnapshot, error) {
	return nil, fmt.Errorf("%s public depth provider is not implemented", provider.exchange)
}

func (provider *coinAnkMarketDataProvider) GetFundingSnapshot(string) (*FundingSnapshot, error) {
	return nil, fmt.Errorf("%s public funding provider is not implemented", provider.exchange)
}

func (provider *coinAnkMarketDataProvider) GetFundingHistory(string, int64, int64) ([]FundingEvent, error) {
	return nil, fmt.Errorf("%s public funding history provider is not implemented", provider.exchange)
}

func (provider *coinAnkMarketDataProvider) GetOpenInterest(string) (*OIData, error) {
	return nil, fmt.Errorf("%s public open-interest provider is not implemented", provider.exchange)
}

func (provider *coinAnkMarketDataProvider) GetContractSpec(string) (*ContractSpec, error) {
	return nil, fmt.Errorf("%s contract specification provider is not implemented", provider.exchange)
}

func (provider *coinAnkMarketDataProvider) ListPerpetualSymbols(int) ([]string, error) {
	return nil, fmt.Errorf("%s perpetual-symbol provider is not implemented", provider.exchange)
}

func (provider *coinAnkMarketDataProvider) ValidateMarketAvailability(symbol string) (*MarketAvailability, error) {
	price, err := provider.GetCurrentPrice(symbol)
	if err != nil {
		return nil, err
	}
	return &MarketAvailability{
		Symbol:    provider.NormalizeSymbol(symbol),
		Price:     price,
		CheckedAt: nowUTC(),
	}, nil
}

func nowUTC() (value time.Time) { return time.Now().UTC() }

// NewMarketDataProvider returns the canonical public provider for an exchange.
// Binance, OKX, and Bitget use native APIs; other supported legacy CEXes keep
// their CoinAnk path until a native provider is added.
func NewMarketDataProvider(exchange string) (MarketDataProvider, error) {
	normalizedExchange := strings.ToLower(strings.TrimSpace(exchange))
	switch normalizedExchange {
	case "", "binance":
		return NewBinanceMarketDataProvider(nil), nil
	case "okx":
		return getRegisteredNativeProvider("okx", func() MarketDataProvider {
			return NewOKXMarketDataProvider()
		}), nil
	case "bitget":
		return getRegisteredNativeProvider("bitget", func() MarketDataProvider {
			return NewBitgetMarketDataProvider()
		}), nil
	case "bybit", "gate", "kucoin", "hyperliquid", "aster":
		return newCoinAnkMarketDataProvider(normalizedExchange), nil
	default:
		return nil, fmt.Errorf("unsupported public market-data exchange: %s", exchange)
	}
}

func getRegisteredNativeProvider(exchange string, create func() MarketDataProvider) MarketDataProvider {
	nativeProviderRegistry.Lock()
	defer nativeProviderRegistry.Unlock()
	if provider, found := nativeProviderRegistry.providers[exchange]; found {
		return provider
	}
	provider := create()
	nativeProviderRegistry.providers[exchange] = provider
	return provider
}

// NewMarketDataProviderWithHTTPClient is intended for provider tests and
// local diagnostics. Production callers should use NewMarketDataProvider.
func NewMarketDataProviderWithHTTPClient(exchange, baseURL string, client *http.Client) (MarketDataProvider, error) {
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "okx":
		return NewOKXMarketDataProviderWithHTTPClient(baseURL, client), nil
	case "bitget":
		return NewBitgetMarketDataProviderWithHTTPClient(baseURL, client), nil
	default:
		return nil, fmt.Errorf("HTTP-client injection is only supported for native OKX and Bitget providers")
	}
}
