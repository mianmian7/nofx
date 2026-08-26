package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"nofx/hook"
	"nofx/market/binanceguard"
	"os"
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

var binancePublicTransport = newBinancePublicTransport(os.Getenv("BINANCE_HTTP_PROXY"))

func newBinancePublicTransport(proxyURL string) http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.MaxConnsPerHost = 40
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(proxyURL))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
			if err == nil {
				err = fmt.Errorf("proxy URL must have a scheme, host, and no embedded credentials")
			}
			log.Printf("Invalid BINANCE_HTTP_PROXY; using direct transport: %v", err)
		} else {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return transport
}

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

var sharedBinancePublicCoordinator = newSharedBinancePublicCoordinator()

type APIClient struct {
	client              *http.Client
	baseURL             string
	coordinator         *binancePublicCoordinator
	coordinatorOnce     sync.Once
	initializationError error
	requestTimeout      time.Duration
}

func NewAPIClient() *APIClient {
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: binancePublicTransport,
	}

	hookRes := hook.HookExec[hook.SetHttpClientResult](hook.SET_HTTP_CLIENT, client)
	var initializationError error
	if hookRes != nil {
		if hookErr := hookRes.Error(); hookErr != nil {
			log.Printf("Binance proxy HTTP client hook failed; direct fallback disabled: %v", hookErr)
			initializationError = fmt.Errorf("Binance proxy client unavailable: %w", hookErr)
			client = newUnavailableHTTPClient(initializationError)
		} else if hookRes.Client == nil {
			log.Printf("Binance proxy HTTP client hook returned no client; direct fallback disabled")
			initializationError = fmt.Errorf("Binance proxy client hook returned no client")
			client = newUnavailableHTTPClient(initializationError)
		} else {
			log.Printf("Using HTTP client set by Hook")
			client = hookRes.Client
		}
	}
	return &APIClient{
		client:              client,
		baseURL:             baseURL,
		coordinator:         sharedBinancePublicCoordinator,
		initializationError: initializationError,
	}
}

func NewAPIClientWithBaseURL(binanceBaseURL string) *APIClient {
	client := NewAPIClient()
	client.baseURL = strings.TrimRight(binanceBaseURL, "/")
	client.coordinator = newSharedBinancePublicCoordinator()
	return client
}

type unavailableRoundTripper struct {
	err error
}

func (transport unavailableRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func newUnavailableHTTPClient(err error) *http.Client {
	return &http.Client{
		Timeout:   binanceRequestTimeout,
		Transport: unavailableRoundTripper{err: err},
	}
}

func (c *APIClient) requestCoordinator() *binancePublicCoordinator {
	c.coordinatorOnce.Do(func() {
		if c.coordinator == nil {
			c.coordinator = newBinancePublicCoordinator()
		}
	})
	return c.coordinator
}

func (c *APIClient) GetDepth(symbol string, limit int) (*BinanceDepthSnapshot, error) {
	return c.GetDepthFresh(symbol, limit)
}

func (c *APIClient) GetDepthFresh(symbol string, limit int) (*BinanceDepthSnapshot, error) {
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
	if err := c.getFreshBinanceJSON(path, &depth); err != nil {
		return nil, err
	}
	depth.ReceivedAt = time.Now().UTC()
	depth.Exchange = "binance"
	depth.Transport = "rest"
	depth.Fresh = true
	return &depth, nil
}

func (c *APIClient) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	return c.GetFundingSnapshotContext(context.Background(), symbol)
}

