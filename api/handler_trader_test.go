package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"nofx/crypto"
	"nofx/manager"
	"nofx/store"

	"github.com/gin-gonic/gin"
)

func TestValidateStartConfirmationSeparatesPaperAndLive(t *testing.T) {
	if err := validateStartConfirmation("paper", ""); err != nil {
		t.Fatalf("paper start must not require live_confirm: %v", err)
	}
	if err := validateStartConfirmation("live", ""); err == nil {
		t.Fatal("live start without live_confirm must fail")
	}
	if err := validateStartConfirmation("live", "true"); err != nil {
		t.Fatalf("live start with live_confirm=true: %v", err)
	}
}

func TestNormalizeStartupDelayRequiresDelayBelowScanInterval(t *testing.T) {
	if delay, err := normalizeStartupDelay(15, 14); err != nil || delay != 14 {
		t.Fatalf("normalizeStartupDelay(15, 14) = (%d, %v), want (14, nil)", delay, err)
	}
	if _, err := normalizeStartupDelay(15, 15); err == nil {
		t.Fatal("startup delay equal to scan interval must be rejected")
	}
	if _, err := normalizeStartupDelay(15, -1); err == nil {
		t.Fatal("negative startup delay must be rejected")
	}
}

func TestValidateFallbackAIModelSelection(t *testing.T) {
	models := []*store.AIModel{
		{ID: "primary", Enabled: true, APIKey: crypto.EncryptedString("primary-key")},
		{ID: "fallback-1", Enabled: true, APIKey: crypto.EncryptedString("fallback-key")},
		{ID: "disabled", Enabled: false, APIKey: crypto.EncryptedString("disabled-key")},
		{ID: "missing-key", Enabled: true},
	}

	validated, err := validateFallbackAIModelSelection(
		"primary",
		[]string{"fallback-1", "primary", "fallback-1"},
		models,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated) != 1 || validated[0] != "fallback-1" {
		t.Fatalf("validated fallback IDs = %#v, want [fallback-1]", validated)
	}

	for _, testCase := range []struct {
		name string
		id   string
	}{
		{name: "unknown", id: "not-configured"},
		{name: "disabled", id: "disabled"},
		{name: "missing credentials", id: "missing-key"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := validateFallbackAIModelSelection("primary", []string{testCase.id}, models); err == nil {
				t.Fatalf("fallback model %q must be rejected", testCase.id)
			}
		})
	}
}

func TestResolveTraderPrimaryModelNameUsesVerifiedCatalog(t *testing.T) {
	model := &store.AIModel{
		CustomModelName: "gpt-5.6-luna",
		ModelNames:      store.EncodeStringList([]string{"gpt-5.6-luna", "gpt-5.6-terra"}),
	}

	if got, err := resolveTraderPrimaryModelName(model, "gpt-5.6-terra"); err != nil || got != "gpt-5.6-terra" {
		t.Fatalf("explicit primary = (%q, %v), want (gpt-5.6-terra, nil)", got, err)
	}
	if got, err := resolveTraderPrimaryModelName(model, ""); err != nil || got != "gpt-5.6-luna" {
		t.Fatalf("default primary = (%q, %v), want (gpt-5.6-luna, nil)", got, err)
	}
	if _, err := resolveTraderPrimaryModelName(model, "not-available"); err == nil {
		t.Fatal("primary model outside the verified catalog must be rejected")
	}
}

type shutdownRecorder struct {
	called bool
}

func (r *shutdownRecorder) Shutdown() {
	r.called = true
}

func TestStopManagedTraderUsesShutdownForAlreadyStoppedTrader(t *testing.T) {
	recorder := &shutdownRecorder{}
	stopManagedTrader(recorder)
	if !recorder.called {
		t.Fatal("stopManagedTrader must invoke Shutdown for a complete trader stop")
	}
}

