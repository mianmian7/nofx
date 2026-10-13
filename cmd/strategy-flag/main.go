// Command strategy-flag reads gates and changes them with an audit trail.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"nofx/store"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/data.db", "SQLite database path")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: strategy-flag -db data/data.db get [-strategy ID]
       strategy-flag -db data/data.db set -strategy ID -flag paper_entry_guard -value true|false -reason "..." [-by name]
       strategy-flag -db data/data.db log [-strategy ID] [-since YYYY-MM-DD]
A set takes effect at the next strategy config reload; no restart is needed.
Get and log open the database read-only. Log dates use UTC.`)
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}
	command := args[0]
	if command != "get" && command != "set" && command != "log" {
		fail(fmt.Errorf("unknown command %q", command))
	}
	fs := flag.NewFlagSet(command, flag.ExitOnError)
	strategy := fs.String("strategy", "", "strategy ID (empty selects all for get/log)")
	var gate, value, reason, by, since *string
	if command == "set" {
		gate = fs.String("flag", "", "execution_guidance|paper_entry_guard")
		value = fs.String("value", "", "true|false")
		reason = fs.String("reason", "", "required audit reason")
		by = fs.String("by", "", "operator name")
	}
	if command == "log" {
		since = fs.String("since", "", "inclusive UTC date YYYY-MM-DD")
	}
	fs.Parse(args[1:])
	if fs.NArg() != 0 {
		fail(fmt.Errorf("unexpected positional arguments"))
	}
	mode := "ro"
	if command == "set" {
		if *strategy == "" || (*value != "true" && *value != "false") {
			fail(fmt.Errorf("set requires -strategy and -value true|false"))
		}
		mode = "rw"
	}
	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode="+mode+"&_pragma=busy_timeout(5000)")
	if err != nil {
		fail(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	switch command {
	case "get":
		query := `SELECT id FROM strategies`
		var params []any
		if *strategy != "" {
			query += ` WHERE id=?`
			params = append(params, *strategy)
		}
		rows, err := db.QueryContext(ctx, query+` ORDER BY id`, params...)
		if err != nil {
			fail(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				fail(err)
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			fail(err)
		}
		if *strategy != "" && len(ids) == 0 {
			fail(sql.ErrNoRows)
		}
		for _, id := range ids {
			flags, err := store.GetStrategyFlags(ctx, db, id)
			if err != nil {
				fail(err)
			}
			fmt.Printf("strategy_id=%s", id)
			for _, f := range store.GateFlags {
				fmt.Printf(" %s=%t", f, flags[f])
			}
			fmt.Println()
		}
	case "set":
		if err := store.MigrateStrategyFlagAudit(db, false); err != nil {
			fail(err)
		}
		changed, err := store.SetStrategyFlag(ctx, db, *strategy, *gate, *value == "true", *by, *reason, time.Now())
		if err != nil {
			fail(err)
		}
		if !changed {
			fmt.Println("unchanged")
			return
		}
		// One pooled connection keeps last_insert_rowid tied to our audit insert.
		var id int64
		var old, next string
		if err := db.QueryRowContext(ctx, `SELECT id, old_value, new_value FROM strategy_config_changes WHERE id=last_insert_rowid()`).Scan(&id, &old, &next); err != nil {
			fail(err)
		}
		fmt.Printf("strategy_id=%s %s: %s → %s audit_id=%d\n", *strategy, *gate, old, next, id)
	case "log":
		query := `SELECT id, strategy_id, field, old_value, new_value, changed_at, changed_by, reason FROM strategy_config_changes WHERE 1=1`
		var params []any
		if *strategy != "" {
			query += ` AND strategy_id=?`
			params = append(params, *strategy)
		}
		if *since != "" {
			t, err := time.Parse("2006-01-02", *since)
			if err != nil {
				fail(err)
			}
			query += ` AND changed_at>=?`
			params = append(params, t.UnixMilli())
		}
		rows, err := db.QueryContext(ctx, query+` ORDER BY changed_at,id`, params...)
		if err != nil {
			fail(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, at int64
			var strategyID, field, old, next, operator, why string
			if err := rows.Scan(&id, &strategyID, &field, &old, &next, &at, &operator, &why); err != nil {
				fail(err)
			}
			fmt.Printf("%d %s strategy_id=%s %s: %s → %s by=%q reason=%q\n", id, time.UnixMilli(at).UTC().Format(time.RFC3339Nano), strategyID, field, old, next, operator, why)
		}
		if err := rows.Err(); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "strategy-flag:", err)
	os.Exit(1)
}
