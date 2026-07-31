package market

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	binancePriceCacheTTL = 2 * time.Second
	binanceDepthCacheTTL = 500 * time.Millisecond
	binanceKlineCacheTTL = 5 * time.Second
	// Keep the last valid K-line snapshot long enough to bridge a short-lived
	// Binance/proxy outage (including the 30s public circuit cooldown). The
	// snapshot is analysis-only; order entry still requires a fresh price.
	binanceKlineStaleTTL        = 20 * time.Minute
	binanceOpenInterestCacheTTL = 15 * time.Second
	binanceFundingCacheTTL      = 10 * time.Second
	binanceTickerCacheTTL       = 30 * time.Second
	binanceMetadataCacheTTL     = 15 * time.Minute

	binanceDefaultRateLimitCooldown = 30 * time.Second
	binanceDefaultBanCooldown       = 15 * time.Minute
	binanceProxyFailureCooldown     = 2 * time.Minute
	// A single process can run several traders at once. Serialize public
	// requests and leave headroom below Binance's per-IP weight window instead
	// of allowing every distinct symbol to burst through MaxConnsPerHost.
	binancePublicRequestInterval = 100 * time.Millisecond
)

// BinancePublicError preserves the upstream classification needed for
// rate-limit handling and proxy health diagnostics.
type BinancePublicError struct {
	StatusCode      int
	Endpoint        string
	Message         string
	RetryAfter      time.Duration
	CircuitUntil    time.Time
	ProxyLikelyDown bool
}

func (requestError *BinancePublicError) Error() string {
	if requestError == nil {
		return "Binance public request failed"
	}
	details := strings.TrimSpace(requestError.Message)
	if details == "" {
		details = http.StatusText(requestError.StatusCode)
	}
	message := fmt.Sprintf("binance request %s failed: %s", requestError.Endpoint, details)
	if requestError.StatusCode > 0 {
		message = fmt.Sprintf(
			"binance request %s failed with status %d: %s",
			requestError.Endpoint,
			requestError.StatusCode,
			details,
		)
	}
	if requestError.ProxyLikelyDown {
		message += "; configured proxy path may be unavailable or bypassed"
	}
	if !requestError.CircuitUntil.IsZero() {
		message += fmt.Sprintf("; public requests paused until %s", requestError.CircuitUntil.Format(time.RFC3339))
	}
	return message
}

type binanceCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

type binanceKlineCacheEntry struct {
	klines    []Kline
	fetchedAt time.Time
}

type binanceCircuitState struct {
	active        bool
	statusCode    int
	message       string
	blockedUntil  time.Time
	probeInFlight bool
}

type binancePublicCoordinator struct {
	requestGroup singleflight.Group

	cacheMutex sync.RWMutex
	cache      map[string]binanceCacheEntry
	klineCache map[string]binanceKlineCacheEntry

	requestGate     chan struct{}
	requestGateOnce sync.Once
	pacingMutex     sync.Mutex
	nextRequestAt   time.Time

	circuitMutex sync.Mutex
	circuit      binanceCircuitState
}

func newBinancePublicCoordinator() *binancePublicCoordinator {
	return &binancePublicCoordinator{
		cache:      make(map[string]binanceCacheEntry),
		klineCache: make(map[string]binanceKlineCacheEntry),
	}
}

func (coordinator *binancePublicCoordinator) getStaleKlines(cacheKey string, now time.Time) ([]Kline, time.Duration, bool) {
	coordinator.cacheMutex.RLock()
	entry, exists := coordinator.klineCache[cacheKey]
	coordinator.cacheMutex.RUnlock()
	if !exists || len(entry.klines) == 0 || entry.fetchedAt.IsZero() {
		return nil, 0, false
	}
	age := now.Sub(entry.fetchedAt)
	if age < 0 || age > binanceKlineStaleTTL {
		return nil, 0, false
	}
	return append([]Kline(nil), entry.klines...), age, true
}

