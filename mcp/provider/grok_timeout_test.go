package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"nofx/mcp"
)

type grokTimeoutTransport func(*http.Request) (*http.Response, error)

func (f grokTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGrokTimeoutOverridePreservesHealthyReasoningAndCycleDeadline(t *testing.T) {
	t.Setenv("AI_GROK_REQUEST_TIMEOUT_SECONDS", "480")
	t.Setenv("AI_STREAM_IDLE_TIMEOUT_SECONDS", "300")
	for _, path := range []string{"text", "stream", "full"} {
		for _, outcome := range []string{"healthy", "attempt-expired", "parent-expired"} {
			t.Run(path+"/"+outcome, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					client := NewGrokClientWithOptions(mcp.WithMaxRetries(3)).(*GrokClient)
					client.APIKey = "test-key"
					client.BaseURL = "https://example.invalid/v1"
					client.Stream = path == "stream"
					attempts := 0
					client.HTTPClient.Transport = grokTimeoutTransport(func(r *http.Request) (*http.Response, error) {
						attempts++
						delay := 394 * time.Second
						if outcome == "attempt-expired" {
							delay = 500 * time.Second
						}
						select {
						case <-time.After(delay):
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
						body := `{"choices":[{"message":{"content":"wait"}}]}`
						header := make(http.Header)
						if path == "stream" {
							header.Set("Content-Type", "text/event-stream")
							body = "data: {\"choices\":[{\"delta\":{\"content\":\"wait\"}}]}\n\ndata: [DONE]\n\n"
						}
						return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
					})
					budget := 10 * time.Minute
					if outcome == "parent-expired" {
						budget = 350 * time.Second
					}
					ctx, cancel := context.WithTimeout(context.Background(), budget)
					defer cancel()
					req := mcp.NewRequestBuilder().WithModel(client.Model).WithUserPrompt("test").MustBuild()
					req.Ctx = ctx
					started := time.Now()
					var err error
					if path == "full" {
						_, err = client.CallWithRequestFull(req)
					} else {
						_, err = client.CallWithRequest(req)
					}
					if attempts != 1 {
						t.Fatalf("unexpected replay: attempts=%d error=%v", attempts, err)
					}
					if outcome == "healthy" {
						if err != nil {
							t.Fatalf("healthy 394s request canceled early: %v", err)
						}
					} else {
						if err == nil {
							t.Fatal("expired attempt accepted")
						}
						limit := 480 * time.Second
						if outcome == "parent-expired" {
							limit = budget - client.Cfg.CompletionReserve
						}
						if time.Since(started) != limit {
							t.Fatalf("duration=%v expected=%v", time.Since(started), limit)
						}
					}
				})
			})
		}
	}
}

func TestGrokTimeoutOverrideDoesNotChangeOtherProvidersOrExplicitOptions(t *testing.T) {
	t.Setenv("AI_GROK_REQUEST_TIMEOUT_SECONDS", "480")
	c := NewGrokClientWithOptions().(*GrokClient)
	if c.Cfg.Timeout != 480*time.Second || c.HTTPClient.Timeout != 480*time.Second || c.Cfg.StreamIdleTimeout != 480*time.Second {
		t.Fatal("Grok timeout override missing")
	}
	plain := mcp.NewClient(mcp.WithProvider(mcp.ProviderOpenAI)).(*mcp.Client)
	if plain.Cfg.Timeout != 300*time.Second {
		t.Fatal("unrelated provider timeout changed")
	}
	explicit := NewGrokClientWithOptions(mcp.WithTimeout(90 * time.Second)).(*GrokClient)
	if explicit.Cfg.Timeout != 90*time.Second || explicit.HTTPClient.Timeout != 90*time.Second {
		t.Fatal("explicit option lost precedence")
	}
}
