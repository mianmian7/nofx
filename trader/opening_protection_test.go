package trader

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
	"nofx/trader/types"
)

type openingProtectionMockTrader struct {
	types.Trader
	stopLossPrices         []float64
	takeProfitPrices       []float64
	stopLossErr            error
	takeProfitErr          error
	closeLongCalls         int
	closeShortCalls        int
	closeLongErr           error
	closeShortErr          error
	cancelStopCalls        int
	positionNotZeroOnClose bool
	openOrders             []types.OpenOrder
}

func (t *openingProtectionMockTrader) SetStopLoss(_ string, _ string, _ float64, price float64) error {
	t.stopLossPrices = append(t.stopLossPrices, price)
	return t.stopLossErr
}

func (t *openingProtectionMockTrader) SetTakeProfit(_ string, _ string, _ float64, price float64) error {
	t.takeProfitPrices = append(t.takeProfitPrices, price)
	return t.takeProfitErr
}

func (t *openingProtectionMockTrader) CancelStopOrders(string) error {
	t.cancelStopCalls++
	return nil
}

func (t *openingProtectionMockTrader) GetOpenOrders(string) ([]types.OpenOrder, error) {
	return t.openOrders, nil
}

func (t *openingProtectionMockTrader) EmergencyClosePosition(ctx context.Context, symbol, positionSide string, quantity float64) (*types.EmergencyCloseResult, error) {
	if strings.EqualFold(positionSide, "LONG") {
		t.closeLongCalls++
		if t.closeLongErr != nil {
			return nil, t.closeLongErr
		}
		if t.positionNotZeroOnClose {
			return &types.EmergencyCloseResult{
				OrderID:      "99901",
				Symbol:       symbol,
				PositionSide: positionSide,
				Quantity:     quantity,
				ExecutedQty:  quantity / 2,
				Status:       "PARTIALLY_FILLED",
				Submitted:    true,
				Filled:       false,
				PositionZero: false,
			}, nil
		}
		return &types.EmergencyCloseResult{
			OrderID:      "99901",
			Symbol:       symbol,
			PositionSide: positionSide,
			Quantity:     quantity,
			ExecutedQty:  quantity,
			Status:       "FILLED",
			Submitted:    true,
			Filled:       true,
			PositionZero: true,
		}, nil
	}

	t.closeShortCalls++
	if t.closeShortErr != nil {
		return nil, t.closeShortErr
	}
	if t.positionNotZeroOnClose {
		return &types.EmergencyCloseResult{
			OrderID:      "99902",
			Symbol:       symbol,
			PositionSide: positionSide,
			Quantity:     quantity,
			ExecutedQty:  quantity / 2,
			Status:       "PARTIALLY_FILLED",
			Submitted:    true,
			Filled:       false,
			PositionZero: false,
		}, nil
	}
	return &types.EmergencyCloseResult{
		OrderID:      "99902",
		Symbol:       symbol,
		PositionSide: positionSide,
		Quantity:     quantity,
		ExecutedQty:  quantity,
		Status:       "FILLED",
		Submitted:    true,
		Filled:       true,
		PositionZero: true,
	}, nil
}

