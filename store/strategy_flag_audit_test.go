package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	_ "modernc.org/sqlite"
)

func flagAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/flags.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE strategies (id TEXT PRIMARY KEY, config TEXT, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := MigrateStrategyFlagAudit(db, false); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestMigrateStrategyFlagAuditDialects(t *testing.T) {
	for _, tc := range []struct {
		name, idType, changedAtType string
		postgres                    bool
	}{
		{"sqlite", "INTEGER PRIMARY KEY AUTOINCREMENT", "INTEGER", false},
		{"postgres", "BIGSERIAL PRIMARY KEY", "BIGINT", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/ddl.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// SQLite accepts both declarations, allowing inspection of emitted DDL
			// without a PostgreSQL server; this does not test PostgreSQL execution.
			for i := 0; i < 2; i++ {
				if err := MigrateStrategyFlagAudit(db, tc.postgres); err != nil {
					t.Fatal(err)
				}
			}
			var ddl string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='strategy_config_changes'`).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ddl, "id "+tc.idType) || !strings.Contains(ddl, "changed_at "+tc.changedAtType+" NOT NULL") {
				t.Fatalf("unexpected %s DDL: %s", tc.name, ddl)
			}
		})
	}
}

func TestSetStrategyFlag(t *testing.T) {
	db := flagAuditDB(t)
	ctx := context.Background()
	raw := `{"execution_guidance":true,"paper_entry_guard":true,"other":{"n":9007199254740993,"list":["x",null]},"ai_config":{"paper_entry_guard":false,"unknown":{"x":"keep"}}}`
	if _, err := db.Exec(`INSERT INTO strategies VALUES ('s1',?,NULL)`, raw); err != nil {
		t.Fatal(err)
	}
	flags, err := GetStrategyFlags(ctx, db, "s1")
	if err != nil || flags["execution_guidance"] || flags["paper_entry_guard"] {
		t.Fatalf("nested precedence: %v %v", flags, err)
	}
	now := time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC)
	changed, err := SetStrategyFlag(ctx, db, "s1", "paper_entry_guard", true, "operator", "test", now)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var updated string
	if err := db.QueryRow(`SELECT config FROM strategies WHERE id='s1'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	var updatedAt any
	if err := db.QueryRow(`SELECT updated_at FROM strategies WHERE id='s1'`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	decode := func(raw string) map[string]any {
		var m map[string]any
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	want := decode(raw)
	want["ai_config"].(map[string]any)["paper_entry_guard"] = true
	if !reflect.DeepEqual(want, decode(updated)) {
		t.Fatal("unrelated config fields changed")
	}
	var field, old, next, by, reason string
	var at int64
	if err := db.QueryRow(`SELECT field,old_value,new_value,changed_at,changed_by,reason FROM strategy_config_changes`).Scan(&field, &old, &next, &at, &by, &reason); err != nil {
		t.Fatal(err)
	}
	if field != "paper_entry_guard" || old != "false" || next != "true" || at != now.UnixMilli() || by != "operator" || reason != "test" {
		t.Fatal("incorrect audit row")
	}
	if changed, err := SetStrategyFlag(ctx, db, "s1", field, true, "operator", "again", now.Add(time.Hour)); err != nil || changed {
		t.Fatalf("unchanged=%v err=%v", changed, err)
	}
	for _, tc := range []struct{ flag, reason string }{{"unknown", "test"}, {field, ""}, {field, "  "}} {
		if _, err := SetStrategyFlag(ctx, db, "s1", tc.flag, false, "", tc.reason, now); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM strategy_config_changes`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit count=%d err=%v", n, err)
	}
	var after string
	if err := db.QueryRow(`SELECT config FROM strategies WHERE id='s1'`).Scan(&after); err != nil || after != updated {
		t.Fatal("no-op or invalid input changed config")
	}
	var afterAt any
	if err := db.QueryRow(`SELECT updated_at FROM strategies WHERE id='s1'`).Scan(&afterAt); err != nil || !reflect.DeepEqual(updatedAt, afterAt) {
		t.Fatal("no-op or invalid input changed updated_at")
	}
	// Audit failure must roll back the config update.
	if _, err := db.Exec(`DROP TABLE strategy_config_changes`); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStrategyFlag(ctx, db, "s1", field, false, "", "rollback", now); err == nil {
		t.Fatal("missing audit table accepted")
	}
	if err := db.QueryRow(`SELECT config FROM strategies WHERE id='s1'`).Scan(&after); err != nil || after != updated {
		t.Fatal("failed audit did not roll back config")
	}
}

func TestSetStrategyFlagLegacy(t *testing.T) {
	db := flagAuditDB(t)
	if _, err := db.Exec(`INSERT INTO strategies VALUES ('legacy','{"execution_guidance":true,"paper_entry_guard":false,"custom":"keep"}',NULL)`); err != nil {
		t.Fatal(err)
	}
	if changed, err := SetStrategyFlag(context.Background(), db, "legacy", "paper_entry_guard", true, "", "test", time.Now()); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	flags, err := GetStrategyFlags(context.Background(), db, "legacy")
	if err != nil || !flags["execution_guidance"] || !flags["paper_entry_guard"] {
		t.Fatalf("legacy flags=%v err=%v", flags, err)
	}
}

func TestStrategyUpdateFlagAudit(t *testing.T) {
	db := flagAuditDB(t)
	gdb, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := NewStrategyStore(gdb)
	if err := s.initTables(); err != nil {
		t.Fatal(err)
	}
	strategy := &Strategy{ID: "s1", UserID: "u1", Config: `{"ai_config":{"execution_guidance":false,"paper_entry_guard":false}}`}
	if err := s.Create(strategy); err != nil {
		t.Fatal(err)
	}
	// Reject an audit insert unless the updated config is already persisted.
	if _, err := db.Exec(`CREATE TRIGGER audit_after_update BEFORE INSERT ON strategy_config_changes
		WHEN json_extract((SELECT config FROM strategies WHERE id=NEW.strategy_id), '$.ai_config.' || NEW.field) != (NEW.new_value='true')
		BEGIN SELECT RAISE(ABORT, 'audit preceded update'); END`); err != nil {
		t.Fatal(err)
	}
	strategy.Config = `{"ai_config":{"execution_guidance":true,"paper_entry_guard":true}}`
	if _, err := db.Exec(`CREATE TRIGGER reject_strategy_update BEFORE UPDATE ON strategies
		BEGIN SELECT RAISE(ABORT, 'update rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(strategy); err == nil {
		t.Fatal("failed strategy update accepted")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM strategy_config_changes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed update audit rows=%d err=%v", n, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_strategy_update`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Update(strategy); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM strategy_config_changes WHERE changed_by='api' AND reason='strategy update' AND old_value='false' AND new_value='true'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit rows=%d err=%v", n, err)
	}
	if _, err := db.Exec(`DROP TABLE strategy_config_changes`); err != nil {
		t.Fatal(err)
	}
	strategy.Config = `{"ai_config":{"paper_entry_guard":false}}`
	if err := s.Update(strategy); err != nil {
		t.Fatalf("audit failure blocked update: %v", err)
	}
	flags, err := GetStrategyFlags(context.Background(), db, "s1")
	if err != nil || flags["paper_entry_guard"] {
		t.Fatal("best-effort update not persisted")
	}
}
