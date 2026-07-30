package store

import (
	"reflect"
	"testing"

	"nofx/crypto"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAIModelStorePersistsConfiguredModelNames(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&AIModel{}); err != nil {
		t.Fatal(err)
	}
	modelStore := NewAIModelStore(database)
	if err := modelStore.UpdateWithModelNames(
		"user-1",
		"openai",
		"OpenAI",
		true,
		"test-key",
		"https://example.com/v1",
		"gpt-5.6-sol",
		[]string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna"},
	); err != nil {
		t.Fatal(err)
	}
	models, err := modelStore.List("user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("model count = %d, want 1", len(models))
	}
	got := DecodeStringList(models[0].ModelNames)
	want := []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model names = %#v, want %#v", got, want)
	}
}

func TestResolveAIModelAPIKeyPrefersStoredCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "environment-key")
	model := &AIModel{
		Provider: "openai",
		APIKey:   crypto.EncryptedString("stored-key"),
	}

	if resolvedAPIKey := ResolveAIModelAPIKey(model); resolvedAPIKey != "stored-key" {
		t.Fatalf("ResolveAIModelAPIKey() = %q, want stored-key", resolvedAPIKey)
	}
}

func TestResolveAIModelAPIKeyUsesProviderEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "environment-key")
	model := &AIModel{Provider: " OpenAI "}

	if resolvedAPIKey := ResolveAIModelAPIKey(model); resolvedAPIKey != "environment-key" {
		t.Fatalf("ResolveAIModelAPIKey() = %q, want environment-key", resolvedAPIKey)
	}
}

func TestResolveAIModelAPIKeyReturnsEmptyForUnsupportedProvider(t *testing.T) {
	model := &AIModel{Provider: "custom-provider"}

	if resolvedAPIKey := ResolveAIModelAPIKey(model); resolvedAPIKey != "" {
		t.Fatalf("ResolveAIModelAPIKey() = %q, want empty string", resolvedAPIKey)
	}
}
