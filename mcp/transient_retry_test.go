package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClientRetriesTransientHTTPStatuses(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 522} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mockHTTP := NewMockHTTPClient()
			attempts := 0
			mockHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return &http.Response{
						StatusCode: status,
						Body:       io.NopCloser(strings.NewReader("temporary upstream failure")),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
					Header:     make(http.Header),
				}, nil
			}

			client := NewClient(
				WithHTTPClient(mockHTTP.ToHTTPClient()),
				WithLogger(NewMockLogger()),
				WithAPIKey("test-key"),
				WithMaxRetries(2),
				WithRetryWaitBase(time.Nanosecond),
			)
			result, err := client.CallWithMessages("system", "user")
			if err != nil {
				t.Fatalf("status %d should retry and succeed: %v", status, err)
			}
			if result != "ok" || attempts != 2 {
				t.Fatalf("status %d result=%q attempts=%d, want ok after one retry", status, result, attempts)
			}
		})
	}
}

func TestClientRetryDiagnosticsClassifyFailureAndLogFinalSuccess(t *testing.T) {
	mockHTTP := NewMockHTTPClient()
	attempts := 0
	mockHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary; api_key=secret-value"}}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
			Header:     make(http.Header),
		}, nil
	}
	logger := NewMockLogger()
	client := NewClient(
		WithHTTPClient(mockHTTP.ToHTTPClient()),
		WithLogger(logger),
		WithAPIKey("test-key"),
		WithProvider(ProviderCustom),
		WithModel("diagnostic-model"),
		WithMaxRetries(2),
		WithRetryWaitBase(time.Nanosecond),
	).(*Client)

	result, err := client.CallWithMessagesWithMetadata(CallMetadata{
		CallID:     "call-diagnostics-1",
		TraderID:   "trader-1",
		StrategyID: "strategy-1",
	}, "system", "user")
	if err != nil || result != "ok" || attempts != 2 {
		t.Fatalf("result=%q attempts=%d err=%v, want success on second attempt", result, attempts, err)
	}

	logs := logger.GetLogs()
	var failure, success string
	for _, entry := range logs {
		if strings.Contains(entry.Message, "ai_call_failure") && strings.Contains(entry.Message, "attempt=1") {
			failure = entry.Message
		}
		if strings.Contains(entry.Message, "ai_call_success") {
			success = entry.Message
		}
	}
	for _, want := range []string{
		"call_id=call-diagnostics-1",
		"provider=custom",
		"model=diagnostic-model",
		"attempt=1",
		"max_attempts=2",
		"error_kind=provider_unavailable",
		"http_status=500",
		"retryable=true",
		"outcome=retrying",
	} {
		if !strings.Contains(failure, want) {
			t.Errorf("failure log missing %q: %s", want, failure)
		}
	}
	if strings.Contains(failure, "secret-value") {
		t.Errorf("failure log leaked provider credential: %s", failure)
	}
	for _, want := range []string{
		"ai_call_success",
		"call_id=call-diagnostics-1",
		"attempt=2",
		"max_attempts=2",
		"outcome=success",
	} {
		if !strings.Contains(success, want) {
			t.Errorf("success log missing %q: %s", want, success)
		}
	}
}

func TestClientFourHundredIsPermanentAndFinalAttemptIsOne(t *testing.T) {
	mockHTTP := NewMockHTTPClient()
	attempts := 0
	mockHTTP.ResponseFunc = func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid request timeout"}}`)),
			Header:     make(http.Header),
		}, nil
	}
	logger := NewMockLogger()
	client := NewClient(
		WithHTTPClient(mockHTTP.ToHTTPClient()),
		WithLogger(logger),
		WithAPIKey("test-key"),
		WithMaxRetries(3),
		WithRetryWaitBase(time.Nanosecond),
	).(*Client)

	_, err := client.CallWithMessages("system", "user")
	if err == nil || !strings.Contains(err.Error(), "after 1 attempts") {
		t.Fatalf("error=%v, want final failure after one attempt", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d, want no retry for HTTP 400", attempts)
	}

	var failure string
	for _, entry := range logger.GetLogs() {
		if strings.Contains(entry.Message, "ai_call_failure") && strings.Contains(entry.Message, "http_status=400") {
			failure = entry.Message
		}
	}
	for _, want := range []string{"error_kind=invalid_request", "retryable=false", "outcome=final_failure"} {
		if !strings.Contains(failure, want) {
			t.Errorf("400 failure log missing %q: %s", want, failure)
		}
	}
}

func TestRequestMetadataIsNotSerialized(t *testing.T) {
	payload, err := json.Marshal(Request{
		Model:    "model",
		Metadata: CallMetadata{CallID: "call-not-on-wire"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if strings.Contains(string(payload), "call-not-on-wire") || strings.Contains(string(payload), "Metadata") {
		t.Fatalf("request metadata should not be serialized: %s", payload)
	}
}
