package hyperliquid

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sonirico/go-hyperliquid"
	"nofx/trader/types"
)

func TestHyperliquid_EmergencyClosePosition_RequestCapture(t *testing.T) {
	var capturedActions []hyperliquid.OrderAction
	mockPositionAmt := "0" // 0 means position is closed on exchange

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		bodyBytes, _ := io.ReadAll(r.Body)

		if r.URL.Path == "/exchange" {
			var payload struct {
				Action hyperliquid.OrderAction `json:"action"`
				Nonce  int64                   `json:"nonce"`
			}
			if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Action.Type == "order" {
				capturedActions = append(capturedActions, payload.Action)

				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "ok",
					"response": map[string]any{
						"type": "order",
						"data": map[string]any{
							"statuses": []map[string]any{
								{
									"filled": map[string]any{
										"totalSz": "0.05",
										"avgPx":   "65000.0",
										"oid":     987654321,
									},
								},
							},
						},
					},
				})
				return
			}
		}

		if r.URL.Path == "/info" {
			var req map[string]interface{}
			_ = json.Unmarshal(bodyBytes, &req)
			reqType, _ := req["type"].(string)

			switch reqType {
			case "meta":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"universe": []map[string]any{
						{"name": "BTC", "szDecimals": 3},
					},
				})
			case "clearinghouseState":
				positionsList := []map[string]any{}
				if mockPositionAmt != "0" {
					positionsList = append(positionsList, map[string]any{
						"position": map[string]any{
							"coin":        "BTC",
							"szi":         mockPositionAmt,
							"entryPx":     "64000.0",
							"leverage":    map[string]any{"type": "cross", "value": 10},
							"positionVal": "3250.0",
						},
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"assetPositions": positionsList,
				})
			case "allMids":
				_ = json.NewEncoder(w).Encode(map[string]string{
					"BTC": "65000.0",
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{})
			}
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer mockServer.Close()

	privateKey, _ := crypto.GenerateKey()
	walletAddr := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()

	exchange := hyperliquid.NewExchange(
		context.Background(),
		privateKey,
		mockServer.URL,
		&hyperliquid.Meta{
			Universe: []hyperliquid.AssetInfo{
				{Name: "BTC", SzDecimals: 3},
			},
		},
		"",
		walletAddr,
		nil,
		nil,
	)

	trader := &HyperliquidTrader{
		exchange:   exchange,
		ctx:        context.Background(),
		walletAddr: walletAddr,
		meta: &hyperliquid.Meta{
			Universe: []hyperliquid.AssetInfo{
				{Name: "BTC", SzDecimals: 3},
			},
		},
	}

	closer, ok := any(trader).(types.EmergencyCloser)
	if !ok {
		t.Fatalf("HyperliquidTrader does not implement types.EmergencyCloser")
	}

	// 1. Test LONG emergency close (IsBuy=false, ReduceOnly=true, Tif=Ioc)
	res, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.05)
	if err != nil {
		t.Fatalf("EmergencyClosePosition LONG failed: %v", err)
	}

	if !res.Submitted || !res.Filled || !res.PositionZero {
		t.Errorf("expected LONG emergency close to succeed with PositionZero=true")
	}
	if res.OrderID != "987654321" {
		t.Errorf("expected real OID 987654321, got: %s", res.OrderID)
	}

	if len(capturedActions) == 0 {
		t.Fatalf("no order action captured")
	}
	orderWire := capturedActions[0].Orders[0]
	if orderWire.IsBuy {
		t.Errorf("expected IsBuy = false for close long, got true")
	}
	if !orderWire.ReduceOnly {
		t.Errorf("expected ReduceOnly = true for emergency close")
	}
	if orderWire.OrderType.Limit.Tif != hyperliquid.TifIoc {
		t.Errorf("expected Tif = Ioc, got: %s", orderWire.OrderType.Limit.Tif)
	}

	// 2. Test SHORT emergency close (IsBuy=true)
	capturedActions = nil
	resShort, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "SHORT", 0.05)
	if err != nil {
		t.Fatalf("EmergencyClosePosition SHORT failed: %v", err)
	}
	if !resShort.Submitted || !resShort.Filled || !resShort.PositionZero {
		t.Errorf("expected SHORT emergency close to succeed with PositionZero=true")
	}
	shortWire := capturedActions[0].Orders[0]
	if !shortWire.IsBuy {
		t.Errorf("expected IsBuy = true for close short, got false")
	}
	if !shortWire.ReduceOnly {
		t.Errorf("expected ReduceOnly = true for close short")
	}

	// 3. Test PositionZero=false when position remains on exchange
	mockPositionAmt = "0.05"
	resNotZero, err := closer.EmergencyClosePosition(context.Background(), "BTCUSDT", "LONG", 0.05)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resNotZero.PositionZero {
		t.Errorf("expected PositionZero = false when position still exists on exchange")
	}
}
