package coinank

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"nofx/provider/coinank/coinank_enum"
)

func TestKlineRejectsMalformedRow(t *testing.T) {
	client := NewCoinankClient("https://coinank.test", "test-key")
	client.httpClient = &http.Client{Transport: coinankRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":[[1000,1060,100]]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	_, err := client.Kline(context.Background(), "BTCUSDT", coinank_enum.Binance, 0, 0, 1, coinank_enum.Minute1)
	if err == nil || !strings.Contains(err.Error(), "want at least 9") {
		t.Fatalf("Kline error = %v, want malformed-row diagnostic", err)
	}
}

func TestCoinankGetRejectsHTTPError(t *testing.T) {
	client := NewCoinankClient("https://coinank.test", "test-key")
	client.httpClient = &http.Client{Transport: coinankRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusGatewayTimeout,
			Body:       io.NopCloser(strings.NewReader(`upstream timeout`)),
			Header:     make(http.Header),
		}, nil
	})}
	_, err := client.Get(context.Background(), "/api/kline/lists", map[string]string{"symbol": "BTCUSDT"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 504") || !strings.Contains(err.Error(), "upstream timeout") {
		t.Fatalf("Get error = %v, want HTTP status and body", err)
	}
}

type coinankRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn coinankRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	return fn(req)
}
