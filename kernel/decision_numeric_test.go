package kernel

import (
	"encoding/json"
	"testing"
)

// A wait does not authorize risk. A decorative fractional confidence must not
// turn an otherwise valid no-trade response into an AI failure.
func TestDecisionWaitIgnoresFractionalConfidence(t *testing.T) {
	var decision Decision
	if err := json.Unmarshal([]byte(`{"symbol":"HYPEUSDT","action":"wait","confidence":63.4,"reasoning":"no entry setup"}`), &decision); err != nil {
		t.Fatalf("valid wait rejected by unused confidence: %v", err)
	}
	if decision.Confidence != 63 {
		t.Fatalf("confidence = %d, want conservative floor 63", decision.Confidence)
	}
	if decision.Action != "wait" || decision.Symbol != "HYPEUSDT" {
		t.Fatalf("wait changed: %+v", decision)
	}
}

func TestDecisionNoTradeConfidenceDoesNotRelaxOpeningParsing(t *testing.T) {
	for _, action := range []string{"open_long", "open_short", "close_long", "close_short", "update_position"} {
		for _, confidence := range []string{"74.9", "74.0", `"74"`} {
			var decision Decision
			raw := `{"symbol":"BTCUSDT","action":"` + action + `","confidence":` + confidence + `}`
			if err := json.Unmarshal([]byte(raw), &decision); err == nil {
				t.Fatalf("strict action %s accepted confidence %s", action, confidence)
			}
		}
	}
	for _, action := range []string{"wait", "hold"} {
		for _, confidence := range []string{"-0.1", "100.1", `"74"`, `true`} {
			var decision Decision
			raw := `{"symbol":"BTCUSDT","action":"` + action + `","confidence":` + confidence + `}`
			if err := json.Unmarshal([]byte(raw), &decision); err == nil {
				t.Fatalf("%s accepted invalid confidence %s", action, confidence)
			}
		}
		var decision Decision
		if err := json.Unmarshal([]byte(`{"symbol":"BTCUSDT","action":"`+action+`","confidence":74.9}`), &decision); err != nil || decision.Confidence != 74 {
			t.Fatalf("%s confidence changed risk threshold: %+v, %v", action, decision, err)
		}
		if err := json.Unmarshal([]byte(`{"symbol":"BTCUSDT","action":"`+action+`","confidence":74.9,"leverage":1.5}`), &decision); err == nil {
			t.Fatal("fractional leverage was silently accepted")
		}
		if err := json.Unmarshal([]byte(`{"symbol":"BTCUSDT","action":"`+action+`","confidence":74.9,"stop_loss":"invalid"}`), &decision); err == nil {
			t.Fatal("unrelated malformed field was swallowed")
		}
	}
}
