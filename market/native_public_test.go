package market

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/fapi/v1/klines" {
			t.Fatalf("unexpected Binance path %s", request.URL.Path)
		}
		if request.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Fatalf("unexpected Binance symbol %q", request.URL.Query().Get("symbol"))
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`[[1000,"10","11","9","10.5","2",1999,"21",4,"1","10.5"]]`))
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
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path == "/api/v5/market/candles" {
			if query.Get("instId") != "BTC-USDT-SWAP" || query.Get("bar") != "1m" {
				t.Fatalf("unexpected OKX candle query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"0","data":[["2000","20","21","19","20.5","3","0","61.5","5","0"],["1000","10","11","9","10.5","2","0","21","4","0"]]}`)
			return
		}
		if request.URL.Path == "/api/v5/market/books" {
			writeJSON(responseWriter, `{"code":"0","data":[{"asks":[["21","2","0","3"]],"bids":[["20","1","0","2"]],"ts":"3000","seqId":"7"}]}`)
			return
		}
		if request.URL.Path == "/api/v5/public/funding-rate" {
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","fundingRate":"0.0012","nextFundingTime":"4000","ts":"3000"}]}`)
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
	if len(klines) != 2 || klines[0].OpenTime != 1000 || klines[1].OpenTime != 2000 {
		t.Fatalf("OKX candles were not sorted ascending: %#v", klines)
	}

	depth, err := provider.GetDepth("BTCUSDT", 20)
	if err != nil || len(depth.Bids) != 1 || depth.Bids[0][0] != "20" || depth.LastUpdateID != 7 {
		t.Fatalf("unexpected OKX depth: %#v, error=%v", depth, err)
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

func TestBitgetProviderParsesMarketDataAndContractSpec(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path == "/api/v2/mix/market/candles" {
			if query.Get("symbol") != "BTCUSDT" || query.Get("granularity") != "1m" {
				t.Fatalf("unexpected Bitget candle query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"00000","data":[["2000","20","21","19","20.5","3","61.5"],["1000","10","11","9","10.5","2","21"]]}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/orderbook" {
			writeJSON(responseWriter, `{"code":"00000","data":{"asks":[["21","2"]],"bids":[["20","1"]],"ts":"3000"}}`)
			return
		}
		if request.URL.Path == "/api/v2/mix/market/current-fund-rate" {
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","fundingRate":"0.002","nextFundingTime":"4000","ts":"3000"}]}`)
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
	if len(klines) != 2 || klines[0].OpenTime != 1000 || klines[1].OpenTime != 2000 {
		t.Fatalf("Bitget candles were not sorted ascending: %#v", klines)
	}
	depth, err := provider.GetDepth("BTCUSDT", 20)
	if err != nil || len(depth.Asks) != 1 || depth.Asks[0][0] != "21" {
		t.Fatalf("unexpected Bitget depth: %#v, error=%v", depth, err)
	}
	funding, err := provider.GetFundingSnapshot("BTCUSDT")
	if err != nil || funding.Rate != 0.002 || funding.NextFundingTime != 4000 {
		t.Fatalf("unexpected Bitget funding: %#v, error=%v", funding, err)
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
