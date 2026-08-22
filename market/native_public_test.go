package market

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeProvidersNormalizeCanonicalSymbols(t *testing.T) {
	okx := NewOKXMarketDataProvider()
	bitget := NewBitgetMarketDataProvider()

	if got := okx.NormalizeSymbol("SKHYNIX-USDT-SWAP"); got != "SKHYNIXUSDT" {
		t.Fatalf("OKX symbol normalization = %q, want SKHYNIXUSDT", got)
	}
	if got := bitget.NormalizeSymbol("btc-usdt-perp"); got != "BTCUSDT" {
		t.Fatalf("Bitget symbol normalization = %q, want BTCUSDT", got)
	}
}

func TestBinanceProviderUsesExistingAPIClient(t *testing.T) {
	nowMs := time.Now().Add(-time.Minute).UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/fapi/v1/klines" {
			t.Fatalf("unexpected Binance path %s", request.URL.Path)
		}
		if request.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Fatalf("unexpected Binance symbol %q", request.URL.Query().Get("symbol"))
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`[[` + strconv.FormatInt(nowMs, 10) + `,"10","11","9","10.5","2",` + strconv.FormatInt(nowMs+59_999, 10) + `,"21",4,"1","10.5"]]`))
	}))
	defer server.Close()

	provider := NewBinanceMarketDataProvider(NewAPIClientWithBaseURL(server.URL))
	klines, err := provider.GetKlines("BTC-USDT-SWAP", "1m", 1)
	if err != nil {
		t.Fatalf("GetKlines returned error: %v", err)
	}
	if len(klines) != 1 || klines[0].Close != 10.5 {
		t.Fatalf("unexpected Binance kline: %#v", klines)
	}
}

func TestBinanceProviderUsesExactContractFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/fapi/v1/exchangeInfo" {
			t.Fatalf("unexpected Binance path %s", request.URL.Path)
		}
		writeJSON(responseWriter, `{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL","pricePrecision":8,"quantityPrecision":8,"filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},{"filterType":"LOT_SIZE","stepSize":"0.001","minQty":"0.001","maxQty":"100"}]}]}`)
	}))
	defer server.Close()

	binanceExchangeInfoCache.Lock()
	binanceExchangeInfoCache.value = nil
	binanceExchangeInfoCache.fetchedAt = time.Time{}
	binanceExchangeInfoCache.Unlock()
	defer func() {
		binanceExchangeInfoCache.Lock()
		binanceExchangeInfoCache.value = nil
		binanceExchangeInfoCache.fetchedAt = time.Time{}
		binanceExchangeInfoCache.Unlock()
	}()

	provider := NewBinanceMarketDataProvider(NewAPIClientWithBaseURL(server.URL))
	spec, err := provider.GetContractSpec("BTCUSDT")
	if err != nil {
		t.Fatalf("GetContractSpec returned error: %v", err)
	}
	if spec.PriceTick != 0.01 || spec.QuantityStep != 0.001 || spec.MinQuantity != 0.001 || spec.MaxQuantity != 100 {
		t.Fatalf("contract filters were not preserved: %#v", spec)
	}
}

