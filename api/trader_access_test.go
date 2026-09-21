package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nofx/manager"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupTraderAccessAPI(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	st, err := store.New(t.TempDir() + "/access.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, userID := range []string{"alice", "bob"} {
		if err := st.Trader().Create(&store.Trader{ID: userID + "-trader", UserID: userID, Name: userID, AIModelID: "model", ExchangeID: "paper", InitialBalance: 100}); err != nil {
			t.Fatal(err)
		}
		if err := st.Equity().Save(&store.EquitySnapshot{TraderID: userID + "-trader", TotalEquity: 100}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: st, traderManager: manager.NewTraderManager()}
	router := gin.New()
	router.Use(gin.RecoveryWithWriter(io.Discard))
	router.Use(func(c *gin.Context) { c.Set("user_id", "alice"); c.Next() })
	router.DELETE("/traders/:id", s.handleDeleteTrader)
	router.PUT("/traders/:id/prompt", s.handleUpdateTraderPrompt)
	router.PUT("/traders/:id/competition", s.handleToggleCompetition)
	router.GET("/traders/:id/grid-risk", s.handleGetGridRiskInfo)
	return s, router
}

func TestTraderHandlersRejectUnownedIDsBeforeRuntimeAccess(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodDelete, "/traders/bob-trader", ""},
		{http.MethodPut, "/traders/bob-trader/prompt", `{"custom_prompt":"untrusted","override_base_prompt":true}`},
		{http.MethodPut, "/traders/bob-trader/competition", `{"show_in_competition":false}`},
		{http.MethodGet, "/traders/bob-trader/grid-risk", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s, router := setupTraderAccessAPI(t)
			// A nil manager is a tripwire: rejection must precede ANY runtime
			// lookup, update, stop or removal, even if the ID is known globally.
			s.traderManager = nil
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404: %s", resp.Code, resp.Body.String())
			}
			other, err := s.store.Trader().GetByID("bob-trader")
			if err != nil || other.CustomPrompt != "" || !other.ShowInCompetition {
				t.Fatalf("other trader changed: %+v, %v", other, err)
			}
			snapshots, err := s.store.Equity().GetLatest("bob-trader", 10)
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("other equity history changed: %+v, %v", snapshots, err)
			}
		})
	}
}

func TestTraderHandlersAllowOwnerConfigUpdates(t *testing.T) {
	s, router := setupTraderAccessAPI(t)
	for _, tc := range []struct{ path, body string }{
		{"/traders/alice-trader/prompt", `{"custom_prompt":"owner prompt","override_base_prompt":true}`},
		{"/traders/alice-trader/competition", `{"show_in_competition":false}`},
	} {
		req := httptest.NewRequest(http.MethodPut, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("owner update failed: %d %s", resp.Code, resp.Body.String())
		}
	}
	got, err := s.store.Trader().GetByID("alice-trader")
	if err != nil || got.CustomPrompt != "owner prompt" || !got.OverrideBasePrompt || got.ShowInCompetition {
		t.Fatalf("owner configuration not saved: %+v, %v", got, err)
	}
}

func TestGetTraderFromQueryReadsOneOwnedRow(t *testing.T) {
	s, _ := setupTraderAccessAPI(t)
	for i := range 5 {
		if err := s.store.Trader().Create(&store.Trader{ID: fmt.Sprintf("older-%d", i), UserID: "alice", Name: "Older", AIModelID: "model", ExchangeID: "paper", CreatedAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	var rowsRead int64
	var queryCount, reloads int
	if err := s.store.GormDB().Callback().Query().After("gorm:query").Register("test:count_trader_rows", func(db *gorm.DB) {
		if db.Statement.Table == "traders" {
			queryCount++
			rowsRead += db.RowsAffected
		}
	}); err != nil {
		t.Fatal(err)
	}
	s.loadUserTraders = func(_ *store.Store, userID string) error {
		if userID != "alice" {
			t.Fatalf("reload requested for wrong user: %s", userID)
		}
		reloads++
		return nil
	}
	for _, query := range []string{"", "?trader_id=alice-trader"} {
		rowsRead, queryCount = 0, 0
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/account"+query, nil)
		c.Set("user_id", "alice")
		_, id, err := s.getTraderFromQuery(c)
		if err != nil || id != "alice-trader" {
			t.Fatalf("resolve %q: id=%q, err=%v", query, id, err)
		}
		if queryCount != 1 || rowsRead != 1 {
			t.Fatalf("expected one query reading one trader; queries=%d rows=%d", queryCount, rowsRead)
		}
	}
	if reloads != 0 {
		t.Fatalf("query resolution must not restore missing runtimes: %d", reloads)
	}
	// Even an account with no traders must be rejected before consulting the
	// global manager. It must not fall back to a different owner's runtime.
	s.traderManager = nil
	for _, userID := range []string{"alice", "empty-user"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/account?trader_id=bob-trader", nil)
		c.Set("user_id", userID)
		if _, _, err := s.getTraderFromQuery(c); err == nil {
			t.Fatal("unowned query must fail")
		}
	}
}

func TestDeleteTraderHandlerPreservesOtherAccount(t *testing.T) {
	s, router := setupTraderAccessAPI(t)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodDelete, "/traders/alice-trader", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("owner delete failed: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := s.store.Trader().GetByID("bob-trader"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id    string
		count int
	}{{"alice-trader", 0}, {"bob-trader", 1}} {
		snapshots, err := s.store.Equity().GetLatest(tc.id, 10)
		if err != nil || len(snapshots) != tc.count {
			t.Fatalf("snapshots for %s: %d, %v", tc.id, len(snapshots), err)
		}
	}
}
