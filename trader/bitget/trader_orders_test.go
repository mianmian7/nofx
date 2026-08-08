package bitget

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"nofx/trader/types"
	"strings"
	"testing"
	"time"
)

type bitgetCapturedRequest struct {
	Path  string
	Query string
	Body  map[string]interface{}
}

type bitgetRecordingTransport struct {
	requests     []bitgetCapturedRequest
	responseBody func(*http.Request) string
	responseErr  func(*http.Request) error
}

func (rt *bitgetRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]interface{}
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(data, &body)
	}
	rt.requests = append(rt.requests, bitgetCapturedRequest{Path: req.URL.Path, Query: req.URL.RawQuery, Body: body})
	if rt.responseErr != nil {
		if err := rt.responseErr(req); err != nil {
			return nil, err
		}
	}
	responseBody := `{"code":"00000","msg":"","data":{}}`
	if rt.responseBody != nil {
		responseBody = rt.responseBody(req)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(responseBody)),
	}, nil
}

func newTestBitgetTrader(rt *bitgetRecordingTransport) *BitgetTrader {
	return &BitgetTrader{
		apiKey:     "key",
		secretKey:  "secret",
		passphrase: "pass",
		httpClient: &http.Client{
			Transport: rt,
		},
		contractsCache: map[string]*BitgetContract{
			"BTCUSDT": {
				Symbol:      "BTCUSDT",
				PricePlace:  1,
				VolumePlace: 3,
			},
		},
		contractsCacheTime: time.Now(),
	}
}

func (rt *bitgetRecordingTransport) requestsForPath(path string) []bitgetCapturedRequest {
	var matches []bitgetCapturedRequest
	for _, request := range rt.requests {
		if request.Path == path {
			matches = append(matches, request)
		}
	}
	return matches
}

