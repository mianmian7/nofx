package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nofx/mcp"
)

func TestNewJevClientWithOptions(t *testing.T) {
	logger := mcp.NewNoopLogger()

	client := NewJevClientWithOptions(
		mcp.WithAPIKey("sk-jev-key-12345"),
		mcp.WithLogger(logger),
	)

	jevClient, ok := client.(*JevClient)
	if !ok {
		t.Fatalf("Expected *JevClient, got %T", client)
	}

	if jevClient.Provider != mcp.ProviderJev {
		t.Errorf("Provider = %s, want %s", jevClient.Provider, mcp.ProviderJev)
	}
	if jevClient.BaseURL != mcp.DefaultJevBaseURL {
		t.Errorf("BaseURL = %s, want %s", jevClient.BaseURL, mcp.DefaultJevBaseURL)
	}
	if jevClient.Model != mcp.DefaultJevModel {
		t.Errorf("Model = %s, want %s", jevClient.Model, mcp.DefaultJevModel)
	}
	if jevClient.APIKey != "sk-jev-key-12345" {
		t.Errorf("APIKey = %s, want sk-jev-key-12345", jevClient.APIKey)
	}
}

func TestJevClientEvaluateMock(t *testing.T) {
	mockResponse := JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]JevAnswer{
			"direction": {
				Type:       "choice",
				Choice:     "open_long",
				Confidence: 0.89,
				Probabilities: map[string]float64{
					"open_long":  0.89,
					"open_short": 0.05,
					"wait":       0.06,
				},
			},
			"market_regime": {
				Type:       "choice",
				Choice:     "strong_trend",
				Confidence: 0.92,
			},
		},
	}
	mockResponse.Usage.InputTokens = 150
	mockResponse.Usage.OutputTokens = 20

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req JevRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := NewJevClientWithOptions(
		mcp.WithAPIKey("test-key"),
		mcp.WithBaseURL(server.URL),
		mcp.WithHTTPClient(server.Client()),
	).(*JevClient)

	req := &JevRequest{
		State: map[string]interface{}{"price": 95000, "rsi": 62},
		Questions: map[string]JevQuestion{
			"direction": {
				Type:         "choice",
				Instructions: "Choose direction",
				Criteria: map[string]string{
					"open_long": "bullish", "open_short": "bearish", "wait": "neutral",
				},
			},
		},
	}

	resp, err := client.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if resp.Model != "jev-1.13.0" {
		t.Errorf("Model = %s, want jev-1.13.0", resp.Model)
	}
	ans, ok := resp.Answers["direction"]
	if !ok || ans.Choice != "open_long" {
		t.Errorf("Answer choice = %s, want open_long", ans.Choice)
	}
	if ans.Confidence != 0.89 {
		t.Errorf("Confidence = %f, want 0.89", ans.Confidence)
	}
}

func TestJevClientCallWithMessages(t *testing.T) {
	mockResponse := JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]JevAnswer{
			"signal": {
				Type:       "choice",
				Choice:     "open_short",
				Confidence: 0.85,
				Probabilities: map[string]float64{
					"open_short": 0.85,
					"open_long":  0.10,
					"wait":       0.05,
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := NewJevClientWithOptions(
		mcp.WithAPIKey("test-key"),
		mcp.WithBaseURL(server.URL),
		mcp.WithHTTPClient(server.Client()),
	)

	res, err := client.CallWithMessages("system prompt", "user prompt")
	if err != nil {
		t.Fatalf("CallWithMessages error: %v", err)
	}

	if len(res) == 0 {
		t.Errorf("Expected non-empty response")
	}
}