func (c *APIClient) GetFundingSnapshotContext(ctx context.Context, symbol string) (*FundingSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
	if err := c.getBinanceJSONWithCacheContext(ctx, path, &raw, true); err != nil {
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

// GetContractSpec returns the normalized Binance contract rules for one
// perpetual symbol. Exact exchangeInfo filters are preferred; precision is
// retained as a fallback for older/mock responses that omit filters.
func (c *APIClient) GetContractSpec(symbol string) (*ContractSpec, error) {
	normalizedSymbol, err := NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	exchangeInfo, err := c.GetExchangeInfo()
	if err != nil {
		return nil, err
	}
	for _, item := range exchangeInfo.Symbols {
		if !strings.EqualFold(item.Symbol, normalizedSymbol) {
			continue
		}
		priceTick := math.Pow10(-item.PricePrecision)
		quantityStep := math.Pow10(-item.QuantityPrecision)
		minimumQuantity := 0.0
		maximumQuantity := 0.0
		for _, filter := range item.Filters {
			switch strings.ToUpper(strings.TrimSpace(filter.FilterType)) {
			case "PRICE_FILTER":
				if parsedTick, parseErr := strconv.ParseFloat(filter.TickSize, 64); parseErr == nil && parsedTick > 0 {
					priceTick = parsedTick
				}
			case "LOT_SIZE":
				if parsedStep, parseErr := strconv.ParseFloat(filter.StepSize, 64); parseErr == nil && parsedStep > 0 {
					quantityStep = parsedStep
				}
				minimumQuantity, _ = strconv.ParseFloat(filter.MinQty, 64)
				maximumQuantity, _ = strconv.ParseFloat(filter.MaxQty, 64)
			case "MARKET_LOT_SIZE":
				if maximumQuantity <= 0 {
					maximumQuantity, _ = strconv.ParseFloat(filter.MaxQty, 64)
				}
			}
		}
		return &ContractSpec{
			Symbol:             normalizedSymbol,
			ExchangeSymbol:     item.Symbol,
			BaseAsset:          item.BaseAsset,
			QuoteAsset:         item.QuoteAsset,
			ContractType:       item.ContractType,
			Status:             item.Status,
			ContractMultiplier: 1,
			PriceTick:          priceTick,
			QuantityStep:       quantityStep,
			MinQuantity:        minimumQuantity,
			MaxQuantity:        maximumQuantity,
			QuantityUnit:       "base",
		}, nil
	}
	return nil, fmt.Errorf("Binance contract %s not found", normalizedSymbol)
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
	return c.GetKlinesFresh(symbol, interval, limit)
}

// GetKlinesFresh is the strict variant used by live decision making. It never
// substitutes the stale snapshot when Binance fails or returns malformed data,
// and it rejects an upstream candle older than the interval freshness bound.
// Callers that need bounded degradation (paper trading, UI, diagnostics) should
// continue to use GetKlines.
func (c *APIClient) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return c.GetKlinesFreshContext(context.Background(), symbol, interval, limit)
}

func (c *APIClient) GetKlinesFreshContext(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	return c.getKlines(ctx, symbol, interval, limit)
}

func (c *APIClient) getKlines(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
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
	if requestErr := c.getFreshBinanceJSONContext(ctx, path, &raw); requestErr != nil {
		return nil, requestErr
	}
	klines, err := parseBinanceKlines(raw)
	if err != nil {
		return nil, err
	}
	if err := validateBinanceKlines(klines); err != nil {
		return nil, err
	}
	intervalDuration, durationErr := TFDuration(interval)
	if durationErr != nil {
		return nil, durationErr
	}
	if err := validateFreshPublicKlines("Binance", symbol, interval, klines, intervalDuration); err != nil {
		return nil, err
	}
	if len(klines) == 0 {
		return nil, fmt.Errorf("binance klines response is empty")
	}
	return klines, nil
}

func parseBinanceKlines(raw [][]json.RawMessage) ([]Kline, error) {
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

func validateBinanceKlines(klines []Kline) error {
	if len(klines) == 0 {
		return fmt.Errorf("binance klines response is empty")
	}
	var previousOpenTime int64
	for index, kline := range klines {
		for field, value := range map[string]float64{
			"open": kline.Open, "high": kline.High, "low": kline.Low, "close": kline.Close,
		} {
			if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("binance kline %d has invalid %s price", index, field)
			}
		}
		if kline.OpenTime > 0 && previousOpenTime > 0 && kline.OpenTime < previousOpenTime {
			return fmt.Errorf("binance klines are not sorted by open time")
		}
		if kline.OpenTime > 0 && kline.CloseTime > 0 && kline.CloseTime < kline.OpenTime {
			return fmt.Errorf("binance kline %d has invalid close time", index)
		}
		if kline.OpenTime > 0 {
			previousOpenTime = kline.OpenTime
		}
	}
	return nil
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

// GetBinanceKlinesFresh returns only a fresh upstream K-line response. It is
// intended for live decision paths that must fail closed during a Binance or
// proxy outage instead of reasoning from an old snapshot.
func GetBinanceKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return NewAPIClient().GetKlinesFresh(symbol, interval, limit)
}

func (c *APIClient) getBinanceJSON(path string, target any) error {
	return c.getBinanceJSONWithCacheContext(context.Background(), path, target, true)
}

func (c *APIClient) getFreshBinanceJSON(path string, target any) error {
	return c.getFreshBinanceJSONContext(context.Background(), path, target)
}

func (c *APIClient) getFreshBinanceJSONContext(ctx context.Context, path string, target any) error {
	return c.getBinanceJSONWithCacheContext(ctx, path, target, false)
}

func (c *APIClient) getBinanceJSONWithCacheContext(ctx context.Context, path string, target any, allowCache bool) error {
	responseBody, err := c.getBinanceResponseBodyContext(ctx, path, allowCache)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("decode Binance response for %s: %w", path, err)
	}
	return nil
}

