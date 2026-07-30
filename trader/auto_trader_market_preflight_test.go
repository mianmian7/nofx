package trader

import (
	"errors"
	"strings"
	"testing"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

type failingBinanceMarketAvailabilityClient struct {
	err error
}

func (client failingBinanceMarketAvailabilityClient) ValidateMarketAvailability(string) (*market.MarketAvailability, error) {
	return nil, client.err
}

func TestPaperOpenStopsBeforeLedgerMutationWhenBinanceMarketIsUnavailable(t *testing.T) {
	broker, err := NewPaperBroker(
		PaperBrokerConfig{InitialBalance: 1_000},
		fixedPaperPriceSource{"MUUSDT": 100},
	)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	before := broker.Snapshot()
	autoTrader := &AutoTrader{
		executionMode:       ExecutionModePaper,
		exchange:            "binance",
		trader:              broker,
		paperBroker:         broker,
		binanceMarketClient: failingBinanceMarketAvailabilityClient{err: errors.New("proxy credentials expired")},
	}
	decision := &kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 300, StopLoss: 90, TakeProfit: 120,
	}

	err = autoTrader.executeDecisionWithRecord(decision, &store.DecisionAction{})
	if err == nil || !strings.Contains(err.Error(), "refusing to open") {
		t.Fatalf("error = %v, want market preflight rejection", err)
	}
	after := broker.Snapshot()
	if after.Balance != before.Balance || after.OpenPositions != 0 || after.PendingOrders != 0 {
		t.Fatalf("paper ledger changed after rejected preflight: before=%#v after=%#v", before, after)
	}
}
