package api

import (
	"testing"

	"nofx/crypto"
	"nofx/store"
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
