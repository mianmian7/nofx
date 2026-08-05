package trader

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"nofx/kernel"
	"nofx/store"
)

type protectionUpdateTrader struct {
	Trader
	stopLossPrices   []float64
	takeProfitPrices []float64
	side             string
	entryPrice       float64
	markPrice        float64
	lastPrice        float64
	marketPriceCalls int
	positionCalls    int
	stopLoss         float64
	takeProfit       float64
	stopLossErr      error
	takeProfitErr    error
}

func (t *protectionUpdateTrader) GetPositions() ([]map[string]interface{}, error) {
	t.positionCalls++
	side := t.side
	if side == "" {
		side = "long"
	}
	entryPrice := t.entryPrice
	if entryPrice == 0 {
		entryPrice = 100
	}
	markPrice := t.markPrice
	if markPrice == 0 {
		markPrice = 106
	}
	stopLoss := t.stopLoss
	if stopLoss == 0 {
		stopLoss = 90
	}
	takeProfit := t.takeProfit
	if takeProfit == 0 {
		takeProfit = 110
	}
	return []map[string]interface{}{{
		"symbol":      "SKHYUSDT",
		"side":        side,
		"positionAmt": 1.0,
		"entryPrice":  entryPrice,
		"markPrice":   markPrice,
		"stop_loss":   stopLoss,
		"take_profit": takeProfit,
	}}, nil
}

type protectionSnapshotTrader struct {
	*protectionUpdateTrader
	snapshot    ProtectionSnapshot
	snapshotErr error
	priceTick   float64
	modifyCalls []string
}

func (t *protectionSnapshotTrader) GetProtectionSnapshot(string, string) (ProtectionSnapshot, error) {
	return t.snapshot, t.snapshotErr
}

func (t *protectionSnapshotTrader) GetProtectionPriceTick(string) (float64, error) {
	return t.priceTick, nil
}

func (t *protectionSnapshotTrader) SetStopLoss(symbol, side string, quantity, price float64) error {
	if err := t.protectionUpdateTrader.SetStopLoss(symbol, side, quantity, price); err != nil {
		return err
	}
	t.snapshot.StopLoss = ProtectionLevelSnapshot{Status: ProtectionPresent, Price: price, OrderID: "sl-new"}
	return nil
}

func (t *protectionSnapshotTrader) SetTakeProfit(symbol, side string, quantity, price float64) error {
	if err := t.protectionUpdateTrader.SetTakeProfit(symbol, side, quantity, price); err != nil {
		return err
	}
	t.snapshot.TakeProfit = ProtectionLevelSnapshot{Status: ProtectionPresent, Price: price, OrderID: "tp-new"}
	return nil
}

type atomicProtectionTrader struct {
	*protectionSnapshotTrader
}

func (t *atomicProtectionTrader) ModifyProtectionOrder(_, orderID, kind, _ string, _ float64, price float64) error {
	t.modifyCalls = append(t.modifyCalls, fmt.Sprintf("%s:%s:%.4f", kind, orderID, price))
	if kind == "stop_loss" {
		t.snapshot.StopLoss.Price = price
	} else {
		t.snapshot.TakeProfit.Price = price
	}
	return nil
}

func (t *protectionUpdateTrader) GetMarketPrice(string) (float64, error) {
	t.marketPriceCalls++
	if t.lastPrice != 0 {
		return t.lastPrice, nil
	}
	if t.markPrice != 0 {
		return t.markPrice, nil
	}
	return 106, nil
}

func TestLoadManagedPositionUsesMarkPriceWithoutLastPriceOverride(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{
		entryPrice: 100, markPrice: 106, lastPrice: 94, stopLoss: 99, takeProfit: 110,
	}
	at := &AutoTrader{exchange: "bitget", trader: exchangeTrader}

	position, err := at.loadManagedPosition("SKHYUSDT")
	if err != nil {
		t.Fatalf("loadManagedPosition failed: %v", err)
	}
	if position.current != 106 {
		t.Fatalf("current = %.4f, want Bitget mark price 106 (last price is 94)", position.current)
	}
	if exchangeTrader.marketPriceCalls != 0 {
		t.Fatalf("GetMarketPrice calls = %d, want none for mark-triggered protection", exchangeTrader.marketPriceCalls)
	}
}

