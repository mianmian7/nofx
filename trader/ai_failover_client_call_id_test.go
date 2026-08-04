package trader

import (
	"testing"

	"nofx/mcp"
)

type correlatedFailoverAIClient struct {
	*fakeFailoverAIClient
	metadata []mcp.CallMetadata
}

func (client *correlatedFailoverAIClient) CallWithMessagesWithMetadata(metadata mcp.CallMetadata, systemPrompt, userPrompt string) (string, error) {
	client.metadata = append(client.metadata, metadata)
	return client.fakeFailoverAIClient.CallWithMessages(systemPrompt, userPrompt)
}

func TestAIModelFailoverClientPreservesCallIDAcrossCandidates(t *testing.T) {
	primary := &correlatedFailoverAIClient{fakeFailoverAIClient: &fakeFailoverAIClient{err: mcp.NewAPIError(503, "overloaded")}}
	fallback := &correlatedFailoverAIClient{fakeFailoverAIClient: &fakeFailoverAIClient{response: "fallback"}}
	failoverClient, err := newAIModelFailoverClient([]failoverCandidate{
		{configuration: AIModelCandidate{ID: "primary", Provider: "openai", ModelName: "primary"}, client: primary},
		{configuration: AIModelCandidate{ID: "fallback", Provider: "deepseek", ModelName: "fallback"}, client: fallback},
	})
	if err != nil {
		t.Fatal(err)
	}

	metadata := mcp.CallMetadata{CallID: "call-failover-1", TraderID: "trader-1", StrategyID: "strategy-1"}
	if response, err := failoverClient.CallWithMessagesWithMetadata(metadata, "system", "user"); err != nil || response != "fallback" {
		t.Fatalf("correlated failover call = (%q, %v), want fallback response", response, err)
	}
	if len(primary.metadata) != 1 || len(fallback.metadata) != 1 {
		t.Fatalf("metadata attempts = %d/%d, want one attempt per candidate", len(primary.metadata), len(fallback.metadata))
	}
	if primary.metadata[0].CallID != metadata.CallID || fallback.metadata[0].CallID != metadata.CallID {
		t.Fatalf("candidate CallIDs = %q/%q, want %q", primary.metadata[0].CallID, fallback.metadata[0].CallID, metadata.CallID)
	}
}
