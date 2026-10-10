package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bitgetCoordJSON() string {
	return fmt.Sprintf(`{"code":"00000","data":[["%d","10","11","9","10","2","20"]]}`, time.Now().Truncate(15*time.Minute).UnixMilli())
}
func TestBitgetCandleSharedDispatchAndCoalescing(t *testing.T) {
	for _, identical := range []bool{false, true} {
		t.Run(strconv.FormatBool(identical), func(t *testing.T) {
			var mu sync.Mutex
			var dispatches []time.Time
			var requests, limited atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				mu.Lock()
				now := time.Now()
				recent := dispatches[:0]
				for _, at := range dispatches {
					if now.Sub(at) < time.Second {
						recent = append(recent, at)
					}
				}
				dispatches = append(recent, now)
				over := len(dispatches) > 20
				mu.Unlock()
				if over {
					limited.Add(1)
					w.WriteHeader(429)
					_, _ = w.Write([]byte(`{"code":"429","msg":"Too Many Requests"}`))
					return
				}
				time.Sleep(100 * time.Millisecond)
				writeJSON(w, bitgetCoordJSON())
			}))
			defer server.Close()
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < 36; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					p := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
					<-start
					sym := "BTCUSDT"
					if !identical {
						sym = fmt.Sprintf("T%dUSDT", i)
					}
					_, err := p.GetKlinesFreshContext(context.Background(), sym, "15m", 182)
					if err != nil {
						t.Error(err)
					}
				}(i)
			}
			close(start)
			wg.Wait()
			if limited.Load() != 0 {
				t.Fatalf("avoidable rate limits: %d", limited.Load())
			}
			if identical {
				if requests.Load() != 1 {
					t.Fatalf("duplicate requests=%d", requests.Load())
				}
				p := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
				if _, err := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182); err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 2 {
					t.Fatal("fresh request reused a completed response")
				}
			}
		})
	}
}
func TestBitgetCandle429WaitsWithoutBlockingMark(t *testing.T) {
	var calls atomic.Int32
	limited := make(chan struct{})
	var first atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != bitgetPublicCandlesPath {
			writeJSON(w, `{"code":"00000","data":[]}`)
			return
		}
		if calls.Add(1) == 1 {
			first.Store(time.Now().UnixNano())
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"code":"429"}`))
			close(limited)
			return
		}
		if time.Since(time.Unix(0, first.Load())) < 1900*time.Millisecond {
			t.Error("retry ignored shared cooldown")
		}
		writeJSON(w, bitgetCoordJSON())
	}))
	defer server.Close()
	p := NewBitgetMarketDataProviderWithHTTPClient(server.URL, server.Client())
	done := make(chan error, 1)
	go func() { _, err := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182); done <- err }()
	<-limited
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := p.httpClient.getFreshContext(ctx, bitgetPublicSymbolPricePath, nil, 0); err != nil {
		t.Fatal("mark request blocked by candle cooldown", err)
	}
	if _, err := p.httpClient.getFreshContext(ctx, okxPublicCandlesPath, url.Values{"bar": {"15m"}}, 0); err != nil {
		t.Fatal("Bitget cooldown blocked independent OKX budget", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestBitgetCandleIdentityAndIndependentVenueBudget(t *testing.T) {
	p := newNativePublicHTTPClient("https://api.bitget.com", nil)
	q := url.Values{"granularity": {"15m"}, "symbol": {"BTCUSDT"}}
	now := time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	a := okxCandleKey(p, q, 0, false, now)
	b := okxCandleKey(p, q, 0, false, now.Add(time.Second))
	if a != b {
		t.Fatal("same Bitget candle window does not coalesce")
	}
	b = okxCandleKey(p, q, 0, false, now.Add(15*time.Minute))
	if a == b {
		t.Fatal("different completed window coalesced")
	}
	q.Set("granularity", "4H")
	a = okxCandleKey(p, q, 0, false, now)
	b = okxCandleKey(p, q, 0, false, now.Add(17*time.Minute))
	if a != b {
		t.Fatal("4h granularity misidentified as a shorter window")
	}
	if okxCandleOrigin("https://api.bitget.com") != okxCandleOrigin("https://www.bitget.com") {
		t.Fatal("same-IP Bitget hosts do not share gate")
	}
	if okxCandleOrigin("https://api.bitget.com") == okxCandleOrigin("https://www.okx.com") {
		t.Fatal("venue cooldowns coupled")
	}
}
