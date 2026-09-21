package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nofx/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupStatusTestAPI(t *testing.T, userID string) (*Server, *gin.Engine) {
	t.Helper()
	s, _ := setupTraderAccessAPI(t)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	router.GET("/api/status", s.handleStatus)
	router.GET("/api/account", s.handleAccount)
	router.GET("/api/positions", s.handlePositions)
	return s, router
}

func TestDashboardStatusPollingDoesNotRestoreOrWrite(t *testing.T) {
	s, router := setupStatusTestAPI(t, "alice")
	for _, userID := range []string{"alice", "bob"} {
		id := userID + "-trader"
		if err := s.store.Trader().UpdateStatus(userID, id, true); err != nil {
			t.Fatal(err)
		}
		if err := s.store.Paper().SavePaperState(id, []byte(`{"balance":75,"fills":[{"order_id":42}]}`)); err != nil {
			t.Fatal(err)
		}
	}
	// Snapshot full persisted records, including timestamps, before polling.
	snapshot := func() interface{} {
		t.Helper()
		var traders []store.Trader
		var ledgers []store.PaperAccountState
		var equity []store.EquitySnapshot
		for _, query := range []*gorm.DB{
			s.store.GormDB().Order("id").Find(&traders),
			s.store.GormDB().Order("trader_id").Find(&ledgers),
			s.store.GormDB().Order("id").Find(&equity),
		} {
			if query.Error != nil {
				t.Fatal(query.Error)
			}
		}
		return []interface{}{traders, ledgers, equity}
	}
	before := snapshot()
	var reloads, writes atomic.Int32
	// Intercept the dangerous seam: the old GET path calls this hook, but the
	// regression test must never actually start a runtime or contact an exchange.
	s.loadUserTraders = func(*store.Store, string) error {
		reloads.Add(1)
		return errors.New("unexpected runtime restoration during GET")
	}
	countWrite := func(*gorm.DB) { writes.Add(1) }
	callbacks := s.store.GormDB().Callback()
	for _, err := range []error{
		callbacks.Create().Before("gorm:create").Register("test:status_writes", countWrite),
		callbacks.Update().Before("gorm:update").Register("test:status_writes", countWrite),
		callbacks.Delete().Before("gorm:delete").Register("test:status_writes", countWrite),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 2 {
		for _, endpoint := range []string{"status", "account", "positions"} {
			for _, query := range []string{"", "?trader_id=alice-trader"} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp := httptest.NewRecorder()
					router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/"+endpoint+query, nil))
					want := http.StatusNotFound
					if endpoint == "status" {
						want = http.StatusServiceUnavailable
					}
					if resp.Code != want {
						t.Errorf("%s%s: status=%d want=%d body=%s", endpoint, query, resp.Code, want, resp.Body.String())
					}
				}()
			}
		}
	}
	wg.Wait()
	if got := reloads.Load(); got != 0 {
		t.Errorf("GET polling invoked runtime restoration %d times, want 0", got)
	}
	if got := writes.Load(); got != 0 {
		t.Errorf("GET polling attempted %d database writes, want 0", got)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Error("GET polling changed persisted traders, Paper ledger or equity history")
	}
	if ids := s.traderManager.GetTraderIDs(); len(ids) != 0 {
		t.Errorf("GET polling created runtime instances: %v", ids)
	}
}

func TestStatusErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name, userID, query, message string
		status                       int
		databaseFailure              bool
	}{
		{"unowned", "alice", "?trader_id=bob-trader", "Trader does not exist or no access permission", http.StatusNotFound, false},
		{"missing", "alice", "?trader_id=missing", "Trader does not exist or no access permission", http.StatusNotFound, false},
		{"no traders", "empty-user", "", "Trader does not exist or no access permission", http.StatusNotFound, false},
		{"query failure", "alice", "?trader_id=alice-trader", "Load trader failed", http.StatusInternalServerError, true},
		{"default query failure", "alice", "", "Load trader failed", http.StatusInternalServerError, true},
		{"runtime absent", "alice", "?trader_id=alice-trader", "Trader runtime is not loaded", http.StatusServiceUnavailable, false},
		{"default runtime absent", "alice", "", "Trader runtime is not loaded", http.StatusServiceUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, router := setupStatusTestAPI(t, tc.userID)
			var reloads int
			s.loadUserTraders = func(*store.Store, string) error { reloads++; return nil }
			if tc.status != http.StatusServiceUnavailable {
				// Failed ownership/DB checks must precede any runtime lookup.
				s.traderManager = nil
			}
			if tc.databaseFailure {
				if err := s.store.GormDB().Callback().Query().Before("gorm:query").Register("test:status_db_failure", func(db *gorm.DB) {
					if db.Statement.Table == "traders" {
						db.AddError(errors.New("private database failure details"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/status"+tc.query, nil))
			if resp.Code != tc.status {
				t.Errorf("status=%d want=%d body=%s", resp.Code, tc.status, resp.Body.String())
			}
			var body map[string]interface{}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if want := map[string]interface{}{"error": tc.message}; !reflect.DeepEqual(body, want) {
				t.Errorf("body=%v want=%v", body, want)
			}
			if reloads != 0 {
				t.Errorf("GET status invoked runtime restoration %d times", reloads)
			}
		})
	}
}

func TestStatusLoadedRuntimePreservesResponseAndDefaultSelection(t *testing.T) {
	s, router := setupStatusTestAPI(t, "alice")
	if err := s.store.AIModel().Create("alice", "status-model", "Status Model", "openai", true, "test-api-key", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Strategy().Create(&store.Strategy{ID: "status-strategy", UserID: "alice", Name: "Status Strategy", Config: "{}"}); err != nil {
		t.Fatal(err)
	}
	exchangeID, err := s.store.Exchange().Create("alice", "binance", "Status Paper Market", true,
		"test-api-key", "test-secret-key", "", true, "", false, false, "", "", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"alice-loaded-older", "alice-loaded-latest"} {
		if err := s.store.Trader().Create(&store.Trader{
			ID: id, UserID: "alice", Name: id, AIModelID: "status-model", ExchangeID: exchangeID,
			StrategyID: "status-strategy", ExecutionMode: "paper", InitialBalance: 100,
			ScanIntervalMinutes: 5, CreatedAt: time.Date(2200, 1, i+1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A newer record belonging to another account must never win the default.
	if err := s.store.GormDB().Model(&store.Trader{}).Where("id = ?", "bob-trader").Update("created_at", time.Date(2201, 1, 1, 0, 0, 0, 0, time.UTC)).Error; err != nil {
		t.Fatal(err)
	}
	// Fixture setup only: Paper clients, no monitors, no decision loop or orders.
	if err := s.traderManager.LoadUserTradersFromStoreWithoutAutoStart(s.store, "alice"); err != nil {
		t.Fatal(err)
	}
	s.loadUserTraders = func(*store.Store, string) error {
		t.Error("GET status must not restore runtimes")
		return errors.New("unexpected restore")
	}
	for _, tc := range []struct{ query, id string }{
		{"?trader_id=alice-loaded-older", "alice-loaded-older"},
		{"", "alice-loaded-latest"},
		{"?trader_id=", "alice-loaded-latest"},
	} {
		runtime, err := s.traderManager.GetTrader(tc.id)
		if err != nil {
			t.Fatalf("fixture runtime %s: %v", tc.id, err)
		}
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/status"+tc.query, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got, want map[string]interface{}
		if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(runtime.GetStatus())
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || got["trader_id"] != tc.id || got["is_running"] != false || got["call_count"] != float64(0) {
			t.Errorf("response changed for %q: got=%v want=%v", tc.query, got, want)
		}
	}
}
