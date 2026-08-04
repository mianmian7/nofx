package okx

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"nofx/trader/types"
)

type capturedRequest struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

type recordingTransport struct {
	requests            []capturedRequest
	algoPendingResponse string
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]interface{}
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		trimmed := strings.TrimSpace(string(data))
		if strings.HasPrefix(trimmed, "{") {
			_ = json.Unmarshal(data, &body)
		} else if strings.HasPrefix(trimmed, "[") {
			var entries []map[string]interface{}
			if json.Unmarshal(data, &entries) == nil && len(entries) > 0 {
				body = entries[0]
			}
		}
	}

	rt.requests = append(rt.requests, capturedRequest{
		Method: req.Method,
		Path:   req.URL.Path,
		Body:   body,
	})

	response := `{"code":"0","msg":"","data":[]}`
	switch req.URL.Path {
	case okxInstrumentsPath:
		response = `{"code":"0","msg":"","data":[{"instId":"BTC-USDT-SWAP","ctVal":"0.01","ctMult":"1","lotSz":"1","minSz":"1","maxMktSz":"100000","tickSz":"0.1","ctType":"linear"}]}`
	case okxOrderPath:
		response = `{"code":"0","msg":"","data":[{"ordId":"123","clOrdId":"abc","sCode":"0","sMsg":""}]}`
	case okxAlgoPendingPath:
		if rt.algoPendingResponse != "" {
			response = rt.algoPendingResponse
		}
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(response)),
	}, nil
}

func (rt *recordingTransport) requestsForPath(path string) []capturedRequest {
	var matches []capturedRequest
	for _, req := range rt.requests {
		if req.Path == path {
			matches = append(matches, req)
		}
	}
	return matches
}

func newTestOKXTrader(rt *recordingTransport, isCrossMargin bool) *OKXTrader {
	return &OKXTrader{
		apiKey:        "key",
		secretKey:     "secret",
		passphrase:    "pass",
		isCrossMargin: isCrossMargin,
		positionMode:  "long_short_mode",
		httpClient: &http.Client{
			Transport: rt,
		},
		cacheDuration:        15 * time.Second,
		instrumentsCache:     make(map[string]*OKXInstrument),
		instrumentsCacheTime: time.Now(),
	}
}

func TestOKXSetLeverageUsesConfiguredMarginMode(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false)

	if err := trader.SetLeverage("BTCUSDT", 5); err != nil {
		t.Fatalf("SetLeverage failed: %v", err)
	}

	leverageRequests := rt.requestsForPath(okxLeveragePath)
	if len(leverageRequests) != 2 {
		t.Fatalf("expected 2 leverage requests, got %d", len(leverageRequests))
	}

	for _, req := range leverageRequests {
		if req.Body["mgnMode"] != "isolated" {
			t.Fatalf("expected isolated leverage mode, got %#v", req.Body["mgnMode"])
		}
	}
}

func TestOKXSetMarginModeUpdatesFutureRequestsWithoutAPIError(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, true)

	if err := trader.SetMarginMode("BTCUSDT", false); err != nil {
		t.Fatalf("SetMarginMode failed: %v", err)
	}

	if len(rt.requestsForPath("/api/v5/account/set-isolated-mode")) != 0 {
		t.Fatal("expected SetMarginMode not to call legacy isolated-mode endpoint")
	}

	if err := trader.SetLeverage("BTCUSDT", 5); err != nil {
		t.Fatalf("SetLeverage failed: %v", err)
	}

	leverageRequests := rt.requestsForPath(okxLeveragePath)
	if len(leverageRequests) != 2 {
		t.Fatalf("expected 2 leverage requests, got %d", len(leverageRequests))
	}

	for _, req := range leverageRequests {
		if req.Body["mgnMode"] != "isolated" {
			t.Fatalf("expected isolated leverage mode after SetMarginMode(false), got %#v", req.Body["mgnMode"])
		}
	}
}

func TestOKXOpenLongUsesConfiguredMarginMode(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false)

	if _, err := trader.OpenLong("BTCUSDT", 0.1, 5); err != nil {
		t.Fatalf("OpenLong failed: %v", err)
	}

	orderRequests := rt.requestsForPath(okxOrderPath)
	if len(orderRequests) == 0 {
		t.Fatal("expected at least one order request")
	}

	lastOrder := orderRequests[len(orderRequests)-1]
	if lastOrder.Body["tdMode"] != "isolated" {
		t.Fatalf("expected isolated tdMode, got %#v", lastOrder.Body["tdMode"])
	}
}

