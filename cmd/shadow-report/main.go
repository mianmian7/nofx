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
	"sort"
	"strings"
	"time"

	"nofx/shadoweval"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/data.db", "SQLite database path (opened read-only)")
	horizon := flag.String("horizon", "1h", "15m|1h|4h|24h")
	group := flag.String("group", "trader", "comma list: trader,model,prompt_version,prompt_hash,symbol,direction,source,status,exchange,regime,executed,timeframe,gate_flags")
	since := flag.String("since", "2026-10-07", "start date (YYYY-MM-DD or RFC3339)")
	executed := flag.String("executed", "", "filter: 0|1")
	source := flag.String("source", "", "filter: ai|jev")
	minN := flag.Int("min-n", 1, "omit groups smaller than this")
	cal := flag.Bool("cal", false, "print calibration buckets")
	asJSON := flag.Bool("json", false, "print JSON")
	balance := flag.Bool("balance", false, "print daily direction balance instead of expectancy")
	vetoLog := flag.String("veto-log", "", "with -balance, append bearish veto counts from this log (MM-DD; log has no year)")
	tz := flag.String("tz", "Asia/Singapore", "timezone for balance days and -since dates")
	flag.Parse()

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		fail(err)
	}
	dateLoc := time.UTC
	if *balance {
		dateLoc = loc
	}
	t, err := time.ParseInLocation("2006-01-02", *since, dateLoc)
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
	if *balance {
		rows, err := shadoweval.BuildBalance(context.Background(), db, t, loc)
		if err != nil {
			fail(err)
		}
		shadoweval.WriteBalanceText(os.Stdout, rows)
		if *vetoLog != "" {
			f, err := os.Open(*vetoLog)
			if err != nil {
				fail(err)
			}
			counts, err := shadoweval.CountBearishVetoes(f, loc)
			f.Close()
			if err != nil {
				fail(err)
			}
			days := make([]string, 0, len(counts))
			for day := range counts {
				days = append(days, day)
			}
			sort.Strings(days)
			fmt.Println("day bearish_vetoes")
			for _, day := range days {
				fmt.Printf("%s %d\n", day, counts[day])
			}
		}
		return
	}
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
