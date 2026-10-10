package market

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

// A cooldown extension arrives while waiters are already asleep. None may use
// their previous wakeup as permission to dispatch; the release must be paced.
func TestOKXCandleQueuedWaitersRecheckExtendedCooldown(t *testing.T) {
	gate := &okxCandleGate{}
	gate.coolDown(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan time.Time, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			if e := gate.admit(ctx); e != nil {
				errs <- e
				return
			}
			result <- time.Now()
		}()
	}
	time.Sleep(40 * time.Millisecond)
	extended := time.Now()
	gate.coolDown(180 * time.Millisecond)
	var times []time.Time
	for len(times) < 4 {
		select {
		case at := <-result:
			times = append(times, at)
		case e := <-errs:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal("queued admission timed out")
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	if times[0].Sub(extended) < 170*time.Millisecond {
		t.Fatal("queued waiter ignored extended cooldown")
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < 65*time.Millisecond {
			t.Fatal("cooldown release caused a request burst")
		}
	}
}
func TestOKXCandleCanceledQueueDoesNotReserveCapacity(t *testing.T) {
	gate := &okxCandleGate{}
	gate.coolDown(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := gate.admit(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if !gate.next.IsZero() {
		t.Fatal("canceled queue advanced dispatch age")
	}
}
