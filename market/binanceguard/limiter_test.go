package binanceguard

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestLimiterSerializesAndPacesRequests(t *testing.T) {
	limiter := newLimiter(2 * time.Millisecond)
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var startsMu sync.Mutex
	var starts []time.Time
	transport := &rateLimitedTransport{
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			current := inFlight.Add(1)
			for {
				previous := maxInFlight.Load()
				if current <= previous || maxInFlight.CompareAndSwap(previous, current) {
					break
				}
			}
			startsMu.Lock()
			starts = append(starts, time.Now())
			startsMu.Unlock()
			time.Sleep(time.Millisecond)
			inFlight.Add(-1)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		limiter: limiter,
	}

	var callers sync.WaitGroup
	for range 4 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if _, err := transport.RoundTrip((&http.Request{}).WithContext(context.Background())); err != nil {
				t.Errorf("RoundTrip: %v", err)
			}
		}()
	}
	callers.Wait()

	if maxInFlight.Load() != 1 {
		t.Fatalf("max in-flight requests = %d, want 1", maxInFlight.Load())
	}
	startsMu.Lock()
	defer startsMu.Unlock()
	for index := 1; index < len(starts); index++ {
		if gap := starts[index].Sub(starts[index-1]); gap < 2*time.Millisecond {
			t.Fatalf("request gap = %s, want at least 2ms", gap)
		}
	}
}

func TestLimiterHonorsContextCancellationWhileQueued(t *testing.T) {
	limiter := newLimiter(time.Hour)
	firstRelease, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer firstRelease()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := limiter.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued acquire error = %v, want context deadline", err)
	} else {
		var admissionError *AdmissionError
		if !errors.As(err, &admissionError) {
			t.Fatalf("queued acquire error = %T, want AdmissionError", err)
		}
	}
}

func TestLimiterRejectsOverflowWithoutConsumingAdmission(t *testing.T) {
	limiter := newLimiter(0)
	limiter.maxNormal = 1
	holderRelease, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer holderRelease()

	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	defer cancelQueued()
	queuedResult := make(chan error, 1)
	go func() {
		_, acquireErr := limiter.acquire(queuedCtx)
		queuedResult <- acquireErr
	}()
	waitForLimiterQueue(t, limiter, false)

	if _, err := limiter.acquire(context.Background()); !errors.Is(err, ErrAdmissionQueueFull) {
		t.Fatalf("overflow acquire error = %v, want ErrAdmissionQueueFull", err)
	} else {
		var admissionError *AdmissionError
		if !errors.As(err, &admissionError) {
			t.Fatalf("overflow acquire error = %T, want AdmissionError", err)
		}
	}
	cancelQueued()
	if err := <-queuedResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation error = %v, want context canceled", err)
	}
}

func TestCriticalRequestPreemptsQueuedNormalRequest(t *testing.T) {
	limiter := newLimiter(0)
	firstRelease, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	events := make(chan string, 2)
	go func() {
		release, acquireErr := limiter.acquireWithPriority(context.Background(), PriorityNormal)
		if acquireErr != nil {
			t.Errorf("normal acquire: %v", acquireErr)
			return
		}
		events <- "normal"
		release()
	}()
	waitForLimiterQueue(t, limiter, false)

	go func() {
		release, acquireErr := limiter.acquireWithPriority(context.Background(), PriorityCritical)
		if acquireErr != nil {
			t.Errorf("critical acquire: %v", acquireErr)
			return
		}
		events <- "critical"
		release()
	}()
	waitForLimiterQueue(t, limiter, true)

	firstRelease()
	if first := <-events; first != "critical" {
		t.Fatalf("first admitted queued request = %s, want critical", first)
	}
	if second := <-events; second != "normal" {
		t.Fatalf("second admitted queued request = %s, want normal", second)
	}
}

func TestRequestPriorityClassifiesSignedOrderMutations(t *testing.T) {
	tests := []struct {
		method   string
		path     string
		priority Priority
	}{
		{method: http.MethodPost, path: "https://fapi.binance.com/fapi/v1/order", priority: PriorityCritical},
		{method: http.MethodDelete, path: "https://fapi.binance.com/fapi/v1/allOpenOrders/", priority: PriorityCritical},
		{method: http.MethodPost, path: "https://fapi.binance.com/fapi/v1/algoOpenOrders", priority: PriorityCritical},
		{method: http.MethodPost, path: "https://fapi.binance.com/fapi/v1/leverage", priority: PriorityCritical},
		{method: http.MethodPost, path: "https://fapi.binance.com/fapi/v1/listenKey", priority: PriorityCritical},
		{method: http.MethodPost, path: "https://fapi.binance.com/fapi/v1/listenKey/", priority: PriorityCritical},
		{method: http.MethodGet, path: "https://fapi.binance.com/fapi/v1/order", priority: PriorityNormal},
		{method: http.MethodPost, path: "https://fapi.binance.com/fapi/v1/account", priority: PriorityNormal},
	}
	for _, test := range tests {
		request, err := http.NewRequest(test.method, test.path, nil)
		if err != nil {
			t.Fatalf("NewRequest(%s): %v", test.path, err)
		}
		if got := requestPriority(request); got != test.priority {
			t.Errorf("requestPriority(%s %s) = %d, want %d", test.method, test.path, got, test.priority)
		}
	}
	if got := requestPriority(&http.Request{Method: http.MethodPost}); got != PriorityNormal {
		t.Fatalf("requestPriority(nil URL) = %d, want normal", got)
	}
}