func TestSetOpeningProtection_StopLossRetryAndFailClosedCompensation(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{stopLossErr: fmt.Errorf("injected stop-loss failure")}
	var recordedDelays []time.Duration
	at := &AutoTrader{
		trader: exchangeTrader,
		sleeper: func(d time.Duration) {
			recordedDelays = append(recordedDelays, d)
		},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": time.Now().UnixMilli()},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T (%v)", err, err)
	}

	if !opErr.Compensated {
		t.Errorf("expected opErr.Compensated = true, got false")
	}
	if opErr.CompensationErr != nil {
		t.Errorf("expected opErr.CompensationErr = nil, got %v", opErr.CompensationErr)
	}
	if opErr.Attempts != 4 {
		t.Errorf("expected 4 attempts (1 initial + 3 retries), got %d", opErr.Attempts)
	}
	if len(recordedDelays) != 3 {
		t.Errorf("expected 3 backoff delays, got %d (%v)", len(recordedDelays), recordedDelays)
	}
	if exchangeTrader.closeLongCalls != 1 {
		t.Errorf("expected 1 CloseLong call for compensation, got %d", exchangeTrader.closeLongCalls)
	}
	if exchangeTrader.cancelStopCalls != 1 {
		t.Errorf("expected 1 CancelStopOrders call for cleanup, got %d", exchangeTrader.cancelStopCalls)
	}
	if len(exchangeTrader.takeProfitPrices) != 0 {
		t.Errorf("take-profit should NOT be attempted when SL failed, got %d calls", len(exchangeTrader.takeProfitPrices))
	}
	if actionRecord.Success {
		t.Errorf("expected actionRecord.Success = false")
	}
	if _, exists := at.positionFirstSeenTime["SKHYUSDT_long"]; exists {
		t.Errorf("position tracking should be removed after confirmed compensation close")
	}
}

func TestSetOpeningProtection_InvalidZeroStopLossTriggersCompensationWithoutRetry(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{}
	var recordedDelays []time.Duration
	at := &AutoTrader{
		trader: exchangeTrader,
		sleeper: func(d time.Duration) {
			recordedDelays = append(recordedDelays, d)
		},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": time.Now().UnixMilli()},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 0, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error for non-positive StopLoss, got nil")
	}

	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T (%v)", err, err)
	}
	if !opErr.Compensated {
		t.Errorf("expected opErr.Compensated = true for zero SL")
	}
	if len(recordedDelays) != 0 {
		t.Errorf("expected 0 backoff delays for invalid SL (no retry), got %d", len(recordedDelays))
	}
	if exchangeTrader.closeLongCalls != 1 {
		t.Errorf("expected compensation close to be called for invalid SL, got %d", exchangeTrader.closeLongCalls)
	}
	if len(exchangeTrader.takeProfitPrices) != 0 {
		t.Errorf("take profit must NOT be attempted when SL is invalid, got %d", len(exchangeTrader.takeProfitPrices))
	}
}

