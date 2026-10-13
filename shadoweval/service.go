package shadoweval

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nofx/logger"
)

// Service ingests new decision records and fills forward returns.
type Service struct {
	DB            *sql.DB
	Prices        PriceSource
	BackfillSince time.Time
	IngestBatch   int
	FillBatch     int
	Interval      time.Duration
	Now           func() time.Time
}

func NewService(db *sql.DB, prices PriceSource, since time.Time) *Service {
	return &Service{DB: db, Prices: prices, BackfillSince: since, IngestBatch: 300, FillBatch: 1500,
		Interval: 2 * time.Minute, Now: time.Now}
}

// Start runs ingest+fill in the background until ctx is cancelled. Errors are
// logged and never propagate to trading.
func (s *Service) Start(ctx context.Context) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warnf("shadow-eval: recovered panic: %v", r)
			}
		}()
		t := time.NewTimer(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.RunOnce(ctx)
			t.Reset(s.Interval)
		}
	}()
}

// RunOnce ingests until caught up (bounded) and runs one fill pass.
func (s *Service) RunOnce(ctx context.Context) {
	total := 0
	for i := 0; i < 40; i++ {
		n, err := s.Ingest(ctx)
		if err != nil {
			logger.Warnf("shadow-eval ingest: %v", err)
			break
		}
		total += n
		if n == 0 {
			break
		}
	}
	if r, ok := s.Prices.(interface{ Reset() }); ok {
		r.Reset()
	}
	filled := 0
	for i := 0; i < 10; i++ {
		n, err := s.Fill(ctx)
		filled += n
		if err != nil {
			logger.Warnf("shadow-eval fill: %v", err)
			break
		}
		if n < s.FillBatch/2 {
			break
		}
	}
	if total > 0 || filled > 0 {
		logger.Infof("shadow-eval: ingested %d records, filled %d rows", total, filled)
	}
}

type traderMeta struct{ exchange, model, promptVersion, timeframe string }

func (s *Service) traderMeta(ctx context.Context) (map[string]traderMeta, error) {
	// Only non-secret columns are selected.
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, COALESCE(e.exchange_type,''), COALESCE(m.provider,''),
		COALESCE(NULLIF(t.primary_model_name,''), NULLIF(m.custom_model_name,''), m.name, ''),
		COALESCE(t.system_prompt_template,''), COALESCE(t.strategy_id,''), COALESCE(t.scan_interval_minutes,0)
		FROM traders t LEFT JOIN exchanges e ON e.id=t.exchange_id LEFT JOIN ai_models m ON m.id=t.ai_model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]traderMeta{}
	for rows.Next() {
		var id, ex, prov, mname, tmpl, strat string
		var scan int
		if err := rows.Scan(&id, &ex, &prov, &mname, &tmpl, &strat, &scan); err != nil {
			return nil, err
		}
		model := prov
		if mname != "" && mname != prov {
			model = prov + ":" + mname
		}
		out[id] = traderMeta{exchange: ex, model: model, promptVersion: tmpl + "@" + strat, timeframe: strconv.Itoa(scan) + "m"}
	}
	return out, rows.Err()
}

// ParseTime handles the formats SQLite drivers return for DATETIME columns.
func ParseTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x.UTC(), nil
	case []byte:
		return ParseTime(string(x))
	case string:
		for _, l := range []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(l, x); err == nil {
				return t.UTC(), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %v", v)
}