func TestOKXProviderParsesMarketDataAndContractSpec(t *testing.T) {
	nowMs := time.Now().Add(-time.Minute).UnixMilli()
	snapshotNowMs := time.Now().UnixMilli()
	previousMs := nowMs - time.Minute.Milliseconds()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path == "/api/v5/market/candles" {
			if query.Get("instId") != "BTC-USDT-SWAP" || query.Get("bar") != "1m" {
				t.Fatalf("unexpected OKX candle query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"0","data":[["`+strconv.FormatInt(nowMs, 10)+`","20","21","19","20.5","3","0","61.5","5","0"],["`+strconv.FormatInt(previousMs, 10)+`","10","11","9","10.5","2","0","21","4","0"]]}`)
			return
		}
		if request.URL.Path == "/api/v5/market/books" {
			writeJSON(responseWriter, `{"code":"0","data":[{"asks":[["21","2","0","3"]],"bids":[["20","1","0","2"]],"ts":"3000","seqId":"7"}]}`)
			return
		}
		if request.URL.Path == "/api/v5/public/funding-rate" {
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","fundingRate":"0.0012","nextFundingTime":"4000","ts":"`+strconv.FormatInt(snapshotNowMs, 10)+`"}]}`)
			return
		}
		if request.URL.Path == "/api/v5/public/mark-price" {
			if query.Get("instType") != "SWAP" || query.Get("instId") != "BTC-USDT-SWAP" {
				t.Fatalf("unexpected OKX mark price query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","markPx":"20.8","ts":"`+strconv.FormatInt(snapshotNowMs, 10)+`"}]}`)
			return
		}
		if request.URL.Path == "/api/v5/public/open-interest" {
			writeJSON(responseWriter, `{"code":"0","data":[{"oi":"100","oiCcy":"2.5","oiUsd":"50000","ts":"3000"}]}`)
			return
		}
		if request.URL.Path == "/api/v5/public/instruments" {
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","instType":"SWAP","baseCcy":"BTC","quoteCcy":"USDT","settleCcy":"USDT","ctVal":"0.01","ctMult":"1","lotSz":"0.1","minSz":"0.1","maxLmtSz":"1000","tickSz":"0.1","state":"live"}]}`)
			return
		}
		responseWriter.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	klines, err := provider.GetKlines("BTCUSDT", "1m", 2)
	if err != nil {
		t.Fatalf("OKX GetKlines returned error: %v", err)
	}
	if len(klines) != 2 || klines[0].OpenTime != previousMs || klines[1].OpenTime != nowMs {
		t.Fatalf("OKX candles were not sorted ascending: %#v", klines)
	}

	depth, err := provider.GetDepth("BTCUSDT", 20)
	if err != nil || len(depth.Bids) != 1 || depth.Bids[0][0] != "20" || depth.LastUpdateID != 7 {
		t.Fatalf("unexpected OKX depth: %#v, error=%v", depth, err)
	}
	if !depth.Fresh || depth.Exchange != "okx" || depth.Transport != "rest" || depth.ReceivedAt.IsZero() {
		t.Fatalf("OKX depth lost freshness proof: %#v", depth)
	}
	funding, err := provider.GetFundingSnapshot("BTCUSDT")
	if err != nil || funding.Rate != 0.0012 || funding.NextFundingTime != 4000 {
		t.Fatalf("unexpected OKX funding: %#v, error=%v", funding, err)
	}
	oi, err := provider.GetOpenInterest("BTCUSDT")
	if err != nil || oi.Latest != 2.5 || oi.NotionalUSD != 50000 || oi.Unit != "base" {
		t.Fatalf("unexpected OKX OI: %#v, error=%v", oi, err)
	}
	spec, err := provider.GetContractSpec("BTCUSDT")
	if err != nil || spec.ContractMultiplier != 0.01 || spec.PriceTick != 0.1 || spec.QuantityStep != 0.1 {
		t.Fatalf("unexpected OKX contract spec: %#v, error=%v", spec, err)
	}
}

func TestOKXProviderParsesNumericDepthSequence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v5/market/books" {
			t.Fatalf("unexpected OKX path %s", request.URL.Path)
		}
		writeJSON(responseWriter, `{"code":"0","data":[{"asks":[["21","2","0","3"]],"bids":[["20","1","0","2"]],"ts":3000,"seqId":7}]}`)
	}))
	defer server.Close()

	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	depth, err := provider.GetDepth("BTCUSDT", 20)
	if err != nil {
		t.Fatalf("OKX GetDepth returned error for numeric sequence fields: %v", err)
	}
	if depth.LastUpdateID != 7 || depth.EventTime != 3000 || depth.TransactionTime != 3000 {
		t.Fatalf("unexpected numeric OKX depth metadata: %#v", depth)
	}
}