func TestFeeInclusiveBreakevenBoundaryUsesQuoteCostsSymmetrically(t *testing.T) {
	costs := protectionBreakevenCosts{
		EntryFeeQuote: 0.20, ExitFeeRate: 0.001, FundingCostQuote: 0.10,
		SlippageRate: 0.0005, ProfitBufferRate: 0.0002, Source: "test actual costs",
	}
	tests := []struct {
		side string
		want float64
	}{
		{side: "long", want: (200 + 0.20 + 0.10 + 200*0.0002) / (2 * (1 - 0.0005) * (1 - 0.001))},
		{side: "short", want: (200 - 0.20 - 0.10 - 200*0.0002) / (2 * (1 + 0.0005) * (1 + 0.001))},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			position := managedPosition{side: tt.side, entry: 100, quantity: 2, breakevenCosts: costs, breakevenCostsKnown: true}
			got, err := feeInclusiveBreakevenBoundary(position)
			if err != nil {
				t.Fatalf("feeInclusiveBreakevenBoundary: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("boundary = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestFallbackBreakevenBoundaryIsExplicitAndConservative(t *testing.T) {
	position := managedPosition{side: "long", entry: 100, quantity: 1}
	ensureManagedPositionProtectionMetadata(&position)
	boundary, err := feeInclusiveBreakevenBoundary(position)
	if err != nil {
		t.Fatalf("feeInclusiveBreakevenBoundary: %v", err)
	}
	if boundary <= 100 || !strings.Contains(position.breakevenCosts.Source, "not an exchange fee quote") {
		t.Fatalf("boundary=%.8f source=%q, want explicit conservative fallback above entry", boundary, position.breakevenCosts.Source)
	}
}

func TestProtectionStopNormalizesOnlyNearFeeBoundaryOnTick(t *testing.T) {
	tests := []struct {
		side       string
		current    float64
		oldStop    float64
		unsafeSign float64
	}{
		{side: "long", current: 105, oldStop: 99, unsafeSign: -1},
		{side: "short", current: 95, oldStop: 101, unsafeSign: 1},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			position := managedPosition{
				side: tt.side, entry: 100, current: tt.current, quantity: 1,
				stopLoss: tt.oldStop, stopLossState: ProtectionPresent, priceTick: 0.01,
				breakevenCosts: fallbackProtectionBreakevenCosts(100, 1), breakevenCostsKnown: true,
			}
			boundary, err := feeInclusiveBreakevenBoundary(position)
			if err != nil {
				t.Fatal(err)
			}
			decision := &kernel.Decision{NewStopLoss: boundary + tt.unsafeSign*0.005}
			if err := validateProtectionUpdate(position, decision); err != nil {
				t.Fatalf("near-boundary stop rejected: %v", err)
			}
			want := math.Ceil(boundary/0.01-1e-9) * 0.01
			if tt.side == "short" {
				want = math.Floor(boundary/0.01+1e-9) * 0.01
			}
			if math.Abs(decision.NewStopLoss-want) > 1e-9 {
				t.Fatalf("normalized stop = %.8f, want %.8f", decision.NewStopLoss, want)
			}
		})
	}
}

func TestProtectionStopRejectsTickClampAcrossCurrentMark(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		t.Run(side, func(t *testing.T) {
			position := managedPosition{
				side: side, entry: 100, quantity: 1, priceTick: 0.1,
				breakevenCosts: fallbackProtectionBreakevenCosts(100, 1), breakevenCostsKnown: true,
				stopLossState: ProtectionPresent,
			}
			boundary, err := feeInclusiveBreakevenBoundary(position)
			if err != nil {
				t.Fatal(err)
			}
			request := boundary - 0.005
			if side == "long" {
				position.stopLoss = 99
				position.current = math.Ceil(boundary/0.1-1e-9)*0.1 - 0.01
			} else {
				position.stopLoss = 101
				position.current = math.Floor(boundary/0.1+1e-9)*0.1 + 0.01
				request = boundary + 0.005
			}
			err = validateProtectionUpdate(position, &kernel.Decision{NewStopLoss: request})
			if err == nil || !strings.Contains(err.Error(), "fee_boundary=") {
				t.Fatalf("error = %v, want tick clamp crossing current mark rejected", err)
			}
		})
	}
}

func TestProtectionStopRejectsUnknownCostsOldStopAndUnsafeNormalization(t *testing.T) {
	tests := []struct {
		name     string
		position managedPosition
		request  float64
		want     string
	}{
		{
			name: "fee costs unavailable",
			position: managedPosition{side: "long", entry: 100, current: 105, quantity: 1, stopLoss: 99,
				stopLossState: ProtectionPresent, breakevenCosts: protectionBreakevenCosts{Source: "fee lookup failed"}},
			request: 101, want: "fee boundary unavailable",
		},
		{
			name: "old stop unavailable",
			position: managedPosition{side: "long", entry: 100, current: 105, quantity: 1,
				stopLossState: ProtectionUnavailable},
			request: 101, want: "state=unavailable",
		},
		{
			name: "too far past boundary",
			position: managedPosition{side: "short", entry: 100, current: 95, quantity: 1, stopLoss: 101,
				stopLossState: ProtectionPresent, priceTick: 0.01,
				breakevenCosts: fallbackProtectionBreakevenCosts(100, 1), breakevenCostsKnown: true},
			request: 100, want: "fee_boundary=",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtectionUpdate(tt.position, &kernel.Decision{NewStopLoss: tt.request})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFeeBoundaryRejectionReportsAllPriceInputs(t *testing.T) {
	position := managedPosition{
		side: "short", entry: 100, current: 95, quantity: 1,
		stopLoss: 101, stopLossState: ProtectionPresent,
		breakevenCosts: fallbackProtectionBreakevenCosts(100, 1), breakevenCostsKnown: true,
	}
	err := validateProtectionUpdate(position, &kernel.Decision{NewStopLoss: 100})
	if err == nil {
		t.Fatal("unsafe profitable stop unexpectedly accepted")
	}
	for _, field := range []string{"requested=", "entry=", "current_mark=", "old_stop=", "fee_boundary="} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error %q missing %q", err, field)
		}
	}
}

func TestProtectionTakeProfitStatesAndSameTickNoOp(t *testing.T) {
	base := managedPosition{
		symbol: "SKHYUSDT", side: "long", entry: 100, current: 106, quantity: 1,
		stopLoss: 99, stopLossState: ProtectionPresent, priceTick: 0.1,
	}
	t.Run("present same tick is no-op", func(t *testing.T) {
		position := base
		position.takeProfit = 110.04
		position.takeProfitState = ProtectionPresent
		decision := &kernel.Decision{NewTakeProfit: 110.049}
		if err := validateProtectionUpdate(position, decision); err != nil {
			t.Fatalf("same-tick take profit rejected: %v", err)
		}
		if decision.NewTakeProfit != 0 {
			t.Fatalf("same-tick take profit = %.8f, want normalized no-op", decision.NewTakeProfit)
		}
	})
	t.Run("confirmed absent is an initial target", func(t *testing.T) {
		position := base
		position.takeProfitState = ProtectionConfirmedAbsent
		plan, err := buildProtectionUpdatePlan(position, &kernel.Decision{NewTakeProfit: 112})
		if err != nil || !plan.initialTakeProfit {
			t.Fatalf("plan=%+v err=%v, want initial target", plan, err)
		}
	})
	t.Run("unavailable degrades only with valid stop", func(t *testing.T) {
		position := base
		position.takeProfitState = ProtectionUnavailable
		decision := &kernel.Decision{NewStopLoss: 101, NewTakeProfit: 112}
		plan, err := buildProtectionUpdatePlan(position, decision)
		if err != nil || plan.degradedReason == "" || decision.NewTakeProfit != 0 {
			t.Fatalf("plan=%+v decision=%+v err=%v, want stop-only degradation", plan, decision, err)
		}
	})
	t.Run("ambiguous fails closed", func(t *testing.T) {
		position := base
		position.takeProfitState = ProtectionAmbiguous
		err := validateProtectionUpdate(position, &kernel.Decision{NewStopLoss: 101, NewTakeProfit: 112})
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("error = %v, want ambiguous fail-closed", err)
		}
	})
}

func TestLiveUpdatePositionRecordsStopOnlyDegradation(t *testing.T) {
	base := &protectionUpdateTrader{entryPrice: 100, markPrice: 106, stopLoss: 99, takeProfit: 110}
	exchangeTrader := &protectionSnapshotTrader{
		protectionUpdateTrader: base,
		priceTick:              0.1,
		snapshot: ProtectionSnapshot{
			StopLoss:   ProtectionLevelSnapshot{Status: ProtectionPresent, Price: 99, OrderID: "sl-old"},
			TakeProfit: ProtectionLevelSnapshot{Status: ProtectionUnavailable},
		},
		snapshotErr: fmt.Errorf("injected TPSL query failure"),
	}
	at := &AutoTrader{exchange: "bitget", executionMode: ExecutionModeLive, trader: exchangeTrader}
	decision := &kernel.Decision{Symbol: "SKHYUSDT", Action: "update_position", NewStopLoss: 101, NewTakeProfit: 112}
	actionRecord := &store.DecisionAction{}

	if err := at.executeDecisionWithRecord(decision, actionRecord); err != nil {
		t.Fatalf("stop-only degraded update failed: %v", err)
	}
	if !actionRecord.Success || !actionRecord.Degraded || !strings.Contains(actionRecord.DegradedReason, "stop only") {
		t.Fatalf("action record = %+v, want explicit partial/degraded success", actionRecord)
	}
	if got, want := base.stopLossPrices, []float64{101}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss calls = %v, want %v", got, want)
	}
	if len(base.takeProfitPrices) != 0 {
		t.Fatalf("take-profit calls = %v, want none", base.takeProfitPrices)
	}
}