func (c *APIClient) getBinanceResponseBody(path string, allowCache bool) ([]byte, error) {
	return c.getBinanceResponseBodyContext(context.Background(), path, allowCache)
}

func (c *APIClient) getBinanceResponseBodyContext(ctx context.Context, path string, allowCache bool) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.initializationError != nil {
		return nil, c.initializationError
	}
	clientBaseURL := c.binanceBaseURL()
	baseRequestKey := clientBaseURL + "|" + path
	requestKey := baseRequestKey
	if !allowCache {
		requestKey += "|fresh"
	}
	coordinator := c.requestCoordinator()
	now := time.Now()
	if allowCache {
		if cachedBody, found := coordinator.getFreshResponse(requestKey, now); found {
			return cachedBody, nil
		}
	}

	response, err, _ := coordinator.requestGroup.Do(requestKey, func() (any, error) {
		requestStartedAt := time.Now()
		if allowCache {
			if cachedBody, found := coordinator.getFreshResponse(requestKey, requestStartedAt); found {
				return cachedBody, nil
			}
		}
		probeRequest, circuitErr := coordinator.beforeRequest(path, requestStartedAt)
		if circuitErr != nil {
			return nil, circuitErr
		}

		responseBody, requestErr := c.executeBinanceRequest(ctx, clientBaseURL, path, probeRequest)
		if requestErr != nil {
			var admissionError *binanceguard.AdmissionError
			if errors.As(requestErr, &admissionError) {
				if probeRequest {
					coordinator.releaseExpiredCircuitProbe()
				}
				return nil, admissionError
			}
			// executeBinanceRequest already opened the circuit for 429/418/451
			// before releasing the admission slot so queued callers observe it
			// immediately. Do not re-open it here.
			if structuredError, ok := asBinancePublicError(requestErr); ok {
				if isBinanceCircuitStatus(structuredError.StatusCode) || !structuredError.CircuitUntil.IsZero() {
					return nil, structuredError
				}
				if probeRequest {
					coordinator.releaseExpiredCircuitProbe()
				}
				return nil, structuredError
			}
			if probeRequest {
				coordinator.releaseExpiredCircuitProbe()
			}
			return nil, &BinancePublicError{
				Endpoint: path,
				Message:  fmt.Sprintf("network request failed after %d bounded attempts: %v", binanceMaxAttempts, requestErr),
			}
		}

		coordinator.recordSuccess()
		cacheKey := requestKey
		if !allowCache {
			cacheKey = baseRequestKey
		}
		coordinator.storeResponse(cacheKey, responseBody, binanceCacheTTL(path), time.Now())
		return responseBody, nil
	})
	if err != nil {
		return nil, err
	}
	responseBody, ok := response.([]byte)
	if !ok {
		return nil, fmt.Errorf("unexpected Binance response type %T", response)
	}
	return append([]byte(nil), responseBody...), nil
}

