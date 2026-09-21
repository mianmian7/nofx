package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultModelRequestMaxConcurrency      = 1
	defaultModelRequestStartIntervalSecond = 15
)

var defaultModelRequestScheduler = newModelRequestScheduler(
	getEnvInt("AI_MODEL_REQUEST_MAX_CONCURRENCY", defaultModelRequestMaxConcurrency),
	time.Duration(getEnvInt(
		"AI_MODEL_REQUEST_START_INTERVAL_SECONDS",
		defaultModelRequestStartIntervalSecond,
	))*time.Second,
)

type modelRequestScheduler struct {
	mu               sync.Mutex
	groups           map[string]*modelRequestGroup
	maxConcurrent    int
	minStartInterval time.Duration

	// Deterministic test hooks; production schedulers leave them nil.
	beforeSlotWait   func()
	afterSlotRelease func()
}

type modelRequestGroup struct {
	slots         chan struct{}
	startGate     chan struct{}
	nextStart     time.Time
	cooldownMu    sync.RWMutex
	cooldownUntil time.Time
	cooldownKind  ErrorKind
}

func newModelRequestScheduler(maxConcurrent int, minStartInterval time.Duration) *modelRequestScheduler {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultModelRequestMaxConcurrency
	}
	if minStartInterval < 0 {
		minStartInterval = 0
	}
	return &modelRequestScheduler{
		groups:           make(map[string]*modelRequestGroup),
		maxConcurrent:    maxConcurrent,
		minStartInterval: minStartInterval,
	}
}

func (scheduler *modelRequestScheduler) acquire(ctx context.Context, key string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	group := scheduler.group(key)
	for {
		if err := ctx.Err(); err != nil {
			return nil, schedulerContextError(ctx, group)
		}
		if err := waitForModelCooldown(ctx, group); err != nil {
			return nil, err
		}

		if scheduler.beforeSlotWait != nil {
			scheduler.beforeSlotWait()
		}
		select {
		case group.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, schedulerContextError(ctx, group)
		}

		releaseSlot := func() {
			<-group.slots
			if scheduler.afterSlotRelease != nil {
				scheduler.afterSlotRelease()
			}
		}
		if err := ctx.Err(); err != nil {
			releaseSlot()
			return nil, schedulerContextError(ctx, group)
		}
		// A different trader may have installed a shared cooldown while this
		// caller was queued for the concurrency slot.
		if remaining, _ := modelCooldownState(group); remaining > 0 {
			releaseSlot()
			continue
		}

		select {
		case <-group.startGate:
		case <-ctx.Done():
			releaseSlot()
			return nil, schedulerContextError(ctx, group)
		}

		wait := time.Until(group.nextStart)
		if wait > 0 {
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
				group.startGate <- struct{}{}
				releaseSlot()
				return nil, schedulerContextError(ctx, group)
			}
		}
		if err := ctx.Err(); err != nil {
			group.startGate <- struct{}{}
			releaseSlot()
			return nil, schedulerContextError(ctx, group)
		}
		// Recheck after pacing: another in-flight request may have published a
		// cooldown while this caller waited. Never hold a concurrency slot while
		// sleeping through that cooldown.
		if remaining, _ := modelCooldownState(group); remaining > 0 {
			group.startGate <- struct{}{}
			releaseSlot()
			continue
		}
		group.nextStart = time.Now().Add(scheduler.minStartInterval)
		group.startGate <- struct{}{}

		var once sync.Once
		return func() {
			once.Do(releaseSlot)
		}, nil
	}
}

func waitForModelCooldown(ctx context.Context, group *modelRequestGroup) error {
	for {
		wait, kind := modelCooldownState(group)
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
			continue
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			remaining, currentKind := modelCooldownState(group)
			if remaining > 0 {
				if currentKind != ErrorKindUnknown {
					kind = currentKind
				}
				return &ModelCooldownError{Kind: kind, Remaining: remaining}
			}
			return ctx.Err()
		}
	}
}

func schedulerContextError(ctx context.Context, group *modelRequestGroup) error {
	if remaining, kind := modelCooldownState(group); remaining > 0 {
		return &ModelCooldownError{Kind: kind, Remaining: remaining}
	}
	return ctx.Err()
}

func modelCooldownState(group *modelRequestGroup) (time.Duration, ErrorKind) {
	group.cooldownMu.RLock()
	until := group.cooldownUntil
	kind := group.cooldownKind
	group.cooldownMu.RUnlock()
	if remaining := time.Until(until); remaining > 0 {
		return remaining, kind
	}
	return 0, ErrorKindUnknown
}

func (scheduler *modelRequestScheduler) extendCooldown(key string, until time.Time, kind ErrorKind) {
	group := scheduler.group(key)
	group.cooldownMu.Lock()
	if until.After(group.cooldownUntil) {
		group.cooldownUntil = until
		group.cooldownKind = kind
	}
	group.cooldownMu.Unlock()
}

func (scheduler *modelRequestScheduler) cooldownState(key string) (time.Duration, ErrorKind) {
	return modelCooldownState(scheduler.group(key))
}

func (scheduler *modelRequestScheduler) group(key string) *modelRequestGroup {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if group := scheduler.groups[key]; group != nil {
		return group
	}
	group := &modelRequestGroup{
		slots:     make(chan struct{}, scheduler.maxConcurrent),
		startGate: make(chan struct{}, 1),
	}
	group.startGate <- struct{}{}
	scheduler.groups[key] = group
	return group
}

func modelRequestScheduleKey(provider, baseURL, model, apiKey string) string {
	identity := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(provider)),
		normalizeModelRequestURL(baseURL),
		strings.ToLower(strings.TrimSpace(model)),
		apiKey,
	}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

// ModelRequestScheduleIdentity returns the credential-safe identity used to
// group calls that share one effective upstream model capacity pool.
func ModelRequestScheduleIdentity(provider, baseURL, model, apiKey string) string {
	return modelRequestScheduleKey(provider, baseURL, model, apiKey)
}

func normalizeModelRequestURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(strings.ToLower(trimmed), "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed.String()
}

func (client *Client) modelRequestScheduleKey(model string) string {
	return modelRequestScheduleKey(client.Provider, client.BaseURL, model, client.APIKey)
}

func (client *Client) acquireModelRequest(metadata CallMetadata, model string, ctx context.Context) (func(), error) {
	if strings.TrimSpace(metadata.TraderID) == "" {
		return func() {}, nil
	}
	waitStart := time.Now()
	release, err := defaultModelRequestScheduler.acquire(ctx, client.modelRequestScheduleKey(model))
	if err != nil {
		return nil, err
	}
	client.Log.Infof(
		"ai_request_admitted call_id=%s cycle_id=%s trader_id=%s provider=%s model=%s wait_ms=%d max_concurrency=%d start_interval_ms=%d",
		boundedLogValue(metadata.CallID, 96), boundedLogValue(metadata.CycleID, 96), boundedLogValue(metadata.TraderID, 96), boundedLogValue(client.Provider, 96), boundedLogValue(model, 96),
		time.Since(waitStart).Milliseconds(), defaultModelRequestScheduler.maxConcurrent,
		defaultModelRequestScheduler.minStartInterval.Milliseconds(),
	)
	return release, nil
}
