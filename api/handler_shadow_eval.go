package api

import (
	"net/http"
	"nofx/shadoweval"
	"nofx/store"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// handleShadowEvalReport is read-only: it aggregates shadow_decisions.
// Query: horizon=15m|1h|4h|24h, group_by=trader,model,..., since=RFC3339|YYYY-MM-DD,
// until=..., executed=0|1, source=ai|jev, min_n=N.
func (s *Server) handleShadowEvalReport(c *gin.Context) {
	if s.store == nil || s.store.DBType() != store.DBTypeSQLite {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Shadow evaluation requires SQLite"})
		return
	}
	q, err := parseShadowQuery(c)
	if err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	db, err := s.store.GormDB().DB()
	if err != nil {
		SafeInternalError(c, "Failed to open database", err)
		return
	}
	rep, err := shadoweval.BuildReport(c.Request.Context(), db, q)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Shadow evaluation has not started yet"})
			return
		}
		SafeInternalError(c, "Failed to build shadow report", err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

type badQuery string

func (e badQuery) Error() string { return string(e) }

func parseShadowTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

func parseShadowQuery(c *gin.Context) (shadoweval.ReportQuery, error) {
	q := shadoweval.ReportQuery{Horizon: c.DefaultQuery("horizon", "1h"), MinN: 1}
	if _, ok := shadoweval.HorizonColumn(q.Horizon); !ok {
		return q, badQuery("horizon must be one of 15m, 1h, 4h, 24h")
	}
	for _, g := range strings.Split(c.DefaultQuery("group_by", "trader"), ",") {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if !shadoweval.ValidGroup(g) {
			return q, badQuery("unsupported group_by field")
		}
		q.GroupBy = append(q.GroupBy, g)
	}
	if len(q.GroupBy) > 4 {
		return q, badQuery("at most 4 group_by fields")
	}
	since := c.DefaultQuery("since", "2026-10-07")
	t, err := parseShadowTime(since)
	if err != nil {
		return q, badQuery("since must be RFC3339 or YYYY-MM-DD")
	}
	q.Since = t
	if v := c.Query("until"); v != "" {
		if q.Until, err = parseShadowTime(v); err != nil {
			return q, badQuery("until must be RFC3339 or YYYY-MM-DD")
		}
	}
	switch v := c.Query("executed"); v {
	case "", "0", "1":
		q.Executed = v
	default:
		return q, badQuery("executed must be 0 or 1")
	}
	switch v := c.Query("source"); v {
	case "", "ai", "jev":
		q.Source = v
	default:
		return q, badQuery("source must be ai or jev")
	}
	if v := c.Query("min_n"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100000 {
			return q, badQuery("min_n must be a positive integer")
		}
		q.MinN = n
	}
	return q, nil
}