func TestSetOpeningProtection_StopLossRetrySuccess(t *testing.T) {
	callCount := 0
	exchangeTrader := &openingProtectionMockTrader{}
	var recordedDelays []time.Duration
	at := &AutoTrader{
		trader: &retrySuccessMockTrader{
			openingProtectionMockTrader: exchangeTrader,
			failAttempts:                2,
			failErr:                     fmt.Errorf("temporary network timeout"),
			callCounter:                 &callCount,
		},
		sleeper: func(d time.Duration) {
			recordedDelays = append(recordedDelays, d)
		},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err != nil {
		t.Fatalf("expected success after retries, got err: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 attempts until success, got %d", callCount)
	}
	if len(recordedDelays) != 2 {
		t.Errorf("expected 2 backoff delays before success, got %d", len(recordedDelays))
	}
	if exchangeTrader.closeLongCalls != 0 {
		t.Errorf("compensation close should NOT be called on success, got %d calls", exchangeTrader.closeLongCalls)
	}
	if len(exchangeTrader.takeProfitPrices) != 1 {
		t.Errorf("take-profit should be placed after SL succeeds, got %d calls", len(exchangeTrader.takeProfitPrices))
	}
}

type retrySuccessMockTrader struct {
	*openingProtectionMockTrader
	failAttempts int
	failErr      error
	callCounter  *int
}

func (m *retrySuccessMockTrader) SetStopLoss(symbol, side string, qty, price float64) error {
	*m.callCounter++
	m.openingProtectionMockTrader.stopLossPrices = append(m.openingProtectionMockTrader.stopLossPrices, price)
	if *m.callCounter <= m.failAttempts {
		return m.failErr
	}
	return nil
}

func TestSetOpeningProtection_VerifiedActiveAvoidsRetryAndClose(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{
		stopLossErr: fmt.Errorf("read timeout from exchange"),
		openOrders: []types.OpenOrder{{
			Symbol:       "SKHYUSDT",
			PositionSide: "LONG",
			Side:         "SELL",
			Type:         "STOP_MARKET",
			StopPrice:    90.0,
			Quantity:     1.0,
		}},
	}
	var recordedDelays []time.Duration
	at := &AutoTrader{
		trader: exchangeTrader,
		sleeper: func(d time.Duration) {
			recordedDelays = append(recordedDelays, d)
		},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err != nil {
		t.Fatalf("expected success via verification, got err: %v", err)
	}
	if len(exchangeTrader.stopLossPrices) != 1 {
		t.Errorf("expected only 1 SetStopLoss call since verification succeeded, got %d", len(exchangeTrader.stopLossPrices))
	}
	if len(recordedDelays) != 0 {
		t.Errorf("expected 0 backoff delays since verified on first attempt, got %d", len(recordedDelays))
	}
	if exchangeTrader.closeLongCalls != 0 {
		t.Errorf("compensation close should NOT be called when verified active, got %d", exchangeTrader.closeLongCalls)
	}
	if len(exchangeTrader.takeProfitPrices) != 1 {
		t.Errorf("take profit should proceed after verified SL, got %d calls", len(exchangeTrader.takeProfitPrices))
	}
}

func TestSetOpeningProtection_TakeProfitFailureMarksDegradedWithoutClosing(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{takeProfitErr: fmt.Errorf("injected take-profit failure")}
	at := &AutoTrader{trader: exchangeTrader}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "SHORT", 1, 100.0, actionRecord)

	if err != nil {
		t.Fatalf("take profit failure should return nil (degraded), got: %v", err)
	}
	if !actionRecord.Degraded {
		t.Errorf("expected actionRecord.Degraded = true")
	}
	if !strings.Contains(actionRecord.DegradedReason, "take profit") {
		t.Errorf("expected DegradedReason to mention take profit, got: %s", actionRecord.DegradedReason)
	}
	if exchangeTrader.closeShortCalls != 0 {
		t.Errorf("compensation close should NEVER be called on TP failure, got %d", exchangeTrader.closeShortCalls)
	}
	if len(exchangeTrader.stopLossPrices) != 1 {
		t.Errorf("stop-loss should have succeeded on first attempt, got %d calls", len(exchangeTrader.stopLossPrices))
	}
}

func TestSetOpeningProtection_CompensationCloseFailureRetainsProtectionAndTracking(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{
		stopLossErr:  fmt.Errorf("SL reject"),
		closeLongErr: fmt.Errorf("connection refused during close"),
	}
	at := &AutoTrader{
		trader:                exchangeTrader,
		sleeper:               func(time.Duration) {},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": 12345},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T (%v)", err, err)
	}
	if opErr.CompensationErr == nil {
		t.Errorf("expected CompensationErr to be populated")
	}
	if opErr.Compensated {
		t.Errorf("expected opErr.Compensated = false when close failed")
	}
	if !at.isSafeMode() {
		t.Errorf("expected safe mode to be ACTIVATED when compensation close fails")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Errorf("expected safeModeCause = %s, got %s", SafeModeCauseProtectionFailure, at.getSafeModeCause())
	}
	if exchangeTrader.cancelStopCalls != 0 {
		t.Errorf("CancelStopOrders must NOT be called when close failed, got %d", exchangeTrader.cancelStopCalls)
	}
	if _, exists := at.positionFirstSeenTime["SKHYUSDT_long"]; !exists {
		t.Errorf("position tracking must be RETAINED when close failed")
	}
}

