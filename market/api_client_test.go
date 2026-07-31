package market

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nofx/hook"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetDepthUsesNormalizedBinanceSymbolAndPreservesTickStrings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/fapi/v1/depth" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if got := req.URL.Query().Get("symbol"); got != "MUUSDT" {
			t.Fatalf("symbol = %q, want MUUSDT", got)
		}
		if got := req.URL.Query().Get("limit"); got != "20" {
			t.Fatalf("limit = %q, want 20", got)
		}
		_, _ = io.WriteString(w, `{"lastUpdateId":42,"E":1784650000000,"T":1784650000001,"bids":[["4119.06","15.874"]],"asks":[["4119.07","1.590"]]}`)
	}))
	defer server.Close()

	depth, err := NewAPIClientWithBaseURL(server.URL).GetDepth("MUUSDT", 20)
	if err != nil {
		t.Fatalf("GetDepth: %v", err)
	}
	if depth.LastUpdateID != 42 || depth.Bids[0][0] != "4119.06" || depth.Asks[0][0] != "4119.07" {
		t.Fatalf("depth = %#v", depth)
	}
}

func TestGetCurrentPriceRetriesTransientReadFailure(t *testing.T) {
	calls := 0
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			if got := req.URL.Query().Get("symbol"); got != "SOXLUSDT" {
				t.Fatalf("symbol = %q, want SOXLUSDT", got)
			}
			return binanceJSONResponse(`{"symbol":"SOXLUSDT","price":"147.51"}`), nil
		})},
	}

	price, err := client.GetCurrentPrice("SOXLUSDT")
	if err != nil {
		t.Fatalf("GetCurrentPrice: %v", err)
	}
	if price != 147.51 || calls != 2 {
		t.Fatalf("price/calls = %v/%d, want 147.51/2", price, calls)
	}
}

func TestSameBinanceRequestIsMergedAcrossClients(t *testing.T) {
	var upstreamCalls atomic.Int32
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if upstreamCalls.Add(1) == 1 {
			close(requestStarted)
		}
		<-releaseRequest
		return binanceJSONResponse(`{"symbol":"MUUSDT","price":"123.45"}`), nil
	})
	coordinator := newBinancePublicCoordinator()
	firstClient := &APIClient{
		baseURL: "https://binance.test", client: &http.Client{Transport: transport}, coordinator: coordinator,
	}
	secondClient := &APIClient{
		baseURL: "https://binance.test", client: &http.Client{Transport: transport}, coordinator: coordinator,
	}

	const callerCount = 20
	start := make(chan struct{})
	results := make(chan error, callerCount)
	var callers sync.WaitGroup
	for callerIndex := 0; callerIndex < callerCount; callerIndex++ {
		callers.Add(1)
		go func(index int) {
			defer callers.Done()
			<-start
			client := firstClient
			if index%2 == 1 {
				client = secondClient
			}
			price, err := client.GetCurrentPrice("MUUSDT")
			if err == nil && price != 123.45 {
				err = errors.New("unexpected shared price")
			}
			results <- err
		}(callerIndex)
	}
	close(start)
	<-requestStarted
	close(releaseRequest)
	callers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("GetCurrentPrice returned error: %v", err)
		}
	}
	if calls := upstreamCalls.Load(); calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

func TestFreshPriceCacheIsSharedAcrossClients(t *testing.T) {
	var upstreamCalls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		return binanceJSONResponse(`{"symbol":"BTCUSDT","price":"65000"}`), nil
	})
	coordinator := newBinancePublicCoordinator()
	firstClient := &APIClient{
		baseURL: "https://binance.test", client: &http.Client{Transport: transport}, coordinator: coordinator,
	}
	secondClient := &APIClient{
		baseURL: "https://binance.test", client: &http.Client{Transport: transport}, coordinator: coordinator,
	}

	if _, err := firstClient.GetCurrentPrice("BTCUSDT"); err != nil {
		t.Fatalf("first GetCurrentPrice: %v", err)
	}
	if _, err := secondClient.GetCurrentPrice("BTCUSDT"); err != nil {
		t.Fatalf("second GetCurrentPrice: %v", err)
	}
	if calls := upstreamCalls.Load(); calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

