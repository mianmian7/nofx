package market

import (
	"context"
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
	Capabilities() MarketCapability

	GetCurrentPrice(symbol string) (float64, error)
	GetCurrentPriceFresh(symbol string) (float64, error)
	GetKlines(symbol, interval string, limit int) ([]Kline, error)
	GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error)
	GetDepth(symbol string, limit int) (*DepthSnapshot, error)
	GetDepthFresh(symbol string, limit int) (*DepthSnapshot, error)
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
func (provider *BinanceMarketDataProvider) Capabilities() MarketCapability {
	return TradingCapabilities
}

func (provider *BinanceMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange("binance", symbol)
}

func (provider *BinanceMarketDataProvider) GetCurrentPrice(symbol string) (float64, error) {
	return provider.GetCurrentPriceFresh(symbol)
}

func (provider *BinanceMarketDataProvider) GetCurrentPriceFresh(symbol string) (float64, error) {
	return provider.client.GetCurrentPriceFresh(provider.NormalizeSymbol(symbol))
}

func (provider *BinanceMarketDataProvider) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	return provider.GetKlinesFresh(symbol, interval, limit)
}

func (provider *BinanceMarketDataProvider) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return provider.GetKlinesFreshContext(context.Background(), symbol, interval, limit)
}

func (provider *BinanceMarketDataProvider) GetKlinesFreshContext(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedSymbol := provider.NormalizeSymbol(symbol)
	klines, err := provider.client.GetKlinesFreshContext(ctx, normalizedSymbol, interval, limit)
	recordKlineHealth(provider.Exchange(), normalizedSymbol, interval, proofFromKlines(provider.Exchange(), "primary-rest", klines), err)
	return klines, err
}

func (provider *BinanceMarketDataProvider) GetDepth(symbol string, limit int) (*DepthSnapshot, error) {
	return provider.GetDepthFresh(symbol, limit)
}

func (provider *BinanceMarketDataProvider) GetDepthFresh(symbol string, limit int) (*DepthSnapshot, error) {
	depth, err := provider.client.GetDepthFresh(provider.NormalizeSymbol(symbol), limit)
	if err == nil {
		recordDepthRESTFallback(provider.Exchange(), symbol, depth)
	}
	return depth, err
}

func (provider *BinanceMarketDataProvider) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	return provider.GetFundingSnapshotContext(context.Background(), symbol)
}

func (provider *BinanceMarketDataProvider) GetFundingSnapshotContext(ctx context.Context, symbol string) (*FundingSnapshot, error) {
	return provider.client.GetFundingSnapshotContext(ctx, provider.NormalizeSymbol(symbol))
}

func (provider *BinanceMarketDataProvider) GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error) {
	return provider.client.GetFundingHistory(provider.NormalizeSymbol(symbol), startTime, endTime)
}

func (provider *BinanceMarketDataProvider) GetOpenInterest(symbol string) (*OIData, error) {
	return provider.GetOpenInterestContext(context.Background(), symbol)
}

func (provider *BinanceMarketDataProvider) GetOpenInterestContext(ctx context.Context, symbol string) (*OIData, error) {
	return provider.client.GetOpenInterestContext(ctx, provider.NormalizeSymbol(symbol))
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

func (provider *unavailableMarketDataProvider) Exchange() string               { return provider.exchange }
func (provider *unavailableMarketDataProvider) Capabilities() MarketCapability { return 0 }

func (provider *unavailableMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange(provider.exchange, symbol)
}

func (provider *unavailableMarketDataProvider) unavailableError() error {
	return fmt.Errorf("%s market-data provider is unavailable: %w", provider.exchange, provider.err)
}

func (provider *unavailableMarketDataProvider) GetCurrentPrice(string) (float64, error) {
	return 0, provider.unavailableError()
}

func (provider *unavailableMarketDataProvider) GetCurrentPriceFresh(string) (float64, error) {
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

func (provider *unavailableMarketDataProvider) GetDepthFresh(string, int) (*DepthSnapshot, error) {
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

func nowUTC() (value time.Time) { return time.Now().UTC() }

func proofFromKlines(exchange, transport string, klines []Kline) FreshnessProof {
	if len(klines) == 0 {
		return FreshnessProof{Exchange: exchange, Transport: transport}
	}
	receivedAt := nowUTC()
	sourceTime := time.UnixMilli(klines[len(klines)-1].OpenTime).UTC()
	return FreshnessProof{
		Exchange: exchange, Transport: transport, SourceTime: sourceTime,
		ReceivedAt: receivedAt, Age: receivedAt.Sub(sourceTime), SequenceOK: true, Reconciled: true,
	}
}

// NewMarketDataProvider returns the canonical public provider for an exchange.
// Trading analysis may use only native exchange data. Exchanges without a
// native provider remain explicitly unavailable; UI-only CoinAnk adapters are
// kept in their API handlers and are never selected here.
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
		return NewUnavailableMarketDataProvider(normalizedExchange, fmt.Errorf("native market-data provider unavailable; CoinAnk is disabled for trading")), nil
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
