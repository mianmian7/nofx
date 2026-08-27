package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeKlinesReturnOnlyExchangeResponse(t *testing.T) {
	now := time.Now().Add(-time.Minute).UnixMilli()
	tests := []struct {
		name string
		path string
		body string
		want float64
		new  func(string, *http.Client) MarketDataProvider
	}{
		{
			name: "bitget",
			path: bitgetPublicCandlesPath,
			body: fmt.Sprintf(`{"code":"00000","data":[[%q,"100","101","99","%s","1","1000"]]}`, strconv.FormatInt(now, 10), "123.45"),
			want: 123.45,
			new: func(baseURL string, client *http.Client) MarketDataProvider {
				return NewBitgetMarketDataProviderWithHTTPClient(baseURL, client)
			},
		},
		{
			name: "okx",
			path: okxPublicCandlesPath,
			body: fmt.Sprintf(`{"code":"0","data":[[%q,"100","101","99","%s","1","0","1000","1","0"]]}`, strconv.FormatInt(now, 10), "987.65"),
			want: 987.65,
			new: func(baseURL string, client *http.Client) MarketDataProvider {
				return NewOKXMarketDataProviderWithHTTPClient(baseURL, client)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != testCase.path {
					t.Fatalf("unexpected native path %s", request.URL.Path)
				}
				writeJSON(writer, testCase.body)
			}))
			defer server.Close()

			provider := testCase.new(server.URL, server.Client())
			klines, err := provider.GetKlinesFresh("BTCUSDT", "1m", 1)
			if err != nil {
				t.Fatalf("GetKlinesFresh returned error: %v", err)
			}
			if len(klines) != 1 || klines[0].Close != testCase.want {
				t.Fatalf("native result = %#v, want close %.2f", klines, testCase.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("native request count = %d, want 1 and no aggregate request", calls.Load())
			}
		})
	}
}

func TestNativeKlineFailureFailsClosedWithoutAggregateFallback(t *testing.T) {
	tests := []struct {
		name string
		path string
		new  func(string, *http.Client) MarketDataProvider
	}{
		{
			name: "bitget",
			path: bitgetPublicCandlesPath,
			new: func(baseURL string, client *http.Client) MarketDataProvider {
				return NewBitgetMarketDataProviderWithHTTPClient(baseURL, client)
			},
		},
		{
			name: "okx",
			path: okxPublicCandlesPath,
			new: func(baseURL string, client *http.Client) MarketDataProvider {
				return NewOKXMarketDataProviderWithHTTPClient(baseURL, client)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != testCase.path {
					t.Fatalf("unexpected native path %s", request.URL.Path)
				}
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = writer.Write([]byte("native exchange unavailable"))
			}))
			defer server.Close()

			provider := testCase.new(server.URL, server.Client())
			klines, err := provider.GetKlinesFresh("BTCUSDT", "1m", 1)
			if err == nil || len(klines) != 0 {
				t.Fatalf("native failure returned klines=%#v, error=%v", klines, err)
			}
			if !strings.Contains(err.Error(), "HTTP 502") {
				t.Fatalf("native failure error = %v, want HTTP 502", err)
			}
			if calls.Load() != nativePublicMaxAttempts {
				t.Fatalf("native request count = %d, want %d native retries and no aggregate fallback", calls.Load(), nativePublicMaxAttempts)
			}
		})
	}
}

func TestNativeKlineContextCancellationIsPropagated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := provider.GetKlinesFreshContext(ctx, "BTCUSDT", "1m", 1); err == nil {
		t.Fatal("context cancellation was not returned by native K-line request")
	}
}

func TestCoinAnkOnlyExchangeIsUnavailableToTradingProviderFactory(t *testing.T) {
	for _, exchange := range []string{"bybit", "gate", "kucoin", "hyperliquid", "aster"} {
		provider, err := NewMarketDataProvider(exchange)
		if err != nil {
			t.Fatalf("NewMarketDataProvider(%s): %v", exchange, err)
		}
		if _, ok := provider.(*unavailableMarketDataProvider); !ok {
			t.Fatalf("%s provider type = %T, want unavailable native provider", exchange, provider)
		}
		if _, err := provider.GetKlines("BTCUSDT", "1m", 1); err == nil || !strings.Contains(err.Error(), "CoinAnk is disabled for trading") {
			t.Fatalf("%s trading provider error = %v, want explicit aggregate-disabled error", exchange, err)
		}
	}
}