func TestSetOpeningProtection_ShortFailClosedCompensation(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{stopLossErr: fmt.Errorf("injected short SL reject")}
	at := &AutoTrader{
		trader:                exchangeTrader,
		sleeper:               func(time.Duration) {},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_short": 12345},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 110, TakeProfit: 90,
	}, "SHORT", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error on short SL failure, got nil")
	}
	if exchangeTrader.closeShortCalls != 1 {
		t.Errorf("expected 1 CloseShort call for short compensation, got %d", exchangeTrader.closeShortCalls)
	}
	if exchangeTrader.cancelStopCalls != 1 {
		t.Errorf("expected CancelStopOrders on confirmed short close, got %d", exchangeTrader.cancelStopCalls)
	}
	if _, exists := at.positionFirstSeenTime["SKHYUSDT_short"]; exists {
		t.Errorf("short position tracking must be removed after successful compensation close")
	}
}

func TestSetOpeningProtection_PaperModeDeterministicNoSleep(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{stopLossErr: fmt.Errorf("paper sl err")}
	var sleepCalled bool
	at := &AutoTrader{
		executionMode: ExecutionModePaper,
		trader:        exchangeTrader,
		sleeper: func(d time.Duration) {
			sleepCalled = true
		},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": 12345},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error in paper mode SL failure, got nil")
	}
	if sleepCalled {
		t.Errorf("sleeper should NOT be called in paper mode")
	}
	if exchangeTrader.closeLongCalls != 1 {
		t.Errorf("compensation close should execute in paper mode, got %d calls", exchangeTrader.closeLongCalls)
	}
}

func TestSetOpeningProtection_NaNStopLossTriggersCompensationWithoutRetry(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{}
	at := &AutoTrader{
		trader: exchangeTrader,
		sleeper: func(d time.Duration) {
			t.Errorf("sleeper must NOT be called for NaN SL")
		},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": 12345},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: math.NaN(), TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error for NaN StopLoss, got nil")
	}

	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T (%v)", err, err)
	}
	if !opErr.Compensated {
		t.Errorf("expected opErr.Compensated = true for NaN SL")
	}
	if exchangeTrader.closeLongCalls != 1 {
		t.Errorf("expected compensation close to be called for NaN SL, got %d", exchangeTrader.closeLongCalls)
	}
}

func TestSetOpeningProtection_DirectionMismatchInOpenOrdersRejectsFalsePositive(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{
		stopLossErr: fmt.Errorf("SL timeout"),
		openOrders: []types.OpenOrder{{
			Symbol:       "SKHYUSDT",
			PositionSide: "LONG",
			Side:         "BUY", // Wrong side: long SL must be SELL!
			Type:         "STOP_MARKET",
			StopPrice:    90.0,
			Quantity:     1.0,
		}},
	}
	at := &AutoTrader{
		trader:  exchangeTrader,
		sleeper: func(time.Duration) {},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T", err)
	}
	if exchangeTrader.closeLongCalls != 1 {
		t.Errorf("expected compensation close because BUY order is not a valid SL for LONG, got %d", exchangeTrader.closeLongCalls)
	}
}

type snapshotMockTrader struct {
	types.Trader
	snapshot types.ProtectionSnapshot
}

func (s *snapshotMockTrader) GetProtectionSnapshot(symbol, positionSide string) (types.ProtectionSnapshot, error) {
	return s.snapshot, nil
}

func (s *snapshotMockTrader) SetStopLoss(string, string, float64, float64) error {
	return fmt.Errorf("SL timeout")
}

func (s *snapshotMockTrader) EmergencyClosePosition(ctx context.Context, symbol, positionSide string, quantity float64) (*types.EmergencyCloseResult, error) {
	return &types.EmergencyCloseResult{
		OrderID:      "99901",
		Symbol:       symbol,
		PositionSide: positionSide,
		Quantity:     quantity,
		ExecutedQty:  quantity,
		Status:       "FILLED",
		Submitted:    true,
		Filled:       true,
		PositionZero: true,
	}, nil
}

