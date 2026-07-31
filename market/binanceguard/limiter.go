// Package binanceguard contains the process-wide admission control shared by
// every Binance HTTP client in the application. Binance applies request-weight
// limits to the egress IP, not to an individual Go client, so per-client
// throttles are insufficient when several traders run in one process.
package binanceguard

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Keep the baseline below seven admissions per second. This leaves useful
// headroom for endpoints whose individual request weight is higher than one
// without making a multi-symbol market-data scan wait for the HTTP timeout.
const defaultRequestInterval = 150 * time.Millisecond

var defaultLimiter = newLimiter(defaultRequestInterval)

// Priority controls where a request waits in the process-wide admission queue.
// Critical requests still observe the same global pacing interval; priority
// only prevents an order/cancel operation from sitting behind a market-data
// burst.
type Priority uint8

const (
	PriorityNormal Priority = iota
	PriorityCritical
)

type requestWaiter struct {
	ready    chan struct{}
	priority Priority
	admitted bool
	canceled bool
	released bool
}

// Limiter serializes request admission and spaces consecutive requests. The
// gate is deliberately process-wide: public market data and signed futures
// requests consume the same Binance IP quota.
type Limiter struct {
	interval      time.Duration
	mu            sync.Mutex
	nextAt        time.Time
	critical      []*requestWaiter
	normal        []*requestWaiter
	dispatching   bool
	inFlight      bool
	criticalBurst int
}

const maxCriticalBurst = 8

func newLimiter(interval time.Duration) *Limiter {
	if interval < 0 {
		interval = 0
	}
	return &Limiter{
		interval: interval,
	}
}

func (limiter *Limiter) acquire(ctx context.Context) (func(), error) {
	return limiter.acquireWithPriority(ctx, PriorityNormal)
}

func (limiter *Limiter) acquireWithPriority(ctx context.Context, priority Priority) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	waiter := &requestWaiter{
		ready:    make(chan struct{}),
		priority: priority,
	}

	limiter.mu.Lock()
	if priority == PriorityCritical {
		limiter.critical = append(limiter.critical, waiter)
	} else {
		limiter.normal = append(limiter.normal, waiter)
	}
	limiter.kickLocked()
	limiter.mu.Unlock()

	select {
	case <-waiter.ready:
		return func() { limiter.release(waiter) }, nil
	case <-ctx.Done():
		limiter.mu.Lock()
		admitted := waiter.admitted
		if !admitted {
			waiter.canceled = true
		}
		limiter.mu.Unlock()
		if admitted {
			// The scheduler may have admitted the request concurrently with
			// context cancellation. Release that admission immediately so the
			// queue cannot remain blocked.
			limiter.release(waiter)
		}
		return nil, ctx.Err()
	}
}

func (limiter *Limiter) release(waiter *requestWaiter) {
	limiter.mu.Lock()
	if !waiter.admitted || waiter.released {
		limiter.mu.Unlock()
		return
	}
	waiter.released = true
	limiter.inFlight = false
	limiter.kickLocked()
	limiter.mu.Unlock()
}

func (limiter *Limiter) kickLocked() {
	if limiter.dispatching || limiter.inFlight || (!limiter.hasPendingLocked()) {
		return
	}
	limiter.dispatching = true
	go limiter.dispatch()
}

func (limiter *Limiter) dispatch() {
	for {
		limiter.mu.Lock()
		if limiter.inFlight || !limiter.hasPendingLocked() {
			limiter.dispatching = false
			limiter.mu.Unlock()
			return
		}

		if wait := time.Until(limiter.nextAt); wait > 0 {
			limiter.mu.Unlock()
			timer := time.NewTimer(wait)
			<-timer.C
			continue
		}

		waiter := limiter.popNextLocked()
		if waiter == nil {
			continue
		}
		waiter.admitted = true
		limiter.inFlight = true
		limiter.nextAt = time.Now().Add(limiter.interval)
		// The admission itself is complete. Let release() start the next
		// dispatch after the HTTP round trip finishes.
		limiter.dispatching = false
		limiter.mu.Unlock()
		close(waiter.ready)
		return
	}
}

func (limiter *Limiter) hasPendingLocked() bool {
	return len(limiter.critical) > 0 || len(limiter.normal) > 0
}

func (limiter *Limiter) popNextLocked() *requestWaiter {
	for len(limiter.critical) > 0 || len(limiter.normal) > 0 {
		useCritical := len(limiter.critical) > 0 &&
			(len(limiter.normal) == 0 || limiter.criticalBurst < maxCriticalBurst)
		var waiter *requestWaiter
		if useCritical {
			waiter = limiter.critical[0]
			limiter.critical = limiter.critical[1:]
			limiter.criticalBurst++
		} else {
			waiter = limiter.normal[0]
			limiter.normal = limiter.normal[1:]
			limiter.criticalBurst = 0
		}
		if waiter.canceled {
			continue
		}
		return waiter
	}
	return nil
}

type rateLimitedTransport struct {
	base    http.RoundTripper
	limiter *Limiter
}

func (transport *rateLimitedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	release, err := transport.limiter.acquireWithPriority(request.Context(), requestPriority(request))
	if err != nil {
		return nil, err
	}
	defer release()
	return transport.base.RoundTrip(request)
}

type priorityContextKey struct{}

// WithPriority allows callers that construct signed SDK requests directly to
// override the transport's endpoint classifier. Most Binance SDK calls are
// classified automatically from method and path, so this is only needed for
// custom endpoints.
func WithPriority(ctx context.Context, priority Priority) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, priorityContextKey{}, priority)
}

func requestPriority(request *http.Request) Priority {
	if request == nil {
		return PriorityNormal
	}
	if priority, ok := request.Context().Value(priorityContextKey{}).(Priority); ok {
		return priority
	}
	if request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodDelete {
		return PriorityNormal
	}
	if request.URL == nil {
		return PriorityNormal
	}
	path := strings.TrimRight(request.URL.Path, "/")
	switch path {
	case "/fapi/v1/order", "/fapi/v1/batchOrders", "/fapi/v1/allOpenOrders",
		"/fapi/v1/algoOrder", "/fapi/v1/algoOpenOrders", "/fapi/v1/countdownCancelAll",
		"/fapi/v1/leverage", "/fapi/v1/marginType", "/fapi/v1/positionSide/dual", "/fapi/v1/positionMargin",
		// A listenKey expiry severs the WebSocket stream mid-trade, so its
		// keepalive must never wait behind a market-data burst.
		"/fapi/v1/listenKey":
		return PriorityCritical
	default:
		return PriorityNormal
	}
}

// NewTransport wraps a Binance transport with the process-wide limiter.
func NewTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, alreadyWrapped := base.(*rateLimitedTransport); alreadyWrapped {
		return base
	}
	return &rateLimitedTransport{base: base, limiter: defaultLimiter}
}

// WrapClient returns a copy of client whose transport participates in the
// process-wide Binance request gate. Client settings such as timeout, proxy,
// cookies, and redirect policy are preserved.
func WrapClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	wrapped := *client
	wrapped.Transport = NewTransport(client.Transport)
	return &wrapped
}
