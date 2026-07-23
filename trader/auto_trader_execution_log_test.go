package trader

import (
	"testing"

	"nofx/kernel"
)

func TestFormatDecisionSuccessLogWaitShowsEntryConfidenceAndThreshold(t *testing.T) {
	got := formatDecisionSuccessLog(kernel.Decision{
		Symbol:     "KORUUSDT",
		Action:     "wait",
		Confidence: 69,
	}, 75)
	want := "✓ KORUUSDT wait succeeded · entry confidence 69% < threshold 75%"
	if got != want {
		t.Fatalf("formatDecisionSuccessLog() = %q, want %q", got, want)
	}
}

func TestFormatDecisionSuccessLogWaitDoesNotInventMissingConfidence(t *testing.T) {
	got := formatDecisionSuccessLog(kernel.Decision{
		Symbol: "KORUUSDT",
		Action: "wait",
	}, 75)
	want := "✓ KORUUSDT wait succeeded · entry confidence unavailable (threshold 75%)"
	if got != want {
		t.Fatalf("formatDecisionSuccessLog() = %q, want %q", got, want)
	}
}

func TestFormatDecisionSuccessLogOpenShowsDecisionConfidence(t *testing.T) {
	got := formatDecisionSuccessLog(kernel.Decision{
		Symbol:     "SOXLUSDT",
		Action:     "open_long",
		Confidence: 82,
	}, 75)
	want := "✓ SOXLUSDT open_long succeeded · confidence 82%"
	if got != want {
		t.Fatalf("formatDecisionSuccessLog() = %q, want %q", got, want)
	}
}
