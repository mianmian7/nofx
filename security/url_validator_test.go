package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelOriginsDefaultToPrivateNetworkDenied(t *testing.T) {
	t.Setenv("NOFX_TRUSTED_MODEL_ORIGINS", "")
	for _, endpoint := range []string{"http://127.0.0.1:12345/v1", "http://10.0.0.1/v1", "http://[::1]/v1"} {
		if err := ValidateModelURL(endpoint); err == nil {
			t.Errorf("private endpoint accepted without operator configuration: %s", endpoint)
		}
	}
}

func TestModelOriginsRequireAnExactExplicitOrigin(t *testing.T) {
	t.Setenv("NOFX_TRUSTED_MODEL_ORIGINS", "http://127.0.0.1:12345, https://[::1]")
	for _, endpoint := range []string{"http://127.0.0.1:12345/v1", "http://127.0.0.1:12345/v1#", "https://[::1]:443/v1"} {
		if err := ValidateModelURL(endpoint); err != nil {
			t.Errorf("configured origin rejected: %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"https://127.0.0.1:12345/v1", "http://127.0.0.1:12346/v1", "http://127.0.0.2:12345/v1", "http://user@127.0.0.1:12345/v1"} {
		if err := ValidateModelURL(endpoint); err == nil {
			t.Errorf("nonmatching origin accepted: %s", endpoint)
		}
	}
}

func TestInvalidModelOriginConfigurationDoesNotGrantTrust(t *testing.T) {
	for _, entry := range []string{"*", "127.0.0.1:12345", "http://user@127.0.0.1:12345", "http://127.0.0.1:12345/v1", "http://127.0.0.1:12345?key=value", "http://127.0.0.1:12345#fragment"} {
		t.Run(entry, func(t *testing.T) {
			t.Setenv("NOFX_TRUSTED_MODEL_ORIGINS", entry)
			if err := ValidateModelURL("http://127.0.0.1:12345/v1"); err == nil {
				t.Fatal("invalid configuration granted private-network access")
			}
		})
	}
}

func TestModelClientCannotFollowRedirectToAnotherPrivateOrigin(t *testing.T) {
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect reached a different private origin")
	}))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, second.URL, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer first.Close()
	// Even another globally configured origin must not inherit this client's trust.
	t.Setenv("NOFX_TRUSTED_MODEL_ORIGINS", first.URL+","+second.URL)
	client, err := SafeHTTPClientForModelURL(first.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("configured endpoint response: %d", response.StatusCode)
	}
	_, err = client.Get(first.URL + "/redirect")
	if err == nil || !strings.Contains(err.Error(), "redirect blocked") {
		t.Fatalf("expected private redirect rejection, got %v", err)
	}
	_, err = client.Get(second.URL)
	if err == nil || !strings.Contains(err.Error(), "blocked connection") {
		t.Fatalf("expected direct private connection rejection, got %v", err)
	}
}