func (c *APIClient) binanceBaseURL() string {
	if c.baseURL == "" {
		return baseURL
	}
	return c.baseURL
}

func (c *APIClient) executeBinanceRequest(ctx context.Context, clientBaseURL, path string, probeRequest bool) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestURL := fmt.Sprintf("%s%s", clientBaseURL, path)
	priority := binancePublicRequestPriority(path, probeRequest)
	var lastErr error
	for attempt := 1; attempt <= binanceMaxAttempts; attempt++ {
		admissionTimeout := c.requestCoordinator().admissionTimeout
		if admissionTimeout <= 0 {
			admissionTimeout = binancePublicAdmissionTimeout
		}
		admissionCtx, cancelAdmission := context.WithTimeout(ctx, admissionTimeout)
		releaseRequestSlot, slotErr := c.requestCoordinator().acquireRequestSlot(admissionCtx, priority)
		cancelAdmission()
		if slotErr != nil {
			return nil, slotErr
		}
		if circuitErr := c.requestCoordinator().requestBlockedAfterAdmission(path, time.Now()); circuitErr != nil {
			releaseRequestSlot()
			return nil, circuitErr
		}

		requestTimeout := c.requestTimeout
		if requestTimeout <= 0 {
			requestTimeout = binanceRequestTimeout
		}
		requestCtx, cancelRequest := context.WithTimeout(ctx, requestTimeout)
		requestCtx = binanceguard.WithPriority(requestCtx, priority)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
		if err != nil {
			releaseRequestSlot()
			cancelRequest()
			return nil, err
		}
		resp, err := c.client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				err = readErr
			} else if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
				requestError := &BinancePublicError{
					StatusCode:      resp.StatusCode,
					Endpoint:        path,
					Message:         binanceErrorMessage(body),
					RetryAfter:      parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
					ProxyLikelyDown: resp.StatusCode == http.StatusUnavailableForLegalReasons,
				}
				err = requestError
				if isBinanceCircuitStatus(resp.StatusCode) || resp.StatusCode < http.StatusInternalServerError {
					// Record rate-limit/ban state before releasing the admission
					// slot so queued callers observe the circuit immediately.
					if isBinanceCircuitStatus(resp.StatusCode) {
						c.requestCoordinator().openCircuit(requestError, time.Now())
					}
					releaseRequestSlot()
					cancelRequest()
					return nil, err
				}
			} else {
				releaseRequestSlot()
				cancelRequest()
				return body, nil
			}
		}
		releaseRequestSlot()
		cancelRequest()
		lastErr = err
		if attempt < binanceMaxAttempts {
			retryTimer := time.NewTimer(binanceRetryDelay * time.Duration(1<<(attempt-1)))
			select {
			case <-retryTimer.C:
			case <-ctx.Done():
				if !retryTimer.Stop() {
					<-retryTimer.C
				}
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

func binancePublicRequestPriority(path string, probeRequest bool) binanceguard.Priority {
	if probeRequest {
		return binanceguard.PriorityCritical
	}
	requestPath := path
	if queryIndex := strings.IndexByte(requestPath, '?'); queryIndex >= 0 {
		requestPath = requestPath[:queryIndex]
	}
	switch requestPath {
	case "/fapi/v1/ticker/price", "/fapi/v1/depth", "/fapi/v1/premiumIndex":
		// Price, book, and funding snapshots drive paper mark-to-market,
		// liquidation/SL/TP checks, and maker-order maintenance.
		return binanceguard.PriorityCritical
	default:
		return binanceguard.PriorityNormal
	}
}

func binanceErrorMessage(responseBody []byte) string {
	var errorResponse struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(responseBody, &errorResponse) == nil && strings.TrimSpace(errorResponse.Msg) != "" {
		return strings.TrimSpace(errorResponse.Msg)
	}
	message := strings.TrimSpace(string(responseBody))
	if len(message) > 300 {
		message = message[:300] + "..."
	}
	return message
}

// GetBinanceDynamicTickers builds a deterministic local candidate universe:
// active USDT perpetual contracts ranked by current 24-hour quote volume.
// It deliberately excludes stablecoin-vs-stablecoin contracts and never calls
// any paid provider or an authenticated exchange endpoint.
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
	if len(ranked) == 0 {
		return nil, fmt.Errorf("Binance dynamic candidate universe is empty: no active USDT perpetual tickers were returned")
	}
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
	return c.GetCurrentPriceFresh(symbol)
}

func (c *APIClient) GetCurrentPriceFresh(symbol string) (float64, error) {
	symbol, err := NormalizeBinanceSymbol(symbol)
	if err != nil {
		return 0, err
	}
	var ticker PriceTicker
	path := binancePath("/fapi/v1/ticker/price", url.Values{"symbol": {symbol}})
	if err := c.getFreshBinanceJSON(path, &ticker); err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

func (c *APIClient) GetOpenInterest(symbol string) (*OIData, error) {
	return c.GetOpenInterestContext(context.Background(), symbol)
}

func (c *APIClient) GetOpenInterestContext(ctx context.Context, symbol string) (*OIData, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	symbol, err := NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	var response struct {
		OpenInterest string `json:"openInterest"`
		Symbol       string `json:"symbol"`
		Time         int64  `json:"time"`
	}
	path := binancePath("/fapi/v1/openInterest", url.Values{"symbol": {symbol}})
	if err := c.getBinanceJSONWithCacheContext(ctx, path, &response, true); err != nil {
		return nil, err
	}
	openInterest, err := strconv.ParseFloat(response.OpenInterest, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Binance open interest for %s: %w", symbol, err)
	}
	return &OIData{Latest: openInterest, Average: openInterest * 0.999, Unit: "base"}, nil
}

type MarketAvailability struct {
	Symbol    string
	Price     float64
	CheckedAt time.Time
}

// ValidateMarketAvailability requires a fresh ticker response before a new
// Binance position can be opened. It deliberately bypasses the short price
// cache while still sharing concurrent preflight requests through singleflight.
func (c *APIClient) ValidateMarketAvailability(symbol string) (*MarketAvailability, error) {
	normalizedSymbol, err := NormalizeBinanceSymbol(symbol)
	if err != nil {
		return nil, err
	}
	exchangeInfo, err := c.GetExchangeInfo()
	if err != nil {
		return nil, fmt.Errorf("verify Binance contract %s: %w", normalizedSymbol, err)
	}
	contractAvailable := false
	for _, contract := range exchangeInfo.Symbols {
		if strings.EqualFold(contract.Symbol, normalizedSymbol) &&
			strings.EqualFold(contract.Status, "TRADING") &&
			strings.EqualFold(contract.QuoteAsset, "USDT") {
			contractAvailable = true
			break
		}
	}
	if !contractAvailable {
		return nil, fmt.Errorf("Binance contract %s is unavailable or not trading", normalizedSymbol)
	}

	var ticker PriceTicker
	path := binancePath("/fapi/v1/ticker/price", url.Values{"symbol": {normalizedSymbol}})
	if err := c.getFreshBinanceJSON(path, &ticker); err != nil {
		return nil, fmt.Errorf("verify fresh Binance price for %s: %w", normalizedSymbol, err)
	}
	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return nil, fmt.Errorf("Binance returned an invalid fresh price %q for %s", ticker.Price, normalizedSymbol)
	}
	return &MarketAvailability{Symbol: normalizedSymbol, Price: price, CheckedAt: time.Now().UTC()}, nil
}

func binancePath(endpoint string, query url.Values) string {
	if len(query) == 0 {
		return endpoint
	}
	return endpoint + "?" + query.Encode()
}
