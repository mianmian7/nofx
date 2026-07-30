package mcp

import (
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
