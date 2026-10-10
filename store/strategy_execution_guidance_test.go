package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestExecutionGuidanceStrategyRoundTripAndDefaults(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	if cfg.ExecutionGuidance {
		t.Fatal("execution guidance enabled by default")
	}
	for _, enabled := range []bool{true, false} {
		merged, err := MergeStrategyConfig(cfg, map[string]any{"ai_config": map[string]any{"execution_guidance": enabled}})
		if err != nil {
			t.Fatal(err)
		}
		if merged.ExecutionGuidance != enabled {
			t.Fatal("nested merge lost execution guidance")
		}
		data, err := json.Marshal(merged)
		if err != nil {
			t.Fatal(err)
		}
		var restored StrategyConfig
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(restored.RiskControl, cfg.RiskControl) {
			t.Fatal("advisory option changed risk configuration")
		}
		if restored.ExecutionGuidance != enabled {
			t.Fatal("round trip lost execution guidance")
		}
		cfg = restored
	}
}
