package mcp

import (
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"
)

// doModelHTTP records transport phases without prompts, credentials or URLs.
// Call IDs correlate these with the scheduler's admission and attempt records.
func (client *Client) doModelHTTP(req *http.Request, metadata CallMetadata) (*http.Response, error) {
	start := time.Now()
	var connectionMS, firstByteMS atomic.Int64
	connectionMS.Store(-1)
	firstByteMS.Store(-1)
	trace := &httptrace.ClientTrace{
		GotConn:              func(httptrace.GotConnInfo) { connectionMS.Store(time.Since(start).Milliseconds()) },
		GotFirstResponseByte: func() { firstByteMS.Store(time.Since(start).Milliseconds()) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	response, err := client.HTTPClient.Do(req)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	kind := "none"
	if err != nil {
		kind = string(ErrorKindOf(err))
	}
	client.Log.Infof("ai_http_response call_id=%s cycle_id=%s trader_id=%s provider=%s model=%s connection_ms=%d first_byte_ms=%d headers_ms=%d http_status=%d error_kind=%s",
		boundedLogValue(metadata.CallID, 96), boundedLogValue(metadata.CycleID, 96), boundedLogValue(metadata.TraderID, 96), boundedLogValue(client.Provider, 96), boundedLogValue(metadata.Model, 96),
		connectionMS.Load(), firstByteMS.Load(), time.Since(start).Milliseconds(), status, kind)
	return response, err
}
