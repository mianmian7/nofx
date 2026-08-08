package bitget

import (
	"net/http"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

// fillHistoryResponse returns a responseBody function that serves an empty
// position book and the given fill-history fillList payload.
func fillHistoryResponse(fills string) func(*http.Request) string {
	return func(req *http.Request) string {
		if req.URL.Path == bitgetPositionPath {
			return `{"code":"00000","msg":"","data":[]}`
		}
		return `{"code":"00000","msg":"","data":{"fillList":[` + fills + `]}}`
	}
}

func TestBitgetGetTradesMapsHedgeAndLiquidationTradeSides(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: fillHistoryResponse(`
		{"tradeId":"t1","symbol":"BTCUSDT","orderId":"o1","side":"buy","price":"60000","baseVolume":"0.1","profit":"0","cTime":"1700000000000","tradeSide":"open"},
		{"tradeId":"t2","symbol":"BTCUSDT","orderId":"o2","side":"sell","price":"61000","baseVolume":"0.1","profit":"100","cTime":"1700000000000","tradeSide":"close"},
		{"tradeId":"t3","symbol":"ETHUSDT","orderId":"o3","side":"sell","price":"3000","baseVolume":"1","profit":"0","cTime":"1700000000000","tradeSide":"open"},
		{"tradeId":"t4","symbol":"ETHUSDT","orderId":"o4","side":"buy","price":"2900","baseVolume":"1","profit":"100","cTime":"1700000000000","tradeSide":"close"},
		{"tradeId":"t5","symbol":"BTCUSDT","orderId":"o5","side":"sell","price":"59000","baseVolume":"0.05","profit":"-50","cTime":"1700000000000","tradeSide":"burst_close_long"},
		{"tradeId":"t6","symbol":"ETHUSDT","orderId":"o6","side":"buy","price":"3100","baseVolume":"0.5","profit":"-30","cTime":"1700000000000","tradeSide":"reduce_close_short"},
		{"tradeId":"t7","symbol":"BTCUSDT","orderId":"o7","side":"buy","price":"60000","baseVolume":"0.1","profit":"0","cTime":"1700000000000","tradeSide":"buy_single"},
		{"tradeId":"t8","symbol":"BTCUSDT","orderId":"o8","side":"sell","price":"62000","baseVolume":"0.1","profit":"200","cTime":"1700000000000","tradeSide":"sell_single"}
	`)}
	trader := newTestBitgetTrader(rt)

	trades, err := trader.GetTrades(time.Now().Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("GetTrades failed: %v", err)
	}
	want := []string{
		"open_long",   // hedge open buy
		"close_short", // hedge close sell closes a short
		"open_short",  // hedge open sell
		"close_long",  // hedge close buy closes a long
		"close_long",  // burst liquidation of a long
		"close_short", // partial liquidation of a short
		"open_long",   // one-way buy_single
		"close_long",  // one-way sell_single
	}
	if len(trades) != len(want) {
		t.Fatalf("trades = %d, want %d", len(trades), len(want))
	}
	for i, action := range want {
		if trades[i].OrderAction != action {
			t.Fatalf("trade %d action = %s, want %s", i, trades[i].OrderAction, action)
		}
	}
}

func TestBitgetGetTradesCloseSideMapsClosedPosition(t *testing.T) {
	// Bitget hedge mode reports the side of the position being closed on a
	// close fill: sell closes a short, buy closes a long. Regression test for
	// the previously inverted close mapping, which made the position builder
	// skip real closes and drop their realized PnL.
	tests := []struct {
		name      string
		tradeSide string
		side      string
		want      string
	}{
		{name: "close buy closes long", tradeSide: "close", side: "buy", want: "close_long"},
		{name: "close sell closes short", tradeSide: "close", side: "sell", want: "close_short"},
		{name: "open buy opens long", tradeSide: "open", side: "buy", want: "open_long"},
		{name: "open sell opens short", tradeSide: "open", side: "sell", want: "open_short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &bitgetRecordingTransport{responseBody: fillHistoryResponse(`
				{"tradeId":"t1","symbol":"BTCUSDT","orderId":"o1","side":"` + tt.side + `","price":"60000","baseVolume":"0.1","profit":"0","cTime":"1700000000000","tradeSide":"` + tt.tradeSide + `"}
			`)}
			trader := newTestBitgetTrader(rt)

			trades, err := trader.GetTrades(time.Now().Add(-24*time.Hour), 100)
			if err != nil {
				t.Fatalf("GetTrades failed: %v", err)
			}
			if len(trades) != 1 || trades[0].OrderAction != tt.want {
				t.Fatalf("trades = %#v, want one trade with action %s", trades, tt.want)
			}
		})
	}
}

func TestBitgetSyncOrdersStoresHedgePositionSide(t *testing.T) {
	rt := &bitgetRecordingTransport{responseBody: fillHistoryResponse(`
		{"tradeId":"t1","symbol":"BTCUSDT","orderId":"o1","side":"buy","price":"60000","baseVolume":"0.1","profit":"0","cTime":"1700000000000","tradeSide":"open"},
		{"tradeId":"t2","symbol":"BTCUSDT","orderId":"o2","side":"sell","price":"61000","baseVolume":"0.1","profit":"100","cTime":"1700000000000","tradeSide":"close"},
		{"tradeId":"t3","symbol":"ETHUSDT","orderId":"o3","side":"sell","price":"3000","baseVolume":"1","profit":"0","cTime":"1700000000000","tradeSide":"open"},
		{"tradeId":"t4","symbol":"ETHUSDT","orderId":"o4","side":"sell","price":"2900","baseVolume":"1","profit":"100","cTime":"1700000000000","tradeSide":"burst_close_long"}
	`)}
	trader := newTestBitgetTrader(rt)

	st, err := store.New(filepath.Join(t.TempDir(), "bitget-sync.db"))
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := trader.SyncOrdersFromBitget("trader-1", "exch-1", "bitget", st); err != nil {
		t.Fatalf("SyncOrdersFromBitget failed: %v", err)
	}

	orders, err := st.Order().GetTraderOrders("trader-1", 100)
	if err != nil {
		t.Fatalf("GetTraderOrders failed: %v", err)
	}
	if len(orders) != 4 {
		t.Fatalf("synced orders = %d, want 4", len(orders))
	}

	// Bitget runs in hedge mode, so every synced order record must carry a real
	// LONG/SHORT position side — never the one-way "BOTH" sentinel.
	want := map[string]string{
		"open_long":   "LONG",
		"close_long":  "LONG",
		"open_short":  "SHORT",
		"close_short": "SHORT",
	}
	got := map[string]string{}
	for _, o := range orders {
		got[o.OrderAction] = o.PositionSide
	}
	for action, side := range want {
		if got[action] != side {
			t.Fatalf("synced %s positionSide = %q, want %q", action, got[action], side)
		}
	}
}
