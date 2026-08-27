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
	// deepseek (group 0) starts at 0, luna (group 1) is interleaved at 1m offset to prevent simultaneous market requests
	if got["deepseek-a"] != 0 {
		t.Errorf("deepseek-a delay = %v, want 0", got["deepseek-a"])
	}
	if got["luna-a"] != 1*time.Minute {
		t.Errorf("luna-a delay = %v, want 1m (inter-model stagger)", got["luna-a"])
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

func TestStartupStaggerPlanMultiModelInterleaving(t *testing.T) {
	inputs := []startupStaggerInput{
		{ID: "gemini-1", Name: "优质-多周期-Gemini", ModelKey: "gemini", ScanInterval: 15 * time.Minute},
		{ID: "grok-1", Name: "优质-多周期-Grok", ModelKey: "grok", ScanInterval: 15 * time.Minute},
		{ID: "luna-1", Name: "优质-多周期-Luna", ModelKey: "luna", ScanInterval: 15 * time.Minute},
		{ID: "sol-1", Name: "优质-多周期-Sol", ModelKey: "sol", ScanInterval: 15 * time.Minute},
		{ID: "gemini-2", Name: "挑战-多周期-Gemini", ModelKey: "gemini", ScanInterval: 15 * time.Minute},
		{ID: "grok-2", Name: "挑战-多周期-Grok", ModelKey: "grok", ScanInterval: 15 * time.Minute},
	}

	got := planStartupStagger(inputs)

	// Model groups alphabetically: gemini (0), grok (1), luna (2), sol (3)
	// Phase 0: gemini-1 (0m), grok-1 (1m), luna-1 (2m), sol-1 (3m) -> ALL distinct minutes!
	// Phase 1: gemini-2 (5m), grok-2 (6m)
	want := map[string]time.Duration{
		"gemini-1": 0 * time.Minute,
		"grok-1":   1 * time.Minute,
		"luna-1":   2 * time.Minute,
		"sol-1":    3 * time.Minute,
		"gemini-2": 5 * time.Minute,
		"grok-2":   6 * time.Minute,
	}

	for id, expected := range want {
		if got[id] != expected {
			t.Errorf("delay for %s = %v, want %v", id, got[id], expected)
		}
	}
}
