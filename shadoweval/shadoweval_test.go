package shadoweval

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestParseRecordAIExecutedAndRejected(t *testing.T) {
	dcol := `[{"action":"open_short","symbol":"SKHYUSDT","price":154.76,"confidence":70,"order_id":47,"success":true,"error":""},
	{"action":"open_long","symbol":"SOLUSDT","confidence":82,"order_id":0,"success":false,"error":"risk/reward below"},
	{"action":"wait","symbol":"MUUSDT","confidence":55,"success":true,"error":""}]`
	rows := ParseRecord(dcol, "")
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0].Direction != "short" || !rows[0].Executed || rows[0].Status != "executed" || *rows[0].Confidence != 70 || *rows[0].DecisionPrice != 154.76 {
		t.Fatalf("row0 %+v", rows[0])
	}
	if rows[1].Direction != "long" || rows[1].Executed || rows[1].Status != "rejected" {
		t.Fatalf("row1 %+v", rows[1])
	}
	if rows[2].Direction != "flat" || rows[2].Executed || rows[2].Status != "wait" {
		t.Fatalf("row2 %+v", rows[2])
	}
}

func TestParseRecordJevVetoFromDecisionJSON(t *testing.T) {
	dj := `[{"symbol":"SOLUSDT","action":"wait","confidence":65,"reasoning":"Jev signal open_short vetoed by anti-whipsaw filter: action probability 0.51 is below threshold 0.75"},
	{"symbol":"BTCUSDT","action":"wait","confidence":80,"reasoning":"Jev System 1 wait (regime=ranging_chop, conf=80%)"}]`
	rows := ParseRecord("", dj)
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	r := rows[0]
	if r.Source != "jev" || r.Direction != "short" || r.Status != "vetoed" || r.Executed || r.Probability == nil || *r.Probability != 0.51 {
		t.Fatalf("veto row %+v", r)
	}
	if rows[1].Regime != "ranging_chop" || rows[1].Direction != "flat" || rows[1].Source != "jev" {
		t.Fatalf("regime row %+v", rows[1])
	}
}

func TestParseRecordMergesReasoningFromModelOutput(t *testing.T) {
	dcol := `[{"action":"wait","symbol":"ETHUSDT","success":true}]`
	dj := `[{"action":"wait","symbol":"ETHUSDT","confidence":48,"reasoning":"Jev signal open_long vetoed: action probability 0.70 is below threshold 0.75 (regime=strong_trend_up)"}]`
	rows := ParseRecord(dcol, dj)
	if len(rows) != 1 || rows[0].Direction != "long" || rows[0].Regime != "strong_trend_up" || *rows[0].Confidence != 48 {
		t.Fatalf("%+v", rows)
	}
}

func TestParseRecordBadJSON(t *testing.T) {
	if rows := ParseRecord("not json", "{"); len(rows) != 0 {
		t.Fatalf("expected none, got %d", len(rows))
	}
}

func TestPromptHashStable(t *testing.T) {
	if PromptHash("") != "" || PromptHash("a") != PromptHash("a") || PromptHash("a") == PromptHash("b") || len(PromptHash("x")) != 12 {
		t.Fatal("prompt hash")
	}
}

func TestBootstrapCI(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	xs := make([]float64, 400)
	for i := range xs {
		xs[i] = 1 + rng.NormFloat64()
	}
	lo, hi := BootstrapCI(xs, 1000, rand.New(rand.NewSource(2)))
	m := mean(xs)
	if !(lo < m && m < hi) || hi-lo > 0.5 || lo < 0.7 {
		t.Fatalf("ci [%f,%f] mean %f", lo, hi, m)
	}
	if a, b := BootstrapCI([]float64{2}, 100, rng); a != 2 || b != 2 {
		t.Fatal("single")
	}
}

type fakePrices map[string]func(int64) (float64, bool)

func (f fakePrices) OpenAt(_ context.Context, sym string, m int64) (float64, bool, error) {
	fn, ok := f[sym]
	if !ok {
		return 0, false, nil
	}
	p, ok := fn(m)
	return p, ok, nil
}

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE traders (id text, ai_model_id text, exchange_id text, strategy_id text, scan_interval_minutes integer, system_prompt_template text, primary_model_name text)`,
		`CREATE TABLE exchanges (id text, exchange_type text, api_key text)`,
		`CREATE TABLE ai_models (id text, name text, provider text, custom_model_name text, api_key text)`,
		`CREATE TABLE decision_records (id integer primary key autoincrement, trader_id text, cycle_number integer, timestamp datetime, system_prompt text, decision_json text, decisions text)`,
		`INSERT INTO traders VALUES ('t1','m1','e1','s1',15,'default','')`,
		`INSERT INTO exchanges VALUES ('e1','binance','SECRET')`,
		`INSERT INTO ai_models VALUES ('m1','Gemini','gemini','gemini-x','SECRET')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil { // idempotent
		t.Fatal(err)
	}
	return db
}