// Ingest converts the next batch of decision records into shadow rows.
func (s *Service) Ingest(ctx context.Context) (int, error) {
	var last sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(decision_record_id) FROM shadow_decisions`).Scan(&last); err != nil {
		return 0, err
	}
	meta, err := s.traderMeta(ctx)
	if err != nil {
		return 0, err
	}
	q := `SELECT id, trader_id, timestamp, system_prompt, decision_json, decisions FROM decision_records WHERE id > ? `
	args := []any{last.Int64}
	if !last.Valid {
		q += `AND timestamp >= ? `
		args = append(args, s.BackfillSince.UTC().Format("2006-01-02 15:04:05"))
	}
	q += `ORDER BY id LIMIT ?`
	args = append(args, s.IngestBatch)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	type rec struct {
		id                   int64
		trader, sp, dj, dcol string
		ts                   time.Time
	}
	var recs []rec
	for rows.Next() {
		var r rec
		var tsRaw any
		var sp, dj, dc sql.NullString
		if err := rows.Scan(&r.id, &r.trader, &tsRaw, &sp, &dj, &dc); err != nil {
			rows.Close()
			return 0, err
		}
		r.ts, err = ParseTime(tsRaw)
		if err != nil {
			continue
		}
		r.sp, r.dj, r.dcol = sp.String, dj.String, dc.String
		recs = append(recs, r)
	}
	rows.Close()
	if len(recs) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO shadow_decisions
		(decision_record_id, action_index, trader_id, exchange, model, prompt_version, prompt_hash, timeframe,
		 symbol, action, direction, source, status, confidence, probability, regime, executed, decided_at,
		 decision_price, price_status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	now := s.Now().UnixMilli()
	for _, r := range recs {
		m := meta[r.trader]
		hash := PromptHash(r.sp)
		parsed := ParseRecord(r.dcol, r.dj)
		if len(parsed) == 0 {
			// keep the watermark moving: one placeholder row per empty record
			parsed = []Parsed{{Index: -1, Symbol: "-", Action: "none", Direction: "none", Source: "ai", Status: "no_action"}}
		}
		for _, p := range parsed {
			status := "pending"
			if p.Direction == "none" {
				status = "skip"
			}
			if _, err := stmt.ExecContext(ctx, r.id, p.Index, r.trader, m.exchange, m.model, m.promptVersion, hash, m.timeframe,
				p.Symbol, p.Action, p.Direction, p.Source, p.Status, nullF(p.Confidence), nullF(p.Probability), p.Regime,
				boolInt(p.Executed), r.ts.UnixMilli(), nullF(p.DecisionPrice), status, now, now); err != nil {
				return 0, err
			}
		}
	}
	return len(recs), tx.Commit()
}

func nullF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Fill computes forward returns for pending rows whose horizons have elapsed.
// Returns are signed by direction for long/short and raw for flat/close.
func (s *Service) Fill(ctx context.Context) (int, error) {
	now := s.Now().UnixMilli()
	rows, err := s.DB.QueryContext(ctx, `SELECT id, symbol, direction, decided_at, entry_price, ret_15m, ret_1h, ret_4h, ret_24h, btc_ret_24h
		FROM shadow_decisions WHERE price_status IN ('pending','partial') AND next_due_at <= ? AND decided_at < ?
		ORDER BY symbol, decided_at LIMIT ?`, now, now-17*60000, s.FillBatch)
	if err != nil {
		return 0, err
	}
	type row struct {
		id       int64
		sym, dir string
		at       int64
		entry    sql.NullFloat64
		rets     [4]sql.NullFloat64
		btc      sql.NullFloat64
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sym, &r.dir, &r.at, &r.entry, &r.rets[0], &r.rets[1], &r.rets[2], &r.rets[3], &r.btc); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, r)
	}
	rows.Close()
	filled := 0
	for _, r := range list {
		if ctx.Err() != nil {
			break
		}
		entryMin := r.at/60000*60000 + 60000 // first tradeable bar after the decision
		entry := r.entry.Float64
		if !r.entry.Valid {
			p, ok, err := s.Prices.OpenAt(ctx, r.sym, entryMin)
			if err != nil {
				logger.Warnf("shadow-eval price %s: %v", r.sym, err)
				continue
			}
			if !ok {
				if now-entryMin > 2*3600*1000 {
					s.DB.ExecContext(ctx, `UPDATE shadow_decisions SET price_status='unavailable', updated_at=? WHERE id=?`, now, r.id)
				} else {
					s.DB.ExecContext(ctx, `UPDATE shadow_decisions SET next_due_at=?, updated_at=? WHERE id=?`, now+10*60000, now, r.id)
				}
				continue
			}
			entry = p
		}
		sign := 1.0
		if r.dir == "short" {
			sign = -1
		}
		complete := true
		var nextDue int64
		for i, h := range Horizons {
			if r.rets[i].Valid {
				continue
			}
			t := entryMin + int64(h.Minutes)*60000
			if t+60000 > now {
				complete = false
				if nextDue == 0 || t+2*60000 < nextDue {
					nextDue = t + 2*60000
				}
				continue
			}
			p, ok, err := s.Prices.OpenAt(ctx, r.sym, t)
			if err != nil || !ok {
				complete = false
				if nextDue == 0 || now+10*60000 < nextDue {
					nextDue = now + 10*60000
				}
				continue
			}
			r.rets[i] = sql.NullFloat64{Float64: (p/entry - 1) * 100 * sign, Valid: true}
		}
		if !r.btc.Valid {
			b1, ok1, _ := s.Prices.OpenAt(ctx, "BTCUSDT", entryMin)
			b0, ok0, _ := s.Prices.OpenAt(ctx, "BTCUSDT", entryMin-24*3600*1000)
			if ok0 && ok1 {
				r.btc = sql.NullFloat64{Float64: (b1/b0 - 1) * 100, Valid: true}
			}
		}
		status := "partial"
		if complete {
			status = "done"
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE shadow_decisions SET entry_price=?, ret_15m=?, ret_1h=?, ret_4h=?, ret_24h=?, btc_ret_24h=?, price_status=?, next_due_at=?, updated_at=? WHERE id=?`,
			entry, nv(r.rets[0]), nv(r.rets[1]), nv(r.rets[2]), nv(r.rets[3]), nv(r.btc), status, nextDue, now, r.id); err != nil {
			return filled, err
		}
		filled++
	}
	return filled, nil
}

func nv(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

// HorizonColumn maps a horizon name to its column.
func HorizonColumn(name string) (string, bool) {
	for _, h := range Horizons {
		if strings.EqualFold(h.Name, name) {
			return h.Column, true
		}
	}
	return "", false
}
