package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OKX documents /market/candles at 40 requests/2 seconds/IP. Leave headroom
// and pace dispatches (including retries), not merely goroutine concurrency.
// https://www.okx.com/docs-v5/en/ (Market Data / Candlesticks)
const okxCandleDispatchInterval = 80 * time.Millisecond
const okxCandleWorkBudget = 40 * time.Second

type okxCandleGate struct {
	mu                 sync.Mutex
	next, blockedUntil time.Time
}

func (g *okxCandleGate) admit(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		now := time.Now()
		at := g.next
		if g.blockedUntil.After(at) {
			at = g.blockedUntil
		}
		if !at.After(now) {
			g.next = now.Add(okxCandleDispatchInterval)
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		if !waitNativePublicRetry(ctx, time.Until(at)) {
			return ctx.Err()
		}
	}
}
func (g *okxCandleGate) coolDown(delay time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	until := time.Now().Add(delay)
	if until.After(g.blockedUntil) {
		g.blockedUntil = until
	}
}

type okxCandleRequestKey struct {
	origin, query                 string
	transport                     any
	window                        int64
	clientTimeout, attemptTimeout time.Duration
	allowCache                    bool
}
type okxCandleFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	body    []byte
	err     error
}
type okxCandleCoordinator struct {
	mu      sync.Mutex
	gates   map[string]*okxCandleGate
	flights map[okxCandleRequestKey]*okxCandleFlight
}

var sharedOKXCandles = okxCandleCoordinator{gates: make(map[string]*okxCandleGate), flights: make(map[okxCandleRequestKey]*okxCandleFlight)}

// Bitget's native candles exhibit the same burst/429 failure mode. Reuse the
// conservative dispatch/coalescing implementation with a separate venue budget;
// a Bitget cooldown must never delay OKX or real-time protection price reads.
var sharedBitgetCandles = okxCandleCoordinator{gates: make(map[string]*okxCandleGate), flights: make(map[okxCandleRequestKey]*okxCandleFlight)}

func okxCandleOrigin(base string) string {
	u, err := url.Parse(base)
	if err == nil {
		for _, domain := range []string{"okx.com", "bitget.com"} {
			if u.Hostname() == domain || strings.HasSuffix(u.Hostname(), "."+domain) {
				return domain
			}
		}
	}
	return base
}
func publicTransportScope(c *http.Client) any {
	rt := c.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if reflect.TypeOf(rt).Comparable() {
		return rt
	}
	return c
}

// Exact simultaneous requests share one bounded operation, never a completed
// response cache. A new candle boundary gets a new flight identity. Separate
// transports do not share responses, but still share the same IP-rate gate.
func okxCandleKey(client nativePublicHTTPClient, query url.Values, attemptTimeout time.Duration, allowCache bool, now time.Time) okxCandleRequestKey {
	interval := query.Get("bar")
	if interval == "" {
		interval = query.Get("granularity")
	}
	return okxCandleRequestKey{origin: client.baseURL, query: query.Encode(), transport: publicTransportScope(client.client), window: now.Truncate(okxIntervalDuration(interval)).UnixMilli(), clientTimeout: client.client.Timeout, attemptTimeout: attemptTimeout, allowCache: allowCache}
}

func (c *okxCandleCoordinator) get(ctx context.Context, client nativePublicHTTPClient, query url.Values, attemptTimeout time.Duration, allowCache bool, run func(context.Context, *okxCandleGate) ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := okxCandleKey(client, query, attemptTimeout, allowCache, time.Now())
	c.mu.Lock()
	gateKey := okxCandleOrigin(client.baseURL)
	gate := c.gates[gateKey]
	if gate == nil {
		gate = &okxCandleGate{}
		c.gates[gateKey] = gate
	}
	f := c.flights[key]
	if f == nil {
		workCtx, cancel := context.WithTimeout(context.Background(), okxCandleWorkBudget)
		f = &okxCandleFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[key] = f
		go func(f *okxCandleFlight) {
			body, err := run(workCtx, gate)
			c.mu.Lock()
			f.body = append([]byte(nil), body...)
			f.err = err
			if c.flights[key] == f {
				delete(c.flights, key)
			}
			close(f.done)
			c.mu.Unlock()
			cancel()
		}(f)
	}
	f.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 && c.flights[key] == f {
			delete(c.flights, key)
			f.cancel()
		}
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return append([]byte(nil), f.body...), f.err
	}
}

func okxCandleThrottled(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	var envelope struct {
		Code json.RawMessage `json:"code"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	var code string
	if json.Unmarshal(envelope.Code, &code) == nil {
		return code == "50011"
	}
	return string(envelope.Code) == "50011"
}
func okxCandleRetryDelay(header string, attempt int) time.Duration {
	delay := 2 * time.Second * time.Duration(1<<uint(attempt-1))
	if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64); err == nil && seconds > 0 && seconds <= int64((time.Duration(1<<63-1))/time.Second) {
		if d := time.Duration(seconds) * time.Second; d > delay {
			delay = d
		}
	}
	if at, err := http.ParseTime(header); err == nil {
		if d := time.Until(at); d > delay {
			delay = d
		}
	}
	return delay
}
