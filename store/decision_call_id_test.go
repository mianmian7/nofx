package store

import (
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDecisionRecordCallIDJSONCompatibility(t *testing.T) {
	record := DecisionRecord{CallID: "call-1", CycleID: "cycle-1", TraderID: "trader-1"}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal DecisionRecord: %v", err)
	}

	var decoded DecisionRecord
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal DecisionRecord: %v", err)
	}
	if decoded.CallID != record.CallID || decoded.CycleID != record.CycleID {
		t.Fatalf("decoded correlation = %q/%q, want %q/%q", decoded.CallID, decoded.CycleID, record.CallID, record.CycleID)
	}

	var legacy DecisionRecord
	if err := json.Unmarshal([]byte(`{"trader_id":"legacy"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy DecisionRecord: %v", err)
	}
	if legacy.CallID != "" || legacy.CycleID != "" {
		t.Fatalf("legacy correlation = %q/%q, want empty", legacy.CallID, legacy.CycleID)
	}
}

func TestDecisionStoreMigratesAndPersistsCorrelationIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`CREATE TABLE decision_records (
		id integer primary key autoincrement,
		trader_id text not null,
		cycle_number integer not null,
		timestamp datetime not null,
		success numeric default false
	)`).Error; err != nil {
		t.Fatal(err)
	}
	store := NewDecisionStore(db)
	if err = store.initTables(); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&DecisionRecordDB{}, "CallID") || !db.Migrator().HasColumn(&DecisionRecordDB{}, "CycleID") {
		t.Fatal("correlation columns were not migrated")
	}
	if !db.Migrator().HasIndex(&DecisionRecordDB{}, "CallID") || !db.Migrator().HasIndex(&DecisionRecordDB{}, "CycleID") {
		t.Fatal("correlation indexes were not migrated")
	}
	record := &DecisionRecord{TraderID: "trader-1", CallID: "call-1", CycleID: "cycle-1", CycleNumber: 1, Success: true}
	if err = store.LogDecision(record); err != nil {
		t.Fatal(err)
	}
	rows, err := store.GetLatestRecords("trader-1", 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("records = %d, err=%v", len(rows), err)
	}
	if rows[0].CallID != record.CallID || rows[0].CycleID != record.CycleID {
		t.Fatalf("stored correlation = %q/%q", rows[0].CallID, rows[0].CycleID)
	}
}
