package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"nofx/hook"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	baseURL                = "https://fapi.binance.com"
	binanceRequestTimeout  = 6 * time.Second
	binanceRetryDelay      = 200 * time.Millisecond
	binanceMaxAttempts     = 3
	binanceExchangeInfoTTL = 15 * time.Minute
	binanceDynamicFreshTTL = 30 * time.Second
	binanceDynamicStaleTTL = 15 * time.Minute
)

var binancePublicTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.MaxConnsPerHost = 40
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	return transport
}()

var binanceExchangeInfoCache struct {
	sync.Mutex
	value     *ExchangeInfo
	fetchedAt time.Time
}

var binanceDynamicTickerCache struct {
	sync.Mutex
	value     []Ticker24hr
	fetchedAt time.Time
}

type APIClient struct {
	client  *http.Client
	baseURL string
}

func NewAPIClient() *APIClient {
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: binancePublicTransport,
	}

	hookRes := hook.HookExec[hook.SetHttpClientResult](hook.SET_HTTP_CLIENT, client)
	if hookRes != nil && hookRes.Error() == nil {
		log.Printf("Using HTTP client set by Hook")
		client = hookRes.GetResult()
	}

	return &APIClient{
		client:  client,
		baseURL: baseURL,
	}
}

func NewAPIClientWithBaseURL(binanceBaseURL string) *APIClient {
	client := NewAPIClient()
	client.baseURL = strings.TrimRight(binanceBaseURL, "/")
	return client
}

func (c *APIClient) GetDepth(symbol string, limit int) (*BinanceDepthSnapshot, error) {
	if limit != 5 && limit != 10 && limit != 20 {
		return nil, fmt.Errorf("binance depth limit must be 5, 10, or 20")
	}
	var err error
	symbol, err = NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	var depth BinanceDepthSnapshot
	path := binancePath("/fapi/v1/depth", url.Values{"symbol": {symbol}, "limit": {strconv.Itoa(limit)}})
	if err := c.getBinanceJSON(path, &depth); err != nil {
		return nil, err
	}
	return &depth, nil
}

func (c *APIClient) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	var err error
	symbol, err = NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		Time            int64  `json:"time"`
	}
	path := binancePath("/fapi/v1/premiumIndex", url.Values{"symbol": {symbol}})
	if err := c.getBinanceJSON(path, &raw); err != nil {
		return nil, err
	}
	markPrice, err := strconv.ParseFloat(raw.MarkPrice, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Binance funding mark price: %w", err)
	}
	indexPrice, err := strconv.ParseFloat(raw.IndexPrice, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Binance funding index price: %w", err)
	}
	rate, err := strconv.ParseFloat(raw.LastFundingRate, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Binance funding rate: %w", err)
	}
	return &FundingSnapshot{
		Symbol: raw.Symbol, MarkPrice: markPrice, IndexPrice: indexPrice,
		Rate: rate, NextFundingTime: raw.NextFundingTime, Time: raw.Time,
	}, nil
}

func (c *APIClient) GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error) {
	var err error
	symbol, err = NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	events := make([]FundingEvent, 0)
	for cursor := startTime; cursor <= endTime; {
		var raw []struct {
			Symbol      string `json:"symbol"`
			FundingRate string `json:"fundingRate"`
			FundingTime int64  `json:"fundingTime"`
			MarkPrice   string `json:"markPrice"`
		}
		path := binancePath("/fapi/v1/fundingRate", url.Values{
			"symbol": {symbol}, "startTime": {strconv.FormatInt(cursor, 10)},
			"endTime": {strconv.FormatInt(endTime, 10)}, "limit": {"1000"},
		})
		if err := c.getBinanceJSON(path, &raw); err != nil {
			return nil, err
		}
		maxFundingTime := cursor - 1
		for _, item := range raw {
			rate, err := strconv.ParseFloat(item.FundingRate, 64)
			if err != nil {
				return nil, fmt.Errorf("parse Binance funding rate at %d: %w", item.FundingTime, err)
			}
			markPrice := 0.0
			if item.MarkPrice != "" {
				markPrice, err = strconv.ParseFloat(item.MarkPrice, 64)
				if err != nil {
					return nil, fmt.Errorf("parse Binance funding mark price at %d: %w", item.FundingTime, err)
				}
			}
			events = append(events, FundingEvent{
				Symbol: item.Symbol, Rate: rate, FundingTime: item.FundingTime, MarkPrice: markPrice,
			})
			if item.FundingTime > maxFundingTime {
				maxFundingTime = item.FundingTime
			}
		}
		if len(raw) < 1000 || maxFundingTime < cursor || maxFundingTime >= endTime {
			break
		}
		cursor = maxFundingTime + 1
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].FundingTime < events[j].FundingTime })
	return events, nil
}