func TestLiveUpdatePositionSetsConfirmedAbsentTakeProfitWithoutCancellation(t *testing.T) {
	base := &protectionUpdateTrader{entryPrice: 100, markPrice: 106, stopLoss: 99, takeProfit: 0}
	exchangeTrader := &protectionSnapshotTrader{
		protectionUpdateTrader: base,
		priceTick:              0.1,
		snapshot: ProtectionSnapshot{
			StopLoss:   ProtectionLevelSnapshot{Status: ProtectionPresent, Price: 99, OrderID: "sl-old"},
			TakeProfit: ProtectionLevelSnapshot{Status: ProtectionConfirmedAbsent},
		},
	}
	at := &AutoTrader{exchange: "bitget", executionMode: ExecutionModeLive, trader: exchangeTrader}
	actionRecord := &store.DecisionAction{}

	if err := at.executeDecisionWithRecord(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position", NewTakeProfit: 112,
	}, actionRecord); err != nil {
		t.Fatalf("initial take-profit update failed: %v", err)
	}
	if !actionRecord.Success || actionRecord.Degraded {
		t.Fatalf("action record = %+v, want full initial-target success", actionRecord)
	}
	if got, want := base.takeProfitPrices, []float64{112}; !slices.Equal(got, want) {
		t.Fatalf("take-profit calls = %v, want %v", got, want)
	}
}

