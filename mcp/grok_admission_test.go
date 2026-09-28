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

func TestGrokQueuedRequestCanWaitForHealthyReasoningPeer(t *testing.T) {
	t.Setenv("AI_REQUEST_ADMISSION_TIMEOUT_SECONDS", "")
	synctest.Test(t, func(t *testing.T) {
		old := defaultModelRequestScheduler
		defaultModelRequestScheduler = newModelRequestScheduler(1, 0)
		defer func() { defaultModelRequestScheduler = old }()
		started := make(chan struct{})
		call := func(id string, slow bool) error {
			httpClient := NewMockHTTPClient()
			httpClient.ResponseFunc = func(r *http.Request) (*http.Response, error) {
				if slow {
					close(started)
					select {
					case <-time.After(70 * time.Second):
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"wait"}}]}`))}, nil
			}
			c := NewClient(WithHTTPClient(httpClient.ToHTTPClient()), WithLogger(NewMockLogger()), WithProvider("grok"), WithBaseURL("https://example.invalid/v1"), WithModel("reasoning-model"), WithAPIKey("test-key"), WithStream(false), WithMaxRetries(1)).(*Client)
			req := NewRequestBuilder().WithModel("reasoning-model").WithUserPrompt("hello").MustBuild()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			req.Ctx = ctx
			req.Metadata = CallMetadata{TraderID: id, CallID: id}
			_, err := c.CallWithRequest(req)
			return err
		}
		done := make(chan error, 1)
		go func() { done <- call("first", true) }()
		<-started
		queuedErr := call("second", false)
		firstErr := <-done
		if firstErr != nil {
			t.Fatalf("healthy first request failed: %v", firstErr)
		}
		if queuedErr != nil {
			t.Fatalf("queued Grok request rejected despite ample lifecycle budget: %v", queuedErr)
		}
	})
}
