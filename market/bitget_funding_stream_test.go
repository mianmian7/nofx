package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestBitgetFundingWebSocketLive(t *testing.T) {
	if os.Getenv("NOFX_LIVE_BITGET_WS_TEST") != "1" {
		t.Skip("set NOFX_LIVE_BITGET_WS_TEST=1 to probe Bitget")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, streamErrors, err := newBitgetFundingConnector("SKHYNIXUSDT", bitgetPublicWebSocketURL)(ctx)
	if err != nil {
		t.Fatalf("connect Bitget ticker: %v", err)
	}
	select {
	case err := <-streamErrors:
		t.Fatalf("Bitget ticker stream: %v", err)
	case event := <-events:
		if event.Value == nil || event.Value.Symbol != "SKHYNIXUSDT" || event.Value.Time <= 0 || !validBitgetPrice(event.Value.MarkPrice) || !validBitgetPrice(event.Value.IndexPrice) {
			t.Fatalf("incomplete live funding event: %#v", event)
		}
	case <-ctx.Done():
		t.Fatalf("Bitget ticker produced no funding event: %v", ctx.Err())
	}
}

func TestParseBitgetFundingTickerMessage(t *testing.T) {
	message := []byte(`{"action":"snapshot","arg":{"instType":"USDT-FUTURES","channel":"ticker","instId":"BTCUSDT"},"data":[{"instId":"BTCUSDT","fundingRate":"0.00025","nextFundingTime":"1787000000000","markPrice":"101.5","indexPrice":"101.25","ts":"1786999999000"}],"ts":1786999999001}`)
	snapshot, sourceTime, ok, err := parseBitgetFundingTickerMessage(message, "BTCUSDT")
	if err != nil {
		t.Fatalf("parse ticker: %v", err)
	}
	if !ok || snapshot == nil {
		t.Fatal("ticker message was not accepted")
	}
	if snapshot.Symbol != "BTCUSDT" || snapshot.Rate != 0.00025 || snapshot.MarkPrice != 101.5 || snapshot.IndexPrice != 101.25 || snapshot.NextFundingTime != 1787000000000 || snapshot.Time != 1786999999000 {
		t.Fatalf("unexpected funding snapshot: %#v", snapshot)
	}
	if sourceTime.UnixMilli() != snapshot.Time {
		t.Fatalf("source time = %d, want %d", sourceTime.UnixMilli(), snapshot.Time)
	}
}

func TestBitgetFundingPrefersFreshTickerStream(t *testing.T) {
	var restCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		restCalls.Add(1)
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	provider := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	provider.fundingStream = func(string) (*FundingSnapshot, FreshnessProof, error) {
		now := time.Now().UTC()
		return &FundingSnapshot{Symbol: "BTCUSDT", Rate: 0.001, MarkPrice: 100, IndexPrice: 99, Time: now.UnixMilli()}, FreshnessProof{
			Exchange: "bitget", Transport: "bitget-websocket", SourceTime: now, ReceivedAt: now, SequenceOK: true, Reconciled: true,
		}, nil
	}

	snapshot, err := provider.GetFundingSnapshot("BTCUSDT")
	if err != nil {
		t.Fatalf("GetFundingSnapshot: %v", err)
	}
	if snapshot.Rate != 0.001 || restCalls.Load() != 0 {
		t.Fatalf("snapshot=%#v rest_calls=%d", snapshot, restCalls.Load())
	}
}

func TestBitgetFundingRESTFallbackHasBoundedBudget(t *testing.T) {
	var restCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		restCalls.Add(1)
		<-request.Context().Done()
	}))
	defer server.Close()

	provider := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	provider.fundingStream = func(string) (*FundingSnapshot, FreshnessProof, error) {
		return nil, FreshnessProof{}, errors.New("stream unavailable")
	}
	provider.fundingRESTBudget = 500 * time.Millisecond
	provider.fundingRESTAttemptTimeout = 100 * time.Millisecond

	started := time.Now()
	_, err := provider.GetFundingSnapshot("BTCUSDT")
	if err == nil {
		t.Fatal("GetFundingSnapshot unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 700*time.Millisecond {
		t.Fatalf("bounded fallback took %s", elapsed)
	}
	if restCalls.Load() != nativePublicMaxAttempts {
		t.Fatalf("REST attempts = %d, want %d", restCalls.Load(), nativePublicMaxAttempts)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
}

func TestBitgetFundingRESTRejectsMissingSourceTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case bitgetPublicFundingPath:
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","fundingRate":"0.001","nextUpdate":"1787000000000"}]}`)
		case bitgetPublicSymbolPricePath:
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","markPrice":"100","indexPrice":"99"}]}`)
		default:
			responseWriter.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	if snapshot, err := provider.GetFundingSnapshot("BTCUSDT"); err == nil {
		t.Fatalf("missing source time accepted: %#v", snapshot)
	}
}

func TestBitgetFundingRESTRejectsFutureSourceTime(t *testing.T) {
	future := strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case bitgetPublicFundingPath:
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","fundingRate":"0.001","nextUpdate":"1787000000000"}]}`)
		case bitgetPublicSymbolPricePath:
			writeJSON(responseWriter, `{"code":"00000","data":[{"symbol":"BTCUSDT","markPrice":"100","indexPrice":"99","ts":"`+future+`"}]}`)
		}
	}))
	defer server.Close()
	provider := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	if snapshot, err := provider.GetFundingSnapshot("BTCUSDT"); err == nil {
		t.Fatalf("future funding source accepted: %#v", snapshot)
	}
}
