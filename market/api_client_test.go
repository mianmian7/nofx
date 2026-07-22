package market

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
