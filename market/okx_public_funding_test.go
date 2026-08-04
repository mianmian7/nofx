package market

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestOKXFundingHistoryBackfillsHistoricalMarkPrice(t *testing.T) {
	var (
		markRequestsMu sync.Mutex
		markRequests   = make(map[int64]int)
	)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		switch request.URL.Path {
		case okxPublicFundingHistoryPath:
			if query.Get("instId") != "SNDK-USDT-SWAP" || query.Get("limit") != "100" || query.Get("after") != "" {
				t.Fatalf("unexpected OKX funding history query: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"0","data":[`+
				`{"instId":"SNDK-USDT-SWAP","fundingRate":"0.002","fundingTime":"2000000"},`+
				`{"instId":"SNDK-USDT-SWAP","fundingRate":"0.001","fundingTime":"1000000"},`+
				`{"instId":"SNDK-USDT-SWAP","fundingRate":"0.0001","fundingTime":"500000"}`+
				`]}`)
		case okxPublicHistoryMarkPath:
			if query.Get("instId") != "SNDK-USDT-SWAP" || query.Get("bar") != "1m" || query.Get("limit") != "100" {
				t.Fatalf("unexpected OKX historical mark query: %s", request.URL.RawQuery)
			}
			after, err := strconv.ParseInt(query.Get("after"), 10, 64)
			if err != nil {
				t.Fatalf("invalid OKX historical mark after query: %v", err)
			}
			fundingTime := after - 1
			markRequestsMu.Lock()
			markRequests[fundingTime]++
			markRequestsMu.Unlock()
			switch fundingTime {
			case 1000000:
				// The response is deliberately newest/future first and includes an
				// exact timestamp boundary plus an older candle.
				writeJSON(responseWriter, `{"code":"0","data":[`+
					`["1000500","10","10","10","10.5","1"],`+
					`["1000000","10","10","10","10","1"],`+
					`["999000","9","9","9","9","1"]`+
					`]}`)
			case 2000000:
				writeJSON(responseWriter, `{"code":"0","data":[`+
					`["2001000","20","20","20","20.1","1"],`+
					`["1999000","19","19","19","19","1"],`+
					`["1999500","19.5","19.5","19.5","19.5","1"]`+
					`]}`)
			default:
				responseWriter.WriteHeader(http.StatusNotFound)
			}
		default:
			responseWriter.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	events, err := provider.GetFundingHistory("SNDKUSDT", 1000000, 2000000)
	if err != nil {
		t.Fatalf("OKX GetFundingHistory returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("OKX funding history length = %d, want 2: %#v", len(events), events)
	}
	if events[0].FundingTime != 1000000 || events[0].MarkPrice != 10 {
		t.Fatalf("unexpected first OKX funding event: %#v", events[0])
	}
	if events[1].FundingTime != 2000000 || events[1].MarkPrice != 19.5 {
		t.Fatalf("unexpected second OKX funding event: %#v", events[1])
	}
	markRequestsMu.Lock()
	defer markRequestsMu.Unlock()
	if markRequests[1000000] != 1 || markRequests[2000000] != 1 || len(markRequests) != 2 {
		t.Fatalf("unexpected historical mark request counts: %#v", markRequests)
	}
}

func TestOKXFundingHistoryRejectsHistoricalMarkFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		markBody   string
		want       string
	}{
		{
			name:       "http error",
			statusCode: http.StatusBadGateway,
			markBody:   `upstream unavailable`,
			want:       "request failed",
		},
		{
			name:     "no candle",
			markBody: `{"code":"0","data":[]}`,
			want:     "no valid candle",
		},
		{
			name:     "no candle at or before funding time",
			markBody: `{"code":"0","data":[["1001","1","1","1","1","1"]]}`,
			want:     "no valid candle",
		},
		{
			name:     "bad price",
			markBody: `{"code":"0","data":[["1000","1","1","1","NaN","1"]]}`,
			want:     "invalid candle price",
		},
		{
			name:     "bad timestamp",
			markBody: `{"code":"0","data":[["not-a-time","1","1","1","1","1"]]}`,
			want:     "parse OKX historical mark candle timestamp",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case okxPublicFundingHistoryPath:
					writeJSON(responseWriter, `{"code":"0","data":[{"fundingRate":"0.001","fundingTime":"1000"}]}`)
				case okxPublicHistoryMarkPath:
					if testCase.statusCode != 0 {
						responseWriter.WriteHeader(testCase.statusCode)
						_, _ = responseWriter.Write([]byte(testCase.markBody))
						return
					}
					writeJSON(responseWriter, testCase.markBody)
				default:
					responseWriter.WriteHeader(http.StatusNotFound)
				}
			}))
			provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
			events, err := provider.GetFundingHistory("SNDKUSDT", 1000, 1000)
			server.Close()
			if err == nil {
				t.Fatalf("expected error, got events %#v", events)
			}
			if !strings.Contains(err.Error(), "SNDKUSDT") || !strings.Contains(err.Error(), "1000") {
				t.Fatalf("error lacks symbol/time context: %v", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %q, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestOKXFundingHistoryPaginatesFundingEventsBeforeBackfill(t *testing.T) {
	var fundingAfter []string
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		switch request.URL.Path {
		case okxPublicFundingHistoryPath:
			fundingAfter = append(fundingAfter, query.Get("after"))
			if query.Get("after") == "" {
				writeJSON(responseWriter, okxFundingHistoryTestPage(200000, 100, 1000))
				return
			}
			if query.Get("after") != "101000" {
				t.Fatalf("unexpected OKX funding pagination cursor: %s", query.Get("after"))
			}
			writeJSON(responseWriter, `{"code":"0","data":[{"fundingRate":"0.001","fundingTime":"100000"}]}`)
		case okxPublicHistoryMarkPath:
			if query.Get("after") != "100001" {
				t.Fatalf("unexpected OKX historical mark cursor: %s", request.URL.RawQuery)
			}
			writeJSON(responseWriter, `{"code":"0","data":[["100000","1","1","1","42","1"]]}`)
		default:
			responseWriter.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := NewOKXMarketDataProviderWithHTTPClient(server.URL, server.Client())
	events, err := provider.GetFundingHistory("SNDKUSDT", 100000, 100000)
	if err != nil {
		t.Fatalf("OKX paginated GetFundingHistory returned error: %v", err)
	}
	if len(events) != 1 || events[0].FundingTime != 100000 || events[0].MarkPrice != 42 {
		t.Fatalf("unexpected paginated OKX funding history: %#v", events)
	}
	if len(fundingAfter) != 2 || fundingAfter[0] != "" || fundingAfter[1] != "101000" {
		t.Fatalf("unexpected funding pagination requests: %#v", fundingAfter)
	}
}

func okxFundingHistoryTestPage(newest int64, count int, step int64) string {
	var builder strings.Builder
	builder.WriteString(`{"code":"0","data":[`)
	for index := 0; index < count; index++ {
		if index > 0 {
			builder.WriteByte(',')
		}
		fundingTime := newest - int64(index)*step
		fmt.Fprintf(&builder, `{"fundingRate":"0.001","fundingTime":"%d"}`, fundingTime)
	}
	builder.WriteString(`]}`)
	return builder.String()
}
