package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitTestSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitTestError(t *testing.T, result <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func TestRetryAfterParsesSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	for name, value := range map[string]string{
		"seconds": "120",
		"date":    now.Add(90 * time.Second).Format(http.TimeFormat),
	} {
		t.Run(name, func(t *testing.T) {
			err := NewAPIErrorWithHeaders(http.StatusTooManyRequests, `{"error":"limited"}`, http.Header{"Retry-After": []string{value}}, now)
			if err.RetryAfter < 89*time.Second || err.RetryAfter > 120*time.Second {
				t.Fatalf("RetryAfter = %s", err.RetryAfter)
			}
		})
	}
}

func TestRequestSchedulerSharesCooldownAndKeepsIdentitiesIndependent(t *testing.T) {
	scheduler := newModelRequestScheduler(1, 0)
	scheduler.extendCooldown("shared", time.Now().Add(40*time.Millisecond), ErrorKindRateLimited)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := scheduler.acquire(ctx, "shared"); ErrorKindOf(err) != ErrorKindRateLimited {
		t.Fatalf("shared acquire error = %v", err)
	}

	release, err := scheduler.acquire(context.Background(), "independent")
	if err != nil {
		t.Fatalf("independent identity blocked: %v", err)
	}
	release()

	time.Sleep(45 * time.Millisecond)
	release, err = scheduler.acquire(context.Background(), "shared")
	if err != nil {
		t.Fatalf("expired cooldown still blocked: %v", err)
	}
	release()
}

func TestExpiredAdmissionCannotAcquireIdleSlotOrLeakCapacity(t *testing.T) {
	scheduler := newModelRequestScheduler(1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := scheduler.acquire(ctx, "shared"); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("expired admission release=%v error=%v", release != nil, err)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
	defer probeCancel()
	release, err := scheduler.acquire(probeCtx, "shared")
	if err != nil {
		t.Fatalf("slot leaked after expired admission: %v", err)
	}
	release()
}

func TestQueuedAdmissionRechecksCooldownBeforeUsingReleasedSlot(t *testing.T) {
	scheduler := newModelRequestScheduler(1, 0)
	firstRelease, err := scheduler.acquire(context.Background(), "shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(firstRelease)

	queued := make(chan struct{})
	var hookOnce sync.Once
	scheduler.beforeSlotWait = func() { hookOnce.Do(func() { close(queued) }) }
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	go func() {
		release, err := scheduler.acquire(ctx, "shared")
		if release != nil {
			release()
		}
		result <- err
	}()
	waitTestSignal(t, queued, "queued scheduler admission")
	scheduler.extendCooldown("shared", time.Now().Add(time.Second), ErrorKindRateLimited)
	firstRelease()

	if err := waitTestError(t, result, "queued scheduler result"); ErrorKindOf(err) != ErrorKindRateLimited {
		t.Fatalf("queued admission error = %v", err)
	}
}

func TestClientsPublishCooldownBeforeReleasingAdmission(t *testing.T) {
	for _, full := range []bool{false, true} {
		name := "ordinary"
		if full {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			oldScheduler := defaultModelRequestScheduler
			scheduler := newModelRequestScheduler(1, 0)
			defaultModelRequestScheduler = scheduler
			t.Cleanup(func() { defaultModelRequestScheduler = oldScheduler })

			firstEntered := make(chan struct{})
			returnRateLimit := make(chan struct{})
			firstHTTP := NewMockHTTPClient()
			var enterOnce sync.Once
			firstHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
				enterOnce.Do(func() { close(firstEntered) })
				<-returnRateLimit
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"upstream_rate_limited"}}`)),
					Header:     http.Header{"Retry-After": []string{"120"}},
				}, nil
			}
			secondHTTP := NewMockHTTPClient()
			secondHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"must not run"}}]}`)), Header: make(http.Header)}, nil
			}
			newClient := func(httpClient *MockHTTPClient) *Client {
				client := NewClient(
					WithHTTPClient(httpClient.ToHTTPClient()), WithLogger(NewMockLogger()),
					WithProvider("custom"), WithBaseURL("https://example.invalid/v1"),
					WithModel("shared-model"), WithAPIKey("shared-key"), WithStream(false), WithMaxRetries(1),
				).(*Client)
				client.Cfg.AdmissionTimeout = 40 * time.Millisecond
				client.Cfg.CompletionReserve = 5 * time.Millisecond
				client.Cfg.MinAttemptBudget = 5 * time.Millisecond
				return client
			}
			firstClient := newClient(firstHTTP)
			secondClient := newClient(secondHTTP)
			call := func(client *Client, traderID string) error {
				req := NewRequestBuilder().WithModel("shared-model").WithUserPrompt("hello").MustBuild()
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()
				req.Ctx = ctx
				req.Metadata = CallMetadata{TraderID: traderID, CallID: traderID + "-call"}
				if full {
					_, err := client.CallWithRequestFull(req)
					return err
				}
				_, err := client.CallWithRequest(req)
				return err
			}

			firstResult := make(chan error, 1)
			secondResult := make(chan error, 1)
			releaseBlocked := make(chan struct{})
			allowReleaseReturn := make(chan struct{})
			var firstDone, secondDone atomic.Bool
			var returnOnce, allowOnce sync.Once
			closeReturnRateLimit := func() { returnOnce.Do(func() { close(returnRateLimit) }) }
			closeAllowRelease := func() { allowOnce.Do(func() { close(allowReleaseReturn) }) }
			t.Cleanup(func() {
				closeReturnRateLimit()
				closeAllowRelease()
				if !firstDone.Load() {
					select {
					case <-firstResult:
					case <-time.After(time.Second):
					}
				}
				if !secondDone.Load() {
					select {
					case <-secondResult:
					case <-time.After(time.Second):
					}
				}
			})
			go func() { firstResult <- call(firstClient, "trader-a") }()
			waitTestSignal(t, firstEntered, "first client HTTP request")

			queued := make(chan struct{})
			var queuedOnce sync.Once
			scheduler.beforeSlotWait = func() { queuedOnce.Do(func() { close(queued) }) }
			go func() { secondResult <- call(secondClient, "trader-b") }()
			waitTestSignal(t, queued, "second client admission queue")

			var releaseOnce sync.Once
			scheduler.afterSlotRelease = func() {
				blocked := false
				releaseOnce.Do(func() {
					blocked = true
					close(releaseBlocked)
				})
				if blocked {
					<-allowReleaseReturn
				}
			}
			closeReturnRateLimit()
			waitTestSignal(t, releaseBlocked, "first admission release")

			secondErr := waitTestError(t, secondResult, "queued client result")
			secondDone.Store(true)
			if ErrorKindOf(secondErr) != ErrorKindRateLimited {
				t.Fatalf("queued client error = %v", secondErr)
			}
			if got := len(secondHTTP.GetRequests()); got != 0 {
				t.Fatalf("queued client sent %d HTTP requests", got)
			}
			closeAllowRelease()
			firstErr := waitTestError(t, firstResult, "rate-limited client result")
			firstDone.Store(true)
			if ErrorKindOf(firstErr) != ErrorKindRateLimited {
				t.Fatalf("rate-limited client error = %v", firstErr)
			}
		})
	}
}

