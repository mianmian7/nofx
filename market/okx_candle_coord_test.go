package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func okxCoordCandleJSON() string {
	ts := time.Now().Truncate(15 * time.Minute).UnixMilli()
	return fmt.Sprintf(`{"code":"0","data":[["%d","10","11","9","10","2","0","20","0"]]}`, ts)
}

// Mirrors the official candles quota, including retries, across clients.
func TestOKXCandleRateBudgetAcrossProviders(t *testing.T) {
	var mu sync.Mutex
	var dispatches []time.Time
	var limited atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now := time.Now()
		alive := dispatches[:0]
		for _, at := range dispatches {
			if now.Sub(at) < 2*time.Second {
				alive = append(alive, at)
			}
		}
		dispatches = append(alive, now)
		over := len(dispatches) > 40
		mu.Unlock()
		if over {
			limited.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"50011","msg":"Too Many Requests"}`))
			return
		}
		writeJSON(w, okxCoordCandleJSON())
	}))
	defer server.Close()
	start := make(chan struct{})
	errs := make(chan error, 48)
	var wg sync.WaitGroup
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			transport := server.Client().Transport
			if i%2 == 0 {
				clone := transport.(*http.Transport).Clone()
				defer clone.CloseIdleConnections()
				transport = clone
			}
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			p := NewOKXMarketDataProviderWithHTTPClient(server.URL, client)
			_, err := p.GetKlinesFreshContext(context.Background(), "T"+strconv.Itoa(i)+"USDT", "15m", 182)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("paced candle read failed: %v", err)
		}
	}
	if n := limited.Load(); n != 0 {
		t.Errorf("shared dispatch budget triggered %d rate-limit responses", n)
	}
}

func TestOKXCandleCoalescesAcrossProviders(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		writeJSON(w, okxCoordCandleJSON())
	}))
	defer server.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
			<-start
			bars, e := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182)
			if e != nil || len(bars) != 1 {
				t.Errorf("candle read: bars=%d err=%v", len(bars), e)
			} else {
				bars[0].Close = 42
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("concurrent same-window request count=%d, want1", n)
	}
}
