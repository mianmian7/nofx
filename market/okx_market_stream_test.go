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

func TestOKXMarketWebSocketLive(t *testing.T) {
	if os.Getenv("NOFX_LIVE_OKX_WS_TEST") != "1" {
		t.Skip("set NOFX_LIVE_OKX_WS_TEST=1 to probe OKX")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	priceEvents, priceErrors, err := newOKXPriceConnector("SKHYNIXUSDT", "SKHYNIX-USDT-SWAP", okxPublicWebSocketURL)(ctx)
	if err != nil {
		t.Fatalf("connect OKX ticker: %v", err)
	}
	select {
	case event := <-priceEvents:
		if event.Value <= 0 || event.SourceTime.IsZero() {
			t.Fatalf("invalid OKX ticker event: %#v", event)
		}
	case err := <-priceErrors:
		t.Fatalf("OKX ticker stream: %v", err)
	case <-ctx.Done():
		t.Fatalf("OKX ticker produced no event: %v", ctx.Err())
	}

	fundingEvents, fundingErrors, err := newOKXFundingConnector("SKHYNIXUSDT", "SKHYNIX-USDT-SWAP", okxPublicWebSocketURL)(ctx)
	if err != nil {
		t.Fatalf("connect OKX funding: %v", err)
	}
	select {
	case event := <-fundingEvents:
		if event.Value == nil || event.Value.Time <= 0 || !validOKXMarkPrice(event.Value.MarkPrice) || event.SourceTime.IsZero() {
			t.Fatalf("invalid OKX funding event: %#v", event)
		}
	case err := <-fundingErrors:
		t.Fatalf("OKX funding stream: %v", err)
	case <-ctx.Done():
		t.Fatalf("OKX funding produced no event: %v", ctx.Err())
	}
}

func TestParseOKXPriceStreamMessage(t *testing.T) {
	message := []byte(`{"arg":{"channel":"tickers","instId":"SKHYNIX-USDT-SWAP"},"data":[{"instId":"SKHYNIX-USDT-SWAP","last":"201.25","ts":"1787000000000"}]}`)
	price, sourceTime, ok, err := parseOKXPriceStreamMessage(message, "SKHYNIX-USDT-SWAP")
	if err != nil {
		t.Fatalf("parse OKX ticker: %v", err)
	}
	if !ok || price != 201.25 || sourceTime.UnixMilli() != 1787000000000 {
		t.Fatalf("price=%v source=%v ok=%v", price, sourceTime, ok)
	}
}

func TestOKXFundingAssemblerRequiresFundingAndMark(t *testing.T) {
	var assembler okxFundingAssembler
	if snapshot, _, ok, err := assembler.apply([]byte(`{"arg":{"channel":"funding-rate","instId":"SKHYNIX-USDT-SWAP"},"data":[{"instId":"SKHYNIX-USDT-SWAP","fundingRate":"0.0003","nextFundingTime":"1787001000000","ts":"1787000000000"}]}`), "SKHYNIXUSDT"); err != nil || ok || snapshot != nil {
		t.Fatalf("funding-only update should remain incomplete: snapshot=%#v ok=%v err=%v", snapshot, ok, err)
	}
	snapshot, sourceTime, ok, err := assembler.apply([]byte(`{"arg":{"channel":"mark-price","instId":"SKHYNIX-USDT-SWAP"},"data":[{"instId":"SKHYNIX-USDT-SWAP","markPx":"200.5","ts":"1787000000100"}]}`), "SKHYNIXUSDT")
	if err != nil || !ok || snapshot == nil {
		t.Fatalf("combined funding snapshot: %#v ok=%v err=%v", snapshot, ok, err)
	}
	if snapshot.Rate != 0.0003 || snapshot.MarkPrice != 200.5 || snapshot.Time != 1787000000000 || sourceTime.UnixMilli() != 1787000000000 {
		t.Fatalf("unexpected combined snapshot: %#v source=%v", snapshot, sourceTime)
	}
}

func TestOKXFundingAssemblerRejectsWrongInstrument(t *testing.T) {
	var assembler okxFundingAssembler
	if snapshot, _, ok, err := assembler.apply([]byte(`{"arg":{"channel":"funding-rate","instId":"BTC-USDT-SWAP"},"data":[{"instId":"BTC-USDT-SWAP","fundingRate":"0.0003","nextFundingTime":"1787001000000","ts":"1787000000000"}]}`), "SKHYNIXUSDT"); err != nil || ok || snapshot != nil {
		t.Fatalf("wrong instrument was accepted: snapshot=%#v ok=%v err=%v", snapshot, ok, err)
	}
	if snapshot, _, ok, err := assembler.apply([]byte(`{"arg":{"channel":"mark-price","instId":"SKHYNIX-USDT-SWAP"},"data":[{"instId":"SKHYNIX-USDT-SWAP","markPx":"200.5","ts":"1787000000100"}]}`), "SKHYNIXUSDT"); err != nil || ok || snapshot != nil {
		t.Fatalf("wrong-instrument funding contaminated assembler: snapshot=%#v ok=%v err=%v", snapshot, ok, err)
	}
}

func TestOKXFundingAssemblerRejectsComponentTimeSkew(t *testing.T) {
	var assembler okxFundingAssembler
	_, _, _, err := assembler.apply([]byte(`{"arg":{"channel":"funding-rate","instId":"SKHYNIX-USDT-SWAP"},"data":[{"instId":"SKHYNIX-USDT-SWAP","fundingRate":"0.0003","nextFundingTime":"1787001000000","ts":"1787000000000"}]}`), "SKHYNIXUSDT")
	if err != nil {
		t.Fatalf("funding update: %v", err)
	}
	if snapshot, _, ok, err := assembler.apply([]byte(`{"arg":{"channel":"mark-price","instId":"SKHYNIX-USDT-SWAP"},"data":[{"instId":"SKHYNIX-USDT-SWAP","markPx":"200.5","ts":"1787000200001"}]}`), "SKHYNIXUSDT"); err == nil || ok || snapshot != nil {
		t.Fatalf("skewed components accepted: snapshot=%#v ok=%v err=%v", snapshot, ok, err)
	}
}

func TestOKXPricePrefersFreshTickerStream(t *testing.T) {
	var restCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		restCalls.Add(1)
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	provider.priceStream = func(string) (float64, FreshnessProof, error) {
		now := time.Now().UTC()
		return 123.5, FreshnessProof{Exchange: "okx", Transport: "okx-websocket", SourceTime: now, ReceivedAt: now, SequenceOK: true, Reconciled: true}, nil
	}
	price, err := provider.GetCurrentPriceFresh("SKHYNIXUSDT")
	if err != nil || price != 123.5 || restCalls.Load() != 0 {
		t.Fatalf("price=%v rest_calls=%d err=%v", price, restCalls.Load(), err)
	}
}

func TestOKXFundingPrefersFreshStream(t *testing.T) {
	var restCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		restCalls.Add(1)
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	provider.fundingStream = func(string) (*FundingSnapshot, FreshnessProof, error) {
		now := time.Now().UTC()
		return &FundingSnapshot{Symbol: "SKHYNIXUSDT", Rate: 0.0002, MarkPrice: 200, Time: now.UnixMilli()}, FreshnessProof{Exchange: "okx", Transport: "okx-websocket", SourceTime: now, ReceivedAt: now, SequenceOK: true, Reconciled: true}, nil
	}
	snapshot, err := provider.GetFundingSnapshot("SKHYNIXUSDT")
	if err != nil || snapshot.Rate != 0.0002 || restCalls.Load() != 0 {
		t.Fatalf("snapshot=%#v rest_calls=%d err=%v", snapshot, restCalls.Load(), err)
	}
}

func TestOKXPriceRESTFallbackHasBoundedBudget(t *testing.T) {
	var restCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		restCalls.Add(1)
		<-request.Context().Done()
	}))
	defer server.Close()
	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	provider.priceStream = func(string) (float64, FreshnessProof, error) {
		return 0, FreshnessProof{}, errors.New("stream unavailable")
	}
	provider.marketRESTBudget = 500 * time.Millisecond
	provider.marketRESTAttemptTimeout = 100 * time.Millisecond
	started := time.Now()
	_, err := provider.GetCurrentPriceFresh("SKHYNIXUSDT")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 700*time.Millisecond {
		t.Fatalf("bounded fallback took %s", elapsed)
	}
	if restCalls.Load() != nativePublicMaxAttempts {
		t.Fatalf("REST attempts=%d, want %d", restCalls.Load(), nativePublicMaxAttempts)
	}
}

