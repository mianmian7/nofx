package market

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamStateRequiresSnapshotAndContinuousSequence(t *testing.T) {
	now := time.Now().UTC()
	state := streamSourceState[int]{}

	if err := state.apply(StreamEvent[int]{Value: 1, Sequence: 10, PreviousSequence: 9, SourceTime: now}, now); !errors.Is(err, ErrStreamNeedsSnapshot) {
		t.Fatalf("first incremental error = %v, want ErrStreamNeedsSnapshot", err)
	}
	if err := state.apply(StreamEvent[int]{Value: 10, Snapshot: true, Sequence: 10, PreviousSequence: -1, SourceTime: now}, now); err != nil {
		t.Fatalf("snapshot apply: %v", err)
	}
	if err := state.apply(StreamEvent[int]{Value: 11, Sequence: 11, PreviousSequence: 10, SourceTime: now.Add(time.Millisecond)}, now.Add(time.Millisecond)); err != nil {
		t.Fatalf("continuous update apply: %v", err)
	}
	if state.value != 11 || state.sequence != 11 || !state.ready || !state.reconciled {
		t.Fatalf("continuous state = %#v", state)
	}

	if err := state.apply(StreamEvent[int]{Value: 13, Sequence: 13, PreviousSequence: 12, SourceTime: now.Add(2 * time.Millisecond)}, now.Add(2*time.Millisecond)); !errors.Is(err, ErrStreamSequenceGap) {
		t.Fatalf("gap error = %v, want ErrStreamSequenceGap", err)
	}
	if state.ready || state.reconciled {
		t.Fatalf("gap left source publishable: %#v", state)
	}
}

func TestRedundantStreamReconnectsAndRequiresNewSnapshotAfterGap(t *testing.T) {
	var connections atomic.Int32
	connector := func(ctx context.Context) (<-chan StreamEvent[int], <-chan error, error) {
		connection := connections.Add(1)
		events := make(chan StreamEvent[int], 2)
		errs := make(chan error, 1)
		now := time.Now().UTC()
		if connection == 1 {
			events <- StreamEvent[int]{Value: 10, Snapshot: true, Sequence: 10, PreviousSequence: -1, SourceTime: now, Exchange: "test", Transport: "ws"}
			events <- StreamEvent[int]{Value: 12, Sequence: 12, PreviousSequence: 11, SourceTime: now.Add(time.Millisecond), Exchange: "test", Transport: "ws"}
		} else {
			events <- StreamEvent[int]{Value: 20, Snapshot: true, Sequence: 20, PreviousSequence: -1, SourceTime: now, Exchange: "test", Transport: "ws"}
		}
		return events, errs, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := newRedundantStream(connector, nil)
	runtime.Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, proof, err := runtime.Snapshot(5 * time.Second)
		if err == nil && value == 20 && proof.Sequence == 20 {
			health := runtime.Health()
			if health[0].SequenceGaps == 0 || health[0].Reconnects == 0 {
				t.Fatalf("health did not record gap/reconnect: %#v", health[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stream did not reconcile after gap; connections=%d health=%#v", connections.Load(), runtime.Health()[0])
}

func TestRedundantStreamReturnsOnlyFreshReconciledSource(t *testing.T) {
	now := time.Now().UTC()
	runtime := newRedundantStream[int](nil, nil)
	runtime.sources[0] = streamSourceState[int]{value: 10, ready: true, reconciled: true, sourceTime: now.Add(-10 * time.Second), receivedAt: now.Add(-10 * time.Second)}
	runtime.sources[1] = streamSourceState[int]{value: 20, ready: true, reconciled: true, sourceTime: now.Add(-time.Second), receivedAt: now.Add(-time.Second)}

	value, proof, err := runtime.snapshotAt(now, 2*time.Second)
	if err != nil {
		t.Fatalf("snapshotAt: %v", err)
	}
	if value != 20 || proof.Age != time.Second || !proof.SequenceOK || !proof.Reconciled {
		t.Fatalf("snapshot = %d/%#v, want fresh standby source", value, proof)
	}

	runtime.sources[1].reconciled = false
	if _, _, err := runtime.snapshotAt(now, 2*time.Second); !errors.Is(err, ErrFreshStreamUnavailable) {
		t.Fatalf("unavailable error = %v, want ErrFreshStreamUnavailable", err)
	}
}

func TestRedundantStreamAllowsBoundedExchangeClockSkew(t *testing.T) {
	now := time.Now().UTC()
	runtime := newRedundantStream[int](nil, nil)
	runtime.sources[0] = streamSourceState[int]{
		value: 10, ready: true, reconciled: true,
		sourceTime: now.Add(100 * time.Millisecond), receivedAt: now,
	}
	value, proof, err := runtime.snapshotAt(now, 2*time.Second)
	if err != nil || value != 10 || proof.Age != 0 {
		t.Fatalf("bounded clock skew snapshot = %d/%#v, error=%v", value, proof, err)
	}
}
