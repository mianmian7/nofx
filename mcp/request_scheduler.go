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

type modelRequestWaiter struct {
	ctx      context.Context
	traderID string
	ready    chan struct{}
	granted  bool // protected by slotMu
}

type modelRequestGroup struct {
	slotMu          sync.Mutex
	waiting         []*modelRequestWaiter
	lastService     map[string]uint64
	serviceSequence uint64
	slots           chan struct{}
	startGate       chan struct{}
	nextStart       time.Time
	cooldownMu      sync.RWMutex
	cooldownUntil   time.Time
	cooldownKind    ErrorKind
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
	return scheduler.acquireForTrader(ctx, key, "")
}

func (scheduler *modelRequestScheduler) acquireForTrader(ctx context.Context, key, traderID string) (func(), error) {
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
		if err := group.acquireSlot(ctx, traderID); err != nil {
			return nil, schedulerContextError(ctx, group)
		}

		releaseSlot := func() {
			group.releaseSlot()
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
		group.markServed(traderID)
		group.startGate <- struct{}{}

		var once sync.Once
		return func() {
			once.Do(releaseSlot)
		}, nil
	}
}

// Admission age persists across failed/expired cycles. FIFO alone repeatedly
// penalizes later phase offsets when healthy reasoning calls take minutes.
// Only callers supplying a trader identity opt into least-recently-served order;
// callers without one retain FIFO order. No slot can preempt an active request.
func (group *modelRequestGroup) acquireSlot(ctx context.Context, traderID string) error {
	waiter := &modelRequestWaiter{ctx: ctx, traderID: traderID, ready: make(chan struct{})}
	group.slotMu.Lock()
	group.waiting = append(group.waiting, waiter)
	group.dispatchSlotsLocked()
	group.slotMu.Unlock()
	select {
	case <-waiter.ready:
		return nil
	case <-ctx.Done():
		group.slotMu.Lock()
		if waiter.granted {
			<-group.slots
		} else {
			for i, queued := range group.waiting {
				if queued == waiter {
					group.waiting = append(group.waiting[:i], group.waiting[i+1:]...)
					break
				}
			}
		}
		group.dispatchSlotsLocked()
		group.slotMu.Unlock()
		return ctx.Err()
	}
}

func (group *modelRequestGroup) dispatchSlotsLocked() {
	for len(group.waiting) > 0 && len(group.slots) < cap(group.slots) {
		best := 0
		for i, candidate := range group.waiting {
			if group.lastService[candidate.traderID] < group.lastService[group.waiting[best].traderID] {
				best = i
			}
		}
		waiter := group.waiting[best]
		group.waiting = append(group.waiting[:best], group.waiting[best+1:]...)
		if waiter.ctx.Err() != nil {
			continue
		}
		group.slots <- struct{}{}
		waiter.granted = true
		close(waiter.ready)
	}
}

func (group *modelRequestGroup) releaseSlot() {
	group.slotMu.Lock()
	<-group.slots
	group.dispatchSlotsLocked()
	group.slotMu.Unlock()
}

func (group *modelRequestGroup) markServed(traderID string) {
	if traderID == "" {
		return
	}
	group.slotMu.Lock()
	if group.lastService == nil {
		group.lastService = make(map[string]uint64)
	}
	group.serviceSequence++
	group.lastService[traderID] = group.serviceSequence
	group.slotMu.Unlock()
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
	return scheduler.groupWithCapacity(key, scheduler.maxConcurrent)
}

func (scheduler *modelRequestScheduler) groupWithCapacity(key string, capacity int) *modelRequestGroup {
	if capacity <= 0 {
		capacity = scheduler.maxConcurrent
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if group := scheduler.groups[key]; group != nil {
		return group
	}
	group := &modelRequestGroup{
		slots:     make(chan struct{}, capacity),
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
	key := modelRequestScheduleKey(client.Provider, client.BaseURL, model, client.APIKey)
	if client.Provider == ProviderGrok {
		// Explicit, Grok-only override. Capacity is fixed when the group is
		// created; changing environment requires restart, never resizing live slots.
		capacity := getEnvInt("AI_GROK_MODEL_REQUEST_MAX_CONCURRENCY", defaultModelRequestScheduler.maxConcurrent)
		defaultModelRequestScheduler.groupWithCapacity(key, capacity)
	}
	return key
}

func (client *Client) acquireModelRequest(metadata CallMetadata, model string, ctx context.Context) (func(), error) {
	if strings.TrimSpace(metadata.TraderID) == "" {
		return func() {}, nil
	}
	waitStart := time.Now()
	traderID := ""
	if client.Provider == ProviderGrok {
		traderID = metadata.TraderID
	}
	key := client.modelRequestScheduleKey(model)
	release, err := defaultModelRequestScheduler.acquireForTrader(ctx, key, traderID)
	if err != nil {
		return nil, err
	}
	client.Log.Infof(
		"ai_request_admitted call_id=%s cycle_id=%s trader_id=%s provider=%s model=%s wait_ms=%d max_concurrency=%d start_interval_ms=%d",
		boundedLogValue(metadata.CallID, 96), boundedLogValue(metadata.CycleID, 96), boundedLogValue(metadata.TraderID, 96), boundedLogValue(client.Provider, 96), boundedLogValue(model, 96),
		time.Since(waitStart).Milliseconds(), cap(defaultModelRequestScheduler.group(key).slots),
		defaultModelRequestScheduler.minStartInterval.Milliseconds(),
	)
	return release, nil
}