func (c *APIClient) GetFundingInfo(symbol string) (*FundingInfo, error) {
	var err error
	symbol, err = NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Symbol                   string `json:"symbol"`
		AdjustedFundingRateCap   string `json:"adjustedFundingRateCap"`
		AdjustedFundingRateFloor string `json:"adjustedFundingRateFloor"`
		FundingIntervalHours     int    `json:"fundingIntervalHours"`
	}
	path := binancePath("/fapi/v1/fundingInfo", url.Values{"symbol": {symbol}})
	if err := c.getBinanceJSON(path, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("Binance funding info for %s not found", symbol)
	}
	capRate, err := strconv.ParseFloat(raw[0].AdjustedFundingRateCap, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Binance funding rate cap: %w", err)
	}
	floorRate, err := strconv.ParseFloat(raw[0].AdjustedFundingRateFloor, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Binance funding rate floor: %w", err)
	}
	return &FundingInfo{
		Symbol: raw[0].Symbol, RateCap: capRate, RateFloor: floorRate,
		IntervalHours: raw[0].FundingIntervalHours,
	}, nil
}

func (c *APIClient) GetExchangeInfo() (*ExchangeInfo, error) {
	binanceExchangeInfoCache.Lock()
	defer binanceExchangeInfoCache.Unlock()

	if binanceExchangeInfoCache.value != nil && time.Since(binanceExchangeInfoCache.fetchedAt) < binanceExchangeInfoTTL {
		return binanceExchangeInfoCache.value, nil
	}

	var exchangeInfo ExchangeInfo
	if err := c.getBinanceJSON("/fapi/v1/exchangeInfo", &exchangeInfo); err != nil {
		return nil, err
	}
	binanceExchangeInfoCache.value = &exchangeInfo
	binanceExchangeInfoCache.fetchedAt = time.Now()
	return &exchangeInfo, nil
}

// Get24hrTickers returns Binance Futures 24-hour statistics for all symbols.
// The endpoint is public and does not require exchange credentials.
func (c *APIClient) Get24hrTickers() ([]Ticker24hr, error) {
	var tickers []Ticker24hr
	if err := c.getBinanceJSON("/fapi/v1/ticker/24hr", &tickers); err != nil {
		return nil, err
	}
	return tickers, nil
}

// GetKlines returns recent Binance USDⓈ-M Futures candles from the official
// public endpoint. Binance symbols are normalized without consulting any
// Hyperliquid/XYZ asset registry.
func (c *APIClient) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	var err error
	symbol, err = NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	interval, err = NormalizeTimeframe(interval)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > binanceMaxKlineLimit {
		limit = binanceMaxKlineLimit
	}

	path := binancePath("/fapi/v1/klines", url.Values{
		"symbol": {symbol}, "interval": {interval}, "limit": {strconv.Itoa(limit)},
	})
	var raw [][]json.RawMessage
	if err := c.getBinanceJSON(path, &raw); err != nil {
		return nil, err
	}

	klines := make([]Kline, 0, len(raw))
	for i, item := range raw {
		if len(item) < 11 {
			return nil, fmt.Errorf("binance kline %d has %d fields, want at least 11", i, len(item))
		}
		openTime, err := parseBinanceInt64(item[0])
		if err != nil {
			return nil, fmt.Errorf("binance kline %d open time: %w", i, err)
		}
		closeTime, err := parseBinanceInt64(item[6])
		if err != nil {
			return nil, fmt.Errorf("binance kline %d close time: %w", i, err)
		}
		trades, err := parseBinanceInt64(item[8])
		if err != nil {
			return nil, fmt.Errorf("binance kline %d trades: %w", i, err)
		}
		values := make([]float64, 8)
		for valueIndex, rawIndex := range []int{1, 2, 3, 4, 5, 7, 9, 10} {
			values[valueIndex], err = parseBinanceFloat(item[rawIndex])
			if err != nil {
				return nil, fmt.Errorf("binance kline %d field %d: %w", i, rawIndex, err)
			}
		}
		klines = append(klines, Kline{
			OpenTime:            openTime,
			Open:                values[0],
			High:                values[1],
			Low:                 values[2],
			Close:               values[3],
			Volume:              values[4],
			CloseTime:           closeTime,
			QuoteVolume:         values[5],
			Trades:              int(trades),
			TakerBuyBaseVolume:  values[6],
			TakerBuyQuoteVolume: values[7],
		})
	}
	return klines, nil
}

