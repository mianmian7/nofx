package types

import (
	"context"
	"fmt"
	"time"
)

// EmergencyCloseVerifier executes bounded polling to verify order fill status
// and consecutive zero-position checks before an emergency exit is considered complete.
type EmergencyCloseVerifier struct {
	PollInterval      time.Duration
	MaxPolls          int
	RequiredZeroCount int
}

// DefaultEmergencyCloseVerifier provides a conservative 5-poll, 2-consecutive-zero verifier.
func DefaultEmergencyCloseVerifier() *EmergencyCloseVerifier {
	return &EmergencyCloseVerifier{
		PollInterval:      200 * time.Millisecond,
		MaxPolls:          5,
		RequiredZeroCount: 2,
	}
}

// Verify polls order status and requires consecutive zero-position checks.
func (v *EmergencyCloseVerifier) Verify(
	ctx context.Context,
	queryOrder func(ctx context.Context) (status string, avgPrice, executedQty float64, err error),
	queryPositionZero func(ctx context.Context) (isZero bool, err error),
) (status string, avgPrice, executedQty float64, filled, positionZero bool, err error) {
	if v == nil {
		v = DefaultEmergencyCloseVerifier()
	}

	status = "SUBMITTED"
	pollInterval := v.PollInterval
	if pollInterval <= 0 {
		pollInterval = 200 * time.Millisecond
	}
	maxPolls := v.MaxPolls
	if maxPolls <= 0 {
		maxPolls = 5
	}
	requiredZero := v.RequiredZeroCount
	if requiredZero <= 0 {
		requiredZero = 2
	}

	// 1. Poll order status until FILLED or failed/terminal
	if queryOrder != nil {
		for poll := 0; poll < maxPolls; poll++ {
			select {
			case <-ctx.Done():
				return status, avgPrice, executedQty, false, false, ctx.Err()
			case <-time.After(pollInterval):
			}

			s, p, q, qErr := queryOrder(ctx)
			if qErr == nil {
				if s != "" {
					status = s
				}
				if p > 0 {
					avgPrice = p
				}
				if q > 0 {
					executedQty = q
				}
				if s == "FILLED" {
					filled = true
					break
				}
				if s == "CANCELED" || s == "EXPIRED" || s == "REJECTED" {
					return status, avgPrice, executedQty, false, false, fmt.Errorf("order terminated with status %s", s)
				}
			}
		}
	}

	if !filled {
		return status, avgPrice, executedQty, false, false, fmt.Errorf("order did not reach FILLED status within polling window (last status: %s)", status)
	}
	if executedQty <= 0 {
		return status, avgPrice, executedQty, true, false, fmt.Errorf("order reported FILLED but executed quantity is zero")
	}

	// 2. Verify position zero with consecutive confirmations
	consecutiveZero := 0
	if queryPositionZero != nil {
		for poll := 0; poll < maxPolls && consecutiveZero < requiredZero; poll++ {
			select {
			case <-ctx.Done():
				return status, avgPrice, executedQty, filled, false, ctx.Err()
			case <-time.After(pollInterval):
			}

			isZero, pErr := queryPositionZero(ctx)
			if pErr == nil && isZero {
				consecutiveZero++
			} else {
				consecutiveZero = 0
			}
		}
	}

	positionZero = (consecutiveZero >= requiredZero)
	return status, avgPrice, executedQty, filled, positionZero, nil
}
