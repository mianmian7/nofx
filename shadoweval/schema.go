// Package shadoweval records every AI decision (executed or not) and later
// scores it against public 1m klines. It is observation-only: it never reads
// or changes trading state beyond its own shadow_decisions table.
package shadoweval

import (
	"database/sql"
	"fmt"
)

// RoundTripFeePct is the assumed round-trip cost (maker 2bps + taker 5bps) in percent.
const RoundTripFeePct = 0.07

// Horizons scored for every directional decision, in minutes.
var Horizons = []struct {
	Name    string
	Minutes int
	Column  string
}{
	{"15m", 15, "ret_15m"},
	{"1h", 60, "ret_1h"},
	{"4h", 240, "ret_4h"},
	{"24h", 1440, "ret_24h"},
}

const createTableSQL = `CREATE TABLE IF NOT EXISTS shadow_decisions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	decision_record_id INTEGER NOT NULL,
	action_index INTEGER NOT NULL,
	trader_id TEXT NOT NULL,
	exchange TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	prompt_version TEXT NOT NULL DEFAULT '',
	prompt_hash TEXT NOT NULL DEFAULT '',
	timeframe TEXT NOT NULL DEFAULT '',
	gate_flags TEXT NOT NULL DEFAULT '',
	symbol TEXT NOT NULL,
	action TEXT NOT NULL,
	direction TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT 'ai',
	status TEXT NOT NULL DEFAULT '',
	confidence REAL,
	probability REAL,
	regime TEXT NOT NULL DEFAULT '',
	executed INTEGER NOT NULL DEFAULT 0,
	decided_at INTEGER NOT NULL,
	decision_price REAL,
	entry_price REAL,
	ret_15m REAL,
	ret_1h REAL,
	ret_4h REAL,
	ret_24h REAL,
	btc_ret_24h REAL,
	price_status TEXT NOT NULL DEFAULT 'pending',
	next_due_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE(decision_record_id, action_index)
)`

// Migrate creates the shadow table and indexes and adds missing gate_flags.
func Migrate(db *sql.DB) error {
	stmts := []string{
		createTableSQL,
		`CREATE INDEX IF NOT EXISTS idx_shadow_decided_at ON shadow_decisions(decided_at)`,
		`CREATE INDEX IF NOT EXISTS idx_shadow_pending ON shadow_decisions(price_status, next_due_at)`,
		`CREATE INDEX IF NOT EXISTS idx_shadow_trader ON shadow_decisions(trader_id, decided_at)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("shadow_decisions migrate: %w", err)
		}
	}
	rows, err := db.Query(`PRAGMA table_info(shadow_decisions)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "gate_flags"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		_, err = db.Exec(`ALTER TABLE shadow_decisions ADD COLUMN gate_flags TEXT NOT NULL DEFAULT ''`)
	}
	return err
}
