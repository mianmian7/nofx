package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHandleHealthReturnsTime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	(&Server{}).handleHealth(c)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	s, _ := body["time"].(string)
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil || time.Since(ts) > time.Minute {
		t.Fatalf("health time = %v (err %v)", body["time"], err)
	}
}