func TestBitgetSetPositionModeUsesHedgeMode(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.setPositionMode(); err != nil {
		t.Fatalf("setPositionMode failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetPositionModePath)
	if len(requests) != 1 {
		t.Fatalf("expected one position-mode request, got %d", len(requests))
	}
	if requests[0].Body["posMode"] != "hedge_mode" {
		t.Fatalf("posMode = %#v, want hedge_mode", requests[0].Body["posMode"])
	}
}

func TestBitgetOpenLongUsesHedgeTradeSide(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.OpenLong("BTCUSDT", 0.1, 5); err != nil {
		t.Fatalf("OpenLong failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetOrderPath)
	if len(requests) != 1 {
		t.Fatalf("expected one open order request, got %d", len(requests))
	}
	if requests[0].Body["side"] != "buy" || requests[0].Body["tradeSide"] != "open" {
		t.Fatalf("open-long body = %#v, want buy/open", requests[0].Body)
	}
}

func TestBitgetCloseShortUsesHedgeTradeSide(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.CloseShort("BTCUSDT", 0.1); err != nil {
		t.Fatalf("CloseShort failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetOrderPath)
	if len(requests) != 1 {
		t.Fatalf("expected one close order request, got %d", len(requests))
	}
	if requests[0].Body["side"] != "sell" || requests[0].Body["tradeSide"] != "close" {
		t.Fatalf("close-short body = %#v, want sell/close", requests[0].Body)
	}
	if _, ok := requests[0].Body["reduceOnly"]; ok {
		t.Fatal("hedge-mode close order must not use one-way reduceOnly")
	}
}

func TestBitgetSetStopLossUsesTPSLEndpoint(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetStopLoss("BTCUSDT", "LONG", 0.1, 90000.06); err != nil {
		t.Fatalf("SetStopLoss failed: %v", err)
	}
	if len(rt.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(rt.requests))
	}

	req := rt.requests[0]
	if req.Path != bitgetTPSLOrderPath {
		t.Fatalf("stop-loss path = %s, want %s", req.Path, bitgetTPSLOrderPath)
	}
	if req.Body["planType"] != "loss_plan" {
		t.Fatalf("planType = %#v, want loss_plan", req.Body["planType"])
	}
	if req.Body["holdSide"] != "long" || req.Body["executePrice"] != "0" {
		t.Fatalf("stop-loss body = %#v, want long hold side and market execute price", req.Body)
	}
	if req.Body["triggerPrice"] != "90000.10000000" {
		t.Fatalf("triggerPrice = %#v, want protective tick rounding", req.Body["triggerPrice"])
	}
	if req.Body["size"] != "0.100" {
		t.Fatalf("size = %#v, want 0.100", req.Body["size"])
	}
}

func TestBitgetSetTakeProfitUsesTPSLEndpointForShort(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetTakeProfit("BTCUSDT", "SHORT", 0.1, 90000.04); err != nil {
		t.Fatalf("SetTakeProfit failed: %v", err)
	}
	if len(rt.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(rt.requests))
	}

	req := rt.requests[0]
	if req.Path != bitgetTPSLOrderPath {
		t.Fatalf("take-profit path = %s, want %s", req.Path, bitgetTPSLOrderPath)
	}
	if req.Body["planType"] != "profit_plan" || req.Body["holdSide"] != "short" {
		t.Fatalf("take-profit body = %#v, want profit_plan/short", req.Body)
	}
	if req.Body["triggerPrice"] != "90000.10000000" {
		t.Fatalf("triggerPrice = %#v, want protective tick rounding", req.Body["triggerPrice"])
	}
}

func TestBitgetGetOpenOrdersMapsAllTPSLPlanTypes(t *testing.T) {
	tests := []struct {
		planType string
		wantType string
	}{
		{planType: "profit_plan", wantType: "TAKE_PROFIT_MARKET"},
		{planType: "pos_profit", wantType: "TAKE_PROFIT_MARKET"},
		{planType: "loss_plan", wantType: "STOP_MARKET"},
		{planType: "pos_loss", wantType: "STOP_MARKET"},
	}
	for _, tt := range tests {
		t.Run(tt.planType, func(t *testing.T) {
			rt := &bitgetRecordingTransport{responseBody: func(req *http.Request) string {
				if req.URL.Path == bitgetPendingPath {
					return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
				}
				return `{"code":"00000","msg":"","data":{"entrustedList":[{"orderId":"plan-1","symbol":"BTCUSDT","side":"sell","posSide":"long","planType":"` + tt.planType + `","triggerPrice":"101.25","size":"0.1"}]}}`
			}}
			trader := newTestBitgetTrader(rt)

			orders, err := trader.GetOpenOrders("BTCUSDT")
			if err != nil {
				t.Fatalf("GetOpenOrders failed: %v", err)
			}
			if len(orders) != 1 || orders[0].Type != tt.wantType || orders[0].StopPrice != 101.25 {
				t.Fatalf("orders = %#v, want one %s at 101.25", orders, tt.wantType)
			}
		})
	}
}

func TestBitgetGetOpenOrdersSurfacesPlanQueryFailure(t *testing.T) {
	rt := &bitgetRecordingTransport{
		responseBody: func(*http.Request) string {
			return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
		},
		responseErr: func(req *http.Request) error {
			if req.URL.Path == "/api/v2/mix/order/orders-plan-pending" {
				return errors.New("injected plan query failure")
			}
			return nil
		},
	}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.GetOpenOrders("BTCUSDT"); err == nil {
		t.Fatal("GetOpenOrders returned nil error for an unavailable TPSL snapshot")
	}
}

func TestBitgetGetOpenOrdersSurfacesPlanJSONFailure(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: func(req *http.Request) string {
		if req.URL.Path == bitgetPendingPath {
			return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
		}
		return `{"code":"00000","msg":"","data":"not-an-object"}`
	}}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.GetOpenOrders("BTCUSDT"); err == nil {
		t.Fatal("GetOpenOrders returned nil error for malformed TPSL JSON")
	}
}

func TestBitgetGetOpenOrdersRejectsInvalidTPSLTrigger(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: func(req *http.Request) string {
		if req.URL.Path == bitgetPendingPath {
			return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
		}
		return `{"code":"00000","msg":"","data":{"entrustedList":[{"orderId":"plan-1","symbol":"BTCUSDT","posSide":"long","planType":"profit_plan","triggerPrice":"not-a-price","size":"0.1"}]}}`
	}}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.GetOpenOrders("BTCUSDT"); err == nil {
		t.Fatal("GetOpenOrders returned nil error for an invalid TPSL trigger price")
	}
}

func TestBitgetProtectionSnapshotDistinguishesTPStates(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
			return `{"code":"00000","msg":"","data":{"entrustedList":[{"orderId":"tp-1","symbol":"BTCUSDT","holdSide":"long","planType":"profit_plan","triggerPrice":"110","size":"0.1"}]}}`
		}}
		snapshot, err := newTestBitgetTrader(rt).GetProtectionSnapshot("BTCUSDT", "long")
		if err != nil || snapshot.TakeProfit.Status != types.ProtectionPresent || snapshot.TakeProfit.Price != 110 || snapshot.TakeProfit.OrderID != "tp-1" {
			t.Fatalf("snapshot=%+v err=%v, want present TP", snapshot, err)
		}
	})

	t.Run("confirmed absent", func(t *testing.T) {
		rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
			return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
		}}
		snapshot, err := newTestBitgetTrader(rt).GetProtectionSnapshot("BTCUSDT", "long")
		if err != nil || snapshot.TakeProfit.Status != types.ProtectionConfirmedAbsent {
			t.Fatalf("snapshot=%+v err=%v, want confirmed_absent TP", snapshot, err)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		rt := &bitgetRecordingTransport{responseErr: func(*http.Request) error {
			return errors.New("injected unavailable")
		}}
		snapshot, err := newTestBitgetTrader(rt).GetProtectionSnapshot("BTCUSDT", "long")
		if err == nil || snapshot.TakeProfit.Status != types.ProtectionUnavailable {
			t.Fatalf("snapshot=%+v err=%v, want unavailable TP", snapshot, err)
		}
	})

	t.Run("unavailable TP preserves confirmed stop", func(t *testing.T) {
		rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
			return `{"code":"00000","msg":"","data":{"entrustedList":[{"orderId":"sl-1","symbol":"BTCUSDT","holdSide":"long","planType":"loss_plan","triggerPrice":"95","size":"0.1"},{"orderId":"tp-bad","symbol":"BTCUSDT","holdSide":"long","planType":"profit_plan","triggerPrice":"not-a-price","size":"0.1"}]}}`
		}}
		snapshot, err := newTestBitgetTrader(rt).GetProtectionSnapshot("BTCUSDT", "long")
		if err == nil || snapshot.StopLoss.Status != types.ProtectionPresent || snapshot.StopLoss.Price != 95 || snapshot.StopLoss.OrderID != "sl-1" {
			t.Fatalf("snapshot=%+v err=%v, want present stop preserved", snapshot, err)
		}
		if snapshot.TakeProfit.Status != types.ProtectionUnavailable {
			t.Fatalf("snapshot=%+v err=%v, want only TP unavailable", snapshot, err)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
			return `{"code":"00000","msg":"","data":{"entrustedList":[{"orderId":"tp-1","symbol":"BTCUSDT","holdSide":"long","planType":"profit_plan","triggerPrice":"110","size":"0.1"},{"orderId":"tp-2","symbol":"BTCUSDT","holdSide":"long","planType":"pos_profit","triggerPrice":"111","size":""}]}}`
		}}
		snapshot, err := newTestBitgetTrader(rt).GetProtectionSnapshot("BTCUSDT", "long")
		if err == nil || snapshot.TakeProfit.Status != types.ProtectionAmbiguous {
			t.Fatalf("snapshot=%+v err=%v, want ambiguous TP", snapshot, err)
		}
	})
}

