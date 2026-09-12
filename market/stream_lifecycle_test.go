package market

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func waitStreamSignal[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("stream lifecycle did not complete")
		var zero T
		return zero
	}
}

func TestStreamStopCancelsBothSourcesAndWithdrawsSnapshot(t *testing.T) {
	started := make(chan context.Context, 2)
	connector := func(ctx context.Context) (<-chan StreamEvent[int], <-chan error, error) {
		started <- ctx
		return make(chan StreamEvent[int]), make(chan error), nil
	}
	runtime := newRedundantStream(connector, connector)
	runtime.Start(context.Background())
	a, b := waitStreamSignal(t, started), waitStreamSignal(t, started)
	runtime.mu.Lock()
	runtime.sources[0].apply(StreamEvent[int]{Value: 7, Snapshot: true, SourceTime: time.Now()}, time.Now())
	runtime.mu.Unlock()
	runtime.Stop()
	runtime.Stop()
	waitStreamSignal(t, a.Done())
	waitStreamSignal(t, b.Done())
	if _, _, err := runtime.Snapshot(time.Second); !errors.Is(err, ErrFreshStreamUnavailable) {
		t.Fatalf("stopped stream remains publishable: %v", err)
	}
	// Stop-before-Start is also safe and cannot resurrect the runtime.
	neverStarted := newRedundantStream(connector, connector)
	neverStarted.Stop()
	neverStarted.Start(context.Background())
}

func TestStreamGapCancelsOldProducerBeforeReconnect(t *testing.T) {
	connections := make(chan context.Context, 2)
	events := make(chan chan StreamEvent[int], 2)
	connector := func(ctx context.Context) (<-chan StreamEvent[int], <-chan error, error) {
		ch := make(chan StreamEvent[int], 2)
		connections <- ctx
		events <- ch
		return ch, make(chan error), nil
	}
	runtime := newRedundantStream(connector, nil)
	runtime.Start(context.Background())
	defer runtime.Stop()
	oldCtx, oldEvents := waitStreamSignal(t, connections), waitStreamSignal(t, events)
	oldEvents <- StreamEvent[int]{Value: 1, Snapshot: true, Sequence: 1, SourceTime: time.Now()}
	oldEvents <- StreamEvent[int]{Value: 3, Sequence: 3, PreviousSequence: 2, SourceTime: time.Now()}
	waitStreamSignal(t, connections)
	newEvents := waitStreamSignal(t, events)
	if oldCtx.Err() == nil {
		t.Fatal("old producer survived replacement connection")
	}
	if _, _, err := runtime.Snapshot(time.Second); !errors.Is(err, ErrFreshStreamUnavailable) {
		t.Fatalf("old snapshot survived gap: %v", err)
	}
	newEvents <- StreamEvent[int]{Value: 10, Snapshot: true, Sequence: 10, SourceTime: time.Now()}
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("new snapshot was not reconciled")
		case <-ticker.C:
			if value, _, err := runtime.Snapshot(time.Second); err == nil && value == 10 {
				return
			}
		}
	}
}

func TestIdleStreamRetirementIsAutomaticAndPreservesActiveStreams(t *testing.T) {
	var mu sync.Mutex
	idle := newRedundantStream[int](nil, nil)
	active := newRedundantStream[int](nil, nil)
	streams := map[string]*RedundantStream[int]{"idle": idle, "active": active}
	demand := map[string]time.Time{"idle": time.Now(), "active": time.Now()}
	metadata := map[string]string{"idle": "proof", "active": "proof"}
	retired := make(chan string, 2)
	mu.Lock()
	for key, runtime := range streams {
		retireIdleStream(&mu, streams, demand, key, runtime, 20*time.Millisecond, func() {
			delete(metadata, key)
			retired <- key
		})
	}
	// A request renews demand even if the first timer callback is already queued.
	demand["active"] = time.Now().Add(80 * time.Millisecond)
	mu.Unlock()
	if key := waitStreamSignal(t, retired); key != "idle" {
		t.Fatalf("retired active key %s", key)
	}
	mu.Lock()
	if streams["idle"] != nil || !demand["idle"].IsZero() || metadata["idle"] != "" || streams["active"] != active || metadata["active"] == "" {
		t.Fatal("incorrect retirement scope")
	}
	// End this lease so the test leaves no long-lived timer.
	demand["active"] = time.Now().Add(-time.Hour)
	mu.Unlock()
	if key := waitStreamSignal(t, retired); key != "active" {
		t.Fatalf("retired key %s", key)
	}
}

