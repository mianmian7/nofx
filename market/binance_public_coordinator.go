package market

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"nofx/market/binanceguard"
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
	// OI and funding are shared only inside a very short successful-response
	// coalescing window. Expiry forces a new upstream request; errors never fall
	// back to an older value.
	binanceOpenInterestCacheTTL = 2 * time.Second
	binanceFundingCacheTTL      = 2 * time.Second
	binanceTickerCacheTTL       = 30 * time.Second
	binanceMetadataCacheTTL     = 15 * time.Minute

	binanceDefaultRateLimitCooldown = 30 * time.Second
	binanceDefaultBanCooldown       = 15 * time.Minute
	binanceProxyFailureCooldown     = 2 * time.Minute
	// A probe normally completes within three 6s attempts plus retry delays.
	// Recover defensively if an abnormal callback exit leaves it marked in flight.
	binancePublicProbeTimeout = 30 * time.Second
	// Admission is deliberately separate from the per-attempt HTTP deadline.
	// Queue pressure is local state and must never be reported as an upstream
	// network outage or open the public-data circuit.
	binancePublicAdmissionTimeout = 30 * time.Second
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

type binanceCircuitState struct {
	active         bool
	statusCode     int
	message        string
	blockedUntil   time.Time
	probeInFlight  bool
	probeStartedAt time.Time
}

type binancePublicCoordinator struct {
	requestGroup singleflight.Group

	cacheMutex sync.RWMutex
	cache      map[string]binanceCacheEntry

	admit            func(context.Context, binanceguard.Priority) (func(), error)
	admissionTimeout time.Duration

	circuitMutex sync.Mutex
	circuit      binanceCircuitState
}

func newBinancePublicCoordinator() *binancePublicCoordinator {
	localLimiter := binanceguard.NewLimiter(0)
	return newBinancePublicCoordinatorWithAdmission(localLimiter.Acquire)
}

func newSharedBinancePublicCoordinator() *binancePublicCoordinator {
	return newBinancePublicCoordinatorWithAdmission(binanceguard.Acquire)
}

func newBinancePublicCoordinatorWithAdmission(
	admit func(context.Context, binanceguard.Priority) (func(), error),
) *binancePublicCoordinator {
	return &binancePublicCoordinator{
		cache:            make(map[string]binanceCacheEntry),
		admit:            admit,
		admissionTimeout: binancePublicAdmissionTimeout,
	}
}

// acquireRequestSlot delegates to the process-wide bounded priority limiter.
// The limiter also coordinates signed order traffic, so public market-data
// bursts cannot consume the egress budget ahead of order/cancel operations.
func (coordinator *binancePublicCoordinator) acquireRequestSlot(
	ctx context.Context,
	priority binanceguard.Priority,
) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if coordinator.admit == nil {
		return nil, &binanceguard.AdmissionError{Cause: errors.New("binance public admission is not configured")}
	}
	return coordinator.admit(ctx, priority)
}

// requestBlockedAfterAdmission closes the race where a caller passed the
// circuit check before another queued request received a rate-limit response.
// It is intentionally checked only after the process-wide slot is acquired, so a
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
	if coordinator.circuit.probeInFlight && !coordinator.circuit.probeStartedAt.IsZero() &&
		now.Sub(coordinator.circuit.probeStartedAt) < binancePublicProbeTimeout {
		return false, coordinator.circuitErrorLocked(endpoint)
	}

	coordinator.circuit.probeInFlight = true
	coordinator.circuit.probeStartedAt = now
	return true, nil
}

func (coordinator *binancePublicCoordinator) recordSuccess() {
	coordinator.circuitMutex.Lock()
	coordinator.circuit = binanceCircuitState{}
	coordinator.circuitMutex.Unlock()
}

// releaseExpiredCircuitProbe clears only an already-expired explicit Binance
// cooldown. Local admission or transport failures must not create or extend a
// process-wide outage after Binance's requested backoff has elapsed.
func (coordinator *binancePublicCoordinator) releaseExpiredCircuitProbe() {
	coordinator.circuitMutex.Lock()
	defer coordinator.circuitMutex.Unlock()
	if !coordinator.circuit.probeInFlight {
		return
	}
	coordinator.circuit = binanceCircuitState{}
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