func TestOKXFundingRESTRejectsMarkWithoutSourceTime(t *testing.T) {
	now := time.Now().UTC().UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case okxPublicFundingPath:
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"SKHYNIX-USDT-SWAP","fundingRate":"0.001","nextFundingTime":"1787001000000","ts":"`+strconv.FormatInt(now, 10)+`"}]}`)
		case okxPublicMarkPricePath:
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"SKHYNIX-USDT-SWAP","markPx":"200"}]}`)
		default:
			responseWriter.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	if snapshot, err := provider.GetFundingSnapshot("SKHYNIXUSDT"); err == nil {
		t.Fatalf("mark without source time accepted: %#v", snapshot)
	}
}

func TestOKXPriceRESTRejectsWrongInstrumentAndFutureTime(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		instID string
		ts     int64
	}{
		{name: "wrong instrument", instID: "BTC-USDT-SWAP", ts: time.Now().UnixMilli()},
		{name: "future timestamp", instID: "SKHYNIX-USDT-SWAP", ts: time.Now().Add(time.Minute).UnixMilli()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				writeJSON(responseWriter, `{"code":"0","data":[{"instId":"`+testCase.instID+`","last":"200","ts":"`+strconv.FormatInt(testCase.ts, 10)+`"}]}`)
			}))
			defer server.Close()
			provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
			if price, err := provider.GetCurrentPriceFresh("SKHYNIXUSDT"); err == nil {
				t.Fatalf("invalid ticker accepted: price=%v", price)
			}
		})
	}
}

func TestOKXFundingRESTRejectsWrongInstrument(t *testing.T) {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case okxPublicFundingPath:
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","fundingRate":"0.001","nextFundingTime":"1787001000000","ts":"`+now+`"}]}`)
		case okxPublicMarkPricePath:
			writeJSON(responseWriter, `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","markPx":"200","ts":"`+now+`"}]}`)
		}
	}))
	defer server.Close()
	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	if snapshot, err := provider.GetFundingSnapshot("SKHYNIXUSDT"); err == nil {
		t.Fatalf("wrong-instrument funding accepted: %#v", snapshot)
	}
}