func TestAdmissionTimeoutPreservesSharedCooldownClassification(t *testing.T) {
	oldScheduler := defaultModelRequestScheduler
	defaultModelRequestScheduler = newModelRequestScheduler(1, 0)
	t.Cleanup(func() { defaultModelRequestScheduler = oldScheduler })
	key := modelRequestScheduleKey("custom", "https://example.invalid/v1", "limited-model", "shared-key")
	defaultModelRequestScheduler.extendCooldown(key, time.Now().Add(time.Second), ErrorKindRateLimited)

	httpClient := NewMockHTTPClient()
	client := NewClient(
		WithHTTPClient(httpClient.ToHTTPClient()), WithLogger(NewMockLogger()),
		WithProvider("custom"), WithBaseURL("https://example.invalid/v1"),
		WithModel("limited-model"), WithAPIKey("shared-key"), WithStream(false),
	).(*Client)
	client.Cfg.AdmissionTimeout = 20 * time.Millisecond
	client.Cfg.CompletionReserve = 5 * time.Millisecond
	client.Cfg.MinAttemptBudget = 5 * time.Millisecond
	req := NewRequestBuilder().WithModel("limited-model").WithUserPrompt("hello").MustBuild()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req.Ctx = ctx
	req.Metadata = CallMetadata{TraderID: "trader-a", CallID: "call-a"}

	_, err := client.CallWithRequest(req)
	if !errors.Is(err, ErrRequestAdmissionExpired) || ErrorKindOf(err) != ErrorKindRateLimited {
		t.Fatalf("admission cooldown error = %v", err)
	}
	if got := len(httpClient.GetRequests()); got != 0 {
		t.Fatalf("cooldown admission sent %d HTTP requests", got)
	}
}

