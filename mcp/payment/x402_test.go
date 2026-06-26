package payment

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nofx/mcp"
)

func TestDoX402RequestStreamRetriesInitialServerError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			http.Error(w, "temporary upstream failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ok\n\n"))
	}))
	defer server.Close()

	resp, err := DoX402RequestStream(
		context.Background(),
		server.Client(),
		func() (*http.Request, error) {
			return http.NewRequest(http.MethodPost, server.URL, nil)
		},
		func(string) (string, error) { return "unused", nil },
		"test-claw402",
		mcp.NewNoopLogger(),
	)
	if err != nil {
		t.Fatalf("DoX402RequestStream returned error: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if got := string(body); got != "data: ok\n\n" {
		t.Fatalf("body = %q, want SSE body", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}