func (s *snapshotMockTrader) CancelStopOrders(string) error {
	return nil
}

func TestSetOpeningProtection_SnapshotPriceMismatchAvoidsFalsePositive(t *testing.T) {
	snapshotTrader := &snapshotMockTrader{
		snapshot: types.ProtectionSnapshot{
			StopLoss: types.ProtectionLevelSnapshot{
				Status: types.ProtectionPresent,
				Price:  50.0, // Stale price mismatch! Expected is 90
			},
		},
	}
	at := &AutoTrader{
		trader:  snapshotTrader,
		sleeper: func(time.Duration) {},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error due to price mismatch, got nil")
	}
}

func TestSetOpeningProtection_EmptySideInOpenOrdersReturnsAmbiguous(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{
		openOrders: []types.OpenOrder{{
			Symbol:       "SKHYUSDT",
			PositionSide: "LONG",
			Side:         "", // Empty side
			Type:         "STOP_MARKET",
			StopPrice:    90.0,
			Quantity:     1.0,
		}},
	}
	at := &AutoTrader{trader: exchangeTrader}

	status, err := at.verifyStopLossActive("SKHYUSDT", "LONG", 90.0)
	if status != types.ProtectionAmbiguous {
		t.Errorf("expected ProtectionAmbiguous for empty order side, got: %s", status)
	}
	if err == nil || !strings.Contains(err.Error(), "empty/unspecified") {
		t.Errorf("expected error mentioning empty/unspecified side, got: %v", err)
	}
}

func TestSetOpeningProtection_OneWayBothPositionSideSupported(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{
		stopLossErr: fmt.Errorf("network timeout"),
		openOrders: []types.OpenOrder{{
			Symbol:       "SKHYUSDT",
			PositionSide: "BOTH", // One-way mode
			Side:         "SELL", // Correct side for long SL
			Type:         "STOP_MARKET",
			StopPrice:    90.0,
			Quantity:     1.0,
		}},
	}
	at := &AutoTrader{
		trader:  exchangeTrader,
		sleeper: func(time.Duration) {},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err != nil {
		t.Fatalf("expected success with BOTH positionSide order, got err: %v", err)
	}
	if exchangeTrader.closeLongCalls != 0 {
		t.Errorf("compensation close should NOT be called when verified via BOTH order, got %d", exchangeTrader.closeLongCalls)
	}
}

func TestSetOpeningProtection_PositionNotZeroFailsCompensationAndActivatesSafeMode(t *testing.T) {
	exchangeTrader := &openingProtectionMockTrader{
		stopLossErr:            fmt.Errorf("SL fail"),
		positionNotZeroOnClose: true,
	}
	at := &AutoTrader{
		trader:                exchangeTrader,
		sleeper:               func(time.Duration) {},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": 12345},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error when position is not zero after close, got nil")
	}

	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T", err)
	}
	if opErr.Compensated {
		t.Errorf("Compensated must be false when PositionZero is false")
	}
	if !at.isSafeMode() {
		t.Errorf("safe mode MUST be activated when PositionZero is not confirmed")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Errorf("expected cause = %s, got %s", SafeModeCauseProtectionFailure, at.getSafeModeCause())
	}
	if exchangeTrader.cancelStopCalls != 0 {
		t.Errorf("CancelStopOrders must NOT be called when position zero is not confirmed, got %d", exchangeTrader.cancelStopCalls)
	}
	if _, exists := at.positionFirstSeenTime["SKHYUSDT_long"]; !exists {
		t.Errorf("position tracking MUST be retained when position zero is not confirmed")
	}
}

type unsupportedCloserMockTrader struct {
	types.Trader
	stopLossCalls int
}

func (u *unsupportedCloserMockTrader) SetStopLoss(string, string, float64, float64) error {
	u.stopLossCalls++
	return fmt.Errorf("SL fail")
}