func TestUnavailableTakeProfitWithoutIndependentStopFailsClosed(t *testing.T) {
	position := managedPosition{
		symbol: "SKHYUSDT", side: "long", entry: 100, current: 106, quantity: 1,
		stopLoss: 99, stopLossState: ProtectionPresent, takeProfitState: ProtectionUnavailable,
	}
	err := validateProtectionUpdate(position, &kernel.Decision{NewTakeProfit: 112})
	if err == nil || !strings.Contains(err.Error(), "state unavailable") {
		t.Fatalf("error = %v, want unavailable TP fail-closed", err)
	}
}

func TestLiveBitgetStyleUpdateUsesAtomicProtectionModification(t *testing.T) {
	base := &protectionUpdateTrader{entryPrice: 100, markPrice: 106, stopLoss: 99, takeProfit: 110}
	snapshotTrader := &protectionSnapshotTrader{
		protectionUpdateTrader: base,
		priceTick:              0.1,
		snapshot: ProtectionSnapshot{
			StopLoss:   ProtectionLevelSnapshot{Status: ProtectionPresent, Price: 99, OrderID: "sl-old"},
			TakeProfit: ProtectionLevelSnapshot{Status: ProtectionPresent, Price: 110, OrderID: "tp-old"},
		},
	}
	exchangeTrader := &atomicProtectionTrader{protectionSnapshotTrader: snapshotTrader}
	at := &AutoTrader{exchange: "bitget", executionMode: ExecutionModeLive, trader: exchangeTrader}

	if err := at.executeDecisionWithRecord(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position", NewStopLoss: 101,
	}, &store.DecisionAction{}); err != nil {
		t.Fatalf("atomic stop update failed: %v", err)
	}
	if got, want := exchangeTrader.modifyCalls, []string{"stop_loss:sl-old:101.0000"}; !slices.Equal(got, want) {
		t.Fatalf("modify calls = %v, want %v", got, want)
	}
	if len(base.stopLossPrices) != 0 {
		t.Fatalf("cancel/create fallback unexpectedly placed stops: %v", base.stopLossPrices)
	}
}