func TestOKXSetStopLossUsesConfiguredMarginMode(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false)

	if err := trader.SetStopLoss("BTCUSDT", "LONG", 0.1, 90000); err != nil {
		t.Fatalf("SetStopLoss failed: %v", err)
	}

	algoRequests := rt.requestsForPath(okxAlgoOrderPath)
	if len(algoRequests) != 1 {
		t.Fatalf("expected 1 algo order request, got %d", len(algoRequests))
	}

	if algoRequests[0].Body["tdMode"] != "isolated" {
		t.Fatalf("expected isolated tdMode, got %#v", algoRequests[0].Body["tdMode"])
	}
}

func TestNormalizeStopTriggerPriceKeepsTickRoundingProtective(t *testing.T) {
	if got := normalizeStopTriggerPrice(99.06, 0.1, "LONG"); math.Abs(got-99.1) > 1e-9 {
		t.Fatalf("long stop tick rounding = %.8f, want 99.10000000", got)
	}
	if got := normalizeStopTriggerPrice(100.04, 0.1, "SHORT"); math.Abs(got-100.0) > 1e-9 {
		t.Fatalf("short stop tick rounding = %.8f, want 100.00000000", got)
	}
}

func TestNormalizeTakeProfitTriggerPriceKeepsTickRoundingProtective(t *testing.T) {
	if got := normalizeTakeProfitTriggerPrice(99.06, 0.1, "LONG"); math.Abs(got-99.0) > 1e-9 {
		t.Fatalf("long take-profit tick rounding = %.8f, want 99.00000000", got)
	}
	if got := normalizeTakeProfitTriggerPrice(100.04, 0.1, "SHORT"); math.Abs(got-100.1) > 1e-9 {
		t.Fatalf("short take-profit tick rounding = %.8f, want 100.10000000", got)
	}
}

func TestOKXSetTakeProfitRoundsAbsolutePriceAndKeepsDirection(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false)

	if err := trader.SetTakeProfit("BTCUSDT", "LONG", 0.1, 90000.06); err != nil {
		t.Fatalf("long SetTakeProfit failed: %v", err)
	}
	if err := trader.SetTakeProfit("BTCUSDT", "SHORT", 0.1, 90000.04); err != nil {
		t.Fatalf("short SetTakeProfit failed: %v", err)
	}
	requests := rt.requestsForPath(okxAlgoOrderPath)
	if len(requests) != 2 {
		t.Fatalf("expected 2 algo order requests, got %d", len(requests))
	}
	if got := requests[0].Body["tpTriggerPx"]; got != "90000.00000000" {
		t.Fatalf("long take-profit trigger = %#v, want 90000.00000000", got)
	}
	if got := requests[1].Body["tpTriggerPx"]; got != "90000.10000000" {
		t.Fatalf("short take-profit trigger = %#v, want 90000.10000000", got)
	}
	if requests[0].Body["tpTriggerPx"] == "90000.06" || requests[1].Body["tpTriggerPx"] == "90000.04" {
		t.Fatal("take-profit request contained an unrounded/raw threshold instead of an absolute tick-aligned price")
	}
}

func TestOKXSetStopLossRoundsTriggerToProtectiveTick(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false)

	if err := trader.SetStopLoss("BTCUSDT", "LONG", 0.1, 90000.06); err != nil {
		t.Fatalf("long SetStopLoss failed: %v", err)
	}
	if err := trader.SetStopLoss("BTCUSDT", "SHORT", 0.1, 90000.04); err != nil {
		t.Fatalf("short SetStopLoss failed: %v", err)
	}

	requests := rt.requestsForPath(okxAlgoOrderPath)
	if len(requests) != 2 {
		t.Fatalf("expected 2 algo order requests, got %d", len(requests))
	}
	if got := requests[0].Body["slTriggerPx"]; got != "90000.10000000" {
		t.Fatalf("long trigger price = %#v, want 90000.10000000", got)
	}
	if got := requests[1].Body["slTriggerPx"]; got != "90000.00000000" {
		t.Fatalf("short trigger price = %#v, want 90000.00000000", got)
	}
}

