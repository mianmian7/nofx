package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func shadowCtx(q string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/shadow-eval/report?"+q, nil)
	return c
}

func TestParseShadowQuery(t *testing.T) {
	q, err := parseShadowQuery(shadowCtx("horizon=4h&group_by=model,direction&executed=1&source=jev&min_n=20&since=2026-10-08"))
	if err != nil || q.Horizon != "4h" || len(q.GroupBy) != 2 || q.Executed != "1" || q.Source != "jev" || q.MinN != 20 || q.Since.Day() != 8 {
		t.Fatalf("%+v %v", q, err)
	}
	for _, bad := range []string{"horizon=2h", "group_by=api_key", "executed=yes", "source=x", "min_n=0", "since=yesterday", "group_by=trader,model,symbol,direction,status"} {
		if _, err := parseShadowQuery(shadowCtx(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
