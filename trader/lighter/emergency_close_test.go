package lighter

import (
	"context"
	"testing"

	tradertypes "nofx/trader/types"
)

func TestLighter_EmergencyClosePosition_InterfaceAndValidation(t *testing.T) {
	trader := &LighterTraderV2{}

	closer, ok := any(trader).(tradertypes.EmergencyCloser)
	if !ok {
		t.Fatalf("LighterTraderV2 does not implement types.EmergencyCloser")
	}

	// Reject non-positive quantity
	if _, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0); err == nil {
		t.Errorf("expected error for zero quantity, got nil")
	}

	// Reject uninitialized TxClient
	if _, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 1.0); err == nil {
		t.Errorf("expected error for uninitialized TxClient, got nil")
	}
}