func TestExecutorConsumesPromptProtectionSnapshot(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{entryPrice: 200, markPrice: 50, stopLoss: 40, takeProfit: 60}
	at := &AutoTrader{exchange: "bitget", trader: exchangeTrader}
	at.replaceManagedPositionSnapshots([]managedPosition{{
		symbol: "SKHYUSDT", side: "long", quantity: 1, entry: 100, current: 106,
		stopLoss: 99, stopLossState: ProtectionPresent, takeProfit: 110, takeProfitState: ProtectionPresent,
	}})

	position, err := at.loadManagedPosition("SKHYUSDT")
	if err != nil {
		t.Fatalf("load cached prompt snapshot: %v", err)
	}
	if position.entry != 100 || position.current != 106 || exchangeTrader.positionCalls != 0 {
		t.Fatalf("position=%+v position calls=%d, want unchanged prompt snapshot", position, exchangeTrader.positionCalls)
	}
}

func (t *protectionUpdateTrader) CancelStopLossOrders(string) error {
	return nil
}

func (t *protectionUpdateTrader) SetStopLoss(_ string, _ string, _ float64, price float64) error {
	t.stopLossPrices = append(t.stopLossPrices, price)
	return t.stopLossErr
}

func (t *protectionUpdateTrader) CancelTakeProfitOrders(string) error {
	return nil
}

func (t *protectionUpdateTrader) SetTakeProfit(_ string, _ string, _ float64, price float64) error {
	t.takeProfitPrices = append(t.takeProfitPrices, price)
	if t.takeProfitErr != nil {
		return t.takeProfitErr
	}
	if price == 113 {
		return fmt.Errorf("injected take-profit failure")
	}
	return nil
}

func TestSetOpeningProtectionReturnsStopLossFailureWithoutClosing(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{stopLossErr: fmt.Errorf("injected stop-loss failure")}
	at := &AutoTrader{trader: exchangeTrader}

	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1)
	if err == nil || !strings.Contains(err.Error(), "stop loss") {
		t.Fatalf("error = %v, want stop-loss failure", err)
	}
	if len(exchangeTrader.takeProfitPrices) != 1 {
		t.Fatalf("take-profit calls = %v, want protection attempt without close", exchangeTrader.takeProfitPrices)
	}
}

func TestSetOpeningProtectionReturnsTakeProfitFailureWithoutClosing(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{takeProfitErr: fmt.Errorf("injected take-profit failure")}
	at := &AutoTrader{trader: exchangeTrader}

	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "SHORT", 1)
	if err == nil || !strings.Contains(err.Error(), "take profit") {
		t.Fatalf("error = %v, want take-profit failure", err)
	}
	if len(exchangeTrader.stopLossPrices) != 1 {
		t.Fatalf("stop-loss calls = %v, want protection attempt without close", exchangeTrader.stopLossPrices)
	}
}

func TestValidateProtectionUpdateAllowsProtectedRunawayExtension(t *testing.T) {
	position := managedPosition{
		symbol: "SKHYUSDT", side: "long", quantity: 1,
		entry: 130.47, current: 135.40, stopLoss: 127.10, takeProfit: 138.20,
	}
	decision := &kernel.Decision{
		Action: "update_position", NewStopLoss: 133.80, NewTakeProfit: 141.00, Confidence: 82,
	}
	if err := validateProtectionUpdate(position, decision); err != nil {
		t.Fatalf("protected runaway extension rejected: %v", err)
	}
}

