package api

import (
	"reflect"
	"testing"
)

func TestParseModelCatalog(t *testing.T) {
	models, err := parseModelCatalog([]byte(`{
		"data": [
			{"id": "gpt-5.6-terra"},
			{"id": "gpt-5.6-sol"},
			{"id": "gpt-5.6-luna"},
			{"id": "gpt-5.6-terra"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestModelCatalogURL(t *testing.T) {
	tests := map[string]string{
		"https://example.com/v1":                   "https://example.com/v1/models",
		"https://example.com/v1/":                  "https://example.com/v1/models",
		"https://example.com/v1/chat/completions#": "https://example.com/v1/models",
	}
	for input, want := range tests {
		got, err := modelCatalogURL(input)
		if err != nil {
			t.Fatalf("modelCatalogURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("modelCatalogURL(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := modelCatalogURL("https://example.com/custom-endpoint#"); err == nil {
		t.Fatal("an unrelated full request URL must not invent a catalog endpoint")
	}
}

func TestValidateFallbackModelNamesAgainstCatalog(t *testing.T) {
	available := []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}
	got, err := validateFallbackModelNamesAgainstCatalog(
		"gpt-5.6-sol",
		[]string{"gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-sol"},
		available,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-5.6-terra", "gpt-5.6-luna"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallbacks = %#v, want %#v", got, want)
	}
	if _, err := validateFallbackModelNamesAgainstCatalog(
		"gpt-5.6-sol",
		[]string{"invented-model"},
		available,
	); err == nil {
		t.Fatal("a model absent from the API catalog must be rejected")
	}
}

func TestValidateModelConfigCatalogSelectionPreservesExplicitEmptyList(t *testing.T) {
	server := &Server{}
	got, err := server.validateModelConfigCatalogSelection(
		t.Context(),
		"user-1",
		"blockrun-base",
		ModelConfigUpdate{ModelNames: []string{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("validated models = %#v, want a non-nil empty list", got)
	}
}
