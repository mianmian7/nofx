package manager

import (
	"testing"
	"time"
)

func TestStartupStaggerPlanUsesAgreedSameModelPhases(t *testing.T) {
	inputs := []startupStaggerInput{
		{ID: "cycle-c", Name: "Cycle C", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
		{ID: "hour-b", Name: "Hour B", ModelKey: "deepseek", ScanInterval: 60 * time.Minute},
		{ID: "cycle-a", Name: "Cycle A", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
		{ID: "hour-a", Name: "Hour A", ModelKey: "deepseek", ScanInterval: 60 * time.Minute},
		{ID: "cycle-b", Name: "Cycle B", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
	}

	got := planStartupStagger(inputs)
	want := map[string]time.Duration{
		"cycle-a": 0,
		"cycle-b": 5 * time.Minute,
		"cycle-c": 10 * time.Minute,
		"hour-a":  20 * time.Minute,
		"hour-b":  40 * time.Minute,
	}
	for id, wantDelay := range want {
		if got[id] != wantDelay {
			t.Errorf("delay for %s = %v, want %v", id, got[id], wantDelay)
		}
	}
}

func TestStartupStaggerPlanSeparatesModelsAndHonorsExplicitDelay(t *testing.T) {
	inputs := []startupStaggerInput{
		{ID: "deepseek-a", Name: "A", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
		{ID: "luna-a", Name: "A", ModelKey: "luna", ScanInterval: 15 * time.Minute},
		{ID: "deepseek-explicit", Name: "B", ModelKey: "deepseek", ScanInterval: 15 * time.Minute, ConfiguredDelay: 7 * time.Minute},
		{ID: "deepseek-c", Name: "C", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
	}

	got := planStartupStagger(inputs)
	if got["deepseek-a"] != 0 || got["luna-a"] != 0 {
		t.Fatalf("different models should each start at their first phase: %#v", got)
	}
	if got["deepseek-explicit"] != 7*time.Minute {
		t.Errorf("explicit delay = %v, want 7m", got["deepseek-explicit"])
	}
	if got["deepseek-c"] != 5*time.Minute {
		t.Errorf("automatic member after explicit override = %v, want 5m", got["deepseek-c"])
	}
}

func TestStartupStaggerPlanUsesStableNameThenIDOrdering(t *testing.T) {
	inputs := []startupStaggerInput{
		{ID: "z", Name: "Same", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
		{ID: "a", Name: "Same", ModelKey: "deepseek", ScanInterval: 15 * time.Minute},
	}

	got := planStartupStagger(inputs)
	if got["a"] != 0 || got["z"] != 5*time.Minute {
		t.Fatalf("name/ID ordering is not stable: %#v", got)
	}
}
