package shadoweval

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// ReportQuery selects which shadow rows to score and how to group them.
type ReportQuery struct {
	Horizon   string    // 15m | 1h | 4h | 24h
	GroupBy   []string  // any of: trader, model, prompt_version, prompt_hash, symbol, direction, source, status, exchange, regime, executed
	Since     time.Time // inclusive
	Until     time.Time // exclusive; zero = no bound
	Executed  string    // "", "1", "0"
	Source    string    // "", "ai", "jev"
	MinN      int       // groups smaller than this are omitted
	Bootstrap int       // resamples (default 1000)
}

// GroupStats is expectancy and calibration for one group, net of RoundTripFeePct.
type GroupStats struct {
	Key          map[string]string `json:"key"`
	N            int               `json:"n"`
	HitRate      float64           `json:"hit_rate"`
	MeanNetPct   float64           `json:"mean_net_pct"`
	MeanGrossPct float64           `json:"mean_gross_pct"`
	CILowPct     float64           `json:"ci95_low_pct"`
	CIHighPct    float64           `json:"ci95_high_pct"`
	Calibration  []CalBucket       `json:"calibration"`
}

// CalBucket compares stated confidence/probability with realized hit rate.
type CalBucket struct {
	Bucket     string  `json:"bucket"`
	N          int     `json:"n"`
	MeanStated float64 `json:"mean_stated"`
	HitRate    float64 `json:"hit_rate"`
	MeanNetPct float64 `json:"mean_net_pct"`
}

type Report struct {
	Horizon     string       `json:"horizon"`
	FeePct      float64      `json:"round_trip_fee_pct"`
	GroupBy     []string     `json:"group_by"`
	TotalRows   int          `json:"total_rows"`
	Groups      []GroupStats `json:"groups"`
	GeneratedAt time.Time    `json:"generated_at"`
}

var groupCols = map[string]string{
	"trader": "trader_id", "model": "model", "prompt_version": "prompt_version", "prompt_hash": "prompt_hash",
	"symbol": "symbol", "direction": "direction", "source": "source", "status": "status", "exchange": "exchange",
	"regime": "regime", "executed": "executed", "timeframe": "timeframe",
}

// ValidGroup reports whether g is an allowed group-by field.
func ValidGroup(g string) bool { _, ok := groupCols[g]; return ok }

