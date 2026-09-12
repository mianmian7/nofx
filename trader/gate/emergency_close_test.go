package gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gateio/gateapi-go/v6"
	"nofx/trader/types"
)

func TestGate_EmergencyClosePosition_RequestCapture(t *testing.T) {
	var capturedOrders []gateapi.FuturesOrder
	mockPositionAmt := "0" // 0 means closed

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path

		switch {
		// Capture futures order
		case path == "/api/v4/futures/usdt/orders" && r.Method == "POST":
			bodyBytes, _ := io.ReadAll(r.Body)
			var order gateapi.FuturesOrder
			_ = json.Unmarshal(bodyBytes, &order)
			capturedOrders = append(capturedOrders, order)

			_ = json.NewEncoder(w).Encode(gateapi.FuturesOrder{
				Id:        12345678,
				Contract:  order.Contract,
				Size:      order.Size,
				Status:    "finished",
				FinishAs:  "filled",
				FillPrice: "65000.0",
			})

		// Contract info
		case path == "/api/v4/futures/usdt/contracts/BTC_USDT":
			_ = json.NewEncoder(w).Encode(gateapi.Contract{
				Name:             "BTC_USDT",
				QuantoMultiplier: "0.0001",
			})

		// Positions query
		case path == "/api/v4/futures/usdt/positions":
			positions := []gateapi.Position{}
			if mockPositionAmt != "0" {
				positions = append(positions, gateapi.Position{
					Contract: "BTC_USDT",
					Size:     500, // 500 * 0.0001 = 0.05
				})
			}
			_ = json.NewEncoder(w).Encode(positions)

		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	defer mockServer.Close()

	config := gateapi.NewConfiguration()
	config.BasePath = mockServer.URL + "/api/v4"
	config.HTTPClient = mockServer.Client()

	trader := &GateTrader{
		client:        gateapi.NewAPIClient(config),
		ctx:           context.Background(),
		cacheDuration: 0,
		contractsCache: map[string]*gateapi.Contract{
			"BTC_USDT": {
				Name:             "BTC_USDT",
				QuantoMultiplier: "0.0001",
			},
		},
	}

	closer, ok := any(trader).(types.EmergencyCloser)
	if !ok {
		t.Fatalf("GateTrader does not implement types.EmergencyCloser")
	}

	// 1. Test LONG emergency close (Size must be negative, reduceOnly=true)
	res, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.05)
	if err != nil {
		t.Fatalf("EmergencyClosePosition LONG failed: %v", err)
	}
	if !res.Submitted || !res.Filled || !res.PositionZero {
		t.Errorf("expected LONG emergency close to succeed with PositionZero=true")
	}

	if len(capturedOrders) == 0 {
		t.Fatalf("no order captured")
	}
	longOrder := capturedOrders[len(capturedOrders)-1]
	if longOrder.Size >= 0 {
		t.Errorf("expected negative Size for LONG close, got: %d", longOrder.Size)
	}
	if !longOrder.ReduceOnly {
		t.Errorf("expected ReduceOnly = true")
	}
	if longOrder.Tif != "ioc" {
		t.Errorf("expected Tif = ioc, got: %s", longOrder.Tif)
	}

	// 2. Test SHORT emergency close (Size must be positive, reduceOnly=true)
	resShort, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "SHORT", 0.05)
	if err != nil {
		t.Fatalf("EmergencyClosePosition SHORT failed: %v", err)
	}
	if !resShort.Submitted || !resShort.Filled || !resShort.PositionZero {
		t.Errorf("expected SHORT emergency close to succeed with PositionZero=true")
	}
	shortOrder := capturedOrders[len(capturedOrders)-1]
	if shortOrder.Size <= 0 {
		t.Errorf("expected positive Size for SHORT close, got: %d", shortOrder.Size)
	}
	if !shortOrder.ReduceOnly {
		t.Errorf("expected ReduceOnly = true")
	}

	// 3. Test PositionZero=false when position remains
	mockPositionAmt = "500"
	resNotZero, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.05)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resNotZero.PositionZero {
		t.Errorf("expected PositionZero = false when position still exists")
	}
}