func TestBitgetProviderParsesMarketDataAndContractSpec(t *testing.T) {
	nowMs := time.Now().Add(-time.Minute).UnixMilli()
	snapshotNowMs := time.Now().UnixMilli()
	previousMs := nowMs - time.Minute.Milliseconds()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path == "/api/v2/mix/market/candles" {
			if query.Get("symbol") != "BTCUSDT" || query.Get("granularity") != "1m" {
				t.Fatalf("unexpected Bitget candle query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"00000","data":[["`+strconv.FormatInt(nowMs, 10)+`","20","21","19","20.5","3","61.5"],["`+strconv.FormatInt(previousMs, 10)+`","10","11","9","10.5","2","21"]]}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/orderbook" {
			writeJSON(responseWriter, `{"code":"00000","data":{"asks":[["21","2"]],"bids":[["20","1"]],"ts":"3000"}}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/current-fund-rate" {
			if query.Get("symbol") != "BTCUSDT" || query.Get("productType") != "USDT-FUTURES" {
				t.Fatalf("unexpected Bitget funding snapshot query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","fundingRate":"0.002","nextUpdate":"4000"}]}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/symbol-price" {
			if query.Get("symbol") != "BTCUSDT" || query.Get("productType") != "USDT-FUTURES" {
				t.Fatalf("unexpected Bitget symbol-price query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","price":"805.2","indexPrice":"804.0728898231652706","markPrice":"805.17","ts":"`+strconv.FormatInt(snapshotNowMs, 10)+`"}]}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/history-fund-rate" {
			if query.Get("symbol") != "BTCUSDT" || query.Get("productType") != "USDT-FUTURES" || query.Get("pageSize") != "100" || query.Get("pageNo") != "1" {
				t.Fatalf("unexpected Bitget funding history query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","fundingRate":"0.001","fundingTime":"2000000"},{"symbol":"BTCUSDT","fundingRate":"0.002","fundingTime":"1000000"}]}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/history-mark-candles" {
			if query.Get("symbol") != "BTCUSDT" || query.Get("productType") != "USDT-FUTURES" || query.Get("granularity") != "1m" || query.Get("limit") != "5" {
				t.Fatalf("unexpected Bitget historical mark query: %s", request.URL.RawQuery)
			}
			fundingTime, parseErr := strconv.ParseInt(query.Get("endTime"), 10, 64)
			if parseErr != nil {
				t.Fatalf("invalid historical mark end time: %v", parseErr)
			}
			fundingTime -= time.Minute.Milliseconds()
			closePrice := "805.17"
			if fundingTime == 2000000 {
				closePrice = "806.17"
			}
			writeJSON(responseWriter, `{"code":"00000","data":[["`+strconv.FormatInt(fundingTime, 10)+`","805","806","804","`+closePrice+`","0","0"]]}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/open-interest" {
			writeJSON(responseWriter, `{"code":"00000","data":{"openInterestList":[{"openInterest":"123","openInterestUsd":"456000"}]}}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/contracts" {
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","symbolStatus":"normal","minTradeNum":"0.001","maxTradeNum":"100","sizeMultiplier":"0.001","pricePlace":"1","volumePlace":"3","priceEndStep":"1"}]}`)
			return
		}
		responseWriter.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	klines, err := provider.GetKlines("BTC-USDT-PERP", "1m", 2)
	if err != nil {
		t.Fatalf("Bitget GetKlines returned error: %v", err)
	}
	if len(klines) != 2 || klines[0].OpenTime != previousMs || klines[1].OpenTime != nowMs {
		t.Fatalf("Bitget candles were not sorted ascending: %#v", klines)
	}
	depth, err := provider.GetDepth("BTCUSDT", 20)
	if err != nil || len(depth.Asks) != 1 || depth.Asks[0][0] != "21" {
		t.Fatalf("unexpected Bitget depth: %#v, error=%v", depth, err)
	}
	if !depth.Fresh || depth.Exchange != "bitget" || depth.Transport != "rest" || depth.ReceivedAt.IsZero() {
		t.Fatalf("Bitget depth lost freshness proof: %#v", depth)
	}
	funding, err := provider.GetFundingSnapshot("BTCUSDT")
	if err != nil || funding.Rate != 0.002 || funding.MarkPrice != 805.17 || funding.IndexPrice != 804.0728898231652706 || funding.NextFundingTime != 4000 || funding.Time != snapshotNowMs {
		t.Fatalf("unexpected Bitget funding: %#v, error=%v", funding, err)
	}
	history, err := provider.GetFundingHistory("BTCUSDT", 1000000, 2000000)
	if err != nil {
		t.Fatalf("Bitget GetFundingHistory returned error: %v", err)
	}
	if len(history) != 2 || history[0].FundingTime != 1000000 || history[0].MarkPrice != 805.17 || history[1].FundingTime != 2000000 || history[1].MarkPrice != 806.17 {
		t.Fatalf("unexpected Bitget funding history: %#v", history)
	}
	oi, err := provider.GetOpenInterest("BTCUSDT")
	if err != nil || oi.Latest != 123 || oi.NotionalUSD != 456000 {
		t.Fatalf("unexpected Bitget OI: %#v, error=%v", oi, err)
	}
	spec, err := provider.GetContractSpec("BTCUSDT")
	if err != nil || spec.PriceTick != 0.1 || spec.QuantityStep != 0.001 || spec.MinQuantity != 0.001 {
		t.Fatalf("unexpected Bitget contract spec: %#v, error=%v", spec, err)
	}
}

func TestBitgetProviderParsesCurrentSizeOpenInterest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != bitgetPublicOpenInterestPath {
			t.Fatalf("unexpected Bitget path %s", request.URL.Path)
		}
		writeJSON(responseWriter, `{"code":"00000","data":{"openInterestList":[{"symbol":"SNDKUSDT","size":"46975.254000000053"}],"ts":"1786893976234"}}`)
	}))
	defer server.Close()
	provider := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	oi, err := provider.GetOpenInterest("SNDKUSDT")
	if err != nil {
		t.Fatalf("GetOpenInterest: %v", err)
	}
	if oi.Latest != 46975.254000000053 || oi.Unit != "base" {
		t.Fatalf("open interest = %#v", oi)
	}
}

