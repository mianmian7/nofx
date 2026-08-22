package market

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

type freshKlineSource func(context.Context) ([]Kline, error)

type freshKlineResult struct {
	klines     []Kline
	transport  string
	receivedAt time.Time
	err        error
}

func hedgedFreshKlines(
	ctx context.Context,
	exchange, symbol, interval string,
	primary, standby freshKlineSource,
) ([]Kline, FreshnessProof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	intervalDuration, err := TFDuration(interval)
	if err != nil {
		return nil, FreshnessProof{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	results := make(chan freshKlineResult, 2)
	run := func(transport string, source freshKlineSource) {
		if source == nil {
			results <- freshKlineResult{transport: transport, err: errors.New("fresh K-line source is not configured")}
			return
		}
		klines, sourceErr := source(requestCtx)
		receivedAt := time.Now().UTC()
		if sourceErr == nil {
			sourceErr = validateFreshPublicKlines(exchange, symbol, interval, klines, intervalDuration)
		}
		results <- freshKlineResult{klines: klines, transport: transport, receivedAt: receivedAt, err: sourceErr}
	}
	go run("primary-rest", primary)
	go run("standby-rest", standby)

	var first freshKlineResult
	select {
	case first = <-results:
	case <-requestCtx.Done():
		return nil, FreshnessProof{}, fmt.Errorf("%s %s fresh K-line sources timed out: %w", exchange, symbol, requestCtx.Err())
	}
	if first.err != nil {
		var second freshKlineResult
		select {
		case second = <-results:
		case <-requestCtx.Done():
			return nil, FreshnessProof{}, fmt.Errorf("%s %s fresh K-line sources timed out after first error %v: %w", exchange, symbol, first.err, requestCtx.Err())
		}
		if second.err != nil {
			return nil, FreshnessProof{}, fmt.Errorf("%s %s fresh K-lines unavailable from both sources: primary/standby errors: %v; %v", exchange, symbol, first.err, second.err)
		}
		return klineResultWithProof(exchange, second)
	}

	// A short comparison window catches immediately available disagreement
	// without making a healthy primary wait for a slow or failed standby.
	comparisonTimer := time.NewTimer(200 * time.Millisecond)
	defer comparisonTimer.Stop()
	select {
	case second := <-results:
		if second.err != nil {
			return klineResultWithProof(exchange, first)
		}
		selected, compareErr := reconcileFreshKlineResults(first, second, intervalDuration)
		if compareErr != nil {
			return nil, FreshnessProof{}, fmt.Errorf("%s %s conflicting fresh K-line sources: %w", exchange, symbol, compareErr)
		}
		return klineResultWithProof(exchange, selected)
	case <-comparisonTimer.C:
		return klineResultWithProof(exchange, first)
	case <-requestCtx.Done():
		return klineResultWithProof(exchange, first)
	}
}

func reconcileFreshKlineResults(left, right freshKlineResult, interval time.Duration) (freshKlineResult, error) {
	leftLatest := left.klines[len(left.klines)-1]
	rightLatest := right.klines[len(right.klines)-1]
	openDelta := time.Duration(absInt64(leftLatest.OpenTime-rightLatest.OpenTime)) * time.Millisecond
	if openDelta > interval {
		return freshKlineResult{}, fmt.Errorf("latest candle times differ by %s", openDelta)
	}
	denominator := math.Max(math.Abs(leftLatest.Close), math.Abs(rightLatest.Close))
	if denominator <= 0 {
		return freshKlineResult{}, errors.New("latest candle close is invalid")
	}
	if math.Abs(leftLatest.Close-rightLatest.Close)/denominator > 0.01 {
		return freshKlineResult{}, fmt.Errorf("latest close differs by more than 1%%: %.8f vs %.8f", leftLatest.Close, rightLatest.Close)
	}
	if rightLatest.OpenTime > leftLatest.OpenTime || (rightLatest.OpenTime == leftLatest.OpenTime && right.receivedAt.After(left.receivedAt)) {
		return right, nil
	}
	return left, nil
}

func klineResultWithProof(exchange string, result freshKlineResult) ([]Kline, FreshnessProof, error) {
	latest := result.klines[len(result.klines)-1]
	sourceTime := time.UnixMilli(latest.OpenTime).UTC()
	return result.klines, FreshnessProof{
		Exchange: exchange, Transport: result.transport, SourceTime: sourceTime,
		ReceivedAt: result.receivedAt, Age: result.receivedAt.Sub(sourceTime),
		SequenceOK: true, Reconciled: true,
	}, nil
}

func proofFromKlines(exchange, transport string, klines []Kline) FreshnessProof {
	if len(klines) == 0 {
		return FreshnessProof{Exchange: exchange, Transport: transport}
	}
	receivedAt := time.Now().UTC()
	sourceTime := time.UnixMilli(klines[len(klines)-1].OpenTime).UTC()
	return FreshnessProof{
		Exchange: exchange, Transport: transport, SourceTime: sourceTime,
		ReceivedAt: receivedAt, Age: receivedAt.Sub(sourceTime), SequenceOK: true, Reconciled: true,
	}
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
