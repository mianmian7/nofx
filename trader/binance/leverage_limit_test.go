package binance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adshao/go-binance/v2/futures"
)

func TestGetMaxLeverageUsesSymbolBracketAndCaches(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/leverageBracket" {
			http.NotFound(w, r)
			return
		}
		requests++
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"symbol": "DRAMUSDT",
				"brackets": []map[string]interface{}{
					{"bracket": 1, "initialLeverage": 20, "notionalCap": 100000, "notionalFloor": 0, "maintMarginRatio": 0.025, "cum": 0},
				},
			},
		})
	}))
	defer server.Close()

	client := futures.NewClient("key", "secret")
	client.BaseURL = server.URL
	client.HTTPClient = server.Client()
	trader := &FuturesTrader{client: client, cacheDuration: 0}

	for i := 0; i < 2; i++ {
		got, err := trader.GetMaxLeverage("DRAMUSDT")
		if err != nil {
			t.Fatalf("GetMaxLeverage failed: %v", err)
		}
		if got != 20 {
			t.Fatalf("max leverage = %d, want 20", got)
		}
	}
	if requests != 1 {
		t.Fatalf("leverage bracket requests = %d, want 1", requests)
	}
}

func TestGetMaxLeverageForNotionalUsesMatchingTier(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/leverageBracket" {
			http.NotFound(w, r)
			return
		}
		requests++
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"symbol": "DRAMUSDT",
				"brackets": []map[string]interface{}{
					{"bracket": 1, "initialLeverage": 20, "notionalCap": 100000, "notionalFloor": 0},
					{"bracket": 2, "initialLeverage": 10, "notionalCap": 500000, "notionalFloor": 100000},
				},
			},
		})
	}))
	defer server.Close()

	client := futures.NewClient("key", "secret")
	client.BaseURL = server.URL
	client.HTTPClient = server.Client()
	trader := &FuturesTrader{client: client}

	got, err := trader.GetMaxLeverageForNotional("DRAMUSDT", 50_000)
	if err != nil || got != 20 {
		t.Fatalf("small-tier leverage = %d, err=%v; want 20", got, err)
	}
	got, err = trader.GetMaxLeverageForNotional("DRAMUSDT", 150_000)
	if err != nil || got != 10 {
		t.Fatalf("large-tier leverage = %d, err=%v; want 10", got, err)
	}
	if requests != 1 {
		t.Fatalf("leverage bracket requests = %d, want 1", requests)
	}
}
