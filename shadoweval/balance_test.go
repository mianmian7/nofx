package shadoweval

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestMigrateGateFlagsOldSchema(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/old.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	old := strings.Replace(createTableSQL, "\tgate_flags TEXT NOT NULL DEFAULT '',\n", "", 1)
	if _, err := db.Exec(old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shadow_decisions
		(decision_record_id,action_index,trader_id,symbol,action,direction,decided_at,created_at,updated_at)
		VALUES (1,0,'t1','BTCUSDT','wait','flat',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	var flags string
	if err := db.QueryRow(`SELECT gate_flags FROM shadow_decisions`).Scan(&flags); err != nil || flags != "" {
		t.Fatalf("old row flags=%q err=%v", flags, err)
	}
	if !ValidGroup("gate_flags") || ValidGroup("config") {
		t.Fatal("gate_flags group validation")
	}
}

func TestBuildBalance(t *testing.T) {
	db := setupDB(t)
	loc := time.FixedZone("SGT", 8*3600)
	base := time.Date(2026, 10, 12, 15, 59, 0, 0, time.UTC)
	cases := []struct {
		trader, action, dir string
		delta               time.Duration
	}{
		{"t1", "open_long", "long", 0},
		{"t1", "open_short_limit", "short", 0},
		{"t1", "wait", "long", 0},
		{"t1", "wait", "short", 0},
		{"t1", "wait", "flat", 0},
		{"t1", "hold", "long", 0},
		{"t1", "close_long", "long", 0},
		{"t1", "none", "none", 0},
		{"t2", "open_long_limit", "long", 0},
		{"t1", "hold", "short", time.Minute},
		{"t1", "open_long", "long", -time.Hour},
	}
	for i, c := range cases {
		if _, err := db.Exec(`INSERT INTO shadow_decisions
			(decision_record_id,action_index,trader_id,model,symbol,action,direction,decided_at,created_at,updated_at)
			VALUES (?,0,?,'model','BTCUSDT',?,?,?,0,0)`, i+1, c.trader, c.action, c.dir, base.Add(c.delta).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := BuildBalance(context.Background(), db, base, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%+v", rows)
	}
	want := BalanceRow{Day: "2026-10-12", TraderID: "t1", Model: "model", OpenLong: 1, OpenShort: 1, Wait: 3, WaitLong: 1, WaitShort: 1, Hold: 1, Total: 7}
	if rows[0] != want || rows[1].TraderID != "t2" || rows[1].OpenLong != 1 || rows[2].Day != "2026-10-13" || rows[2].Hold != 1 {
		t.Fatalf("rows=%+v", rows)
	}
	var out bytes.Buffer
	WriteBalanceText(&out, rows)
	if !strings.Contains(out.String(), "14.3% 14.3% 42.9%") {
		t.Fatal(out.String())
	}
}

func TestCountBearishVetoes(t *testing.T) {
	log := `10-12 22:56:20 [INFO] open_long blocked by bearish_only bias
10-12 23:00:00 [INFO] blocked by filter: bearish_only bias
10-13 00:00:00 [INFO] blocked by bearish_only bias
10-13 00:00:01 [INFO] blocked by bullish bias
10-13 00:00:02 [INFO] bearish_only bias
garbage blocked by bearish_only bias
13-40 00:00:00 [INFO] blocked by bearish_only bias
02-29 00:00:00 [INFO] blocked by bearish_only bias
`
	counts, err := CountBearishVetoes(strings.NewReader(log), time.FixedZone("SGT", 8*3600))
	if err != nil || len(counts) != 3 || counts["10-12"] != 2 || counts["10-13"] != 1 || counts["02-29"] != 1 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
}