func TestBitgetModifyProtectionOrderUsesAtomicTPSLEndpoint(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.ModifyProtectionOrder("BTCUSDT", "sl-1", "stop_loss", "LONG", 0.1, 100.04); err != nil {
		t.Fatalf("ModifyProtectionOrder failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetModifyTPSLPath)
	if len(requests) != 1 {
		t.Fatalf("modify requests = %d, want one", len(requests))
	}
	body := requests[0].Body
	if body["orderId"] != "sl-1" ||
		body["productType"] != "USDT-FUTURES" ||
		body["marginCoin"] != "USDT" ||
		body["triggerType"] != "mark_price" ||
		body["triggerPrice"] != "100.10000000" ||
		body["executePrice"] != "0" ||
		body["size"] != "0.100" {
		t.Fatalf("modify body = %#v, want atomic mark-price stop modification", body)
	}
}

func TestBitgetCancelPlanOrdersRejectsFailureList(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: func(req *http.Request) string {
		if req.Method == http.MethodGet {
			return `{"code":"00000","msg":"","data":{"entrustedList":[{"orderId":"tp-1","symbol":"BTCUSDT","holdSide":"long","planType":"profit_plan","triggerPrice":"110","size":"0.1"}]}}`
		}
		return `{"code":"00000","msg":"","data":{"successList":[],"failureList":[{"orderId":"tp-1","errorCode":"40001","errorMsg":"not cancelled"}]}}`
	}}
	trader := newTestBitgetTrader(rt)

	if err := trader.CancelTakeProfitOrders("BTCUSDT"); err == nil || !strings.Contains(err.Error(), "failure") && !strings.Contains(err.Error(), "not cancelled") {
		t.Fatalf("cancel error = %v, want failureList rejection", err)
	}
}

func TestBitgetCloseLongUsesHedgeTradeSide(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.CloseLong("BTCUSDT", 0.1); err != nil {
		t.Fatalf("CloseLong failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetOrderPath)
	if len(requests) != 1 {
		t.Fatalf("expected one close order request, got %d", len(requests))
	}
	if requests[0].Body["side"] != "buy" || requests[0].Body["tradeSide"] != "close" {
		t.Fatalf("close-long body = %#v, want buy/close", requests[0].Body)
	}
	if _, ok := requests[0].Body["reduceOnly"]; ok {
		t.Fatal("hedge-mode close order must not use one-way reduceOnly")
	}
}

func TestBitgetOpenShortUsesHedgeTradeSide(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.OpenShort("BTCUSDT", 0.1, 5); err != nil {
		t.Fatalf("OpenShort failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetOrderPath)
	if len(requests) != 1 {
		t.Fatalf("expected one open order request, got %d", len(requests))
	}
	if requests[0].Body["side"] != "sell" || requests[0].Body["tradeSide"] != "open" {
		t.Fatalf("open-short body = %#v, want sell/open", requests[0].Body)
	}
}

func TestBitgetSetStopLossUsesShortHoldSide(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetStopLoss("BTCUSDT", "SHORT", 0.1, 90000.06); err != nil {
		t.Fatalf("SetStopLoss failed: %v", err)
	}
	req := rt.requests[0]
	if req.Body["planType"] != "loss_plan" || req.Body["holdSide"] != "short" {
		t.Fatalf("short stop-loss body = %#v, want loss_plan/short", req.Body)
	}
	if req.Body["triggerPrice"] != "90000.00000000" {
		t.Fatalf("triggerPrice = %#v, want short stop floored toward market", req.Body["triggerPrice"])
	}
}

func TestBitgetSetTakeProfitUsesLongHoldSide(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetTakeProfit("BTCUSDT", "LONG", 0.1, 90000.04); err != nil {
		t.Fatalf("SetTakeProfit failed: %v", err)
	}
	req := rt.requests[0]
	if req.Body["planType"] != "profit_plan" || req.Body["holdSide"] != "long" {
		t.Fatalf("long take-profit body = %#v, want profit_plan/long", req.Body)
	}
	if req.Body["triggerPrice"] != "90000.00000000" {
		t.Fatalf("triggerPrice = %#v, want long target floored toward market", req.Body["triggerPrice"])
	}
}

func TestBitgetSetStopLossRejectsNonPositiveTriggerWithoutRequest(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetStopLoss("BTCUSDT", "LONG", 0.1, 0); err == nil {
		t.Fatal("SetStopLoss accepted a zero trigger price")
	}
	if err := trader.SetStopLoss("BTCUSDT", "LONG", 0.1, math.Inf(1)); err == nil {
		t.Fatal("SetStopLoss accepted an infinite trigger price")
	}
	if len(rt.requests) != 0 {
		t.Fatalf("SetStopLoss emitted %d request(s) for invalid input", len(rt.requests))
	}
}

func TestBitgetSetTakeProfitRejectsNonPositiveTriggerWithoutRequest(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetTakeProfit("BTCUSDT", "LONG", 0.1, -5); err == nil {
		t.Fatal("SetTakeProfit accepted a negative trigger price")
	}
	if err := trader.SetTakeProfit("BTCUSDT", "LONG", 0.1, math.NaN()); err == nil {
		t.Fatal("SetTakeProfit accepted a NaN trigger price")
	}
	if len(rt.requests) != 0 {
		t.Fatalf("SetTakeProfit emitted %d request(s) for invalid input", len(rt.requests))
	}
}

func TestBitgetSetStopLossSurfacesAPIError(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
		return `{"code":"40001","msg":"bad request","data":{}}`
	}}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetStopLoss("BTCUSDT", "LONG", 0.1, 90000); err == nil || !strings.Contains(err.Error(), "40001") {
		t.Fatalf("SetStopLoss error = %v, want Bitget API code 40001 surfaced", err)
	}
}

func TestBitgetSetTakeProfitSurfacesAPIError(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
		return `{"code":"40001","msg":"bad request","data":{}}`
	}}
	trader := newTestBitgetTrader(rt)

	if err := trader.SetTakeProfit("BTCUSDT", "LONG", 0.1, 110000); err == nil || !strings.Contains(err.Error(), "40001") {
		t.Fatalf("SetTakeProfit error = %v, want Bitget API code 40001 surfaced", err)
	}
}

func TestBitgetModifyProtectionOrderValidatesInput(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.ModifyProtectionOrder("BTCUSDT", "", "stop_loss", "LONG", 0.1, 100); err == nil {
		t.Fatal("ModifyProtectionOrder accepted an empty order ID")
	}
	if err := trader.ModifyProtectionOrder("BTCUSDT", "sl-1", "trailing", "LONG", 0.1, 100); err == nil {
		t.Fatal("ModifyProtectionOrder accepted an unsupported kind")
	}
	if err := trader.ModifyProtectionOrder("BTCUSDT", "sl-1", "stop_loss", "LONG", 0.1, 0); err == nil {
		t.Fatal("ModifyProtectionOrder accepted a zero trigger price")
	}
	if len(rt.requests) != 0 {
		t.Fatalf("ModifyProtectionOrder emitted %d request(s) for invalid input", len(rt.requests))
	}
}

func TestBitgetModifyProtectionOrderTakeProfitShort(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	trader := newTestBitgetTrader(rt)

	if err := trader.ModifyProtectionOrder("BTCUSDT", "tp-1", "take_profit", "SHORT", 0.1, 90000.04); err != nil {
		t.Fatalf("ModifyProtectionOrder failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetModifyTPSLPath)
	if len(requests) != 1 {
		t.Fatalf("modify requests = %d, want one", len(requests))
	}
	body := requests[0].Body
	if body["orderId"] != "tp-1" ||
		body["productType"] != "USDT-FUTURES" ||
		body["marginCoin"] != "USDT" ||
		body["triggerType"] != "mark_price" ||
		body["triggerPrice"] != "90000.10000000" ||
		body["executePrice"] != "0" ||
		body["size"] != "0.100" {
		t.Fatalf("modify take-profit body = %#v, want atomic mark-price short target modification", body)
	}
}

func TestBitgetProtectionSnapshotParsesHedgePosSide(t *testing.T) {
	// Real Bitget orders-plan-pending responses carry posSide (long/short),
	// not holdSide. The snapshot must classify by that field.
	rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
		return `{"code":"00000","msg":"","data":{"entrustedList":[
			{"orderId":"sl-1","symbol":"BTCUSDT","posSide":"long","planType":"loss_plan","triggerPrice":"95","size":"0.1"},
			{"orderId":"tp-1","symbol":"BTCUSDT","posSide":"long","planType":"profit_plan","triggerPrice":"110","size":"0.1"}
		]}}`
	}}
	trader := newTestBitgetTrader(rt)

	snapshot, err := trader.GetProtectionSnapshot("BTCUSDT", "long")
	if err != nil {
		t.Fatalf("GetProtectionSnapshot failed: %v", err)
	}
	if snapshot.StopLoss.Status != types.ProtectionPresent || snapshot.StopLoss.Price != 95 || snapshot.StopLoss.OrderID != "sl-1" {
		t.Fatalf("stop snapshot = %+v, want present long stop", snapshot.StopLoss)
	}
	if snapshot.TakeProfit.Status != types.ProtectionPresent || snapshot.TakeProfit.Price != 110 || snapshot.TakeProfit.OrderID != "tp-1" {
		t.Fatalf("take-profit snapshot = %+v, want present long target", snapshot.TakeProfit)
	}

	// The same pending list queried for the short side must not leak long-side
	// protection orders into the snapshot.
	shortSnapshot, err := trader.GetProtectionSnapshot("BTCUSDT", "short")
	if err != nil {
		t.Fatalf("GetProtectionSnapshot(short) failed: %v", err)
	}
	if shortSnapshot.StopLoss.Status != types.ProtectionConfirmedAbsent || shortSnapshot.TakeProfit.Status != types.ProtectionConfirmedAbsent {
		t.Fatalf("short snapshot = %+v, want confirmed absent for both levels", shortSnapshot)
	}
}

func TestBitgetProtectionSnapshotFallsBackToHoldSide(t *testing.T) {
	// Older/mixed payloads may leave posSide empty but carry holdSide; the
	// position side must still be recognized.
	rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
		return `{"code":"00000","msg":"","data":{"entrustedList":[
			{"orderId":"tp-1","symbol":"BTCUSDT","holdSide":"short","planType":"profit_plan","triggerPrice":"90","size":"0.1"}
		]}}`
	}}
	trader := newTestBitgetTrader(rt)

	snapshot, err := trader.GetProtectionSnapshot("BTCUSDT", "short")
	if err != nil {
		t.Fatalf("GetProtectionSnapshot failed: %v", err)
	}
	if snapshot.TakeProfit.Status != types.ProtectionPresent || snapshot.TakeProfit.Price != 90 || snapshot.TakeProfit.OrderID != "tp-1" {
		t.Fatalf("take-profit snapshot = %+v, want present short target via holdSide fallback", snapshot.TakeProfit)
	}
}

