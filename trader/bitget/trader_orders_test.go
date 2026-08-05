package bitget

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"nofx/trader/types"
	"strings"
	"testing"
	"time"
)

type bitgetCapturedRequest struct {
	Path string
	Body map[string]interface{}
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
	rt.requests = append(rt.requests, bitgetCapturedRequest{Path: req.URL.Path, Body: body})
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
