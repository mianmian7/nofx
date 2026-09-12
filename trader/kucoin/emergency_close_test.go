package kucoin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nofx/trader/types"
)

type capturedKuCoinRequest struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

type kucoinRecordingTransport struct {
	requests        []capturedKuCoinRequest
	mockPositionAmt string
}

func (rt *kucoinRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]interface{}
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		trimmed := strings.TrimSpace(string(data))
		if strings.HasPrefix(trimmed, "{") {
			_ = json.Unmarshal(data, &body)
		}
	}

	rt.requests = append(rt.requests, capturedKuCoinRequest{
		Method: req.Method,
		Path:   req.URL.Path,
		Body:   body,
	})

	response := `{"code":"200000","msg":"","data":{}}`
	switch {
	case req.Method == "POST" && req.URL.Path == kucoinOrderPath:
		response = `{"code":"200000","msg":"","data":{"orderId":"kc_emg_9876"}}`
	case req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/api/v1/orders/"):
		response = `{"code":"200000","msg":"","data":{"id":"kc_emg_9876","symbol":"XBTUSDTM","status":"done","dealAvgPrice":65000.0,"dealSize":5}}`
	case req.Method == "GET" && req.URL.Path == "/api/v1/positions":
		if rt.mockPositionAmt != "0" {
			response = `{"code":"200000","msg":"","data":[{"symbol":"XBTUSDTM","currentQty":5,"isOpen":true,"settleCurrency":"USDT","mgnMode":"CROSS"}]}`
		} else {
			response = `{"code":"200000","msg":"","data":[]}`
		}
	case req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/api/v1/contracts/"):
		response = `{"code":"200000","msg":"","data":[{"symbol":"XBTUSDTM","multiplier":0.001,"tickSize":0.1,"lotSize":1}]}`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(response)),
	}, nil
}

func TestKuCoin_EmergencyClosePosition_RequestCapture(t *testing.T) {
	rt := &kucoinRecordingTransport{mockPositionAmt: "0"}
	trader := &KuCoinTrader{
		apiKey:        "key",
		secretKey:     "secret",
		passphrase:    "pass",
		cacheDuration: 0,
		httpClient: &http.Client{
			Transport: rt,
		},
		contractsCache: map[string]*KuCoinContract{
			"XBTUSDTM": {
				Symbol:     "XBTUSDTM",
				Multiplier: 0.001,
				TickSize:   0.1,
				LotSize:    1,
			},
		},
		contractsCacheTime: time.Now(),
	}

	closer, ok := any(trader).(types.EmergencyCloser)
	if !ok {
		t.Fatalf("KuCoinTrader does not implement types.EmergencyCloser")
	}

	// 1. Test LONG emergency close
	res, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.005)
	if err != nil {
		t.Fatalf("EmergencyClosePosition LONG failed: %v", err)
	}

	if !res.Submitted || !res.Filled || !res.PositionZero {
		t.Errorf("expected LONG emergency close to succeed with PositionZero=true, got res: %+v", res)
	}

	var postOrders []capturedKuCoinRequest
	for _, r := range rt.requests {
		if r.Method == "POST" && r.Path == kucoinOrderPath {
			postOrders = append(postOrders, r)
		}
	}
	if len(postOrders) == 0 {
		t.Fatalf("no order request captured")
	}
	lastOrder := postOrders[0]
	if lastOrder.Body["side"] != "sell" {
		t.Errorf("captured side = %v, want sell", lastOrder.Body["side"])
	}
	if lastOrder.Body["reduceOnly"] != true {
		t.Errorf("captured reduceOnly = %v, want true", lastOrder.Body["reduceOnly"])
	}
	if lastOrder.Body["closeOrder"] != true {
		t.Errorf("captured closeOrder = %v, want true", lastOrder.Body["closeOrder"])
	}

	// 2. Test SHORT emergency close
	resShort, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "SHORT", 0.005)
	if err != nil {
		t.Fatalf("EmergencyClosePosition SHORT failed: %v", err)
	}
	if !resShort.Submitted || !resShort.PositionZero {
		t.Errorf("expected SHORT emergency close to succeed with PositionZero=true")
	}

	postOrders = nil
	for _, r := range rt.requests {
		if r.Method == "POST" && r.Path == kucoinOrderPath {
			postOrders = append(postOrders, r)
		}
	}
	if len(postOrders) < 2 {
		t.Fatalf("expected at least 2 POST order requests")
	}
	shortOrder := postOrders[len(postOrders)-1]
	if shortOrder.Body["side"] != "buy" {
		t.Errorf("captured side for short = %v, want buy", shortOrder.Body["side"])
	}
	if shortOrder.Body["reduceOnly"] != true {
		t.Errorf("captured reduceOnly for short = %v, want true", shortOrder.Body["reduceOnly"])
	}

	// 3. Test PositionZero=false when position remains
	rt.mockPositionAmt = "5"
	resNotZero, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.005)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resNotZero.PositionZero {
		t.Errorf("expected PositionZero = false when position still exists on KuCoin")
	}
}