func TestCallWithRequestLongRetryAfterStopsWithinCycleBudgetAndSharesCooldown(t *testing.T) {
	oldScheduler := defaultModelRequestScheduler
	defaultModelRequestScheduler = newModelRequestScheduler(1, 0)
	t.Cleanup(func() { defaultModelRequestScheduler = oldScheduler })

	limitedHTTP := NewMockHTTPClient()
	limitedHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"upstream_rate_limited"}}`)),
			Header:     http.Header{"Retry-After": []string{"120"}},
		}, nil
	}
	client := NewClient(
		WithHTTPClient(limitedHTTP.ToHTTPClient()), WithLogger(NewMockLogger()),
		WithProvider("custom"), WithBaseURL("https://example.invalid/v1"),
		WithModel("limited-model"), WithAPIKey("shared-key"), WithStream(false), WithMaxRetries(3),
	).(*Client)
	client.Cfg.CompletionReserve = 5 * time.Millisecond
	client.Cfg.MinAttemptBudget = 5 * time.Millisecond
	client.Cfg.AdmissionTimeout = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := NewRequestBuilder().WithModel("limited-model").WithUserPrompt("hello").MustBuild()
	req.Ctx = ctx
	req.Metadata = CallMetadata{TraderID: "trader-a", CallID: "call-a"}
	if _, err := client.CallWithRequest(req); err == nil {
		t.Fatal("long Retry-After should fail the current cycle")
	}
	if got := len(limitedHTTP.GetRequests()); got != 1 {
		t.Fatalf("HTTP attempts = %d, want 1", got)
	}

	blockedHTTP := NewMockHTTPClient()
	blockedHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
	}
	blocked := NewClient(
		WithHTTPClient(blockedHTTP.ToHTTPClient()), WithLogger(NewMockLogger()),
		WithProvider("custom"), WithBaseURL("https://example.invalid/v1"),
		WithModel("limited-model"), WithAPIKey("shared-key"), WithStream(false),
	).(*Client)
	blocked.Cfg.CompletionReserve = 5 * time.Millisecond
	blocked.Cfg.MinAttemptBudget = 5 * time.Millisecond
	blockedReq := NewRequestBuilder().WithModel("limited-model").WithUserPrompt("hello").MustBuild()
	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer blockedCancel()
	blockedReq.Ctx = blockedCtx
	blockedReq.Metadata = CallMetadata{TraderID: "trader-b", CallID: "call-b"}
	if _, err := blocked.CallWithRequest(blockedReq); ErrorKindOf(err) != ErrorKindRateLimited {
		t.Fatalf("shared cooldown error = %v", err)
	}
	if got := len(blockedHTTP.GetRequests()); got != 0 {
		t.Fatalf("shared cooldown sent %d requests", got)
	}

	independent := NewClient(
		WithHTTPClient(blockedHTTP.ToHTTPClient()), WithLogger(NewMockLogger()),
		WithProvider("custom"), WithBaseURL("https://example.invalid/v1"),
		WithModel("limited-model"), WithAPIKey("independent-key"), WithStream(false),
	).(*Client)
	independent.Cfg.CompletionReserve = 5 * time.Millisecond
	independent.Cfg.MinAttemptBudget = 5 * time.Millisecond
	independentReq := NewRequestBuilder().WithModel("limited-model").WithUserPrompt("hello").MustBuild()
	independentReq.Ctx = context.Background()
	independentReq.Metadata = CallMetadata{TraderID: "trader-c", CallID: "call-c"}
	if _, err := independent.CallWithRequest(independentReq); err != nil {
		t.Fatalf("independent identity was blocked: %v", err)
	}
}

func TestUsageLimitIsNotImmediatelyRetried(t *testing.T) {
	cooldownErr := &ModelCooldownError{Kind: ErrorKindRateLimited, Remaining: time.Minute}
	if !IsFailoverEligible(cooldownErr) {
		t.Fatal("shared rate-limit cooldown should allow an independent fallback candidate")
	}

	err := NewAPIError(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`)
	if ErrorKindOf(err) != ErrorKindUsageLimit {
		t.Fatalf("kind = %s", ErrorKindOf(err))
	}
	if NewClient().(*Client).IsRetryableError(err) {
		t.Fatal("usage exhaustion must not be immediately retried against the same identity")
	}
}

func TestUsageLimitCooldownHonorsLongerRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		configured, retryAfter, want time.Duration
	}{
		{"provider longer", time.Minute, time.Hour, time.Hour},
		{"configuration longer", 20 * time.Minute, time.Minute, 20 * time.Minute},
		{"default with longer provider hint", 0, time.Hour, time.Hour},
		{"default longer", 0, time.Minute, 15 * time.Minute},
		{"no provider hint", 20 * time.Minute, 0, 20 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(WithLogger(NewMockLogger())).(*Client)
			client.Cfg.UsageLimitCooldown = tc.configured
			err := &APIError{StatusCode: http.StatusTooManyRequests, Kind: ErrorKindUsageLimit, RetryAfter: tc.retryAfter}
			if got := client.cooldownForError(1, err); got != tc.want {
				t.Fatalf("usage cooldown=%s, want %s", got, tc.want)
			}
		})
	}
}
