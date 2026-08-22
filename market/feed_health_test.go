package market

import (
	"testing"
	"time"
)

func TestLiveFeedHealthReportsFreshnessAndReconciliation(t *testing.T) {
	key := "test|BTCUSDT"
	runtime := newRedundantStream[*DepthSnapshot](nil, nil)
	now := time.Now().UTC()
	runtime.sources[0] = streamSourceState[*DepthSnapshot]{
		value:    &DepthSnapshot{Bids: [][]string{{"100", "1"}}, Asks: [][]string{{"101", "1"}}},
		exchange: "test", transport: "ws", sequence: 10,
		sourceTime: now.Add(-10 * time.Second), receivedAt: now.Add(-10 * time.Second),
		ready: true, reconciled: true,
	}
	sharedDepthStreams.mu.Lock()
	sharedDepthStreams.streams[key] = runtime
	sharedDepthStreams.lastRequested[key] = now
	sharedDepthStreams.mu.Unlock()
	defer func() {
		sharedDepthStreams.mu.Lock()
		delete(sharedDepthStreams.streams, key)
		delete(sharedDepthStreams.lastRequested, key)
		delete(sharedDepthStreams.restFallbacks, key)
		sharedDepthStreams.mu.Unlock()
	}()

	health := LiveFeedHealthSnapshot()
	if health.Ready || health.DepthStreams[key].Ready {
		t.Fatalf("stale stream reported ready: %#v", health.DepthStreams[key])
	}

	runtime.mu.Lock()
	runtime.sources[0].sourceTime = time.Now().UTC()
	runtime.sources[0].receivedAt = time.Now().UTC()
	runtime.mu.Unlock()
	health = LiveFeedHealthSnapshot()
	stream := health.DepthStreams[key]
	if !health.Ready || !stream.Ready || stream.Sources[0].Exchange != "test" || stream.Sources[0].Transport != "ws" || stream.Sources[0].SourceTime.IsZero() || stream.Sources[0].ReceivedAt.IsZero() {
		t.Fatalf("fresh stream health = %#v", stream)
	}
}

func TestLiveFeedHealthReportsBitgetFundingStream(t *testing.T) {
	key := "TESTUSDT"
	runtime := newRedundantStream[*FundingSnapshot](nil, nil)
	now := time.Now().UTC()
	runtime.sources[0] = streamSourceState[*FundingSnapshot]{
		value:    &FundingSnapshot{Symbol: key, Rate: 0.001, MarkPrice: 100, IndexPrice: 99, Time: now.UnixMilli()},
		exchange: "bitget", transport: "bitget-websocket", sequence: now.UnixMilli(),
		sourceTime: now, receivedAt: now, ready: true, reconciled: true,
	}
	sharedFundingStreams.mu.Lock()
	sharedFundingStreams.streams[key] = runtime
	sharedFundingStreams.lastRequested[key] = now
	sharedFundingStreams.mu.Unlock()
	defer func() {
		sharedFundingStreams.mu.Lock()
		delete(sharedFundingStreams.streams, key)
		delete(sharedFundingStreams.lastRequested, key)
		delete(sharedFundingStreams.restFallbacks, key)
		delete(sharedFundingStreams.restCounts, key)
		delete(sharedFundingStreams.lastErrors, key)
		sharedFundingStreams.mu.Unlock()
	}()

	health := LiveFeedHealthSnapshot()
	stream := health.FundingStreams[key]
	if !health.Ready || !stream.Active || !stream.Ready || stream.Sources[0].Transport != "bitget-websocket" || stream.Sources[0].SourceTime.IsZero() {
		t.Fatalf("funding stream health = %#v", stream)
	}
}

