package types

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEmergencyCloseVerifier_Success(t *testing.T) {
	v := &EmergencyCloseVerifier{
		PollInterval:      time.Millisecond,
		MaxPolls:          5,
		RequiredZeroCount: 2,
	}

	status, _, _, filled, positionZero, err := v.Verify(
		context.Background(),
		func(ctx context.Context) (string, float64, float64, error) {
			return "FILLED", 100.0, 1.0, nil
		},
		func(ctx context.Context) (bool, error) {
			return true, nil
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filled {
		t.Errorf("expected filled = true")
	}
	if !positionZero {
		t.Errorf("expected positionZero = true")
	}
	if status != "FILLED" {
		t.Errorf("expected status = FILLED, got: %s", status)
	}
}

func TestEmergencyCloseVerifier_PositionNotZero(t *testing.T) {
	v := &EmergencyCloseVerifier{
		PollInterval:      time.Millisecond,
		MaxPolls:          3,
		RequiredZeroCount: 2,
	}

	_, _, _, filled, positionZero, err := v.Verify(
		context.Background(),
		func(ctx context.Context) (string, float64, float64, error) {
			return "FILLED", 100.0, 1.0, nil
		},
		func(ctx context.Context) (bool, error) {
			return false, nil // Position never zero
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filled {
		t.Errorf("expected filled = true")
	}
	if positionZero {
		t.Errorf("expected positionZero = false when position never zero")
	}
}

func TestEmergencyCloseVerifier_TerminalOrderStatusReturnsError(t *testing.T) {
	v := &EmergencyCloseVerifier{
		PollInterval: time.Millisecond,
		MaxPolls:     3,
	}

	_, _, _, _, _, err := v.Verify(
		context.Background(),
		func(ctx context.Context) (string, float64, float64, error) {
			return "REJECTED", 0, 0, nil
		},
		func(ctx context.Context) (bool, error) {
			return false, nil
		},
	)

	if err == nil {
		t.Fatalf("expected error for terminal status REJECTED, got nil")
	}
}

func TestEmergencyCloseVerifier_ContextTimeout(t *testing.T) {
	v := &EmergencyCloseVerifier{
		PollInterval: 100 * time.Millisecond,
		MaxPolls:     5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, _, _, _, err := v.Verify(
		ctx,
		func(ctx context.Context) (string, float64, float64, error) {
			return "NEW", 0, 0, nil
		},
		func(ctx context.Context) (bool, error) {
			return false, nil
		},
	)

	if err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected context timeout error, got: %v", err)
	}
}