func parseBinanceFloat(raw json.RawMessage) (float64, error) {
	return strconv.ParseFloat(strings.Trim(string(raw), `"`), 64)
}

func parseBinanceInt64(raw json.RawMessage) (int64, error) {
	return strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
}

func GetBinanceKlines(symbol, interval string, limit int) ([]Kline, error) {
	return NewAPIClient().GetKlines(symbol, interval, limit)
}

func (c *APIClient) getBinanceJSON(path string, target any) error {
	clientBaseURL := c.baseURL
	if clientBaseURL == "" {
		clientBaseURL = baseURL
	}
	url := fmt.Sprintf("%s%s", clientBaseURL, path)
	var lastErr error
	for attempt := 1; attempt <= binanceMaxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), binanceRequestTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			return err
		}
		resp, err := c.client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				err = readErr
			} else if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
				err = fmt.Errorf("binance request %s failed with status %d", path, resp.StatusCode)
				if resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
					cancel()
					return err
				}
			} else if decodeErr := json.Unmarshal(body, target); decodeErr != nil {
				err = decodeErr
			} else {
				cancel()
				return nil
			}
		}
		cancel()
		lastErr = err
		if attempt < binanceMaxAttempts {
			time.Sleep(binanceRetryDelay * time.Duration(1<<(attempt-1)))
		}
	}
	return lastErr
}

// GetBinanceDynamicTickers builds a deterministic local candidate universe:
// active USDT perpetual contracts ranked by current 24-hour quote volume.
// It deliberately excludes stablecoin-vs-stablecoin contracts and never calls
// Claw402, Vergex, NofxOS, or an authenticated exchange endpoint.
func (c *APIClient) GetBinanceDynamicTickers(limit int) ([]Ticker24hr, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 10 {
		limit = 10
	}

	binanceDynamicTickerCache.Lock()
	defer binanceDynamicTickerCache.Unlock()
	if len(binanceDynamicTickerCache.value) > 0 && time.Since(binanceDynamicTickerCache.fetchedAt) < binanceDynamicFreshTTL {
		return limitBinanceTickers(binanceDynamicTickerCache.value, limit), nil
	}

	exchangeInfo, err := c.GetExchangeInfo()
	if err != nil {
		return staleBinanceDynamicTickers(limit, fmt.Errorf("get Binance exchange info: %w", err))
	}
	tickers, err := c.Get24hrTickers()
	if err != nil {
		return staleBinanceDynamicTickers(limit, fmt.Errorf("get Binance 24h tickers: %w", err))
	}

	stableBases := map[string]bool{
		"BUSD": true, "DAI": true, "FDUSD": true, "TUSD": true,
		"USDC": true, "USDP": true, "USDT": true,
	}
	eligible := make(map[string]bool, len(exchangeInfo.Symbols))
	for _, symbol := range exchangeInfo.Symbols {
		if symbol.Status != "TRADING" || symbol.QuoteAsset != "USDT" || symbol.ContractType != "PERPETUAL" {
			continue
		}
		if stableBases[strings.ToUpper(symbol.BaseAsset)] {
			continue
		}
		eligible[strings.ToUpper(symbol.Symbol)] = true
	}

	ranked := make([]Ticker24hr, 0, len(eligible))
	for _, ticker := range tickers {
		ticker.Symbol = strings.ToUpper(strings.TrimSpace(ticker.Symbol))
		if !eligible[ticker.Symbol] {
			continue
		}
		if _, err := strconv.ParseFloat(ticker.QuoteVolume, 64); err != nil {
			continue
		}
		ranked = append(ranked, ticker)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left, _ := strconv.ParseFloat(ranked[i].QuoteVolume, 64)
		right, _ := strconv.ParseFloat(ranked[j].QuoteVolume, 64)
		if left == right {
			return ranked[i].Symbol < ranked[j].Symbol
		}
		return left > right
	})
	binanceDynamicTickerCache.value = append([]Ticker24hr(nil), ranked...)
	binanceDynamicTickerCache.fetchedAt = time.Now()
	return limitBinanceTickers(ranked, limit), nil
}