func TestBinance429UsesRetryAfterAndOpensCircuit(t *testing.T) {
	var upstreamCalls atomic.Int32
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"code":-1003,"msg":"Too many requests"}`)),
				Header:     http.Header{"Retry-After": []string{"3"}},
			}, nil
		})},
		coordinator: newBinancePublicCoordinator(),
	}

	_, err := client.GetCurrentPrice("BTCUSDT")
	var requestError *BinancePublicError
	if !errors.As(err, &requestError) {
		t.Fatalf("error = %v, want BinancePublicError", err)
	}
	if requestError.StatusCode != http.StatusTooManyRequests || requestError.RetryAfter != 3*time.Second {
		t.Fatalf("request error = %#v", requestError)
	}
	if time.Until(requestError.CircuitUntil) < 2*time.Second {
		t.Fatalf("circuit until = %s, want Retry-After based cooldown", requestError.CircuitUntil)
	}
	if _, err := client.GetDepth("BTCUSDT", 20); err == nil {
		t.Fatal("second request unexpectedly bypassed open circuit")
	}
	if calls := upstreamCalls.Load(); calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

func TestBinance451MarksProxyUnavailableAndStopsOtherEndpoints(t *testing.T) {
	var upstreamCalls atomic.Int32
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusUnavailableForLegalReasons,
				Body: io.NopCloser(strings.NewReader(
					`{"code":0,"msg":"Service unavailable from a restricted location"}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
		coordinator: newBinancePublicCoordinator(),
	}

	_, err := client.GetCurrentPrice("BTCUSDT")
	var requestError *BinancePublicError
	if !errors.As(err, &requestError) || !requestError.ProxyLikelyDown {
		t.Fatalf("error = %v, want proxy-unavailable BinancePublicError", err)
	}
	if !strings.Contains(err.Error(), "proxy path may be unavailable") {
		t.Fatalf("error = %q, want proxy diagnostic", err)
	}
	if _, err := client.GetOpenInterest("ETHUSDT"); err == nil {
		t.Fatal("request to another endpoint unexpectedly bypassed open circuit")
	}
	if calls := upstreamCalls.Load(); calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

func TestRepeatedNetworkFailureOpensShortCircuit(t *testing.T) {
	var upstreamCalls atomic.Int32
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalls.Add(1)
			return nil, errors.New("proxy connection reset")
		})},
		coordinator: newBinancePublicCoordinator(),
	}

	_, err := client.GetCurrentPrice("BTCUSDT")
	var requestError *BinancePublicError
	if !errors.As(err, &requestError) || requestError.StatusCode != 0 {
		t.Fatalf("error = %v, want network BinancePublicError", err)
	}
	if _, err := client.GetDepth("ETHUSDT", 20); err == nil {
		t.Fatal("second endpoint unexpectedly bypassed network-error circuit")
	}
	if calls := upstreamCalls.Load(); calls != binanceMaxAttempts {
		t.Fatalf("upstream calls = %d, want %d", calls, binanceMaxAttempts)
	}
}

func TestValidateMarketAvailabilityRequiresFreshTradingPrice(t *testing.T) {
	resetBinanceCandidateCaches(t)
	var tickerCalls atomic.Int32
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/fapi/v1/exchangeInfo":
				return binanceJSONResponse(`{"symbols":[{"symbol":"MUUSDT","status":"TRADING","quoteAsset":"USDT","contractType":"TRADIFI_PERPETUAL"}]}`), nil
			case "/fapi/v1/ticker/price":
				price := "100"
				if tickerCalls.Add(1) > 1 {
					price = "101"
				}
				return binanceJSONResponse(`{"symbol":"MUUSDT","price":"` + price + `"}`), nil
			default:
				t.Fatalf("unexpected Binance path %s", req.URL.Path)
				return nil, nil
			}
		})},
		coordinator: newBinancePublicCoordinator(),
	}

	if price, err := client.GetCurrentPrice("MUUSDT"); err != nil || price != 100 {
		t.Fatalf("initial cached price = %v, error = %v", price, err)
	}
	availability, err := client.ValidateMarketAvailability("MUUSDT")
	if err != nil {
		t.Fatalf("ValidateMarketAvailability: %v", err)
	}
	if availability.Price != 101 || tickerCalls.Load() != 2 {
		t.Fatalf("availability/calls = %#v/%d, want fresh price 101 from 2 calls", availability, tickerCalls.Load())
	}
}