// BuildReport scores directional (long/short) decisions with a filled horizon.
func BuildReport(ctx context.Context, db *sql.DB, q ReportQuery) (*Report, error) {
	col, ok := HorizonColumn(q.Horizon)
	if !ok {
		return nil, fmt.Errorf("unknown horizon %q", q.Horizon)
	}
	if len(q.GroupBy) == 0 {
		q.GroupBy = []string{"trader"}
	}
	sel := make([]string, 0, len(q.GroupBy))
	for _, g := range q.GroupBy {
		c, ok := groupCols[g]
		if !ok {
			return nil, fmt.Errorf("unknown group %q", g)
		}
		sel = append(sel, "CAST("+c+" AS TEXT)")
	}
	if q.Bootstrap <= 0 {
		q.Bootstrap = 1000
	}
	sqlq := fmt.Sprintf(`SELECT %s, %s, confidence, probability FROM shadow_decisions
		WHERE direction IN ('long','short') AND %s IS NOT NULL AND decided_at >= ?`, strings.Join(sel, ","), col, col)
	args := []any{q.Since.UnixMilli()}
	if !q.Until.IsZero() {
		sqlq += ` AND decided_at < ?`
		args = append(args, q.Until.UnixMilli())
	}
	if q.Executed == "0" || q.Executed == "1" {
		sqlq += ` AND executed = ?`
		args = append(args, q.Executed)
	}
	if q.Source != "" {
		sqlq += ` AND source = ?`
		args = append(args, q.Source)
	}
	rows, err := db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type obs struct {
		net, stated float64
		hasStated   bool
	}
	groups := map[string][]obs{}
	keys := map[string][]string{}
	total := 0
	for rows.Next() {
		vals := make([]sql.NullString, len(sel))
		dst := make([]any, 0, len(sel)+3)
		for i := range vals {
			dst = append(dst, &vals[i])
		}
		var ret float64
		var conf, prob sql.NullFloat64
		dst = append(dst, &ret, &conf, &prob)
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		kv := make([]string, len(vals))
		for i, v := range vals {
			kv[i] = v.String
		}
		k := strings.Join(kv, "\x1f")
		o := obs{net: ret - RoundTripFeePct}
		if prob.Valid {
			o.stated, o.hasStated = prob.Float64*100, true
		} else if conf.Valid {
			o.stated, o.hasStated = conf.Float64, true
		}
		groups[k] = append(groups[k], o)
		keys[k] = kv
		total++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rep := &Report{Horizon: q.Horizon, FeePct: RoundTripFeePct, GroupBy: q.GroupBy, TotalRows: total, GeneratedAt: time.Now().UTC()}
	rng := rand.New(rand.NewSource(42))
	for k, list := range groups {
		if len(list) < q.MinN || len(list) == 0 {
			continue
		}
		nets := make([]float64, len(list))
		hits := 0
		for i, o := range list {
			nets[i] = o.net
			if o.net > 0 {
				hits++
			}
		}
		g := GroupStats{Key: map[string]string{}, N: len(list), HitRate: float64(hits) / float64(len(list)), MeanNetPct: mean(nets)}
		g.MeanGrossPct = g.MeanNetPct + RoundTripFeePct
		g.CILowPct, g.CIHighPct = BootstrapCI(nets, q.Bootstrap, rng)
		for i, name := range q.GroupBy {
			g.Key[name] = keys[k][i]
		}
		buckets := map[int][]obs{}
		for _, o := range list {
			if !o.hasStated {
				continue
			}
			b := int(math.Floor(o.stated/10)) * 10
			if b >= 100 {
				b = 90
			}
			buckets[b] = append(buckets[b], o)
		}
		bk := make([]int, 0, len(buckets))
		for b := range buckets {
			bk = append(bk, b)
		}
		sort.Ints(bk)
		for _, b := range bk {
			bl := buckets[b]
			var st, nt float64
			h := 0
			for _, o := range bl {
				st += o.stated
				nt += o.net
				if o.net > 0 {
					h++
				}
			}
			n := float64(len(bl))
			g.Calibration = append(g.Calibration, CalBucket{Bucket: fmt.Sprintf("%d-%d", b, b+10), N: len(bl),
				MeanStated: st / n, HitRate: float64(h) / n, MeanNetPct: nt / n})
		}
		rep.Groups = append(rep.Groups, g)
	}
	sort.Slice(rep.Groups, func(i, j int) bool {
		if rep.Groups[i].N != rep.Groups[j].N {
			return rep.Groups[i].N > rep.Groups[j].N
		}
		return fmt.Sprint(rep.Groups[i].Key) < fmt.Sprint(rep.Groups[j].Key)
	})
	return rep, nil
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// BootstrapCI returns the 2.5%/97.5% percentile bootstrap interval of the mean.
func BootstrapCI(xs []float64, resamples int, rng *rand.Rand) (float64, float64) {
	if len(xs) < 2 {
		m := mean(xs)
		return m, m
	}
	means := make([]float64, resamples)
	for r := 0; r < resamples; r++ {
		s := 0.0
		for i := 0; i < len(xs); i++ {
			s += xs[rng.Intn(len(xs))]
		}
		means[r] = s / float64(len(xs))
	}
	sort.Float64s(means)
	lo := means[int(0.025*float64(resamples))]
	hi := means[int(math.Min(float64(resamples-1), 0.975*float64(resamples)))]
	return lo, hi
}

// WriteText renders a compact plain-text report.
func WriteText(w io.Writer, r *Report, showCal bool) {
	fmt.Fprintf(w, "shadow-eval horizon=%s fee=%.2f%% rows=%d group_by=%s\n", r.Horizon, r.FeePct, r.TotalRows, strings.Join(r.GroupBy, ","))
	fmt.Fprintf(w, "%-48s %6s %6s %9s %9s %21s\n", "group", "n", "hit%", "gross%", "net%", "net CI95%")
	for _, g := range r.Groups {
		parts := make([]string, 0, len(r.GroupBy))
		for _, k := range r.GroupBy {
			parts = append(parts, g.Key[k])
		}
		name := strings.Join(parts, " | ")
		if len(name) > 48 {
			name = name[:48]
		}
		flag := ""
		if g.CILowPct > 0 {
			flag = " *edge"
		}
		fmt.Fprintf(w, "%-48s %6d %6.1f %+9.3f %+9.3f [%+8.3f,%+8.3f]%s\n", name, g.N, g.HitRate*100, g.MeanGrossPct, g.MeanNetPct, g.CILowPct, g.CIHighPct, flag)
		if showCal {
			for _, c := range g.Calibration {
				fmt.Fprintf(w, "    stated %-7s n=%-5d mean_stated=%5.1f realized_hit=%5.1f%% net=%+.3f%%\n", c.Bucket, c.N, c.MeanStated, c.HitRate*100, c.MeanNetPct)
			}
		}
	}
}