func TestUpdateTraderAllowsPaperResetWhenInitialBalanceUnchanged(t *testing.T) {
	const (
		userID   = "paper-reset-user"
		traderID = "paper-reset-trader"
		modelID  = "paper-reset-model"
	)

	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.User().Create(&store.User{ID: userID, Email: "paper-reset@example.com", PasswordHash: "test"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AIModel().Create(userID, modelID, "Paper Reset Model", "openai", true, "test-api-key", ""); err != nil {
		t.Fatalf("create AI model: %v", err)
	}
	strategyID := "paper-reset-strategy"
	if err := st.Strategy().Create(&store.Strategy{ID: strategyID, UserID: userID, Name: "Paper Reset Strategy", Config: "{}"}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	exchangeID, err := st.Exchange().Create(
		userID, "binance", "Paper Market", true,
		"test-api-key", "test-secret-key", "", true,
		"", false, false,
		"", "", "",
		"", "", "", 0,
	)
	if err != nil {
		t.Fatalf("create exchange: %v", err)
	}
	if err := st.Trader().Create(&store.Trader{
		ID:                  traderID,
		UserID:              userID,
		Name:                "Paper Reset Trader",
		AIModelID:           modelID,
		ExchangeID:          exchangeID,
		StrategyID:          strategyID,
		ExecutionMode:       "paper",
		InitialBalance:      100,
		ScanIntervalMinutes: 5,
	}); err != nil {
		t.Fatalf("create trader: %v", err)
	}
	if err := st.Paper().SavePaperState(traderID, []byte(`{"balance":75}`)); err != nil {
		t.Fatalf("save paper state: %v", err)
	}

	gin.SetMode(gin.TestMode)
	server := &Server{store: st, traderManager: manager.NewTraderManager()}
	router := gin.New()
	router.PUT("/api/traders/:id", func(c *gin.Context) {
		c.Set("user_id", userID)
		server.handleUpdateTrader(c)
	})
	body := []byte(`{"name":"Paper Reset Trader","ai_model_id":"paper-reset-model","exchange_id":"` + exchangeID + `","execution_mode":"paper","initial_balance":100,"reset_paper_account":true,"scan_interval_minutes":5}`)
	request := httptest.NewRequest(http.MethodPut, "/api/traders/"+traderID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("paper reset with unchanged balance returned %d, body: %s", response.Code, response.Body.String())
	}
	if _, found, err := st.Paper().LoadPaperState(traderID); err != nil {
		t.Fatalf("load paper state: %v", err)
	} else if found {
		t.Fatal("paper reset must clear persisted paper account state")
	}
}

func TestUpdateTraderReturns5xxWhenRuntimeReloadFails(t *testing.T) {
	const (
		userID   = "reload-failure-user"
		traderID = "reload-failure-trader"
		modelID  = "reload-failure-model"
	)

	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.User().Create(&store.User{ID: userID, Email: "reload-failure@example.com", PasswordHash: "test"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AIModel().Create(userID, modelID, "Reload Failure Model", "custom", true, "test-api-key", "http://localhost:8317/v1"); err != nil {
		t.Fatalf("create AI model: %v", err)
	}
	exchangeID, err := st.Exchange().Create(
		userID, "binance", "Reload Failure Market", true,
		"test-api-key", "test-secret-key", "", true,
		"", false, false,
		"", "", "",
		"", "", "", 0,
	)
	if err != nil {
		t.Fatalf("create exchange: %v", err)
	}
	if err := st.Trader().Create(&store.Trader{
		ID:                  traderID,
		UserID:              userID,
		Name:                "Reload Failure Trader",
		AIModelID:           modelID,
		ExchangeID:          exchangeID,
		ExecutionMode:       "paper",
		InitialBalance:      100,
		ScanIntervalMinutes: 5,
	}); err != nil {
		t.Fatalf("create trader: %v", err)
	}

	gin.SetMode(gin.TestMode)
	server := &Server{
		store:         st,
		traderManager: manager.NewTraderManager(),
		loadUserTraders: func(*store.Store, string) error {
			return errors.New("injected runtime reload failure")
		},
	}
	router := gin.New()
	router.PUT("/api/traders/:id", func(c *gin.Context) {
		c.Set("user_id", userID)
		server.handleUpdateTrader(c)
	})
	body := []byte(`{"name":"Reload Failure Trader","ai_model_id":"` + modelID + `","exchange_id":"` + exchangeID + `","execution_mode":"paper","initial_balance":100,"scan_interval_minutes":5}`)
	request := httptest.NewRequest(http.MethodPut, "/api/traders/"+traderID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("reload failure returned %d, want 500; body: %s", response.Code, response.Body.String())
	}
}

func TestUpdateTraderReturns5xxWhenTargetTraderInitializationFails(t *testing.T) {
	const (
		userID   = "target-init-failure-user"
		traderID = "target-init-failure-trader"
		modelID  = "target-init-failure-model"
	)

	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.User().Create(&store.User{ID: userID, Email: "target-init-failure@example.com", PasswordHash: "test"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AIModel().Create(userID, modelID, "Target Init Failure Model", "openai", true, "test-api-key", ""); err != nil {
		t.Fatalf("create AI model: %v", err)
	}
	strategyID := "target-init-failure-strategy"
	if err := st.Strategy().Create(&store.Strategy{ID: strategyID, UserID: userID, Name: "Target Init Failure Strategy", Config: "{}"}); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	exchangeID, err := st.Exchange().Create(
		userID, "indodax", "Indodax Market", true,
		"test-api-key", "test-secret-key", "", true,
		"", false, false,
		"", "", "",
		"", "", "", 0,
	)
	if err != nil {
		t.Fatalf("create exchange: %v", err)
	}
	if err := st.Trader().Create(&store.Trader{
		ID:                  traderID,
		UserID:              userID,
		Name:                "Target Init Failure Trader",
		AIModelID:           modelID,
		ExchangeID:          exchangeID,
		StrategyID:          strategyID,
		ExecutionMode:       "paper",
		InitialBalance:      100,
		ScanIntervalMinutes: 5,
	}); err != nil {
		t.Fatalf("create trader: %v", err)
	}

	gin.SetMode(gin.TestMode)
	server := &Server{store: st, traderManager: manager.NewTraderManager()}
	router := gin.New()
	router.PUT("/api/traders/:id", func(c *gin.Context) {
		c.Set("user_id", userID)
		server.handleUpdateTrader(c)
	})
	body := []byte(`{"name":"Target Init Failure Trader","ai_model_id":"` + modelID + `","exchange_id":"` + exchangeID + `","execution_mode":"paper","initial_balance":100,"scan_interval_minutes":5}`)
	request := httptest.NewRequest(http.MethodPut, "/api/traders/"+traderID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("target initialization failure returned %d, want 500; body: %s", response.Code, response.Body.String())
	}
	if _, err := server.traderManager.GetTrader(traderID); err == nil {
		t.Fatal("target trader unexpectedly remained loaded after initialization failure")
	}
	if loadErr := server.traderManager.GetLoadError(traderID); loadErr == nil {
		t.Fatal("target trader initialization failure was not recorded")
	}
}

func TestUpdateTraderResetFailureCompensatesCapturedRuntimeState(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		wasRunning   bool
		wantRestarts int
	}{
		{name: "stopped runtime", wasRunning: false, wantRestarts: 0},
		{name: "running runtime", wasRunning: true, wantRestarts: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const (
				userID   = "reset-failure-user"
				traderID = "reset-failure-trader"
				modelID  = "reset-failure-model"
			)

			st, err := store.New(t.TempDir() + "/nofx.db")
			if err != nil {
				t.Fatalf("store.New failed: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if err := st.User().Create(&store.User{ID: userID, Email: testCase.name + "@example.com", PasswordHash: "test"}); err != nil {
				t.Fatalf("create user: %v", err)
			}
			if err := st.AIModel().Create(userID, modelID, "Reset Failure Model", "openai", true, "test-api-key", ""); err != nil {
				t.Fatalf("create AI model: %v", err)
			}
			strategyID := "reset-failure-strategy"
			if err := st.Strategy().Create(&store.Strategy{ID: strategyID, UserID: userID, Name: "Reset Failure Strategy", Config: "{}"}); err != nil {
				t.Fatalf("create strategy: %v", err)
			}
			exchangeID, err := st.Exchange().Create(
				userID, "binance", "Reset Failure Market", true,
				"test-api-key", "test-secret-key", "", true,
				"", false, false,
				"", "", "",
				"", "", "", 0,
			)
			if err != nil {
				t.Fatalf("create exchange: %v", err)
			}
			if err := st.Trader().Create(&store.Trader{
				ID:                  traderID,
				UserID:              userID,
				Name:                "Reset Failure Trader",
				AIModelID:           modelID,
				ExchangeID:          exchangeID,
				StrategyID:          strategyID,
				ExecutionMode:       "paper",
				InitialBalance:      100,
				ScanIntervalMinutes: 5,
			}); err != nil {
				t.Fatalf("create trader: %v", err)
			}

			transactionErr := errors.New("injected reset transaction failure")
			loadCalls := 0
			restartCalls := 0
			server := &Server{store: st, traderManager: manager.NewTraderManager()}
			server.traderUpdateHooks = traderUpdateHooks{
				traderRunning: func(string) bool { return testCase.wasRunning },
				resetPaperAccount: func(string, *store.Trader) error {
					return transactionErr
				},
				loadUserTradersNoAutoStart: func(st *store.Store, userID string) error {
					loadCalls++
					return server.traderManager.LoadUserTradersFromStoreWithoutAutoStart(st, userID)
				},
				restartPaperTrader: func(string) error {
					restartCalls++
					return nil
				},
			}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.PUT("/api/traders/:id", func(c *gin.Context) {
				c.Set("user_id", userID)
				server.handleUpdateTrader(c)
			})
			body := []byte(`{"name":"Reset Failure Trader","ai_model_id":"` + modelID + `","exchange_id":"` + exchangeID + `","execution_mode":"paper","initial_balance":100,"reset_paper_account":true,"scan_interval_minutes":5}`)
			request := httptest.NewRequest(http.MethodPut, "/api/traders/"+traderID, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusInternalServerError {
				t.Fatalf("reset transaction failure returned %d, want 500; body: %s", response.Code, response.Body.String())
			}
			if loadCalls != 1 {
				t.Fatalf("rollback compensation load calls = %d, want 1", loadCalls)
			}
			if restartCalls != testCase.wantRestarts {
				t.Fatalf("rollback compensation restart calls = %d, want %d", restartCalls, testCase.wantRestarts)
			}
		})
	}
}