func TestProxyHookFailureDoesNotFallBackToDirectTransport(t *testing.T) {
	originalHook, hookExists := hook.Hooks[hook.SET_HTTP_CLIENT]
	hook.RegisterHook(hook.SET_HTTP_CLIENT, func(args ...any) any {
		return &hook.SetHttpClientResult{Err: errors.New("proxy credentials expired")}
	})
	t.Cleanup(func() {
		if hookExists {
			hook.Hooks[hook.SET_HTTP_CLIENT] = originalHook
		} else {
			delete(hook.Hooks, hook.SET_HTTP_CLIENT)
		}
	})

	client := NewAPIClient()
	client.coordinator = newBinancePublicCoordinator()
	_, err := client.GetCurrentPrice("BTCUSDT")
	if err == nil || !strings.Contains(err.Error(), "proxy client unavailable") {
		t.Fatalf("error = %v, want fail-closed proxy error", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func resetBinanceCandidateCaches(t *testing.T) {
	t.Helper()
	binanceExchangeInfoCache.Lock()
	binanceExchangeInfoCache.value = nil
	binanceExchangeInfoCache.fetchedAt = time.Time{}
	binanceExchangeInfoCache.Unlock()
	binanceDynamicTickerCache.Lock()
	binanceDynamicTickerCache.value = nil
	binanceDynamicTickerCache.fetchedAt = time.Time{}
	binanceDynamicTickerCache.Unlock()
	t.Cleanup(func() {
		binanceExchangeInfoCache.Lock()
		binanceExchangeInfoCache.value = nil
		binanceExchangeInfoCache.fetchedAt = time.Time{}
		binanceExchangeInfoCache.Unlock()
		binanceDynamicTickerCache.Lock()
		binanceDynamicTickerCache.value = nil
		binanceDynamicTickerCache.fetchedAt = time.Time{}
		binanceDynamicTickerCache.Unlock()
	})
}

func binanceJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestGetBinanceDynamicSymbolsFiltersAndRanksUSDTPerpetuals(t *testing.T) {
	resetBinanceCandidateCaches(t)
	client := &APIClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/fapi/v1/exchangeInfo":
			body = `{"symbols":[
				{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL"},
				{"symbol":"ETHUSDT","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDT","contractType":"PERPETUAL"},
				{"symbol":"1000PEPEUSDT","status":"TRADING","baseAsset":"1000PEPE","quoteAsset":"USDT","contractType":"PERPETUAL"},
				{"symbol":"USDCUSDT","status":"TRADING","baseAsset":"USDC","quoteAsset":"USDT","contractType":"PERPETUAL"},
				{"symbol":"SOLUSDT","status":"SETTLING","baseAsset":"SOL","quoteAsset":"USDT","contractType":"PERPETUAL"},
				{"symbol":"XRPUSD","status":"TRADING","baseAsset":"XRP","quoteAsset":"USD","contractType":"PERPETUAL"}
			]}`
		case "/fapi/v1/ticker/24hr":
			body = `[
				{"symbol":"BTCUSDT","priceChangePercent":"2","quoteVolume":"900"},
				{"symbol":"ETHUSDT","priceChangePercent":"1","quoteVolume":"700"},
				{"symbol":"1000PEPEUSDT","priceChangePercent":"12","quoteVolume":"1200"},
				{"symbol":"USDCUSDT","priceChangePercent":"0","quoteVolume":"5000"},
				{"symbol":"SOLUSDT","priceChangePercent":"5","quoteVolume":"2000"},
				{"symbol":"XRPUSD","priceChangePercent":"3","quoteVolume":"3000"}
			]`
		default:
			t.Fatalf("unexpected Binance path %s", req.URL.Path)
		}
		return binanceJSONResponse(body), nil
	})}}

	symbols, err := client.GetBinanceDynamicSymbols(2)
	if err != nil {
		t.Fatalf("GetBinanceDynamicSymbols returned error: %v", err)
	}
	want := []string{"1000PEPEUSDT", "BTCUSDT"}
	if len(symbols) != len(want) {
		t.Fatalf("symbols = %#v, want %#v", symbols, want)
	}
	for i := range want {
		if symbols[i] != want[i] {
			t.Fatalf("symbols = %#v, want %#v", symbols, want)
		}
	}
}

