package api

import (
	"bytes"
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
	if err := st.AIModel().Create(userID, modelID, "Paper Reset Model", "custom", true, "test-api-key", "http://localhost:8317/v1"); err != nil {
		t.Fatalf("create AI model: %v", err)
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
