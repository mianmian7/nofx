package trader

import (
	"context"
	"errors"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"nofx/trader/types"
)

type deadlineMarketClient struct{}

func (deadlineMarketClient) ValidateMarketAvailability(string) (*market.MarketAvailability, error) {
	return &market.MarketAvailability{Price: 100}, nil
}

type deadlineExchange struct {
	openingProtectionMockTrader
	cancel                 context.CancelFunc
	cancelAt               string
	marginCalls, openCalls int
	compensationContextErr error
}

func (e *deadlineExchange) GetPositions() ([]map[string]interface{}, error) { return nil, nil }
func (e *deadlineExchange) GetBalance() (map[string]interface{}, error) {
	if e.cancelAt == "balance" {
		e.cancel()
	}
	return map[string]interface{}{"availableBalance": float64(1000), "totalEquity": float64(1000)}, nil
}
func (e *deadlineExchange) SetMarginMode(string, bool) error {
	e.marginCalls++
	if e.cancelAt == "margin" {
		e.cancel()
	}
	return nil
}
func (e *deadlineExchange) OpenLong(string, float64, int) (map[string]interface{}, error) {
	e.openCalls++
	if e.cancelAt == "entry" {
		e.cancel()
	}
	return map[string]interface{}{"orderId": int64(1)}, nil
}
func (e *deadlineExchange) OpenShort(s string, q float64, l int) (map[string]interface{}, error) {
	return e.OpenLong(s, q, l)
}
func (e *deadlineExchange) EmergencyClosePosition(ctx context.Context, s, side string, q float64) (*types.EmergencyCloseResult, error) {
	e.compensationContextErr = ctx.Err()
	return e.openingProtectionMockTrader.EmergencyClosePosition(ctx, s, side, q)
}

func deadlineDecision(action string) kernel.Decision {
	d := kernel.Decision{Symbol: "MUUSDT", Action: action, PositionSizeUSD: 100, Leverage: 1, StopLoss: 90, TakeProfit: 120}
	if action == "open_short" {
		d.StopLoss, d.TakeProfit = 110, 80
	}
	return d
}

func TestDecisionDeadlineRejectsEntryAfterPreparation(t *testing.T) {
	for _, action := range []string{"open_long", "open_short"} {
		for _, stage := range []string{"before", "balance", "margin"} {
			t.Run(action+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				e := &deadlineExchange{cancel: cancel, cancelAt: stage}
				at := &AutoTrader{trader: e, exchange: "binance", binanceMarketClient: deadlineMarketClient{}}
				if stage == "before" {
					cancel()
				}
				d := deadlineDecision(action)
				err := at.executeDecisionWithRecordContext(ctx, &d, &store.DecisionAction{})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want cancellation", err)
				}
				wantMargin := 0
				if stage == "margin" {
					wantMargin = 1
				}
				if e.openCalls != 0 || e.marginCalls != wantMargin {
					t.Fatalf("writes after expiry: opens=%d margin=%d", e.openCalls, e.marginCalls)
				}
			})
		}
	}
}

func TestDecisionDeadlineDoesNotCancelOpeningProtection(t *testing.T) {
	for _, action := range []string{"open_long", "open_short"} {
		for _, failSL := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/protected", true: "/compensated"}[failSL], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				e := &deadlineExchange{cancel: cancel, cancelAt: "entry"}
				if failSL {
					e.stopLossErr = errors.New("injected protection failure")
				}
				at := &AutoTrader{trader: e, exchange: "binance", binanceMarketClient: deadlineMarketClient{}, positionFirstSeenTime: map[string]int64{}, sleeper: func(time.Duration) {}}
				d := deadlineDecision(action)
				err := at.executeDecisionWithRecordContext(ctx, &d, &store.DecisionAction{})
				if e.openCalls != 1 || len(e.stopLossPrices) == 0 {
					t.Fatalf("entry/protection calls = %d/%d", e.openCalls, len(e.stopLossPrices))
				}
				if failSL {
					var protectionErr *OpeningProtectionError
					if !errors.As(err, &protectionErr) || !protectionErr.Compensated {
						t.Fatalf("error = %v, want compensated protection failure", err)
					}
					if e.compensationContextErr != nil || e.closeLongCalls+e.closeShortCalls != 1 {
						t.Fatal("emergency close must have an independent live context")
					}
				} else if err != nil || len(e.takeProfitPrices) != 1 {
					t.Fatalf("protection incomplete: %v", err)
				}
			})
		}
	}
}

func TestDecisionDeadlineStopsRemainingBatch(t *testing.T) {
	for _, cancelBefore := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		at := &AutoTrader{isRunning: true, sleeper: func(time.Duration) { cancel() }}
		if cancelBefore {
			cancel()
		}
		record := &store.DecisionRecord{}
		at.executeDecisionBatchContext(ctx, []kernel.Decision{{Action: "hold"}, {Action: "open_long"}}, &kernel.Context{}, record)
		cancel()
		want := 2
		if cancelBefore {
			want = 1
		}
		if len(record.Decisions) != want || record.Decisions[want-1].Success || record.Decisions[want-1].Error == "" {
			t.Fatalf("expired decision not recorded: %+v", record.Decisions)
		}
	}
}

type deadlinePaperSource struct{ cancel context.CancelFunc }

func (s deadlinePaperSource) GetMarketPrice(string) (float64, error) { s.cancel(); return 100, nil }
func (s deadlinePaperSource) GetDepth(string, int) (*market.BinanceDepthSnapshot, error) {
	s.cancel()
	return &market.BinanceDepthSnapshot{Bids: [][]string{{"100", "10"}}, Asks: [][]string{{"100", "10"}}}, nil
}

func TestPaperDecisionDeadlinePreventsLedgerCommit(t *testing.T) {
	for _, maker := range []bool{false, true} {
		for _, action := range []string{"open_long", "open_short"} {
			ctx, cancel := context.WithCancel(context.Background())
			b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1000, MakerFirst: maker}, deadlinePaperSource{cancel})
			if err != nil {
				t.Fatal(err)
			}
			d := deadlineDecision(action)
			_, err = b.ExecuteDecisionContext(ctx, &d)
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("maker=%v %s error=%v", maker, action, err)
			}
			if len(b.positions) != 0 || len(b.pendingOrders) != 0 || len(b.fills) != 0 || b.balance != 1000 || b.totalFees != 0 {
				t.Fatal("expired entry mutated paper ledger")
			}
		}
	}
}
