package bybit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	bybit "github.com/bybit-exchange/bybit.go.api"
	"nofx/trader/types"
)

func TestBybit_EmergencyClosePosition_RequestCapture(t *testing.T) {
	var capturedOrders []map[string]interface{}
	mockPositionAmt := "0" // Simulated position amt on exchange

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path

		switch {
		// Capture order submission
		case path == "/v5/order/create" && r.Method == "POST":
			bodyBytes, _ := io.ReadAll(r.Body)
			var body map[string]interface{}
			_ = json.Unmarshal(bodyBytes, &body)
			capturedOrders = append(capturedOrders, body)

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"retCode": 0,
				"retMsg":  "OK",
				"result": map[string]interface{}{
					"orderId":     "bybit_order_12345",
					"orderLinkId": "custom_link_id",
				},
			})

		// Mock order history query
		case path == "/v5/order/history":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"retCode": 0,
				"retMsg":  "OK",
				"result": map[string]interface{}{
					"list": []map[string]interface{}{
						{
							"orderId":     "bybit_order_12345",
							"orderStatus": "Filled",
							"avgPrice":    "65000.0",
							"cumExecQty":  "0.05",
							"cumExecFee":  "0.02",
						},
					},
				},
			})

		// Mock position query
		case path == "/v5/position/list":
			positionsList := []map[string]interface{}{}
			if mockPositionAmt != "0" {
				positionsList = append(positionsList, map[string]interface{}{
					"symbol":      "BTCUSDT",
					"side":        "Buy", // long in Bybit
					"size":        mockPositionAmt,
					"entryPrice":  "64000.0",
					"markPrice":   "65000.0",
					"leverage":    "10",
					"positionIdx": 0,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"retCode": 0,
				"retMsg":  "OK",
				"result": map[string]interface{}{
					"list": positionsList,
				},
			})

		// Instrument info
		case path == "/v5/market/instruments-info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"retCode": 0,
				"result": map[string]interface{}{
					"list": []map[string]interface{}{
						{
							"symbol": "BTCUSDT",
							"lotSizeFilter": map[string]interface{}{
								"qtyStep": "0.001",
								"minQty":  "0.001",
							},
						},
					},
				},
			})

		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"retCode": 0,
				"result":  map[string]interface{}{},
			})
		}
	}))
	defer mockServer.Close()

	client := bybit.NewBybitHttpClient("test_key", "test_secret", bybit.WithBaseURL(mockServer.URL))
	trader := &BybitTrader{
		apiKey:        "test_key",
		secretKey:     "test_secret",
		client:        client,
		cacheDuration: 0,
		qtyStepCache:  make(map[string]float64),
	}

	closer, ok := any(trader).(types.EmergencyCloser)
	if !ok {
		t.Fatalf("BybitTrader does not implement types.EmergencyCloser")
	}

	// 1. Test LONG emergency close (should be Sell, reduceOnly=true, qty=0.050)
	res, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.05)
	if err != nil {
		t.Fatalf("EmergencyClosePosition LONG failed: %v", err)
	}

	if !res.Submitted {
		t.Errorf("expected Submitted = true")
	}
	if !res.Filled {
		t.Errorf("expected Filled = true")
	}
	if !res.PositionZero {
		t.Errorf("expected PositionZero = true when position list is empty")
	}

	if len(capturedOrders) == 0 {
		t.Fatalf("no order captured")
	}
	lastOrder := capturedOrders[len(capturedOrders)-1]
	if lastOrder["side"] != "Sell" {
		t.Errorf("captured side = %v, want Sell", lastOrder["side"])
	}
	if lastOrder["reduceOnly"] != true {
		t.Errorf("captured reduceOnly = %v, want true", lastOrder["reduceOnly"])
	}
	if lastOrder["orderType"] != "Market" {
		t.Errorf("captured orderType = %v, want Market", lastOrder["orderType"])
	}

	// 2. Test SHORT emergency close (should be Buy, reduceOnly=true)
	resShort, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "SHORT", 0.02)
	if err != nil {
		t.Fatalf("EmergencyClosePosition SHORT failed: %v", err)
	}
	if !resShort.Submitted || !resShort.Filled || !resShort.PositionZero {
		t.Errorf("expected short emergency close to succeed and confirm zero")
	}
	shortOrder := capturedOrders[len(capturedOrders)-1]
	if shortOrder["side"] != "Buy" {
		t.Errorf("captured side for short = %v, want Buy", shortOrder["side"])
	}
	if shortOrder["reduceOnly"] != true {
		t.Errorf("captured reduceOnly for short = %v, want true", shortOrder["reduceOnly"])
	}

	// 3. Test PositionZero=false when position still exists
	mockPositionAmt = "0.05"
	resNotZero, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.05)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resNotZero.PositionZero {
		t.Errorf("expected PositionZero = false when position still exists on exchange")
	}
}
