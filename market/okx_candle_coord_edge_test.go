package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func waitOKXFlight(t *testing.T, origin string, waiters int) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		sharedOKXCandles.mu.Lock()
		found := false
		for key, f := range sharedOKXCandles.flights {
			if key.origin == origin && f.waiters >= waiters {
				found = true
			}
		}
		sharedOKXCandles.mu.Unlock()
		if found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("shared flight did not acquire expected waiters")
}

func TestOKXCandleLeaderCancellationDoesNotCancelFollower(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			writeJSON(w, okxCoordCandleJSON())
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	p := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	lead := make(chan error, 1)
	follow := make(chan error, 1)
	go func() { _, e := p.GetKlinesFreshContext(ctx, "BTCUSDT", "15m", 182); lead <- e }()
	<-entered
	go func() { _, e := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182); follow <- e }()
	waitOKXFlight(t, server.URL, 2)
	cancel()
	if e := <-lead; !errors.Is(e, context.Canceled) {
		t.Fatalf("leader error=%v", e)
	}
	close(release)
	if e := <-follow; e != nil {
		t.Fatalf("follower lost shared operation: %v", e)
	}
	if calls.Load() != 1 {
		t.Fatal("follower launched a duplicate")
	}
}

func TestOKXCandleLastCancellationRetiresFlight(t *testing.T) {
	entered := make(chan struct{}, 1)
	abandoned := make(chan struct{}, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-r.Context().Done()
			abandoned <- struct{}{}
			return
		}
		writeJSON(w, okxCoordCandleJSON())
	}))
	defer server.Close()
	p := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := p.GetKlinesFreshContext(ctx, "BTCUSDT", "15m", 182); done <- e }()
	<-entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	// New callers must not join the canceled work, even before old work returns.
	_, err := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182)
	if err != nil {
		t.Fatalf("replacement failed: %v", err)
	}
	select {
	case <-abandoned:
	case <-time.After(time.Second):
		t.Fatal("abandoned HTTP request not canceled")
	}
}

func TestOKXCandleSharedCooldownAndNonCandleIsolation(t *testing.T) {
	throttled := make(chan time.Time, 1)
	var attempts atomic.Int32
	var early atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != okxPublicCandlesPath {
			writeJSON(w, `{"code":"0","data":[]}`)
			return
		}
		if attempts.Add(1) == 1 {
			throttled <- time.Now()
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			writeJSON(w, `{"code":"50011"}`)
			return
		}
		writeJSON(w, okxCoordCandleJSON())
	}))
	defer server.Close()
	p := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	first := make(chan error, 1)
	go func() { _, e := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182); first <- e }()
	at := <-throttled
	sharedOKXCandles.mu.Lock()
	gate := sharedOKXCandles.gates[server.URL]
	sharedOKXCandles.mu.Unlock()
	until := time.Now().Add(time.Second)
	for {
		gate.mu.Lock()
		blocked := gate.blockedUntil.After(at)
		gate.mu.Unlock()
		if blocked {
			break
		}
		if time.Now().After(until) {
			t.Fatal("cooldown not published")
		}
		time.Sleep(time.Millisecond)
	}
	other := newNativePublicHTTPClient(server.URL, server.Client())
	if _, e := other.getFresh(okxPublicTickerPath, url.Values{}); e != nil {
		t.Fatalf("risk quote endpoint blocked: %v", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, e := p.GetKlinesFreshContext(ctx, "ETHUSDT", "15m", 182)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("queued caller ignored deadline: %v", e)
	}
	if attempts.Load() != 1 {
		early.Store(true)
	}
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	if early.Load() {
		t.Fatal("another candle request bypassed shared cooldown")
	}
	if time.Since(at) < 2*time.Second {
		t.Fatal("Retry-After was not respected")
	}
}

func TestOKXCandleAPI50011RetriesBoundedly(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeJSON(w, `{"code":"50011","msg":"limited"}`)
			return
		}
		writeJSON(w, okxCoordCandleJSON())
	}))
	defer server.Close()
	p := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	at := time.Now()
	_, e := p.GetKlinesFreshContext(context.Background(), "BTCUSDT", "15m", 182)
	if e != nil || calls.Load() != 2 || time.Since(at) < 2*time.Second {
		t.Fatalf("API-code throttle not retried safely: calls=%d err=%v", calls.Load(), e)
	}
}

func TestOKXCandlePersistentThrottleHonorsParent(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		writeJSON(w, `{"code":"50011"}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	p := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	_, e := p.GetKlinesFreshContext(ctx, "BTCUSDT", "15m", 182)
	if !errors.Is(e, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("persistent throttle escaped deadline: calls=%d err=%v", calls.Load(), e)
	}
}

func TestOKXCandleRequestIdentityAndNoCompletedFreshCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeJSON(w, okxCoordCandleJSON()) }))
	defer server.Close()
	client := newNativePublicHTTPClient(server.URL, server.Client())
	q := url.Values{"bar": {"15m"}, "instId": {"BTC-USDT-SWAP"}, "limit": {"182"}}
	at := time.Date(2026, 10, 9, 0, 14, 59, 0, time.UTC)
	k := okxCandleKey(client, q, 0, false, at)
	if k == okxCandleKey(client, q, 0, false, at.Add(time.Second)) {
		t.Fatal("new candle boundary joined old flight")
	}
	if k == okxCandleKey(client, q, 0, true, at) {
		t.Fatal("cache-allowed read shared with fresh read")
	}
	q.Set("limit", "100")
	if k == okxCandleKey(client, q, 0, false, at) {
		t.Fatal("different history lengths shared")
	}
	q.Set("limit", "182")
	q.Set("after", "123")
	if k == okxCandleKey(client, q, 0, false, at) {
		t.Fatal("pagination ignored")
	}
	q.Del("after")
	for i := 0; i < 2; i++ {
		if _, e := client.getFresh(okxPublicCandlesPath, q); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("fresh reader received completed cached data")
	}
}

func TestOKXCandleRetryAfterDateAndCooldownExtension(t *testing.T) {
	until := time.Now().Add(6 * time.Second).UTC().Truncate(time.Second)
	d := okxCandleRetryDelay(until.Format(http.TimeFormat), 1)
	if d < 4*time.Second || d > 6*time.Second {
		t.Fatalf("HTTP-date Retry-After=%s", d)
	}
	if d := okxCandleRetryDelay("invalid", 2); d != 4*time.Second {
		t.Fatalf("fallback delay=%s", d)
	}
	g := &okxCandleGate{}
	g.coolDown(5 * time.Second)
	at := g.blockedUntil
	g.coolDown(time.Second)
	if !g.blockedUntil.Equal(at) {
		t.Fatal("a shorter delay truncated shared cooldown")
	}
}