func (u *unsupportedCloserMockTrader) GetOpenOrders(string) ([]types.OpenOrder, error) {
	return nil, nil
}

func TestSetOpeningProtection_UnsupportedEmergencyCloserConservativelyFails(t *testing.T) {
	unsupportedTrader := &unsupportedCloserMockTrader{}
	at := &AutoTrader{
		trader:                unsupportedTrader,
		sleeper:               func(time.Duration) {},
		positionFirstSeenTime: map[string]int64{"SKHYUSDT_long": 12345},
	}

	actionRecord := &store.DecisionAction{}
	err := at.setOpeningProtection(&kernel.Decision{
		Symbol: "SKHYUSDT", StopLoss: 90, TakeProfit: 110,
	}, "LONG", 1, 100.0, actionRecord)

	if err == nil {
		t.Fatalf("expected error when EmergencyCloser is not supported, got nil")
	}

	var opErr *OpeningProtectionError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OpeningProtectionError, got: %T", err)
	}
	if opErr.Compensated {
		t.Errorf("Compensated must be false when EmergencyCloser is unsupported")
	}
	if !at.isSafeMode() {
		t.Errorf("safe mode MUST be activated when EmergencyCloser is unsupported")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Errorf("expected cause = %s, got %s", SafeModeCauseProtectionFailure, at.getSafeModeCause())
	}
	if _, exists := at.positionFirstSeenTime["SKHYUSDT_long"]; !exists {
		t.Errorf("position tracking MUST be retained when EmergencyCloser is unsupported")
	}
}

func TestSetOpeningProtection_SafeModeProtectionFailureCannotAutoRecover(t *testing.T) {
	at := &AutoTrader{}
	// 1. Protection failure cause
	at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "critical SL failure")
	if at.getSafeModeCause().CanAutoRecover() {
		t.Errorf("SafeModeCauseProtectionFailure must NOT be auto-recoverable")
	}

	recovered := at.tryDeactivateSafeModeOnAIRecovery()
	if recovered {
		t.Errorf("tryDeactivateSafeModeOnAIRecovery should return false for protection failure")
	}
	if !at.isSafeMode() {
		t.Errorf("safe mode must REMAIN active for protection failure even if AI succeeds")
	}

	// 2. AI failure cause (fresh instance so protection latch does not rightly block downgrade)
	atAI := &AutoTrader{}
	atAI.setSafeModeWithCause(true, SafeModeCauseAI, "3 AI errors")
	if !atAI.getSafeModeCause().CanAutoRecover() {
		t.Errorf("SafeModeCauseAI MUST be auto-recoverable")
	}
	recovered = atAI.tryDeactivateSafeModeOnAIRecovery()
	if !recovered {
		t.Errorf("tryDeactivateSafeModeOnAIRecovery should return true for AI failure")
	}
	if atAI.isSafeMode() {
		t.Errorf("safe mode for AI failure should be cleared on AI recovery")
	}
}

func TestExecuteDecisions_SafeModeBlocksSubsequentOpensInSameCycle(t *testing.T) {
	at := &AutoTrader{isRunning: true}
	at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "critical protection failure")

	decisions := []kernel.Decision{
		{Action: "open_long", Symbol: "SYM1", StopLoss: 90, TakeProfit: 110},
		{Action: "open_short", Symbol: "SYM2", StopLoss: 110, TakeProfit: 90},
	}

	record := &store.DecisionRecord{}
	at.executeDecisionBatch(decisions, &kernel.Context{}, record)

	if len(record.Decisions) != 2 {
		t.Fatalf("expected 2 action records, got %d", len(record.Decisions))
	}
	for i, d := range record.Decisions {
		if !strings.Contains(d.Error, "blocked by safe mode") {
			t.Errorf("decision [%d] error = %q, want 'blocked by safe mode'", i, d.Error)
		}
		if d.Success {
			t.Errorf("decision [%d] success = true, want false", i)
		}
	}
}