// GetBinanceTradFiTickers returns every active Binance USDⓈ-M TradFi perpetual.
// This is a separate universe from the crypto-only dynamic Top 10 so selecting
// a watchlist never silently changes the default candidate-ranking policy.
func (c *APIClient) GetBinanceTradFiTickers() ([]BinanceTradFiTicker, error) {
	exchangeInfo, err := c.GetExchangeInfo()
	if err != nil {
		return nil, fmt.Errorf("get Binance exchange info: %w", err)
	}
	tickers, err := c.Get24hrTickers()
	if err != nil {
		return nil, fmt.Errorf("get Binance 24h tickers: %w", err)
	}

	contracts := make(map[string]SymbolInfo, len(exchangeInfo.Symbols))
	for _, symbol := range exchangeInfo.Symbols {
		if symbol.Status != "TRADING" || symbol.QuoteAsset != "USDT" || symbol.ContractType != "TRADIFI_PERPETUAL" {
			continue
		}
		contracts[strings.ToUpper(symbol.Symbol)] = symbol
	}

	result := make([]BinanceTradFiTicker, 0, len(contracts))
	for _, ticker := range tickers {
		ticker.Symbol = strings.ToUpper(strings.TrimSpace(ticker.Symbol))
		contract, ok := contracts[ticker.Symbol]
		if !ok {
			continue
		}
		if _, err := strconv.ParseFloat(ticker.QuoteVolume, 64); err != nil {
			continue
		}
		result = append(result, BinanceTradFiTicker{
			Ticker24hr:     ticker,
			BaseAsset:      strings.ToUpper(contract.BaseAsset),
			UnderlyingType: strings.ToUpper(contract.UnderlyingType),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, _ := strconv.ParseFloat(result[i].QuoteVolume, 64)
		right, _ := strconv.ParseFloat(result[j].QuoteVolume, 64)
		if left == right {
			return result[i].Symbol < result[j].Symbol
		}
		return left > right
	})
	return result, nil
}

func staleBinanceDynamicTickers(limit int, upstreamErr error) ([]Ticker24hr, error) {
	if len(binanceDynamicTickerCache.value) > 0 && time.Since(binanceDynamicTickerCache.fetchedAt) < binanceDynamicStaleTTL {
		log.Printf("Binance dynamic candidate refresh failed; using cached candidates: %v", upstreamErr)
		return limitBinanceTickers(binanceDynamicTickerCache.value, limit), nil
	}
	return nil, upstreamErr
}

func limitBinanceTickers(tickers []Ticker24hr, limit int) []Ticker24hr {
	if len(tickers) < limit {
		limit = len(tickers)
	}
	return append([]Ticker24hr(nil), tickers[:limit]...)
}

func (c *APIClient) GetBinanceDynamicSymbols(limit int) ([]string, error) {
	tickers, err := c.GetBinanceDynamicTickers(limit)
	if err != nil {
		return nil, err
	}
	symbols := make([]string, 0, len(tickers))
	for _, ticker := range tickers {
		symbols = append(symbols, ticker.Symbol)
	}
	return symbols, nil
}

func (c *APIClient) GetCurrentPrice(symbol string) (float64, error) {
	symbol, err := NormalizeBinanceSymbol(symbol)
	if err != nil {
		return 0, err
	}
	var ticker PriceTicker
	path := binancePath("/fapi/v1/ticker/price", url.Values{"symbol": {symbol}})
	if err := c.getBinanceJSON(path, &ticker); err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

func binancePath(endpoint string, query url.Values) string {
	if len(query) == 0 {
		return endpoint
	}
	return endpoint + "?" + query.Encode()
}
