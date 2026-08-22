package mcp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestSchedulerLimitsConcurrencyAndSpacesStarts(t *testing.T) {
	scheduler := newModelRequestScheduler(2, 4*time.Millisecond)
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var startsMu sync.Mutex
	starts := make([]time.Time, 0, 6)

	var callers sync.WaitGroup
	for range 6 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			release, err := scheduler.acquire(context.Background(), "shared")
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
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
			time.Sleep(10 * time.Millisecond)
			inFlight.Add(-1)
			release()
		}()
	}
	callers.Wait()

	if got := maxInFlight.Load(); got != 2 {
		t.Fatalf("max in-flight = %d, want 2", got)
	}
	startsMu.Lock()
	defer startsMu.Unlock()
	if len(starts) != 6 {
		t.Fatalf("start count = %d, want 6", len(starts))
	}
	for index := 1; index < len(starts); index++ {
		if gap := starts[index].Sub(starts[index-1]); gap < 3*time.Millisecond {
			t.Fatalf("start gap = %s, want at least 3ms", gap)
		}
	}
}

func TestRequestSchedulerKeepsGroupsIndependent(t *testing.T) {
	scheduler := newModelRequestScheduler(1, time.Hour)
	releaseA, err := scheduler.acquire(context.Background(), "group-a")
	if err != nil {
		t.Fatalf("acquire group A: %v", err)
	}
	defer releaseA()

	startedB := make(chan struct{})
	go func() {
		releaseB, acquireErr := scheduler.acquire(context.Background(), "group-b")
		if acquireErr != nil {
			return
		}
		close(startedB)
		releaseB()
	}()

	select {
	case <-startedB:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("group B was blocked by group A")
	}
}

func TestRequestSchedulerHonorsCancellationWhileQueued(t *testing.T) {
	scheduler := newModelRequestScheduler(1, 0)
	release, err := scheduler.acquire(context.Background(), "shared")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := scheduler.acquire(ctx, "shared"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued acquire error = %v, want context deadline", err)
	}
}

func TestRequestSchedulerIdentityNormalizesURLAndProtectsCredentials(t *testing.T) {
	first := modelRequestScheduleKey(" DeepSeek ", "HTTPS://Example.COM:443/v1/", " DeepSeek-V4-Flash ", "secret-a")
	second := modelRequestScheduleKey("deepseek", "https://example.com/v1", "deepseek-v4-flash", "secret-a")
	if first != second {
		t.Fatalf("equivalent identities differ: %q != %q", first, second)
	}
	if first == modelRequestScheduleKey("deepseek", "https://example.com/v1", "deepseek-v4-flash", "secret-b") {
		t.Fatal("different credentials unexpectedly share a group")
	}
	if first == modelRequestScheduleKey("deepseek", "https://example.com/v1", "another-model", "secret-a") {
		t.Fatal("different models unexpectedly share a group")
	}
	if first == "" || first == "secret-a" {
		t.Fatalf("unsafe or empty identity: %q", first)
	}
}