func TestGetBinanceTradFiTickersIncludesTradingUSDTTradFiPerpetuals(t *testing.T) {
	resetBinanceCandidateCaches(t)
	client := &APIClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/fapi/v1/exchangeInfo":
			body = `{"symbols":[
				{"symbol":"SKHYNIXUSDT","status":"TRADING","baseAsset":"SKHYNIX","quoteAsset":"USDT","contractType":"TRADIFI_PERPETUAL","underlyingType":"KR_EQUITY"},
				{"symbol":"QQQUSDT","status":"TRADING","baseAsset":"QQQ","quoteAsset":"USDT","contractType":"TRADIFI_PERPETUAL","underlyingType":"EQUITY"},
				{"symbol":"XAUUSDT","status":"TRADING","baseAsset":"XAU","quoteAsset":"USDT","contractType":"TRADIFI_PERPETUAL","underlyingType":"COMMODITY"},
				{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL","underlyingType":"COIN"},
				{"symbol":"OLDUSDT","status":"SETTLING","baseAsset":"OLD","quoteAsset":"USDT","contractType":"TRADIFI_PERPETUAL","underlyingType":"EQUITY"}
			]}`
		case "/fapi/v1/ticker/24hr":
			body = `[
				{"symbol":"SKHYNIXUSDT","lastPrice":"1303.80","priceChangePercent":"6.85","quoteVolume":"1600"},
				{"symbol":"QQQUSDT","lastPrice":"708.81","priceChangePercent":"1.04","quoteVolume":"150"},
				{"symbol":"XAUUSDT","lastPrice":"4081.31","priceChangePercent":"1.58","quoteVolume":"1300"},
				{"symbol":"BTCUSDT","lastPrice":"66828.40","priceChangePercent":"3.82","quoteVolume":"11000"},
				{"symbol":"OLDUSDT","lastPrice":"1","priceChangePercent":"0","quoteVolume":"1"}
			]`
		default:
			t.Fatalf("unexpected Binance path %s", req.URL.Path)
		}
		return binanceJSONResponse(body), nil
	})}}

	tickers, err := client.GetBinanceTradFiTickers()
	if err != nil {
		t.Fatalf("GetBinanceTradFiTickers returned error: %v", err)
	}
	want := []struct {
		symbol         string
		underlyingType string
	}{
		{symbol: "SKHYNIXUSDT", underlyingType: "KR_EQUITY"},
		{symbol: "XAUUSDT", underlyingType: "COMMODITY"},
		{symbol: "QQQUSDT", underlyingType: "EQUITY"},
	}
	if len(tickers) != len(want) {
		t.Fatalf("tickers = %#v, want %d entries", tickers, len(want))
	}
	for i := range want {
		if tickers[i].Symbol != want[i].symbol || tickers[i].UnderlyingType != want[i].underlyingType {
			t.Fatalf("tickers[%d] = %#v, want symbol=%s underlyingType=%s", i, tickers[i], want[i].symbol, want[i].underlyingType)
		}
	}
}

func TestGetKlinesKeepsBinanceTradFiSymbol(t *testing.T) {
	client := &APIClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/fapi/v1/klines" {
			t.Fatalf("unexpected Binance path %s", req.URL.Path)
		}
		if got := req.URL.Query().Get("symbol"); got != "MUUSDT" {
			t.Fatalf("symbol query = %q, want MUUSDT", got)
		}
		if got := req.URL.Query().Get("interval"); got != "1m" {
			t.Fatalf("interval query = %q, want 1m", got)
		}
		return binanceJSONResponse(`[[1784650000000,"120.1","121.2","119.8","120.9","10.5",1784650059999,"1269.45",42,"5.2","628.68","0"]]`), nil
	})}}

	klines, err := client.GetKlines("MUUSDT", "1m", 3)
	if err != nil {
		t.Fatalf("GetKlines returned error: %v", err)
	}
	if len(klines) != 1 || klines[0].Close != 120.9 || klines[0].Trades != 42 {
		t.Fatalf("klines = %#v", klines)
	}
}