func TestInactiveKlineHealthDoesNotFailReadiness(t *testing.T) {
	key := "test|INACTIVEUSDT|1m"
	now := time.Now().UTC()
	klineHealthRegistry.Lock()
	klineHealthRegistry.entries[key] = KlineFeedHealth{
		Ready: true, Exchange: "test", Symbol: "INACTIVEUSDT", Interval: "1m",
		Transport: "rest", SourceTime: now.Add(-3 * time.Minute), ReceivedAt: now.Add(-3 * time.Minute),
		LastRequested: now.Add(-3 * time.Minute),
	}
	klineHealthRegistry.Unlock()
	defer func() {
		klineHealthRegistry.Lock()
		delete(klineHealthRegistry.entries, key)
		klineHealthRegistry.Unlock()
	}()

	health := LiveFeedHealthSnapshot()
	if !health.Ready || health.KlineSources[key].Active || !health.KlineSources[key].Ready {
		t.Fatalf("inactive K-line poisoned global readiness: %#v", health.KlineSources[key])
	}
}

func TestLiveFeedHealthReportsOKXPriceAndFundingStreams(t *testing.T) {
	key := "OKXTESTUSDT"
	now := time.Now().UTC()
	priceRuntime := newRedundantStream[float64](nil, nil)
	priceRuntime.sources[0] = streamSourceState[float64]{value: 100, exchange: "okx", transport: "okx-websocket", sequence: now.UnixMilli(), sourceTime: now, receivedAt: now, ready: true, reconciled: true}
	fundingRuntime := newRedundantStream[*FundingSnapshot](nil, nil)
	fundingRuntime.sources[0] = streamSourceState[*FundingSnapshot]{value: &FundingSnapshot{Symbol: key, Rate: 0.001, MarkPrice: 100, Time: now.UnixMilli()}, exchange: "okx", transport: "okx-websocket", sequence: now.UnixMilli(), sourceTime: now, receivedAt: now, ready: true, reconciled: true}
	sharedOKXStreams.mu.Lock()
	sharedOKXStreams.prices[key] = priceRuntime
	sharedOKXStreams.fundings[key] = fundingRuntime
	sharedOKXStreams.lastPriceRequested[key] = now
	sharedOKXStreams.lastFundingRequested[key] = now
	sharedOKXStreams.mu.Unlock()
	defer func() {
		sharedOKXStreams.mu.Lock()
		delete(sharedOKXStreams.prices, key)
		delete(sharedOKXStreams.fundings, key)
		delete(sharedOKXStreams.lastPriceRequested, key)
		delete(sharedOKXStreams.lastFundingRequested, key)
		delete(sharedOKXStreams.priceRESTFallbacks, key)
		delete(sharedOKXStreams.fundingRESTFallbacks, key)
		delete(sharedOKXStreams.priceRESTCounts, key)
		delete(sharedOKXStreams.fundingRESTCounts, key)
		delete(sharedOKXStreams.priceErrors, key)
		delete(sharedOKXStreams.fundingErrors, key)
		sharedOKXStreams.mu.Unlock()
	}()

	health := LiveFeedHealthSnapshot()
	if !health.Ready || !health.PriceStreams[key].Ready || !health.FundingStreams["okx|"+key].Ready {
		t.Fatalf("OKX stream health not ready: price=%#v funding=%#v", health.PriceStreams[key], health.FundingStreams["okx|"+key])
	}
}

func TestDepthRESTHealthClampsAllowedExchangeClockSkew(t *testing.T) {
	key := "test|CLOCKSKEWUSDT"
	now := time.Now().UTC()
	sharedDepthStreams.mu.Lock()
	sharedDepthStreams.streams[key] = newRedundantStream[*DepthSnapshot](nil, nil)
	sharedDepthStreams.lastRequested[key] = now
	sharedDepthStreams.restFallbacks[key] = FreshnessProof{Exchange: "test", Transport: "rest", SourceTime: now.Add(750 * time.Millisecond), ReceivedAt: now, SequenceOK: true, Reconciled: true}
	sharedDepthStreams.mu.Unlock()
	defer func() {
		sharedDepthStreams.mu.Lock()
		delete(sharedDepthStreams.streams, key)
		delete(sharedDepthStreams.lastRequested, key)
		delete(sharedDepthStreams.restFallbacks, key)
		sharedDepthStreams.mu.Unlock()
	}()

	health := LiveFeedHealthSnapshot()
	if !health.Ready || !health.DepthStreams[key].Ready || health.DepthStreams[key].RESTFallback.Age != 0 {
		t.Fatalf("allowed clock skew was rejected: %#v", health.DepthStreams[key])
	}
}
