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
	defaultModelRequestMaxConcurrency      = 2
	defaultModelRequestStartIntervalSecond = 30
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
}

type modelRequestGroup struct {
	slots     chan struct{}
	startGate chan struct{}
	nextStart time.Time
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

	select {
	case group.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	releaseSlot := func() { <-group.slots }
	select {
	case <-group.startGate:
	case <-ctx.Done():
		releaseSlot()
		return nil, ctx.Err()
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
			return nil, ctx.Err()
		}
	}
	group.nextStart = time.Now().Add(scheduler.minStartInterval)
	group.startGate <- struct{}{}

	var once sync.Once
	return func() {
		once.Do(releaseSlot)
	}, nil
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

func (client *Client) acquireModelRequest(metadata CallMetadata, model string, ctx context.Context) (func(), error) {
	if strings.TrimSpace(metadata.TraderID) == "" {
		return func() {}, nil
	}
	waitStart := time.Now()
	release, err := defaultModelRequestScheduler.acquire(ctx, modelRequestScheduleKey(
		client.Provider,
		client.BaseURL,
		model,
		client.APIKey,
	))
	if err != nil {
		return nil, err
	}
	client.Log.Infof(
		"ai_request_admitted trader_id=%s provider=%s model=%s wait_ms=%d max_concurrency=%d start_interval_ms=%d",
		boundedLogValue(metadata.TraderID, 96), boundedLogValue(client.Provider, 96), boundedLogValue(model, 96),
		time.Since(waitStart).Milliseconds(), defaultModelRequestScheduler.maxConcurrent,
		defaultModelRequestScheduler.minStartInterval.Milliseconds(),
	)
	return release, nil
}