func (coordinator *binancePublicCoordinator) storeKlines(cacheKey string, klines []Kline, fetchedAt time.Time) {
	if len(klines) == 0 {
		return
	}
	coordinator.cacheMutex.Lock()
	if coordinator.klineCache == nil {
		coordinator.klineCache = make(map[string]binanceKlineCacheEntry)
	}
	coordinator.klineCache[cacheKey] = binanceKlineCacheEntry{
		klines:    append([]Kline(nil), klines...),
		fetchedAt: fetchedAt,
	}
	coordinator.cacheMutex.Unlock()
}

// acquireRequestSlot applies a process-wide admission gate for one Binance
// public coordinator. It intentionally serializes actual HTTP calls, while
// singleflight still removes duplicate requests for the same URL. This keeps
// different symbols/timeframes from creating a burst that trips the shared
// egress IP's weight limit.
func (coordinator *binancePublicCoordinator) acquireRequestSlot(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	coordinator.requestGateOnce.Do(func() {
		coordinator.requestGate = make(chan struct{}, 1)
	})
	select {
	case coordinator.requestGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	coordinator.pacingMutex.Lock()
	now := time.Now()
	if wait := time.Until(coordinator.nextRequestAt); wait > 0 {
		coordinator.pacingMutex.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			<-coordinator.requestGate
			return nil, ctx.Err()
		}
		coordinator.pacingMutex.Lock()
		now = time.Now()
	}
	coordinator.nextRequestAt = now.Add(binancePublicRequestInterval)
	coordinator.pacingMutex.Unlock()

	return func() { <-coordinator.requestGate }, nil
}

// requestBlockedAfterAdmission closes the race where a caller passed the
// circuit check before another queued request received a rate-limit response.
// It is intentionally checked only after the pacing gate is acquired, so a
// burst already waiting in the process does not continue hitting Binance
// after the first 429/418/451 has opened the circuit.
func (coordinator *binancePublicCoordinator) requestBlockedAfterAdmission(endpoint string, now time.Time) error {
	coordinator.circuitMutex.Lock()
	defer coordinator.circuitMutex.Unlock()
	if !coordinator.circuit.active || !now.Before(coordinator.circuit.blockedUntil) {
		return nil
	}
	return coordinator.circuitErrorLocked(endpoint)
}

func (coordinator *binancePublicCoordinator) getFreshResponse(requestKey string, now time.Time) ([]byte, bool) {
	coordinator.cacheMutex.RLock()
	entry, exists := coordinator.cache[requestKey]
	coordinator.cacheMutex.RUnlock()
	if !exists || !now.Before(entry.expiresAt) {
		return nil, false
	}
	return append([]byte(nil), entry.body...), true
}

func (coordinator *binancePublicCoordinator) storeResponse(requestKey string, responseBody []byte, ttl time.Duration, now time.Time) {
	if ttl <= 0 {
		return
	}
	coordinator.cacheMutex.Lock()
	coordinator.cache[requestKey] = binanceCacheEntry{
		body:      append([]byte(nil), responseBody...),
		expiresAt: now.Add(ttl),
	}
	coordinator.cacheMutex.Unlock()
}

func (coordinator *binancePublicCoordinator) beforeRequest(endpoint string, now time.Time) (bool, error) {
	coordinator.circuitMutex.Lock()
	defer coordinator.circuitMutex.Unlock()

	if !coordinator.circuit.active {
		return false, nil
	}
	if now.Before(coordinator.circuit.blockedUntil) {
		return false, coordinator.circuitErrorLocked(endpoint)
	}
	if coordinator.circuit.probeInFlight {
		return false, coordinator.circuitErrorLocked(endpoint)
	}

	coordinator.circuit.probeInFlight = true
	return true, nil
}

func (coordinator *binancePublicCoordinator) recordSuccess() {
	coordinator.circuitMutex.Lock()
	coordinator.circuit = binanceCircuitState{}
	coordinator.circuitMutex.Unlock()
}