func TestValidateProtectionUpdateRejectsUnprotectedTargetChasing(t *testing.T) {
	position := managedPosition{
		symbol: "SKHYUSDT", side: "long", quantity: 1,
		entry: 130.47, current: 135.40, stopLoss: 127.10, takeProfit: 138.20,
	}
	decision := &kernel.Decision{
		Action: "update_position", NewTakeProfit: 141.00, Confidence: 90,
	}
	err := validateProtectionUpdate(position, decision)
	if err == nil || !strings.Contains(err.Error(), "tightened new_stop_loss") {
		t.Fatalf("error = %v, want paired stop requirement", err)
	}
}

func TestValidateProtectionUpdateTreatsUnchangedTakeProfitAsNoOp(t *testing.T) {
	tests := []struct {
		name string
		side string
		stop float64
		tp   float64
	}{
		{name: "long", side: "long", stop: 103, tp: 110},
		{name: "short", side: "short", stop: 142, tp: 135},
		{name: "short target only", side: "short", stop: 0, tp: 135},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := managedPosition{
				side: tt.side, entry: 150, current: 141.5, stopLoss: 145, takeProfit: tt.tp,
			}
			if tt.side == "long" {
				position.entry = 100
				position.current = 106
				position.stopLoss = 90
			}
			decision := &kernel.Decision{
				Action: "update_position", NewStopLoss: tt.stop, NewTakeProfit: tt.tp, Confidence: 85,
			}
			if err := validateProtectionUpdate(position, decision); err != nil {
				t.Fatalf("unchanged take profit rejected: %v", err)
			}
			if decision.NewTakeProfit != 0 {
				t.Fatalf("unchanged take profit was not normalized: %v", decision.NewTakeProfit)
			}
		})
	}
}

func TestLiveShortUpdatePositionTightensStopWithoutChangingTakeProfit(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{
		side: "short", entryPrice: 150, markPrice: 141.5, stopLoss: 145, takeProfit: 135,
	}
	at := &AutoTrader{exchange: "binance", trader: exchangeTrader}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 142, NewTakeProfit: 135, Confidence: 85,
	}
	actionRecord := &store.DecisionAction{}

	if err := at.executeDecisionWithRecord(decision, actionRecord); err != nil {
		t.Fatalf("short stop-only update rejected: %v", err)
	}
	if !actionRecord.Success {
		t.Fatal("short stop-only update was not marked successful")
	}
	if got, want := exchangeTrader.stopLossPrices, []float64{142}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss prices = %v, want %v", got, want)
	}
	if len(exchangeTrader.takeProfitPrices) != 0 {
		t.Fatalf("take-profit replacement calls = %v, want none", exchangeTrader.takeProfitPrices)
	}
}

func TestLiveLongUpdatePositionTightensStopWithoutChangingTakeProfit(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{}
	at := &AutoTrader{exchange: "binance", trader: exchangeTrader}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 103, NewTakeProfit: 110, Confidence: 85,
	}
	actionRecord := &store.DecisionAction{}

	if err := at.executeDecisionWithRecord(decision, actionRecord); err != nil {
		t.Fatalf("long stop-only update rejected: %v", err)
	}
	if !actionRecord.Success {
		t.Fatal("long stop-only update was not marked successful")
	}
	if got, want := exchangeTrader.stopLossPrices, []float64{103}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss prices = %v, want %v", got, want)
	}
	if len(exchangeTrader.takeProfitPrices) != 0 {
		t.Fatalf("take-profit replacement calls = %v, want none", exchangeTrader.takeProfitPrices)
	}
}

func TestExecuteUpdatePositionClassifiesProtectionRejection(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{
		side: "short", entryPrice: 150, markPrice: 141.5, stopLoss: 145, takeProfit: 135,
	}
	at := &AutoTrader{exchange: "binance", trader: exchangeTrader}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 141.1, NewTakeProfit: 135, Confidence: 90,
	}

	err := at.executeDecisionWithRecord(decision, &store.DecisionAction{})
	if err == nil || !isProtectionUpdateRejected(err) {
		t.Fatalf("error = %v, want classified protection rejection", err)
	}
	if !strings.Contains(err.Error(), "must stay above current price") {
		t.Fatalf("error = %v, want preserved rejection reason", err)
	}
}