func writeJSON(responseWriter http.ResponseWriter, body string) {
	responseWriter.Header().Set("Content-Type", "application/json")
	_, _ = responseWriter.Write([]byte(body))
}

func TestNativeProviderTestHelpersUseExpectedQueryEncoding(t *testing.T) {
	values := url.Values{"instId": {"BTC-USDT-SWAP"}, "limit": {strconv.Itoa(20)}}
	encoded := values.Encode()
	if !strings.Contains(encoded, "instId=BTC-USDT-SWAP") || !strings.Contains(encoded, "limit=20") {
		t.Fatalf("unexpected query encoding: %s", encoded)
	}
}

func TestNativePublicHTTPClientCachesNormalReadsButNotFreshReads(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		writeJSON(responseWriter, `{"code":"0","data":[]}`)
	}))
	defer server.Close()

	client := newNativePublicHTTPClient(server.URL, server.Client())
	query := url.Values{"symbol": {"BTCUSDT"}}
	if _, err := client.get("/cached", query); err != nil {
		t.Fatalf("first cached request returned error: %v", err)
	}
	if _, err := client.get("/cached", query); err != nil {
		t.Fatalf("second cached request returned error: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("normal request count = %d, want 1", requestCount)
	}
	if _, err := client.getFresh("/cached", query); err != nil {
		t.Fatalf("fresh request returned error: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("fresh request count = %d, want 2", requestCount)
	}
}

func TestNativePublicHTTPRetriesTransientNetworkThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	client := newNativePublicHTTPClient("https://native.test", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("temporary TLS/network failure")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":"0","data":[]}`)),
			Header:     make(http.Header),
		}, nil
	})})
	if _, err := client.get("/api/v5/market/ticker", url.Values{"instId": {"BTC-USDT-SWAP"}}); err != nil {
		t.Fatalf("native retry error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("native request calls = %d, want one retry", calls.Load())
	}
}

func TestNativePublicHTTPDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	client := newNativePublicHTTPClient("https://native.test", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`bad symbol`)),
			Header:     make(http.Header),
		}, nil
	})})
	if _, err := client.get("/api/v5/market/ticker", url.Values{"instId": {"BAD"}}); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("native client error = %v, want HTTP 400", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("native client-error calls = %d, want no retry", calls.Load())
	}
}

func TestOKXFundingSnapshotReportsMarkRequestFailure(t *testing.T) {
	provider := NewOKXMarketDataProviderWithHTTPClient("https://okx.test", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == okxPublicFundingPath {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":"0","data":[{"fundingRate":"0.001","nextFundingTime":"2000","ts":"1000"}]}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusGatewayTimeout,
			Body:       io.NopCloser(strings.NewReader(`mark endpoint timeout`)),
			Header:     make(http.Header),
		}, nil
	})})
	if _, err := provider.GetFundingSnapshot("BTCUSDT"); err == nil || !strings.Contains(err.Error(), "mark price") || !strings.Contains(err.Error(), "HTTP 504") {
		t.Fatalf("funding snapshot error = %v, want mark-price HTTP diagnostic", err)
	}
}

func TestValidateFreshPublicKlinesRejectsStaleData(t *testing.T) {
	staleKlines := []Kline{{OpenTime: time.Now().Add(-10 * time.Minute).UnixMilli()}}
	if err := validateFreshPublicKlines("OKX", "BTCUSDT", "1m", staleKlines, time.Minute); err == nil {
		t.Fatal("stale K-lines were accepted as fresh")
	}

	freshKlines := []Kline{{OpenTime: time.Now().Add(-5 * time.Second).UnixMilli()}}
	if err := validateFreshPublicKlines("OKX", "BTCUSDT", "1m", freshKlines, time.Minute); err != nil {
		t.Fatalf("fresh K-lines were rejected: %v", err)
	}
}

func TestMarketDataProviderFactoryDoesNotFallbackToBinance(t *testing.T) {
	provider, err := NewMarketDataProvider("unsupported-exchange")
	if err == nil || provider != nil {
		t.Fatalf("unsupported exchange returned provider=%#v, error=%v", provider, err)
	}

	requestedProvider := NewUnavailableMarketDataProvider("okx", err)
	if requestedProvider.Exchange() != "okx" {
		t.Fatalf("unavailable provider exchange = %q, want okx", requestedProvider.Exchange())
	}
	if _, providerErr := requestedProvider.GetKlines("BTCUSDT", "1m", 10); providerErr == nil {
		t.Fatal("unavailable provider unexpectedly returned market data")
	}
}

func TestMarketDataProviderFactorySharesNativeProviderCache(t *testing.T) {
	firstOKX, err := NewMarketDataProvider("okx")
	if err != nil {
		t.Fatalf("create OKX provider: %v", err)
	}
	secondOKX, err := NewMarketDataProvider("OKX")
	if err != nil {
		t.Fatalf("create second OKX provider: %v", err)
	}
	if firstOKX != secondOKX {
		t.Fatal("OKX provider factory did not reuse the native provider cache")
	}
	if _, ok := firstOKX.(*OKXMarketDataProvider); !ok {
		t.Fatalf("OKX factory returned %T", firstOKX)
	}

	bitgetProvider, err := NewMarketDataProvider("bitget")
	if err != nil {
		t.Fatalf("create Bitget provider: %v", err)
	}
	if _, ok := bitgetProvider.(*BitgetMarketDataProvider); !ok {
		t.Fatalf("Bitget factory returned %T", bitgetProvider)
	}
}

func TestOKXBarIntervalFormatting(t *testing.T) {
	cases := map[string]string{
		"1m":  "1m",
		"15m": "15m",
		"1h":  "1H",
		"1H":  "1H",
		"2h":  "2H",
		"4h":  "4H",
		"4H":  "4H",
		"1d":  "1D",
		"1w":  "1W",
	}
	for input, want := range cases {
		got, err := okxBarInterval(input)
		if err != nil {
			t.Fatalf("okxBarInterval(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("okxBarInterval(%q) = %q, want %q", input, got, want)
		}
	}
}
