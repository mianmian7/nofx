package shadoweval

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type BalanceRow struct {
	Day, TraderID, Model                                        string
	OpenLong, OpenShort, Wait, WaitLong, WaitShort, Hold, Total int
}

// BuildBalance groups all non-placeholder decisions by local day and trader.
// Model is the last observed model in each group; Total includes other actions.
func BuildBalance(ctx context.Context, db *sql.DB, since time.Time, loc *time.Location) ([]BalanceRow, error) {
	if loc == nil {
		loc = time.UTC
	}
	rows, err := db.QueryContext(ctx, `SELECT decided_at, trader_id, model, action, direction
		FROM shadow_decisions WHERE decided_at >= ? AND action != 'none' ORDER BY decided_at, id`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ day, trader string }
	groups := map[key]*BalanceRow{}
	for rows.Next() {
		var at int64
		var trader, model, action, direction string
		if err := rows.Scan(&at, &trader, &model, &action, &direction); err != nil {
			return nil, err
		}
		day := time.UnixMilli(at).In(loc).Format("2006-01-02")
		k := key{day, trader}
		r := groups[k]
		if r == nil {
			r = &BalanceRow{Day: day, TraderID: trader}
			groups[k] = r
		}
		r.Model = model
		r.Total++
		switch {
		case strings.HasPrefix(action, "open_long"):
			r.OpenLong++
		case strings.HasPrefix(action, "open_short"):
			r.OpenShort++
		case action == "wait":
			r.Wait++
			if direction == "long" {
				r.WaitLong++
			} else if direction == "short" {
				r.WaitShort++
			}
		case action == "hold":
			r.Hold++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]BalanceRow, 0, len(groups))
	for _, r := range groups {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].TraderID < out[j].TraderID
	})
	return out, nil
}

func WriteBalanceText(w io.Writer, rows []BalanceRow) {
	fmt.Fprintln(w, "day trader model total open_long open_short wait wait_long wait_short hold open_long% open_short% wait%")
	for _, r := range rows {
		share := func(n int) float64 {
			if r.Total == 0 {
				return 0
			}
			return 100 * float64(n) / float64(r.Total)
		}
		fmt.Fprintf(w, "%s %s %s %d %d %d %d %d %d %d %.1f%% %.1f%% %.1f%%\n",
			r.Day, r.TraderID, r.Model, r.Total, r.OpenLong, r.OpenShort, r.Wait, r.WaitLong, r.WaitShort, r.Hold,
			share(r.OpenLong), share(r.OpenShort), share(r.Wait))
	}
}

// CountBearishVetoes returns MM-DD keys: the logger contains no year or zone.
// Dates are interpreted in loc without converting to a different timezone.
func CountBearishVetoes(r io.Reader, loc *time.Location) (map[string]int, error) {
	if loc == nil {
		loc = time.UTC
	}
	counts := map[string]int{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "blocked by") || !strings.Contains(line, "bearish_only bias") || len(line) < 6 || line[5] != ' ' {
			continue
		}
		// A leap year validates MM-DD even for February 29.
		if _, err := time.ParseInLocation("2006-01-02", "2000-"+line[:5], loc); err == nil {
			counts[line[:5]]++
		}
	}
	return counts, scanner.Err()
}
