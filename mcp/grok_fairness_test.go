package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func grokFairnessClient(id string, handler func(*http.Request) (*http.Response, error)) (*Client, *Request) {
	transport := NewMockHTTPClient()
	transport.ResponseFunc = handler
	c := NewClient(WithHTTPClient(transport.ToHTTPClient()), WithLogger(NewMockLogger()), WithProvider(ProviderGrok), WithBaseURL("https://example.invalid/v1"), WithModel("reasoning-model"), WithAPIKey("test-key"), WithStream(false), WithMaxRetries(3)).(*Client)
	req := NewRequestBuilder().WithModel("reasoning-model").WithUserPrompt("test").MustBuild()
	req.Metadata = CallMetadata{TraderID: id, CallID: id}
	return c, req
}
func grokFairnessResponse() *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"wait"}}]}`))}
}

func TestGrokConcurrencyOverrideIsScopedAndBounded(t *testing.T) {
	t.Setenv("AI_GROK_MODEL_REQUEST_MAX_CONCURRENCY", "2")
	synctest.Test(t, func(t *testing.T) {
		old := defaultModelRequestScheduler
		defaultModelRequestScheduler = newModelRequestScheduler(1, 0)
		defer func() { defaultModelRequestScheduler = old }()
		for _, provider := range []string{ProviderGrok, ProviderOpenAI} {
			c, _ := grokFairnessClient("probe", nil)
			c.Provider = provider
			metadata := CallMetadata{TraderID: "probe"}
			release1, err := c.acquireModelRequest(metadata, c.Model, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var release2 func()
			if provider == ProviderGrok {
				secondCtx, stop := context.WithTimeout(context.Background(), time.Second)
				release2, err = c.acquireModelRequest(metadata, c.Model, secondCtx)
				stop()
				if err != nil {
					release1()
					t.Fatalf("Grok-only override did not admit second request: %v", err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			release, err := c.acquireModelRequest(metadata, c.Model, ctx)
			cancel()
			if err == nil {
				release()
				t.Fatal("configured provider concurrency exceeded")
			}
			release1()
			if release2 != nil {
				release2()
			}
			release, err = c.acquireModelRequest(metadata, c.Model, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			release()
		}
	})
}

func TestGrokHealthySlowAttemptNotSplitAcrossRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		c, req := grokFairnessClient("", func(r *http.Request) (*http.Response, error) {
			attempts++
			select {
			case <-time.After(220 * time.Second):
				return grokFairnessResponse(), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		req.Ctx = ctx
		_, err := c.CallWithRequest(req)
		if err != nil || attempts != 1 {
			t.Fatalf("healthy request within configured 300s cap must complete once, attempts=%d error=%v", attempts, err)
		}
	})
}

func TestGrokAttemptPolicyAcrossRequestPaths(t *testing.T) {
	for _, path := range []string{"text", "stream", "full"} {
		for _, outcome := range []string{"healthy", "deadline", "transient", "parent-budget"} {
			t.Run(path+"/"+outcome, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					attempts := 0
					c, req := grokFairnessClient("", func(r *http.Request) (*http.Response, error) {
						attempts++
						if outcome == "transient" && attempts == 1 {
							return nil, io.ErrUnexpectedEOF
						}
						duration := 220 * time.Second
						if outcome == "deadline" || outcome == "parent-budget" {
							duration = 350 * time.Second
						}
						select {
						case <-time.After(duration):
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
						response := grokFairnessResponse()
						if path == "stream" {
							response.Header.Set("Content-Type", "text/event-stream")
							response.Body = io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"wait\"}}]}\n\ndata: [DONE]\n\n"))
						}
						return response, nil
					})
					c.Cfg.StreamIdleTimeout = 5 * time.Minute
					c.Stream = path == "stream"
					budget := 10 * time.Minute
					if outcome == "parent-budget" {
						budget = 250 * time.Second
					}
					ctx, cancel := context.WithTimeout(context.Background(), budget)
					defer cancel()
					req.Ctx = ctx
					start := time.Now()
					var err error
					if path == "full" {
						_, err = c.CallWithRequestFull(req)
					} else {
						_, err = c.CallWithRequest(req)
					}
					wantAttempts := 1
					if outcome == "transient" {
						wantAttempts = 2
					}
					if attempts != wantAttempts {
						t.Fatalf("attempts=%d want=%d err=%v", attempts, wantAttempts, err)
					}
					if outcome == "healthy" || outcome == "transient" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						if err == nil {
							t.Fatal("expired request succeeded")
						}
						limit := 300 * time.Second
						if outcome == "parent-budget" {
							limit = budget - c.completionReserve()
						}
						if time.Since(start) != limit {
							t.Fatalf("duration=%v expected existing cap %v", time.Since(start), limit)
						}
					}
				})
			})
		}
	}
}

func TestGrokCanceledWaiterDoesNotConsumeServiceOrCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scheduler := newModelRequestScheduler(1, 0)
		release, err := scheduler.acquireForTrader(context.Background(), "group", "busy")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := scheduler.acquireForTrader(ctx, "group", "canceled"); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; err != context.Canceled {
			t.Fatalf("cancel: %v", err)
		}
		release()
		release()
		group := scheduler.group("group")
		if len(group.slots) != 0 || len(group.waiting) != 0 || group.lastService["canceled"] != 0 {
			t.Fatal("canceled waiter leaked capacity or service age")
		}
		release, err = scheduler.acquireForTrader(context.Background(), "group", "next")
		if err != nil {
			t.Fatal(err)
		}
		release()
	})
}

func TestGrokUnservedTraderGetsSlotBeforeRecentlyServedTrader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := defaultModelRequestScheduler
		defaultModelRequestScheduler = newModelRequestScheduler(1, 0)
		defer func() { defaultModelRequestScheduler = old }()
		var order []string
		call := func(id string) error {
			c, r := grokFairnessClient(id, func(*http.Request) (*http.Response, error) {
				order = append(order, id)
				return grokFairnessResponse(), nil
			})
			_, err := c.CallWithRequest(r)
			return err
		}
		if err := call("served"); err != nil {
			t.Fatal(err)
		}
		key := modelRequestScheduleKey(ProviderGrok, "https://example.invalid/v1", "reasoning-model", "test-key")
		release, err := defaultModelRequestScheduler.acquire(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 2)
		go func() { done <- call("served") }()
		synctest.Wait()
		go func() { done <- call("unserved") }()
		synctest.Wait()
		release()
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if len(order) != 3 || order[1] != "unserved" {
			t.Fatalf("unserved trader starved by recently served caller: %v", order)
		}
	})
}
