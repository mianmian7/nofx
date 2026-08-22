package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMarketDataHealthEndpointIncludesCapabilitiesAndStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/market-data/health", nil)

	server := &Server{}
	server.handleMarketDataHealth(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response struct {
		Ready        bool              `json:"ready"`
		Capabilities map[string]string `json:"capabilities"`
		KlineSources map[string]any    `json:"kline_sources"`
		DepthStreams map[string]any    `json:"depth_streams"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Capabilities["binance"] == "" || response.Capabilities["lighter"] != "none" || response.KlineSources == nil || response.DepthStreams == nil {
		t.Fatalf("market-data health response = %#v", response)
	}
}