func TestValidateProtectionUpdateRejectsInvalidShortProtectionChanges(t *testing.T) {
	tests := []struct {
		name     string
		position managedPosition
		decision *kernel.Decision
		want     string
	}{
		{
			name:     "stop below current price",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 145, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 141.1, NewTakeProfit: 135, Confidence: 90},
			want:     "must stay above current price",
		},
		{
			name:     "profitable stop below fee-inclusive floor",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 151, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 149.9, NewTakeProfit: 135, Confidence: 90},
			want:     "breakeven plus fees",
		},
		{
			name:     "target extension too early",
			position: managedPosition{side: "short", entry: 150, current: 145, stopLoss: 148, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 146, NewTakeProfit: 130, Confidence: 90},
			want:     "premature",
		},
		{
			name:     "target extension wrong direction",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 145, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 143, NewTakeProfit: 140, Confidence: 90},
			want:     "may only extend below",
		},
		{
			name:     "target extension too large",
			position: managedPosition{side: "short", entry: 150, current: 141.5, stopLoss: 145, takeProfit: 135},
			decision: &kernel.Decision{NewStopLoss: 143, NewTakeProfit: 130, Confidence: 90},
			want:     "too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtectionUpdate(tt.position, tt.decision)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateProtectionUpdateRejectsInvalidLongProtectionChanges(t *testing.T) {
	tests := []struct {
		name     string
		position managedPosition
		decision *kernel.Decision
		want     string
	}{
		{
			name:     "stop above current price",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 90, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 107, NewTakeProfit: 110, Confidence: 90},
			want:     "must stay below current price",
		},
		{
			name:     "profitable stop below fee-inclusive floor",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 99, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 100.05, NewTakeProfit: 110, Confidence: 90},
			want:     "breakeven plus fees",
		},
		{
			name:     "target extension too early",
			position: managedPosition{side: "long", entry: 100, current: 102, stopLoss: 95, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 101, NewTakeProfit: 115, Confidence: 90},
			want:     "premature",
		},
		{
			name:     "target extension wrong direction",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 90, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 103, NewTakeProfit: 105, Confidence: 90},
			want:     "may only extend beyond",
		},
		{
			name:     "target extension too large",
			position: managedPosition{side: "long", entry: 100, current: 106, stopLoss: 90, takeProfit: 110},
			decision: &kernel.Decision{NewStopLoss: 103, NewTakeProfit: 116, Confidence: 90},
			want:     "too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtectionUpdate(tt.position, tt.decision)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestPaperUpdatePositionReplacesProtection(t *testing.T) {
	prices := fixedPaperPriceSource{"SKHYUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	at := &AutoTrader{
		executionMode: ExecutionModePaper,
		exchange:      "binance",
		trader:        broker,
		paperBroker:   broker,
	}
	open := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 300, StopLoss: 90, TakeProfit: 110,
	}
	if err := at.executeDecisionWithRecord(open, &store.DecisionAction{}); err != nil {
		t.Fatalf("open: %v", err)
	}

	prices["SKHYUSDT"] = 106
	update := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 101, NewTakeProfit: 113, Confidence: 85,
	}
	if err := at.executeDecisionWithRecord(update, &store.DecisionAction{}); err != nil {
		t.Fatalf("update protection: %v", err)
	}

	prices["SKHYUSDT"] = 108
	secondUpdate := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 102, NewTakeProfit: 114, Confidence: 85,
	}
	if err := at.executeDecisionWithRecord(secondUpdate, &store.DecisionAction{}); err != nil {
		t.Fatalf("replace protection: %v", err)
	}

	positions, err := broker.GetPositions()
	if err != nil || len(positions) != 1 {
		t.Fatalf("positions = %#v, err=%v", positions, err)
	}
	if got := floatFromPosition(positions[0], "stop_loss"); got != 102 {
		t.Fatalf("stop loss = %v, want 102", got)
	}
	if got := floatFromPosition(positions[0], "take_profit"); got != 114 {
		t.Fatalf("take profit = %v, want 114", got)
	}
	orders, err := broker.GetOpenOrders("SKHYUSDT")
	if err != nil || len(orders) != 1 || orders[0].Price != 114 {
		t.Fatalf("open orders = %#v, err=%v; want replacement target 114", orders, err)
	}
	if events := broker.RecentOrderEvents(10); len(events) != 3 || events[1].Status != "CANCELED" || events[1].Reason != "protection_replaced" || events[2].Status != "NEW" || events[2].Reason != "protection_updated" {
		t.Fatalf("protection replacement events = %#v, want cancellation followed by replacement", events)
	}
}