func TestGetKlinesUsesLastValidSnapshotAfterTransientFailure(t *testing.T) {
	var calls atomic.Int32
	coordinator := newBinancePublicCoordinator()
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return binanceJSONResponse(`[[1784650000000,"120.1","121.2","119.8","120.9","10.5",1784650059999,"1269.45",42,"5.2","628.68","0"]]`), nil
			}
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"code":-1003,"msg":"Too many requests"}`)),
				Header:     make(http.Header),
			}, nil
		})},
		coordinator: coordinator,
	}

	first, err := client.GetKlines("MUUSDT", "1m", 3)
	if err != nil {
		t.Fatalf("initial GetKlines: %v", err)
	}
	path := binancePath("/fapi/v1/klines", url.Values{
		"symbol": {"MUUSDT"}, "interval": {"1m"}, "limit": {"3"},
	})
	coordinator.cacheMutex.Lock()
	entry := coordinator.cache[client.binanceBaseURL()+"|"+path]
	entry.expiresAt = time.Now().Add(-time.Second)
	coordinator.cache[client.binanceBaseURL()+"|"+path] = entry
	coordinator.cacheMutex.Unlock()

	second, err := client.GetKlines("MUUSDT", "1m", 3)
	if err != nil {
		t.Fatalf("fallback GetKlines: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	if len(second) != len(first) || second[0].Close != first[0].Close {
		t.Fatalf("fallback klines = %#v, want %#v", second, first)
	}
}

func TestGetKlinesFreshRejectsUpstreamFailureInsteadOfUsingSnapshot(t *testing.T) {
	var calls atomic.Int32
	coordinator := newBinancePublicCoordinator()
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return binanceJSONResponse(`[[1784650000000,"120.1","121.2","119.8","120.9","10.5",1784650059999,"1269.45",42,"5.2","628.68","0"]]`), nil
			}
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"code":-1003,"msg":"Too many requests"}`)),
				Header:     make(http.Header),
			}, nil
		})},
		coordinator: coordinator,
	}

	if _, err := client.GetKlines("MUUSDT", "1m", 3); err != nil {
		t.Fatalf("initial GetKlines: %v", err)
	}
	path := binancePath("/fapi/v1/klines", url.Values{
		"symbol": {"MUUSDT"}, "interval": {"1m"}, "limit": {"3"},
	})
	coordinator.cacheMutex.Lock()
	entry := coordinator.cache[client.binanceBaseURL()+"|"+path]
	entry.expiresAt = time.Now().Add(-time.Second)
	coordinator.cache[client.binanceBaseURL()+"|"+path] = entry
	coordinator.cacheMutex.Unlock()

	if _, err := client.GetKlinesFresh("MUUSDT", "1m", 3); err == nil {
		t.Fatal("GetKlinesFresh unexpectedly returned the cached K-line snapshot")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestGetKlinesUsesLastValidSnapshotAfterEmptyResponse(t *testing.T) {
	var calls atomic.Int32
	coordinator := newBinancePublicCoordinator()
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return binanceJSONResponse(`[[1784650000000,"120.1","121.2","119.8","120.9","10.5",1784650059999,"1269.45",42,"5.2","628.68","0"]]`), nil
			}
			return binanceJSONResponse(`[]`), nil
		})},
		coordinator: coordinator,
	}

	first, err := client.GetKlines("MUUSDT", "15m", 200)
	if err != nil {
		t.Fatalf("initial GetKlines: %v", err)
	}
	path := binancePath("/fapi/v1/klines", url.Values{
		"symbol": {"MUUSDT"}, "interval": {"15m"}, "limit": {"200"},
	})
	coordinator.cacheMutex.Lock()
	entry := coordinator.cache[client.binanceBaseURL()+"|"+path]
	entry.expiresAt = time.Now().Add(-time.Second)
	coordinator.cache[client.binanceBaseURL()+"|"+path] = entry
	coordinator.cacheMutex.Unlock()

	second, err := client.GetKlines("MUUSDT", "15m", 200)
	if err != nil {
		t.Fatalf("empty-response fallback GetKlines: %v", err)
	}
	if calls.Load() != 2 || len(second) != len(first) {
		t.Fatalf("calls/klines = %d/%#v, want 2 and the last valid snapshot", calls.Load(), second)
	}
}

func TestBinanceCoordinatorSerializesDistinctPublicRequests(t *testing.T) {
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	coordinator := newBinancePublicCoordinator()
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			current := inFlight.Add(1)
			for {
				previous := maxInFlight.Load()
				if current <= previous || maxInFlight.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			inFlight.Add(-1)
			return binanceJSONResponse(`{"symbol":"BTCUSDT","price":"65000"}`), nil
		})},
		coordinator: coordinator,
	}

	var callers sync.WaitGroup
	for _, symbol := range []string{"BTCUSDT", "ETHUSDT"} {
		callers.Add(1)
		go func(symbol string) {
			defer callers.Done()
			if _, err := client.GetCurrentPrice(symbol); err != nil {
				t.Errorf("GetCurrentPrice(%s): %v", symbol, err)
			}
		}(symbol)
	}
	callers.Wait()
	if maxInFlight.Load() != 1 {
		t.Fatalf("max concurrent Binance requests = %d, want 1", maxInFlight.Load())
	}
}