func TestIngestFillReportEndToEnd(t *testing.T) {
	db := setupDB(t)
	base := time.Date(2026, 10, 8, 0, 0, 30, 0, time.UTC)
	ins := `INSERT INTO decision_records (trader_id, cycle_number, timestamp, system_prompt, decision_json, decisions) VALUES (?,?,?,?,?,?)`
	// old record before backfill window must be skipped
	db.Exec(ins, "t1", 0, "2026-10-01 00:00:00+00:00", "p", "", `[{"action":"open_long","symbol":"BTCUSDT","success":true}]`)
	for i := 0; i < 30; i++ {
		ts := base.Add(time.Duration(i) * 15 * time.Minute).Format("2006-01-02 15:04:05.999999999-07:00")
		db.Exec(ins, "t1", i+1, ts, "prompt-v1", "", `[{"action":"open_short","symbol":"ETHUSDT","confidence":80,"order_id":1,"success":true,"error":""},{"action":"wait","symbol":"BTCUSDT","confidence":50,"success":true}]`)
	}
	db.Exec(ins, "t1", 99, base.Format("2006-01-02 15:04:05-07:00"), "p", "", "[]") // empty record -> placeholder
	// ETH falls 0.01% per minute => short earns; BTC flat
	t0 := base.Truncate(time.Minute).UnixMilli()
	prices := fakePrices{
		"ETHUSDT": func(m int64) (float64, bool) { return 1000 * (1 - 0.0001*float64(m-t0)/60000), true },
		"BTCUSDT": func(m int64) (float64, bool) { return 50000, true },
	}
	now := base.Add(72 * time.Hour)
	svc := NewService(db, prices, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	svc.Now = func() time.Time { return now }
	svc.IngestBatch = 7
	ctx := context.Background()
	svc.RunOnce(ctx)
	var n, skipped, done int
	db.QueryRow(`SELECT COUNT(*) FROM shadow_decisions WHERE direction!='none'`).Scan(&n)
	db.QueryRow(`SELECT COUNT(*) FROM shadow_decisions WHERE price_status='skip'`).Scan(&skipped)
	db.QueryRow(`SELECT COUNT(*) FROM shadow_decisions WHERE price_status='done'`).Scan(&done)
	if n != 60 || skipped != 1 || done != 60 {
		t.Fatalf("n=%d skipped=%d done=%d", n, skipped, done)
	}
	var model, ex, pv, tf, ph string
	var r1h float64
	db.QueryRow(`SELECT model, exchange, prompt_version, timeframe, prompt_hash, ret_1h FROM shadow_decisions WHERE symbol='ETHUSDT' LIMIT 1`).Scan(&model, &ex, &pv, &tf, &ph, &r1h)
	if model != "gemini:gemini-x" || ex != "binance" || pv != "default@s1" || tf != "15m" || ph != PromptHash("prompt-v1") {
		t.Fatalf("meta %s %s %s %s %s", model, ex, pv, tf, ph)
	}
	if math.Abs(r1h-0.6/(1-0.0001)) > 0.01 { // 60 min * 0.01% ≈ 0.6% in short direction
		t.Fatalf("ret_1h=%f", r1h)
	}
	// rerun is idempotent
	svc.RunOnce(ctx)
	db.QueryRow(`SELECT COUNT(*) FROM shadow_decisions WHERE direction!='none'`).Scan(&n)
	if n != 60 {
		t.Fatalf("rerun n=%d", n)
	}
	rep, err := BuildReport(ctx, db, ReportQuery{Horizon: "1h", GroupBy: []string{"trader", "direction"}, Since: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 1 || rep.Groups[0].N != 30 || rep.Groups[0].HitRate != 1 || rep.Groups[0].Key["direction"] != "short" {
		t.Fatalf("report %+v", rep.Groups)
	}
	g := rep.Groups[0]
	if math.Abs(g.MeanGrossPct-g.MeanNetPct-RoundTripFeePct) > 1e-9 || len(g.Calibration) != 1 || g.Calibration[0].Bucket != "80-90" {
		t.Fatalf("group %+v", g)
	}
	var buf bytes.Buffer
	WriteText(&buf, rep, true)
	if !strings.Contains(buf.String(), "*edge") || !strings.Contains(buf.String(), "80-90") {
		t.Fatal(buf.String())
	}
	if _, err := BuildReport(ctx, db, ReportQuery{Horizon: "2h"}); err == nil {
		t.Fatal("bad horizon accepted")
	}
	if _, err := BuildReport(ctx, db, ReportQuery{Horizon: "1h", GroupBy: []string{"api_key"}}); err == nil {
		t.Fatal("bad group accepted")
	}
}

func TestFillDefersFutureHorizons(t *testing.T) {
	db := setupDB(t)
	base := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	db.Exec(`INSERT INTO decision_records (trader_id, cycle_number, timestamp, system_prompt, decision_json, decisions) VALUES ('t1',1,?,'p','',?)`,
		base.Format("2006-01-02 15:04:05-07:00"), `[{"action":"open_long","symbol":"ETHUSDT","success":true,"order_id":1}]`)
	prices := fakePrices{"ETHUSDT": func(int64) (float64, bool) { return 100, true }, "BTCUSDT": func(int64) (float64, bool) { return 1, true }}
	svc := NewService(db, prices, base.Add(-time.Hour))
	now := base.Add(2 * time.Hour)
	svc.Now = func() time.Time { return now }
	svc.RunOnce(context.Background())
	var st string
	var r1h, r4h sql.NullFloat64
	var due int64
	db.QueryRow(`SELECT price_status, ret_1h, ret_4h, next_due_at FROM shadow_decisions`).Scan(&st, &r1h, &r4h, &due)
	if st != "partial" || !r1h.Valid || r4h.Valid || due != base.Add(time.Minute+4*time.Hour+2*time.Minute).UnixMilli() {
		t.Fatalf("st=%s r1h=%v r4h=%v due=%d", st, r1h, r4h, due)
	}
}
