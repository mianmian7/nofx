package kernel

import (
	"encoding/json"
	"testing"

	"nofx/market"
	"nofx/mcp"
	"nofx/store"
)

type correlatedAIClientStub struct {
	mcp.AIClient
	metadata mcp.CallMetadata
}

func (stub *correlatedAIClientStub) CallWithMessagesWithMetadata(metadata mcp.CallMetadata, _, _ string) (string, error) {
	stub.metadata = metadata
	return `[{"symbol":"BTCUSDT","action":"wait","reasoning":"test"}]`, nil
}

func TestGetFullDecisionPropagatesOneCallIDThroughMCPMetadata(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	client := &correlatedAIClientStub{}
	ctx := &Context{
		TraderID:       "trader-test",
		StrategyID:     "strategy-test",
		Account:        AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
		MarketDataMap:  map[string]*market.Data{"BTCUSDT": {}},
		CandidateCoins: []CandidateCoin{{Symbol: "BTCUSDT"}},
	}

	decision, err := GetFullDecisionWithStrategy(ctx, client, &StrategyEngine{config: &config}, "")
	if err != nil {
		t.Fatalf("GetFullDecisionWithStrategy() error = %v", err)
	}
	if decision == nil {
		t.Fatal("decision = nil, want correlated decision")
	}
	if decision.CallID == "" {
		t.Fatal("decision CallID is empty, want non-empty")
	}
	if decision.CallID != client.metadata.CallID {
		t.Fatalf("decision CallID = %q, MCP metadata CallID = %q", decision.CallID, client.metadata.CallID)
	}
	if client.metadata.TraderID != ctx.TraderID || client.metadata.StrategyID != ctx.StrategyID {
		t.Fatalf("metadata IDs = %q/%q, want %q/%q", client.metadata.TraderID, client.metadata.StrategyID, ctx.TraderID, ctx.StrategyID)
	}

	payload, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("marshal FullDecision: %v", err)
	}
	var decoded FullDecision
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal FullDecision: %v", err)
	}
	if decoded.CallID != decision.CallID {
		t.Fatalf("decoded CallID = %q, want %q", decoded.CallID, decision.CallID)
	}
}