func (coordinator *binancePublicCoordinator) releaseProbeAfterTransientFailure(now time.Time) {
	coordinator.circuitMutex.Lock()
	defer coordinator.circuitMutex.Unlock()
	if !coordinator.circuit.probeInFlight {
		return
	}
	coordinator.circuit.probeInFlight = false
	coordinator.circuit.blockedUntil = now.Add(binanceDefaultRateLimitCooldown)
}

func (coordinator *binancePublicCoordinator) openCircuit(requestError *BinancePublicError, now time.Time) {
	if requestError == nil {
		return
	}
	cooldown := requestError.RetryAfter
	if cooldown <= 0 {
		switch requestError.StatusCode {
		case http.StatusTooManyRequests:
			cooldown = binanceDefaultRateLimitCooldown
		case http.StatusUnavailableForLegalReasons:
			cooldown = binanceProxyFailureCooldown
		default:
			cooldown = binanceDefaultBanCooldown
		}
	}

	coordinator.circuitMutex.Lock()
	coordinator.circuit = binanceCircuitState{
		active:       true,
		statusCode:   requestError.StatusCode,
		message:      requestError.Message,
		blockedUntil: now.Add(cooldown),
	}
	requestError.CircuitUntil = coordinator.circuit.blockedUntil
	coordinator.circuitMutex.Unlock()
}

func (coordinator *binancePublicCoordinator) openTransientCircuit(endpoint string, cause error, now time.Time) error {
	requestError := &BinancePublicError{
		Endpoint:     endpoint,
		Message:      fmt.Sprintf("proxy or upstream network unavailable: %v", cause),
		RetryAfter:   binanceDefaultRateLimitCooldown,
		CircuitUntil: now.Add(binanceDefaultRateLimitCooldown),
	}
	coordinator.circuitMutex.Lock()
	coordinator.circuit = binanceCircuitState{
		active:       true,
		message:      requestError.Message,
		blockedUntil: requestError.CircuitUntil,
	}
	coordinator.circuitMutex.Unlock()
	return requestError
}

func (coordinator *binancePublicCoordinator) circuitErrorLocked(endpoint string) error {
	return &BinancePublicError{
		StatusCode:      coordinator.circuit.statusCode,
		Endpoint:        endpoint,
		Message:         coordinator.circuit.message,
		CircuitUntil:    coordinator.circuit.blockedUntil,
		ProxyLikelyDown: coordinator.circuit.statusCode == http.StatusUnavailableForLegalReasons,
	}
}

func binanceCacheTTL(endpoint string) time.Duration {
	switch {
	case strings.HasPrefix(endpoint, "/fapi/v1/ticker/price"):
		return binancePriceCacheTTL
	case strings.HasPrefix(endpoint, "/fapi/v1/depth"):
		return binanceDepthCacheTTL
	case strings.HasPrefix(endpoint, "/fapi/v1/klines"):
		return binanceKlineCacheTTL
	case strings.HasPrefix(endpoint, "/fapi/v1/openInterest"):
		return binanceOpenInterestCacheTTL
	case strings.HasPrefix(endpoint, "/fapi/v1/premiumIndex"):
		return binanceFundingCacheTTL
	case strings.HasPrefix(endpoint, "/fapi/v1/ticker/24hr"):
		return binanceTickerCacheTTL
	case strings.HasPrefix(endpoint, "/fapi/v1/exchangeInfo"), strings.HasPrefix(endpoint, "/fapi/v1/fundingInfo"):
		return binanceMetadataCacheTTL
	default:
		return 0
	}
}

func parseRetryAfter(headerValue string, now time.Time) time.Duration {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(headerValue); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(headerValue); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

func isBinanceCircuitStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusUnavailableForLegalReasons ||
		statusCode == http.StatusTeapot
}

func asBinancePublicError(err error) (*BinancePublicError, bool) {
	var requestError *BinancePublicError
	if !errors.As(err, &requestError) {
		return nil, false
	}
	return requestError, true
}
