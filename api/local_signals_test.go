package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLocalSignalRoutesRequireAuthentication(t *testing.T) {
	s := NewServer(nil, nil, nil, 0)
	for _, path := range []string{"/api/local-signals", "/api/local-signals/history?symbol=BTCUSDT"} {
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status %d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestLocalSignalQueryValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{}
	r := gin.New()
	r.GET("/signals", s.handleLocalSignals)
	r.GET("/history", s.handleLocalSignalHistory)
	for _, path := range []string{"/signals?exchange=hyperliquid", "/history?exchange=okx&symbol=BTCUSDC", "/history?exchange=binance&symbol=../BTC"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status %d", path, w.Code)
		}
	}
}
