package store

import (
	"encoding/json"
	"testing"
)

func TestDecisionRecordCallIDJSONCompatibility(t *testing.T) {
	record := DecisionRecord{CallID: "call-1", TraderID: "trader-1"}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal DecisionRecord: %v", err)
	}

	var decoded DecisionRecord
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal DecisionRecord: %v", err)
	}
	if decoded.CallID != record.CallID {
		t.Fatalf("decoded CallID = %q, want %q", decoded.CallID, record.CallID)
	}

	var legacy DecisionRecord
	if err := json.Unmarshal([]byte(`{"trader_id":"legacy"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy DecisionRecord: %v", err)
	}
	if legacy.CallID != "" {
		t.Fatalf("legacy CallID = %q, want empty", legacy.CallID)
	}
}
