package kernel

import (
	"errors"
	"testing"

	"nofx/market"
	"nofx/store"
)

func TestLiveDecisionRejectsPreloadedMarketDataWithoutFreshProof(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	engine := &StrategyEngine{config: &config}
	ctx := &Context{
		RequireFreshMarketData: true,
		MarketDataMap: map[string]*market.Data{
			"BTCUSDT": {},
		},
	}

	_, err := GetFullDecisionWithStrategy(ctx, nil, engine, "")
	var unavailable *MarketDataUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want MarketDataUnavailableError", err)
	}
}