func TestBinanceCircuitStopsQueuedDistinctRequests(t *testing.T) {
	var upstreamCalls atomic.Int32
	coordinator := newBinancePublicCoordinator()
	client := &APIClient{
		baseURL: "https://binance.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			upstreamCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"code":-1003,"msg":"Too many requests"}`)),
				Header:     make(http.Header),
			}, nil
		})},
		coordinator: coordinator,
	}

	start := make(chan struct{})
	var callers sync.WaitGroup
	for _, request := range []func() error{
		func() error { _, err := client.GetCurrentPrice("BTCUSDT"); return err },
		func() error { _, err := client.GetOpenInterest("ETHUSDT"); return err },
	} {
		callers.Add(1)
		go func(request func() error) {
			defer callers.Done()
			<-start
			_ = request()
		}(request)
	}
	close(start)
	callers.Wait()
	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want only the first request before circuit open", upstreamCalls.Load())
	}
}

func TestGetBinanceDynamicSymbolsRetriesTransientRequestFailure(t *testing.T) {
	resetBinanceCandidateCaches(t)
	exchangeInfoCalls := 0
	client := &APIClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/fapi/v1/exchangeInfo":
			exchangeInfoCalls++
			if exchangeInfoCalls == 1 {
				return nil, errors.New("TLS handshake timeout")
			}
			return binanceJSONResponse(`{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL"}]}`), nil
		case "/fapi/v1/ticker/24hr":
			return binanceJSONResponse(`[{"symbol":"BTCUSDT","lastPrice":"65000","quoteVolume":"1000"}]`), nil
		default:
			t.Fatalf("unexpected Binance path %s", req.URL.Path)
			return nil, nil
		}
	})}}

	symbols, err := client.GetBinanceDynamicSymbols(10)
	if err != nil {
		t.Fatalf("GetBinanceDynamicSymbols returned error: %v", err)
	}
	if exchangeInfoCalls != 2 {
		t.Fatalf("exchangeInfo calls = %d, want 2", exchangeInfoCalls)
	}
	if len(symbols) != 1 || symbols[0] != "BTCUSDT" {
		t.Fatalf("symbols = %#v, want [BTCUSDT]", symbols)
	}
}

func TestGetBinanceDynamicSymbolsUsesStaleCacheAfterRefreshFailure(t *testing.T) {
	resetBinanceCandidateCaches(t)
	goodClient := &APIClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/fapi/v1/exchangeInfo":
			return binanceJSONResponse(`{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL"}]}`), nil
		case "/fapi/v1/ticker/24hr":
			return binanceJSONResponse(`[{"symbol":"BTCUSDT","lastPrice":"65000","quoteVolume":"1000"}]`), nil
		default:
			t.Fatalf("unexpected Binance path %s", req.URL.Path)
			return nil, nil
		}
	})}}
	if _, err := goodClient.GetBinanceDynamicSymbols(10); err != nil {
		t.Fatalf("initial candidate load returned error: %v", err)
	}

	binanceDynamicTickerCache.Lock()
	binanceDynamicTickerCache.fetchedAt = time.Now().Add(-binanceDynamicFreshTTL - time.Second)
	binanceDynamicTickerCache.Unlock()
	tickerCalls := 0
	failingClient := &APIClient{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/fapi/v1/ticker/24hr" {
			tickerCalls++
			return nil, errors.New("temporary upstream failure")
		}
		t.Fatalf("unexpected Binance path %s", req.URL.Path)
		return nil, nil
	})}}

	symbols, err := failingClient.GetBinanceDynamicSymbols(10)
	if err != nil {
		t.Fatalf("cached candidate fallback returned error: %v", err)
	}
	if tickerCalls != binanceMaxAttempts {
		t.Fatalf("ticker calls = %d, want %d", tickerCalls, binanceMaxAttempts)
	}
	if len(symbols) != 1 || symbols[0] != "BTCUSDT" {
		t.Fatalf("symbols = %#v, want stale [BTCUSDT]", symbols)
	}
}
