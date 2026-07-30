package trader

import (
	"testing"
	"time"

	"nofx/mcp"
)

type fakeFailoverAIClient struct {
	response         string
	err              error
	callCount        int
	lastRequestModel string
}

func (client *fakeFailoverAIClient) SetAPIKey(string, string, string) {}

func (client *fakeFailoverAIClient) SetTimeout(time.Duration) {}

func (client *fakeFailoverAIClient) CallWithMessages(string, string) (string, error) {
	client.callCount++
	return client.response, client.err
}

func (client *fakeFailoverAIClient) CallWithRequest(request *mcp.Request) (string, error) {
	client.callCount++
	if request != nil {
		client.lastRequestModel = request.Model
	}
	return client.response, client.err
}

func (client *fakeFailoverAIClient) CallWithRequestStream(*mcp.Request, func(string)) (string, error) {
	client.callCount++
	return client.response, client.err
}

func (client *fakeFailoverAIClient) CallWithRequestFull(*mcp.Request) (*mcp.LLMResponse, error) {
	client.callCount++
	if client.err != nil {
		return nil, client.err
	}
	return &mcp.LLMResponse{Content: client.response}, nil
}

func TestAIModelFailoverClientPrefersSiblingModelBeforeIndependentProvider(t *testing.T) {
	primaryClient := &fakeFailoverAIClient{err: mcp.NewAPIError(503, "overloaded")}
	siblingClient := &fakeFailoverAIClient{response: "terra response"}
	independentClient := &fakeFailoverAIClient{response: "independent response"}

	failoverClient, err := newAIModelFailoverClient([]failoverCandidate{
		{
			configuration: AIModelCandidate{ID: "primary", Provider: "codex", APIKey: "shared", ModelName: "sol"},
			client:        primaryClient,
		},
		{
			configuration: AIModelCandidate{ID: "sibling", Provider: "codex", APIKey: "shared", ModelName: "terra"},
			client:        siblingClient,
		},
		{
			configuration: AIModelCandidate{ID: "independent", Provider: "openai", APIKey: "other", ModelName: "gpt-5"},
			client:        independentClient,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := failoverClient.CallWithMessages("system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if result != "terra response" {
		t.Fatalf("expected sibling model response, got %q", result)
	}
	if primaryClient.callCount != 1 || siblingClient.callCount != 1 || independentClient.callCount != 0 {
		t.Fatalf("unexpected call counts: primary=%d sibling=%d independent=%d", primaryClient.callCount, siblingClient.callCount, independentClient.callCount)
	}
}

func TestAIModelFailoverClientSkipsSiblingModelsAfterSharedAuthenticationFailure(t *testing.T) {
	primaryClient := &fakeFailoverAIClient{err: mcp.NewAPIError(503, "auth_unavailable")}
	siblingClient := &fakeFailoverAIClient{response: "should be cooling down"}
	independentClient := &fakeFailoverAIClient{response: "independent response"}

	failoverClient, err := newAIModelFailoverClient([]failoverCandidate{
		{
			configuration: AIModelCandidate{ID: "primary", Provider: "codex", APIKey: "shared", ModelName: "sol"},
			client:        primaryClient,
		},
		{
			configuration: AIModelCandidate{ID: "sibling", Provider: "codex", APIKey: "shared", ModelName: "terra"},
			client:        siblingClient,
		},
		{
			configuration: AIModelCandidate{ID: "independent", Provider: "openai", APIKey: "other", ModelName: "gpt-5"},
			client:        independentClient,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := failoverClient.CallWithMessages("system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if result != "independent response" {
		t.Fatalf("expected independent provider response, got %q", result)
	}
	if siblingClient.callCount != 0 {
		t.Fatalf("shared authentication failure should cooldown sibling model, got %d calls", siblingClient.callCount)
	}
}

func TestAIModelFailoverClientRewritesRequestModel(t *testing.T) {
	primaryClient := &fakeFailoverAIClient{err: mcp.NewAPIError(503, "overloaded")}
	siblingClient := &fakeFailoverAIClient{response: "terra response"}
	failoverClient, err := newAIModelFailoverClient([]failoverCandidate{
		{configuration: AIModelCandidate{ID: "primary", Provider: "codex", APIKey: "shared", ModelName: "sol"}, client: primaryClient},
		{configuration: AIModelCandidate{ID: "sibling", Provider: "codex", APIKey: "shared", ModelName: "terra"}, client: siblingClient},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = failoverClient.CallWithRequest(&mcp.Request{Model: "sol"})
	if err != nil {
		t.Fatal(err)
	}
	if siblingClient.lastRequestModel != "terra" {
		t.Fatalf("expected request model terra, got %q", siblingClient.lastRequestModel)
	}
}

func TestAIModelFailoverClientDoesNotProbeDuringAuthenticationCooldown(t *testing.T) {
	primaryClient := &fakeFailoverAIClient{err: mcp.NewAPIError(503, "auth_unavailable")}
	failoverClient, err := newAIModelFailoverClient([]failoverCandidate{
		{
			configuration: AIModelCandidate{ID: "primary", Provider: "codex", APIKey: "shared", ModelName: "sol"},
			client:        primaryClient,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := failoverClient.CallWithMessages("system", "user"); err == nil {
		t.Fatal("first authentication failure must be returned")
	}
	if primaryClient.callCount != 1 {
		t.Fatalf("first call count = %d, want 1", primaryClient.callCount)
	}

	if _, err := failoverClient.CallWithMessages("system", "user"); err == nil {
		t.Fatal("cooldown call must return an availability error")
	}
	if primaryClient.callCount != 1 {
		t.Fatalf("cooldown must suppress provider probe, call count = %d", primaryClient.callCount)
	}

	failoverClient.mu.RLock()
	cooldownRemaining := time.Until(failoverClient.candidates[0].cooldownUntil)
	failoverClient.mu.RUnlock()
	if cooldownRemaining < 29*time.Minute {
		t.Fatalf("authentication cooldown remaining = %s, want approximately 30 minutes", cooldownRemaining)
	}
}

func TestAIModelFailoverClientReturnsToPrimaryAndClearsFallbackState(t *testing.T) {
	primaryClient := &fakeFailoverAIClient{err: mcp.NewAPIError(503, "overloaded")}
	fallbackClient := &fakeFailoverAIClient{response: "fallback response"}
	failoverClient, err := newAIModelFailoverClient([]failoverCandidate{
		{
			configuration: AIModelCandidate{ID: "primary", Provider: "openai", APIKey: "primary", ModelName: "gpt-primary"},
			client:        primaryClient,
		},
		{
			configuration: AIModelCandidate{ID: "fallback", Provider: "deepseek", APIKey: "fallback", ModelName: "deepseek-chat"},
			client:        fallbackClient,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result, callErr := failoverClient.CallWithMessages("system", "user"); callErr != nil || result != "fallback response" {
		t.Fatalf("fallback call = (%q, %v), want fallback response", result, callErr)
	}
	provider, model, modelID, reason, fallbackSince := failoverClient.RuntimeState()
	if provider != "deepseek" || model != "deepseek-chat" || modelID != "fallback" {
		t.Fatalf("fallback runtime model = %s/%s (%s)", provider, model, modelID)
	}
	if reason != mcp.ErrorKindProviderUnavailable || fallbackSince.IsZero() {
		t.Fatalf("fallback state = reason %q, since %v", reason, fallbackSince)
	}

	primaryClient.err = nil
	primaryClient.response = "primary recovered"
	failoverClient.mu.Lock()
	failoverClient.candidates[0].cooldownUntil = time.Now().Add(-time.Second)
	failoverClient.mu.Unlock()

	if result, callErr := failoverClient.CallWithMessages("system", "user"); callErr != nil || result != "primary recovered" {
		t.Fatalf("recovery call = (%q, %v), want primary recovered", result, callErr)
	}
	provider, model, modelID, reason, fallbackSince = failoverClient.RuntimeState()
	if provider != "openai" || model != "gpt-primary" || modelID != "primary" {
		t.Fatalf("recovered runtime model = %s/%s (%s)", provider, model, modelID)
	}
	if reason != mcp.ErrorKindUnknown || !fallbackSince.IsZero() {
		t.Fatalf("recovered state retained failover metadata: reason %q, since %v", reason, fallbackSince)
	}
}
