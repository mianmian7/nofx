package market

import (
	"sync"
	"time"
)

type DepthFeedHealth struct {
	Active       bool                  `json:"active"`
	Ready        bool                  `json:"ready"`
	RESTFallback *FreshnessProof       `json:"rest_fallback,omitempty"`
	Sources      [2]StreamSourceHealth `json:"sources"`
}

type FundingFeedHealth struct {
	Active       bool                  `json:"active"`
	Ready        bool                  `json:"ready"`
	RESTFallback *FreshnessProof       `json:"rest_fallback,omitempty"`
	RESTCount    uint64                `json:"rest_fallback_count"`
	Sources      [2]StreamSourceHealth `json:"sources"`
	LastError    string                `json:"last_error,omitempty"`
}

type PriceFeedHealth struct {
	Active       bool                  `json:"active"`
	Ready        bool                  `json:"ready"`
	RESTFallback *FreshnessProof       `json:"rest_fallback,omitempty"`
	RESTCount    uint64                `json:"rest_fallback_count"`
	Sources      [2]StreamSourceHealth `json:"sources"`
	LastError    string                `json:"last_error,omitempty"`
}

type LiveFeedHealth struct {
	Ready          bool                         `json:"ready"`
	AsOf           time.Time                    `json:"as_of"`
	Capabilities   map[string]string            `json:"capabilities"`
	KlineSources   map[string]KlineFeedHealth   `json:"kline_sources"`
	DepthStreams   map[string]DepthFeedHealth   `json:"depth_streams"`
	FundingStreams map[string]FundingFeedHealth `json:"funding_streams"`
	PriceStreams   map[string]PriceFeedHealth   `json:"price_streams"`
}

type KlineFeedHealth struct {
	Active        bool          `json:"active"`
	Ready         bool          `json:"ready"`
	Exchange      string        `json:"exchange"`
	Symbol        string        `json:"symbol"`
	Interval      string        `json:"interval"`
	Transport     string        `json:"transport"`
	SourceTime    time.Time     `json:"source_time"`
	ReceivedAt    time.Time     `json:"received_at"`
	FreshnessAge  time.Duration `json:"freshness_age"`
	LastError     string        `json:"last_error,omitempty"`
	LastRequested time.Time     `json:"-"`
}

const klineDemandActiveWindow = 60 * time.Second

var klineHealthRegistry = struct {
	sync.Mutex
	entries map[string]KlineFeedHealth
}{entries: make(map[string]KlineFeedHealth)}

func recordKlineHealth(exchange, symbol, interval string, proof FreshnessProof, err error) {
	key := exchange + "|" + symbol + "|" + interval
	now := time.Now().UTC()
	entry := KlineFeedHealth{
		Ready: err == nil, Exchange: exchange, Symbol: symbol, Interval: interval,
		Transport: proof.Transport, SourceTime: proof.SourceTime, ReceivedAt: proof.ReceivedAt,
		LastRequested: now,
	}
	if err != nil {
		entry.LastError = err.Error()
	}
	klineHealthRegistry.Lock()
	klineHealthRegistry.entries[key] = entry
	klineHealthRegistry.Unlock()
}

func klineHealthSnapshot(now time.Time) map[string]KlineFeedHealth {
	klineHealthRegistry.Lock()
	defer klineHealthRegistry.Unlock()
	result := make(map[string]KlineFeedHealth, len(klineHealthRegistry.entries))
	for key, entry := range klineHealthRegistry.entries {
		entry.Active = !entry.LastRequested.IsZero() && now.Sub(entry.LastRequested) <= klineDemandActiveWindow
		freshReady := entry.Ready
		if !entry.SourceTime.IsZero() {
			entry.FreshnessAge = now.Sub(entry.SourceTime)
			if intervalDuration, err := TFDuration(entry.Interval); err == nil {
				freshReady = freshReady && entry.FreshnessAge >= 0 && entry.FreshnessAge <= 2*intervalDuration+30*time.Second
			}
		}
		entry.Ready = !entry.Active || freshReady
		result[key] = entry
	}
	return result
}

