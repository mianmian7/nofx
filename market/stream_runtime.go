package market

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrStreamNeedsSnapshot    = errors.New("stream requires a reconciled snapshot")
	ErrStreamSequenceGap      = errors.New("stream sequence gap")
	ErrFreshStreamUnavailable = errors.New("fresh reconciled stream unavailable")
)

const streamMaxFutureSkew = 5 * time.Second

type StreamEvent[T any] struct {
	Value            T
	Snapshot         bool
	Sequence         int64
	PreviousSequence int64
	SourceTime       time.Time
	Exchange         string
	Transport        string
}

type StreamConnector[T any] func(context.Context) (<-chan StreamEvent[T], <-chan error, error)

type streamSourceState[T any] struct {
	value        T
	sequence     int64
	sourceTime   time.Time
	receivedAt   time.Time
	exchange     string
	transport    string
	ready        bool
	reconciled   bool
	lastError    error
	reconnects   uint64
	sequenceGaps uint64
}

func (state *streamSourceState[T]) apply(event StreamEvent[T], receivedAt time.Time) error {
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	if event.SourceTime.IsZero() {
		state.invalidate(errors.New("stream event has no source time"))
		return state.lastError
	}
	if event.SourceTime.After(receivedAt.Add(streamMaxFutureSkew)) {
		state.invalidate(errors.New("stream event source time is in the future"))
		return state.lastError
	}
	if event.Snapshot {
		state.value = event.Value
		state.sequence = event.Sequence
		state.sourceTime = event.SourceTime.UTC()
		state.receivedAt = receivedAt.UTC()
		state.exchange = event.Exchange
		state.transport = event.Transport
		state.ready = true
		state.reconciled = true
		state.lastError = nil
		return nil
	}
	if !state.ready || !state.reconciled {
		state.invalidate(ErrStreamNeedsSnapshot)
		return ErrStreamNeedsSnapshot
	}
	// Some venues send an empty heartbeat with the same current and previous
	// sequence. It proves liveness without mutating the book.
	if event.Sequence == state.sequence && event.PreviousSequence == state.sequence {
		state.sourceTime = event.SourceTime.UTC()
		state.receivedAt = receivedAt.UTC()
		return nil
	}
	if event.PreviousSequence != state.sequence || event.Sequence <= state.sequence {
		state.sequenceGaps++
		state.invalidate(fmt.Errorf("%w: previous=%d current=%d event_previous=%d event=%d", ErrStreamSequenceGap, state.sequence, state.sequence, event.PreviousSequence, event.Sequence))
		return state.lastError
	}
	state.value = event.Value
	state.sequence = event.Sequence
	state.sourceTime = event.SourceTime.UTC()
	state.receivedAt = receivedAt.UTC()
	state.exchange = event.Exchange
	state.transport = event.Transport
	state.lastError = nil
	return nil
}

func (state *streamSourceState[T]) invalidate(err error) {
	state.ready = false
	state.reconciled = false
	state.lastError = err
}

type StreamSourceHealth struct {
	Exchange     string        `json:"exchange"`
	Transport    string        `json:"transport"`
	Ready        bool          `json:"ready"`
	Reconciled   bool          `json:"reconciled"`
	Sequence     int64         `json:"sequence"`
	SourceTime   time.Time     `json:"source_time"`
	ReceivedAt   time.Time     `json:"received_at"`
	FreshnessAge time.Duration `json:"freshness_age"`
	Reconnects   uint64        `json:"reconnects"`
	SequenceGaps uint64        `json:"sequence_gaps"`
	LastError    string        `json:"last_error,omitempty"`
}

type RedundantStream[T any] struct {
	mu         sync.RWMutex
	connectors [2]StreamConnector[T]
	sources    [2]streamSourceState[T]
	startOnce  sync.Once
}

func newRedundantStream[T any](primary, standby StreamConnector[T]) *RedundantStream[T] {
	return &RedundantStream[T]{connectors: [2]StreamConnector[T]{primary, standby}}
}

func (runtime *RedundantStream[T]) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtime.startOnce.Do(func() {
		for sourceIndex, connector := range runtime.connectors {
			if connector != nil {
				go runtime.runSource(ctx, sourceIndex, connector)
			}
		}
	})
}

