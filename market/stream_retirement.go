package market

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Keep briefly idle feeds warm across decision cycles, but release abandoned
// symbols without requiring another request or a health poll to trigger cleanup.
// Default reduced from 5m to 1m to prevent 7x24 background streaming in low-frequency cycles.
const defaultStreamIdleTimeout = 1 * time.Minute

func streamIdleTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("NOFX_STREAM_IDLE_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultStreamIdleTimeout
}

func isMarketStreamDisabled() bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv("NOFX_DISABLE_MARKET_STREAM")))
	return val == "true" || val == "1" || val == "yes"
}

// Called while holding mu after registering a new runtime. Demand only updates
// lastRequested; the timer rechecks it under the same lock before retiring. This
// also handles a request racing with an already fired timer (Timer.Reset alone
// cannot prevent that callback from deleting an active stream).
func retireIdleStream[T any](mu *sync.Mutex, streams map[string]*RedundantStream[T], lastRequested map[string]time.Time, key string, runtime *RedundantStream[T], idle time.Duration, clearMetadata func()) {
	var expire func()
	expire = func() {
		mu.Lock()
		if streams[key] != runtime {
			mu.Unlock()
			return
		}
		if remaining := time.Until(lastRequested[key].Add(idle)); remaining > 0 {
			time.AfterFunc(remaining, expire)
			mu.Unlock()
			return
		}
		delete(streams, key)
		delete(lastRequested, key)
		clearMetadata()
		mu.Unlock()
		runtime.Stop()
	}
	time.AfterFunc(idle, expire)
}