func LiveFeedHealthSnapshot() LiveFeedHealth {
	fundingStreams := sharedFundingStreams.health()
	for key, entry := range sharedOKXStreams.fundingHealth() {
		fundingStreams["okx|"+key] = entry
	}
	health := LiveFeedHealth{
		Ready: true,
		AsOf:  time.Now().UTC(),
		Capabilities: map[string]string{
			"binance":     TradingCapabilities.String(),
			"okx":         TradingCapabilities.String(),
			"bitget":      TradingCapabilities.String(),
			"bybit":       (CapabilityPrice | CapabilityKlines).String(),
			"gate":        (CapabilityPrice | CapabilityKlines).String(),
			"kucoin":      "none",
			"hyperliquid": (CapabilityPrice | CapabilityKlines).String(),
			"aster":       (CapabilityPrice | CapabilityKlines).String(),
			"lighter":     "none",
			"indodax":     "none",
		},
		KlineSources:   klineHealthSnapshot(time.Now().UTC()),
		DepthStreams:   sharedDepthStreams.health(),
		FundingStreams: fundingStreams,
		PriceStreams:   sharedOKXStreams.priceHealth(),
	}
	for _, source := range health.KlineSources {
		if source.Active && !source.Ready {
			health.Ready = false
			break
		}
	}
	for _, stream := range health.DepthStreams {
		if stream.Active && !stream.Ready {
			health.Ready = false
			break
		}
	}
	for _, stream := range health.FundingStreams {
		if stream.Active && !stream.Ready {
			health.Ready = false
			break
		}
	}
	for _, stream := range health.PriceStreams {
		if stream.Active && !stream.Ready {
			health.Ready = false
			break
		}
	}
	return health
}

func (registry *fundingStreamRegistry) health() map[string]FundingFeedHealth {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := make(map[string]FundingFeedHealth, len(registry.streams))
	now := time.Now().UTC()
	for key, runtime := range registry.streams {
		sources := runtime.Health()
		active := now.Sub(registry.lastRequested[key]) <= fundingDemandActiveWindow
		streamReady := false
		for _, source := range sources {
			if source.Ready && source.Reconciled && source.FreshnessAge >= 0 && source.FreshnessAge <= bitgetFundingFreshness {
				streamReady = true
				break
			}
		}
		var fallback *FreshnessProof
		fallbackReady := false
		if proof, found := registry.restFallbacks[key]; found {
			copy := proof
			copy.Age = clampedStreamAge(now, copy.SourceTime)
			fallback = &copy
			fallbackReady = copy.Age >= 0 && copy.Age <= fundingDemandActiveWindow && now.Sub(copy.ReceivedAt) <= fundingDemandActiveWindow
		}
		result[key] = FundingFeedHealth{
			Active: active, Ready: streamReady || fallbackReady || !active,
			RESTFallback: fallback, RESTCount: registry.restCounts[key],
			Sources: sources, LastError: registry.lastErrors[key],
		}
	}
	return result
}

func (registry *depthStreamRegistry) health() map[string]DepthFeedHealth {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := make(map[string]DepthFeedHealth, len(registry.streams))
	now := time.Now().UTC()
	for key, runtime := range registry.streams {
		sources := runtime.Health()
		active := now.Sub(registry.lastRequested[key]) <= depthDemandActiveWindow
		streamReady := false
		for _, source := range sources {
			if source.Ready && source.Reconciled && source.FreshnessAge >= 0 && source.FreshnessAge <= coinAnkDepthFreshness {
				streamReady = true
				break
			}
		}
		var fallback *FreshnessProof
		fallbackReady := false
		if proof, found := registry.restFallbacks[key]; found {
			copy := proof
			copy.Age = clampedStreamAge(now, copy.SourceTime)
			fallback = &copy
			fallbackReady = copy.Age >= 0 && copy.Age <= depthDemandActiveWindow && now.Sub(copy.ReceivedAt) <= depthDemandActiveWindow
		}
		result[key] = DepthFeedHealth{
			Active: active, Ready: streamReady || fallbackReady || !active,
			RESTFallback: fallback, Sources: sources,
		}
	}
	return result
}