func (runtime *RedundantStream[T]) runSource(ctx context.Context, sourceIndex int, connector StreamConnector[T]) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		events, streamErrors, err := connector(ctx)
		if err != nil {
			runtime.markDisconnected(sourceIndex, err)
			if !waitStreamRetry(ctx, backoff) {
				return
			}
			backoff = nextStreamBackoff(backoff)
			continue
		}
		backoff = 100 * time.Millisecond
		reconnect := false
		for !reconnect {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-streamErrors:
				if !ok || err == nil {
					err = errors.New("stream connection closed")
				}
				runtime.markDisconnected(sourceIndex, err)
				reconnect = true
			case event, ok := <-events:
				if !ok {
					runtime.markDisconnected(sourceIndex, errors.New("stream event channel closed"))
					reconnect = true
					continue
				}
				runtime.mu.Lock()
				err := runtime.sources[sourceIndex].apply(event, time.Now().UTC())
				runtime.mu.Unlock()
				if err != nil {
					runtime.mu.Lock()
					runtime.sources[sourceIndex].reconnects++
					runtime.mu.Unlock()
					reconnect = true
				}
			}
		}
		if !waitStreamRetry(ctx, backoff) {
			return
		}
		backoff = nextStreamBackoff(backoff)
	}
}

func (runtime *RedundantStream[T]) markDisconnected(sourceIndex int, err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.sources[sourceIndex].reconnects++
	runtime.sources[sourceIndex].invalidate(err)
}

func (runtime *RedundantStream[T]) Snapshot(maxAge time.Duration) (T, FreshnessProof, error) {
	return runtime.snapshotAt(time.Now().UTC(), maxAge)
}

func (runtime *RedundantStream[T]) snapshotAt(now time.Time, maxAge time.Duration) (T, FreshnessProof, error) {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	bestIndex := -1
	for sourceIndex := range runtime.sources {
		source := runtime.sources[sourceIndex]
		if !source.ready || !source.reconciled || source.sourceTime.IsZero() {
			continue
		}
		age, validAge := boundedStreamAge(now, source.sourceTime)
		if !validAge || maxAge <= 0 || age > maxAge {
			continue
		}
		if bestIndex < 0 || source.sourceTime.After(runtime.sources[bestIndex].sourceTime) {
			bestIndex = sourceIndex
		}
	}
	if bestIndex < 0 {
		var zero T
		return zero, FreshnessProof{}, ErrFreshStreamUnavailable
	}
	best := runtime.sources[bestIndex]
	return best.value, FreshnessProof{
		Exchange:   best.exchange,
		Transport:  best.transport,
		SourceTime: best.sourceTime,
		ReceivedAt: best.receivedAt,
		Age:        clampedStreamAge(now, best.sourceTime),
		Sequence:   best.sequence,
		SequenceOK: true,
		Reconciled: true,
	}, nil
}

func (runtime *RedundantStream[T]) Health() [2]StreamSourceHealth {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	var health [2]StreamSourceHealth
	for index, source := range runtime.sources {
		health[index] = StreamSourceHealth{
			Exchange: source.exchange, Transport: source.transport,
			Ready: source.ready, Reconciled: source.reconciled, Sequence: source.sequence,
			SourceTime: source.sourceTime, ReceivedAt: source.receivedAt,
			Reconnects: source.reconnects, SequenceGaps: source.sequenceGaps,
		}
		if !source.sourceTime.IsZero() {
			health[index].FreshnessAge = clampedStreamAge(time.Now().UTC(), source.sourceTime)
		}
		if source.lastError != nil {
			health[index].LastError = source.lastError.Error()
		}
	}
	return health
}

func boundedStreamAge(now, sourceTime time.Time) (time.Duration, bool) {
	age := now.Sub(sourceTime)
	if age < -streamMaxFutureSkew {
		return 0, false
	}
	if age < 0 {
		return 0, true
	}
	return age, true
}

func clampedStreamAge(now, sourceTime time.Time) time.Duration {
	age, valid := boundedStreamAge(now, sourceTime)
	if !valid {
		return -streamMaxFutureSkew
	}
	return age
}

func waitStreamRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextStreamBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > 5*time.Second {
		return 5 * time.Second
	}
	return next
}