func TestOKXPlaceLimitOrderUsesConfiguredMarginMode(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false)

	_, err := trader.PlaceLimitOrder(&types.LimitOrderRequest{
		Symbol:       "BTCUSDT",
		Side:         "BUY",
		PositionSide: "LONG",
		Price:        95000,
		Quantity:     0.1,
		Leverage:     3,
	})
	if err != nil {
		t.Fatalf("PlaceLimitOrder failed: %v", err)
	}

	orderRequests := rt.requestsForPath(okxOrderPath)
	if len(orderRequests) != 1 {
		t.Fatalf("expected 1 limit order request, got %d", len(orderRequests))
	}

	if orderRequests[0].Body["tdMode"] != "isolated" {
		t.Fatalf("expected isolated tdMode, got %#v", orderRequests[0].Body["tdMode"])
	}
}

func TestOKXCrossMarginModeUsedInLeverage(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, true) // cross margin

	if err := trader.SetLeverage("BTCUSDT", 10); err != nil {
		t.Fatalf("SetLeverage failed: %v", err)
	}

	leverageRequests := rt.requestsForPath(okxLeveragePath)
	if len(leverageRequests) != 2 {
		t.Fatalf("expected 2 leverage requests, got %d", len(leverageRequests))
	}

	for _, req := range leverageRequests {
		if req.Body["mgnMode"] != "cross" {
			t.Fatalf("expected cross leverage mode, got %#v", req.Body["mgnMode"])
		}
	}
}

func TestOKXOpenShortUsesConfiguredMarginMode(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false) // isolated

	if _, err := trader.OpenShort("BTCUSDT", 0.1, 5); err != nil {
		t.Fatalf("OpenShort failed: %v", err)
	}

	orderRequests := rt.requestsForPath(okxOrderPath)
	if len(orderRequests) == 0 {
		t.Fatal("expected at least one order request")
	}

	lastOrder := orderRequests[len(orderRequests)-1]
	if lastOrder.Body["tdMode"] != "isolated" {
		t.Fatalf("expected isolated tdMode for OpenShort, got %#v", lastOrder.Body["tdMode"])
	}
}

func TestOKXSetTakeProfitUsesConfiguredMarginMode(t *testing.T) {
	rt := &recordingTransport{}
	trader := newTestOKXTrader(rt, false) // isolated

	if err := trader.SetTakeProfit("BTCUSDT", "LONG", 0.1, 100000); err != nil {
		t.Fatalf("SetTakeProfit failed: %v", err)
	}

	algoRequests := rt.requestsForPath(okxAlgoOrderPath)
	if len(algoRequests) != 1 {
		t.Fatalf("expected 1 algo order request, got %d", len(algoRequests))
	}

	if algoRequests[0].Body["tdMode"] != "isolated" {
		t.Fatalf("expected isolated tdMode for SetTakeProfit, got %#v", algoRequests[0].Body["tdMode"])
	}
}

func TestOKXProtectionCancellationDoesNotDeleteOppositeAlgoOrder(t *testing.T) {
	rt := &recordingTransport{algoPendingResponse: `{"code":"0","msg":"","data":[{"algoId":"sl-1","instId":"BTC-USDT-SWAP","slTriggerPx":"90000","tpTriggerPx":"0"},{"algoId":"tp-1","instId":"BTC-USDT-SWAP","slTriggerPx":"0","tpTriggerPx":"100000"}]}`}
	trader := newTestOKXTrader(rt, false)

	if err := trader.CancelStopLossOrders("BTCUSDT"); err != nil {
		t.Fatalf("CancelStopLossOrders failed: %v", err)
	}
	if err := trader.CancelTakeProfitOrders("BTCUSDT"); err != nil {
		t.Fatalf("CancelTakeProfitOrders failed: %v", err)
	}

	var canceled []string
	for _, request := range rt.requestsForPath(okxCancelAlgoPath) {
		body, ok := request.Body["algoId"]
		if !ok {
			t.Fatalf("cancel request missing algoId: %#v", request.Body)
		}
		canceled = append(canceled, body.(string))
	}
	if len(canceled) != 2 || canceled[0] != "sl-1" || canceled[1] != "tp-1" {
		t.Fatalf("canceled algo IDs = %v, want [sl-1 tp-1]", canceled)
	}
}
