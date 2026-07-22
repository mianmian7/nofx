package market

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetFundingSnapshotReturnsCurrentRateMarkAndNextTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/fapi/v1/premiumIndex" || req.URL.Query().Get("symbol") != "XAUUSDT" {
			t.Fatalf("request = %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"symbol":"XAUUSDT","markPrice":"4119.06500000","indexPrice":"4119.07000000","lastFundingRate":"0.00010000","nextFundingTime":1784678400000,"time":1784664000000}`)
	}))
	defer server.Close()

	snapshot, err := NewAPIClientWithBaseURL(server.URL).GetFundingSnapshot("XAUUSDT")
	if err != nil {
		t.Fatalf("GetFundingSnapshot: %v", err)
	}
	if snapshot.Symbol != "XAUUSDT" || snapshot.MarkPrice != 4119.065 || snapshot.IndexPrice != 4119.07 || snapshot.Rate != 0.0001 || snapshot.NextFundingTime != 1784678400000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestGetFundingHistoryReturnsEventsAscending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/fapi/v1/fundingRate" || req.URL.Query().Get("symbol") != "MUUSDT" || req.URL.Query().Get("startTime") != "100" || req.URL.Query().Get("endTime") != "300" || req.URL.Query().Get("limit") != "1000" {
			t.Fatalf("request = %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[
			{"symbol":"MUUSDT","fundingRate":"-0.00020000","fundingTime":200,"markPrice":"120.50"},
			{"symbol":"MUUSDT","fundingRate":"0.00010000","fundingTime":100,"markPrice":"119.25"}
		]`)
	}))
	defer server.Close()

	events, err := NewAPIClientWithBaseURL(server.URL).GetFundingHistory("MUUSDT", 100, 300)
	if err != nil {
		t.Fatalf("GetFundingHistory: %v", err)
	}
	if len(events) != 2 || events[0].FundingTime != 100 || events[1].FundingTime != 200 || events[1].Rate != -0.0002 || events[1].MarkPrice != 120.5 {
		t.Fatalf("events = %#v", events)
	}
}

func TestGetFundingInfoUsesExchangeIntervalInsteadOfAssumingEightHours(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/fapi/v1/fundingInfo" || req.URL.Query().Get("symbol") != "XAUUSDT" {
			t.Fatalf("request = %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[{"symbol":"XAUUSDT","adjustedFundingRateCap":"0.00300000","adjustedFundingRateFloor":"-0.00300000","fundingIntervalHours":4}]`)
	}))
	defer server.Close()

	info, err := NewAPIClientWithBaseURL(server.URL).GetFundingInfo("XAUUSDT")
	if err != nil {
		t.Fatalf("GetFundingInfo: %v", err)
	}
	if info.Symbol != "XAUUSDT" || info.IntervalHours != 4 || info.RateCap != 0.003 || info.RateFloor != -0.003 {
		t.Fatalf("info = %#v", info)
	}
}

func TestGetFundingHistoryPaginatesBeyondOneThousandEvents(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		start := int64(1000)
		count := 1000
		if calls == 2 {
			if got := req.URL.Query().Get("startTime"); got != "2000" {
				t.Fatalf("second startTime = %q, want 2000", got)
			}
			start, count = 2000, 1
		}
		items := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			items = append(items, map[string]any{
				"symbol": "MUUSDT", "fundingRate": "0.0001",
				"fundingTime": start + int64(i), "markPrice": "100",
			})
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()

	events, err := NewAPIClientWithBaseURL(server.URL).GetFundingHistory("MUUSDT", 1000, 3000)
	if err != nil {
		t.Fatalf("GetFundingHistory: %v", err)
	}
	if calls != 2 || len(events) != 1001 || events[1000].FundingTime != 2000 {
		t.Fatalf("calls=%d events=%d last=%#v", calls, len(events), events[len(events)-1])
	}
}