func TestOldIdleCallbackCannotRetireReplacementStream(t *testing.T) {
	var mu sync.Mutex
	old, replacement := newRedundantStream[int](nil, nil), newRedundantStream[int](nil, nil)
	streams := map[string]*RedundantStream[int]{"BTC": old}
	demand := map[string]time.Time{"BTC": time.Now()}
	retired := make(chan struct{}, 1)
	mu.Lock()
	retireIdleStream(&mu, streams, demand, "BTC", old, time.Millisecond, func() { retired <- struct{}{} })
	streams["BTC"] = replacement
	mu.Unlock()
	select {
	case <-retired:
		t.Fatal("old lease retired replacement")
	case <-time.After(30 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	if streams["BTC"] != replacement {
		t.Fatal("replacement removed")
	}
}

func TestRetiredStreamsIgnoreLateRESTMetadata(t *testing.T) {
	funding := &FundingSnapshot{Time: time.Now().UnixMilli(), MarkPrice: 100, IndexPrice: 100}
	bitget := fundingStreamRegistry{
		streams:       map[string]*RedundantStream[*FundingSnapshot]{},
		restFallbacks: map[string]FreshnessProof{}, restCounts: map[string]uint64{}, lastErrors: map[string]string{},
	}
	okx := okxMarketStreamRegistry{
		prices: map[string]*RedundantStream[float64]{}, fundings: map[string]*RedundantStream[*FundingSnapshot]{},
		priceRESTFallbacks: map[string]FreshnessProof{}, fundingRESTFallbacks: map[string]FreshnessProof{},
		priceRESTCounts: map[string]uint64{}, fundingRESTCounts: map[string]uint64{},
		priceErrors: map[string]string{}, fundingErrors: map[string]string{},
	}
	for _, err := range []error{nil, errors.New("late REST failure")} {
		bitget.recordREST("BTCUSDT", funding, err)
		okx.recordFundingREST("BTCUSDT", funding, err)
		okx.recordPriceREST("BTCUSDT", &okxTickerRow{Ts: "1789171200000"}, err)
	}
	if len(bitget.restFallbacks)+len(bitget.restCounts)+len(bitget.lastErrors)+len(okx.priceRESTFallbacks)+len(okx.fundingRESTFallbacks)+len(okx.priceRESTCounts)+len(okx.fundingRESTCounts)+len(okx.priceErrors)+len(okx.fundingErrors) != 0 {
		t.Fatal("late REST result recreated metadata for a retired stream")
	}
}

func TestDepthRESTMetadataOnlyTracksRegisteredStreams(t *testing.T) {
	const symbol = "NOFXRETIREUSDT"
	const key = "binance|" + symbol
	depth := &DepthSnapshot{Fresh: true, ReceivedAt: time.Now(), EventTime: time.Now().UnixMilli()}
	sharedDepthStreams.mu.Lock()
	sharedDepthStreams.streams[key] = newRedundantStream[*DepthSnapshot](nil, nil)
	sharedDepthStreams.mu.Unlock()
	t.Cleanup(func() {
		sharedDepthStreams.mu.Lock()
		delete(sharedDepthStreams.streams, key)
		delete(sharedDepthStreams.restFallbacks, key)
		sharedDepthStreams.mu.Unlock()
	})
	recordDepthRESTFallback("binance", symbol, depth)
	sharedDepthStreams.mu.Lock()
	_, tracked := sharedDepthStreams.restFallbacks[key]
	delete(sharedDepthStreams.streams, key)
	delete(sharedDepthStreams.restFallbacks, key)
	sharedDepthStreams.mu.Unlock()
	if !tracked {
		t.Fatal("registered stream lost its REST fallback")
	}
	recordDepthRESTFallback("binance", symbol, depth)
	sharedDepthStreams.mu.Lock()
	_, orphan := sharedDepthStreams.restFallbacks[key]
	sharedDepthStreams.mu.Unlock()
	if orphan {
		t.Fatal("REST-only or retired stream created orphan metadata")
	}
}