func TestWrappedClientsShareTheDefaultLimiter(t *testing.T) {
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		current := inFlight.Add(1)
		for {
			previous := maxInFlight.Load()
			if current <= previous || maxInFlight.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	first := WrapClient(&http.Client{Transport: transport})
	second := WrapClient(&http.Client{Transport: transport})

	var callers sync.WaitGroup
	for _, client := range []*http.Client{first, second} {
		callers.Add(1)
		go func(client *http.Client) {
			defer callers.Done()
			request, err := http.NewRequest(http.MethodGet, "https://fapi.binance.com/fapi/v1/time", nil)
			if err != nil {
				t.Errorf("NewRequest: %v", err)
				return
			}
			response, err := client.Do(request)
			if err != nil {
				t.Errorf("Do: %v", err)
				return
			}
			response.Body.Close()
		}(client)
	}
	callers.Wait()
	if maxInFlight.Load() != 1 {
		t.Fatalf("max in-flight requests across clients = %d, want 1", maxInFlight.Load())
	}
}

func TestCriticalBurstCapsAtMaxThenAllowsNormal(t *testing.T) {
	limiter := newLimiter(0)
	var mu sync.Mutex
	var admitted []Priority

	holderRelease, err := limiter.acquireWithPriority(context.Background(), PriorityNormal)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}

	const criticalCount = 20
	criticalDone := make([]chan struct{}, criticalCount)
	for i := range criticalCount {
		done := make(chan struct{})
		criticalDone[i] = done
		go func(done chan struct{}) {
			release, err := limiter.acquireWithPriority(context.Background(), PriorityCritical)
			if err != nil {
				t.Errorf("critical acquire: %v", err)
				close(done)
				return
			}
			mu.Lock()
			admitted = append(admitted, PriorityCritical)
			mu.Unlock()
			release()
			close(done)
		}(done)
	}

	normalDone := make(chan struct{})
	go func() {
		release, err := limiter.acquireWithPriority(context.Background(), PriorityNormal)
		if err != nil {
			t.Errorf("normal acquire: %v", err)
			close(normalDone)
			return
		}
		mu.Lock()
		admitted = append(admitted, PriorityNormal)
		mu.Unlock()
		release()
		close(normalDone)
	}()

	time.Sleep(20 * time.Millisecond)
	holderRelease()

	for _, done := range criticalDone {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("critical request never admitted")
		}
	}
	select {
	case <-normalDone:
	case <-time.After(2 * time.Second):
		t.Fatal("normal request never admitted")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(admitted) != criticalCount+1 {
		t.Fatalf("admissions = %d, want %d", len(admitted), criticalCount+1)
	}
	for i := 0; i < maxCriticalBurst; i++ {
		if admitted[i] != PriorityCritical {
			t.Fatalf("admission %d = %v, want critical", i, admitted[i])
		}
	}
	if admitted[maxCriticalBurst] != PriorityNormal {
		t.Fatalf(
			"admission %d = %v, want normal once the critical burst cap is reached",
			maxCriticalBurst, admitted[maxCriticalBurst],
		)
	}
}

func TestCanceledWaitersAreSkipped(t *testing.T) {
	limiter := newLimiter(0)
	var mu sync.Mutex
	var admitted []Priority

	holderRelease, err := limiter.acquireWithPriority(context.Background(), PriorityNormal)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	canceledErr := make(chan error, 1)
	go func() {
		_, err := limiter.acquireWithPriority(cancelCtx, PriorityNormal)
		canceledErr <- err
	}()
	if err := <-canceledErr; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled acquire error = %v, want context deadline", err)
	}

	done := make(chan struct{})
	go func() {
		release, err := limiter.acquireWithPriority(context.Background(), PriorityNormal)
		if err != nil {
			t.Errorf("second normal acquire: %v", err)
			close(done)
			return
		}
		mu.Lock()
		admitted = append(admitted, PriorityNormal)
		mu.Unlock()
		release()
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	holderRelease()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second normal request never admitted")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(admitted) != 1 || admitted[0] != PriorityNormal {
		t.Fatalf("admissions = %v, want only the second normal request", admitted)
	}
}

func waitForLimiterQueue(t *testing.T, limiter *Limiter, critical bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		limiter.mu.Lock()
		queued := len(limiter.normal) > 0
		if critical {
			queued = len(limiter.critical) > 0
		}
		limiter.mu.Unlock()
		if queued {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("request was not queued (critical=%t)", critical)
}
