package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GateFlags lists the boolean gates in stable alphabetical order.
var GateFlags = []string{"execution_guidance", "paper_entry_guard"}

func MigrateStrategyFlagAudit(db *sql.DB, postgres bool) error {
	idType, changedAtType := "INTEGER PRIMARY KEY AUTOINCREMENT", "INTEGER"
	if postgres {
		idType, changedAtType = "BIGSERIAL PRIMARY KEY", "BIGINT"
	}
	for _, query := range []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS strategy_config_changes (
		id %s, strategy_id TEXT NOT NULL, field TEXT NOT NULL,
		old_value TEXT NOT NULL DEFAULT '', new_value TEXT NOT NULL DEFAULT '', changed_at %s NOT NULL,
		changed_by TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '')`, idType, changedAtType),
		`CREATE INDEX IF NOT EXISTS idx_strategy_config_changes ON strategy_config_changes(strategy_id, changed_at)`,
	} {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("strategy flag audit migrate: %w", err)
		}
	}
	return nil
}

func strategyFlags(raw string) (map[string]bool, error) {
	var config StrategyConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, err
	}
	return map[string]bool{"execution_guidance": config.ExecutionGuidance, "paper_entry_guard": config.PaperEntryGuard}, nil
}

func GetStrategyFlags(ctx context.Context, db *sql.DB, strategyID string) (map[string]bool, error) {
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT config FROM strategies WHERE id=?`, strategyID).Scan(&raw); err != nil {
		return nil, err
	}
	return strategyFlags(raw)
}

func SetStrategyFlag(ctx context.Context, db *sql.DB, strategyID, flag string, value bool, changedBy, reason string, now time.Time) (changed bool, err error) {
	allowed := false
	for _, f := range GateFlags {
		allowed = allowed || flag == f
	}
	if !allowed {
		return false, fmt.Errorf("unknown gate flag %q", flag)
	}
	if strings.TrimSpace(reason) == "" {
		return false, fmt.Errorf("reason is required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT config FROM strategies WHERE id=?`, strategyID).Scan(&raw); err != nil {
		return false, err
	}
	flags, err := strategyFlags(raw)
	if err != nil {
		return false, err
	}
	if flags[flag] == value {
		return false, nil
	}
	var config map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber() // Preserve large integer values in unrelated fields.
	if err := decoder.Decode(&config); err != nil {
		return false, err
	}
	if config == nil {
		return false, fmt.Errorf("strategy config must be an object")
	}
	ai, _ := config["ai_config"].(map[string]any)
	if ai == nil {
		ai = map[string]any{}
		// Creating ai_config changes precedence for both gates; preserve the other gate.
		for _, f := range GateFlags {
			ai[f] = flags[f]
		}
		config["ai_config"] = ai
	}
	ai[flag] = value
	updated, err := json.Marshal(config)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE strategies SET config=?, updated_at=? WHERE id=?`, string(updated), now.UTC().Format(time.RFC3339Nano), strategyID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO strategy_config_changes
		(strategy_id,field,old_value,new_value,changed_at,changed_by,reason) VALUES (?,?,?,?,?,?,?)`,
		strategyID, flag, strconv.FormatBool(flags[flag]), strconv.FormatBool(value), now.UnixMilli(), changedBy, reason); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
