package mcp

import (
	"testing"
	"time"
)

func TestDefaultConfigRequestTimeoutDefault(t *testing.T) {
	t.Setenv("AI_REQUEST_TIMEOUT_SECONDS", "")
	cfg := DefaultConfig()
	want := DefaultRequestTimeoutSeconds * time.Second
	if cfg.Timeout != want || cfg.HTTPClient.Timeout != want {
		t.Fatalf("timeout = %v / http %v, want %v", cfg.Timeout, cfg.HTTPClient.Timeout, want)
	}
}

func TestDefaultConfigRequestTimeoutEnv(t *testing.T) {
	t.Setenv("AI_REQUEST_TIMEOUT_SECONDS", "95")
	cfg := DefaultConfig()
	if cfg.Timeout != 95*time.Second || cfg.HTTPClient.Timeout != 95*time.Second {
		t.Fatalf("timeout = %v / http %v, want 95s", cfg.Timeout, cfg.HTTPClient.Timeout)
	}
}
