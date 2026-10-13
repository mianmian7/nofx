// Command shadow-report prints the shadow evaluation report from a nofx
// SQLite database opened read-only.
//
//	go run ./cmd/shadow-report -db data/data.db -horizon 1h -group trader,direction -cal
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"nofx/shadoweval"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/data.db", "SQLite database path (opened read-only)")
	horizon := flag.String("horizon", "1h", "15m|1h|4h|24h")
	group := flag.String("group", "trader", "comma list: trader,model,prompt_version,prompt_hash,symbol,direction,source,status,exchange,regime,executed,timeframe")
	since := flag.String("since", "2026-10-07", "start date (YYYY-MM-DD or RFC3339)")
	executed := flag.String("executed", "", "filter: 0|1")
	source := flag.String("source", "", "filter: ai|jev")
	minN := flag.Int("min-n", 1, "omit groups smaller than this")
	cal := flag.Bool("cal", false, "print calibration buckets")
	asJSON := flag.Bool("json", false, "print JSON")
	flag.Parse()

	t, err := time.Parse("2006-01-02", *since)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, *since); err != nil {
			fail(err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		fail(err)
	}
	defer db.Close()
	q := shadoweval.ReportQuery{Horizon: *horizon, Since: t, Executed: *executed, Source: *source, MinN: *minN}
	for _, g := range strings.Split(*group, ",") {
		if g = strings.TrimSpace(g); g != "" {
			q.GroupBy = append(q.GroupBy, g)
		}
	}
	rep, err := shadoweval.BuildReport(context.Background(), db, q)
	if err != nil {
		fail(err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}
	shadoweval.WriteText(os.Stdout, rep, *cal)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "shadow-report:", err)
	os.Exit(1)
}