func TestPaperUpdatePositionUsesShortTakeProfitProtectionOrder(t *testing.T) {
	prices := fixedPaperPriceSource{"SKHYUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "open_short", Leverage: 3,
		PositionSizeUSD: 300,
	}); err != nil {
		t.Fatalf("open short: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position", NewTakeProfit: 95,
	}); err != nil {
		t.Fatalf("set short take profit: %v", err)
	}
	orders, err := broker.GetOpenOrders("SKHYUSDT")
	if err != nil || len(orders) != 1 {
		t.Fatalf("short protection orders = %#v, err=%v", orders, err)
	}
	if orders[0].PositionSide != "SHORT" || orders[0].Side != "BUY" || orders[0].Price != 95 {
		t.Fatalf("short protection order = %#v, want BUY SHORT @ 95", orders[0])
	}
}

func TestPaperStopOnlyUpdatePreservesTakeProfitSemantics(t *testing.T) {
	prices := fixedPaperPriceSource{"SKHYUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000, TakerFeeBPS: 5, SlippageBPS: 2}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	at := &AutoTrader{executionMode: ExecutionModePaper, exchange: "binance", trader: broker, paperBroker: broker}
	if err := at.executeDecisionWithRecord(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 300, StopLoss: 90, TakeProfit: 110,
	}, &store.DecisionAction{}); err != nil {
		t.Fatalf("open: %v", err)
	}
	prices["SKHYUSDT"] = 106
	beforeEvents := broker.RecentOrderEvents(10)
	if err := at.executeDecisionWithRecord(&kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position", NewStopLoss: 101,
	}, &store.DecisionAction{}); err != nil {
		t.Fatalf("paper stop-only update: %v", err)
	}
	positions, err := broker.GetPositions()
	if err != nil || len(positions) != 1 {
		t.Fatalf("positions=%#v err=%v", positions, err)
	}
	if got := floatFromPosition(positions[0], "stop_loss"); got != 101 {
		t.Fatalf("stop loss = %v, want 101", got)
	}
	if got := floatFromPosition(positions[0], "take_profit"); got != 110 {
		t.Fatalf("take profit = %v, want unchanged 110", got)
	}
	if afterEvents := broker.RecentOrderEvents(10); len(afterEvents) != len(beforeEvents) {
		t.Fatalf("stop-only update changed TP order events: before=%#v after=%#v", beforeEvents, afterEvents)
	}
}

func TestLiveUpdatePositionRestoresStopLossWhenTakeProfitReplacementFails(t *testing.T) {
	exchangeTrader := &protectionUpdateTrader{}
	at := &AutoTrader{
		exchange: "binance",
		trader:   exchangeTrader,
	}
	decision := &kernel.Decision{
		Symbol: "SKHYUSDT", Action: "update_position",
		NewStopLoss: 101, NewTakeProfit: 113, Confidence: 85,
	}
	actionRecord := &store.DecisionAction{}

	err := at.executeDecisionWithRecord(decision, actionRecord)
	if err == nil || !strings.Contains(err.Error(), "old stop 90.0000 restored") {
		t.Fatalf("error = %v, want restored-stop failure result", err)
	}
	if actionRecord.Success {
		t.Fatal("action record unexpectedly marked successful")
	}
	if got, want := exchangeTrader.stopLossPrices, []float64{101, 90}; !slices.Equal(got, want) {
		t.Fatalf("stop-loss prices = %v, want %v", got, want)
	}
	if got, want := exchangeTrader.takeProfitPrices, []float64{113, 110}; !slices.Equal(got, want) {
		t.Fatalf("take-profit prices = %v, want %v", got, want)
	}
}