func TestBitgetGetOpenOrdersUsesSideSpecificTriggerFields(t *testing.T) {
	// pos_profit/pos_loss orders carry their trigger in
	// stopSurplusTriggerPrice/stopLossTriggerPrice, not triggerPrice.
	rt := &bitgetRecordingTransport{responseBody: func(req *http.Request) string {
		if req.URL.Path == bitgetPendingPath {
			return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
		}
		return `{"code":"00000","msg":"","data":{"entrustedList":[
			{"orderId":"pos-tp","symbol":"BTCUSDT","posSide":"long","planType":"pos_profit","triggerPrice":"0","stopSurplusTriggerPrice":"110","size":""},
			{"orderId":"pos-sl","symbol":"BTCUSDT","posSide":"short","planType":"pos_loss","triggerPrice":"","stopLossTriggerPrice":"95","size":""}
		]}}`
	}}
	trader := newTestBitgetTrader(rt)

	orders, err := trader.GetOpenOrders("BTCUSDT")
	if err != nil {
		t.Fatalf("GetOpenOrders failed: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("orders = %d, want 2", len(orders))
	}
	if orders[0].Type != "TAKE_PROFIT_MARKET" || orders[0].StopPrice != 110 {
		t.Fatalf("pos_profit order = %#v, want TAKE_PROFIT_MARKET at 110", orders[0])
	}
	if orders[1].Type != "STOP_MARKET" || orders[1].StopPrice != 95 {
		t.Fatalf("pos_loss order = %#v, want STOP_MARKET at 95", orders[1])
	}
}

func TestBitgetCancelPlanOrdersGroupsByOriginalPlanType(t *testing.T) {
	rt := &bitgetRecordingTransport{}
	rt.responseBody = func(req *http.Request) string {
		if req.Method == http.MethodGet {
			return `{"code":"00000","msg":"","data":{"entrustedList":[
					{"orderId":"sl-1","symbol":"BTCUSDT","holdSide":"long","planType":"loss_plan","triggerPrice":"95","size":"0.1"},
					{"orderId":"sl-2","symbol":"BTCUSDT","holdSide":"long","planType":"pos_loss","triggerPrice":"94","size":""}
				]}}`
		}
		var captured *bitgetCapturedRequest
		for i := len(rt.requests) - 1; i >= 0; i-- {
			if rt.requests[i].Path == bitgetCancelPlanPath {
				captured = &rt.requests[i]
				break
			}
		}
		if captured == nil {
			return `{"code":"00000","msg":"","data":{}}`
		}
		ids, _ := captured.Body["orderIdList"].([]interface{})
		var parts []string
		for _, item := range ids {
			if m, ok := item.(map[string]interface{}); ok {
				if id, ok := m["orderId"].(string); ok && id != "" {
					parts = append(parts, fmt.Sprintf(`{"orderId":%q}`, id))
				}
			}
		}
		return `{"code":"00000","msg":"","data":{"successList":[` + strings.Join(parts, ",") + `],"failureList":[]}}`
	}
	trader := newTestBitgetTrader(rt)

	if err := trader.CancelStopLossOrders("BTCUSDT"); err != nil {
		t.Fatalf("CancelStopLossOrders failed: %v", err)
	}

	cancels := rt.requestsForPath(bitgetCancelPlanPath)
	if len(cancels) != 2 {
		t.Fatalf("cancel requests = %d, want one per original planType", len(cancels))
	}
	planTypes := map[string]bool{}
	for _, cancel := range cancels {
		planType, _ := cancel.Body["planType"].(string)
		planTypes[planType] = true
	}
	if !planTypes["loss_plan"] || !planTypes["pos_loss"] {
		t.Fatalf("cancel planTypes = %v, want loss_plan and pos_loss preserved", planTypes)
	}
}

func TestBitgetPendingPlanQueryUsesProfitLossFilter(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: func(*http.Request) string {
		return `{"code":"00000","msg":"","data":{"entrustedList":[]}}`
	}}
	trader := newTestBitgetTrader(rt)

	if _, err := trader.GetOpenOrders("BTCUSDT"); err != nil {
		t.Fatalf("GetOpenOrders failed: %v", err)
	}
	requests := rt.requestsForPath(bitgetPendingPlanPath)
	if len(requests) != 1 {
		t.Fatalf("pending plan requests = %d, want one", len(requests))
	}
	if !strings.Contains(requests[0].Query, "planType=profit_loss") {
		t.Fatalf("pending plan query = %q, want planType=profit_loss filter", requests[0].Query)
	}
}
