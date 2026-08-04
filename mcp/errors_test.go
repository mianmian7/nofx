package mcp

import (
	"context"
	"testing"
	"time"
)

func TestNewAPIErrorClassifiesAuthenticationPoolFailure(t *testing.T) {
	err := NewAPIError(503, `{"error":"auth_unavailable"}`)

	if got := ErrorKindOf(err); got != ErrorKindAuthUnavailable {
		t.Fatalf("expected auth_unavailable, got %s", got)
	}
	if !IsFailoverEligible(err) {
		t.Fatal("authentication pool failure should be eligible for failover")
	}
	retryDelay := retryWaitDuration(2*time.Second, 1, err)
	if retryDelay < 30*time.Second || retryDelay > 38*time.Second {
		t.Fatalf("authentication pool retry should be 30 seconds plus bounded jitter, got %s", retryDelay)
	}
}

func TestAPIErrorPermanentCredentialFailureDoesNotFailover(t *testing.T) {
	err := NewAPIError(401, `{"error":"invalid api key"}`)

	if got := ErrorKindOf(err); got != ErrorKindInvalidCredentials {
		t.Fatalf("expected invalid_credentials, got %s", got)
	}
	if IsFailoverEligible(err) {
		t.Fatal("permanent credential failure should not trigger model failover")
	}
}

func TestTransientAPIStatusesAreRetryableAndFailoverEligible(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 522} {
		err := NewAPIError(status, "temporary upstream failure")
		if got := ErrorKindOf(err); got != ErrorKindProviderUnavailable {
			t.Errorf("status %d classified as %s, want provider_unavailable", status, got)
		}
		if !IsFailoverEligible(err) {
			t.Errorf("status %d should be eligible for failover", status)
		}
		client := NewClient()
		if !client.(*Client).IsRetryableError(err) {
			t.Errorf("status %d should be retryable", status)
		}
	}
}

func TestDeadlineExceededIsRetryableNetworkFailure(t *testing.T) {
	if got := ErrorKindOf(context.DeadlineExceeded); got != ErrorKindNetworkUnavailable {
		t.Fatalf("context deadline classified as %s, want network_unavailable", got)
	}
	if !IsFailoverEligible(context.DeadlineExceeded) {
		t.Fatal("context deadline should be eligible for failover")
	}
	if !NewClient().(*Client).IsRetryableError(context.DeadlineExceeded) {
		t.Fatal("context deadline should be retried by the client")
	}
}
