package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

type depthMarketStub struct {
	symbol string
	limit  int
	calls  int
}

func (s *depthMarketStub) GetDepth(symbol string, limit int) (*market.BinanceDepthSnapshot, error) {
	s.calls++
	s.symbol, s.limit = symbol, limit
	return &market.BinanceDepthSnapshot{
		LastUpdateID: 42,
		Bids:         [][]string{{"4119.06", "15.874"}},
		Asks:         [][]string{{"4119.07", "1.590"}},
	}, nil
}

func TestHandleDepthRejectsQuerySuffixContaminatedSymbol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &depthMarketStub{}
	server := &Server{router: gin.New(), depthMarketClient: stub}
	server.router.GET("/api/depth", server.handleDepth)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/depth?symbol=SOXLUSDT%26limit%3D20&limit=20", nil)
	server.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("upstream calls = %d, want 0", stub.calls)
	}
}

func TestHandleDepthReturnsPublicBinanceSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &depthMarketStub{}
	server := &Server{router: gin.New(), depthMarketClient: stub}
	server.router.GET("/api/depth", server.handleDepth)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/depth?symbol=MUUSDT&limit=20", nil)
	server.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.symbol != "MUUSDT" || stub.limit != 20 {
		t.Fatalf("upstream args = %q/%d", stub.symbol, stub.limit)
	}
	var got market.BinanceDepthSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Bids[0][0] != "4119.06" || got.Asks[0][0] != "4119.07" {
		t.Fatalf("depth = %#v", got)
	}
}
