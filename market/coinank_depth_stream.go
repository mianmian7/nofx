package market

// CoinAnk depth streaming is retained for diagnostics/legacy display only.
// Native trading providers deliberately use their exchange REST depth path and
// never call streamedDepth.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
)

const (
	coinAnkDepthFreshness   = 3 * time.Second
	coinAnkDepthWarmup      = 1200 * time.Millisecond
	coinAnkDepthDialTimeout = 5 * time.Second
	depthDemandActiveWindow = 60 * time.Second
)

type depthStreamRegistry struct {
	mu            sync.Mutex
	streams       map[string]*RedundantStream[*DepthSnapshot]
	lastRequested map[string]time.Time
	restFallbacks map[string]FreshnessProof
}

var sharedDepthStreams = depthStreamRegistry{
	streams:       make(map[string]*RedundantStream[*DepthSnapshot]),
	lastRequested: make(map[string]time.Time),
	restFallbacks: make(map[string]FreshnessProof),
}

func streamedDepth(exchange, symbol string, limit int) (*DepthSnapshot, error) {
	runtime, err := sharedDepthStreams.get(exchange, symbol)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(coinAnkDepthWarmup)
	for {
		depth, proof, snapshotErr := runtime.Snapshot(coinAnkDepthFreshness)
		if snapshotErr == nil && depth != nil && len(depth.Bids) > 0 && len(depth.Asks) > 0 {
			clone := cloneDepthSnapshot(depth, limit)
			clone.Exchange = exchange
			clone.Transport = proof.Transport
			clone.ReceivedAt = proof.ReceivedAt
			clone.Fresh = true
			return clone, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s %s live depth stream not ready: %w", exchange, symbol, ErrFreshStreamUnavailable)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (registry *depthStreamRegistry) get(exchange, symbol string) (*RedundantStream[*DepthSnapshot], error) {
	normalizedExchange := strings.ToLower(strings.TrimSpace(exchange))
	normalizedSymbol := NormalizeForExchange(normalizedExchange, symbol)
	coinAnkExchange, err := toCoinAnkExchange(normalizedExchange)
	if err != nil {
		return nil, err
	}
	key := normalizedExchange + "|" + normalizedSymbol
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.lastRequested[key] = time.Now().UTC()
	if existing := registry.streams[key]; existing != nil {
		return existing, nil
	}
	connector := newCoinAnkDepthConnector(normalizedExchange, normalizedSymbol, coinAnkExchange)
	runtime := newRedundantStream(connector, connector)
	runtime.Start(context.Background())
	registry.streams[key] = runtime
	return runtime, nil
}

func recordDepthRESTFallback(exchange, symbol string, depth *DepthSnapshot) {
	if depth == nil || !depth.Fresh || depth.ReceivedAt.IsZero() {
		return
	}
	key := strings.ToLower(strings.TrimSpace(exchange)) + "|" + NormalizeForExchange(exchange, symbol)
	sourceTime := depth.ReceivedAt
	if depth.EventTime > 0 {
		sourceTime = time.UnixMilli(depth.EventTime).UTC()
	}
	sharedDepthStreams.mu.Lock()
	sharedDepthStreams.restFallbacks[key] = FreshnessProof{
		Exchange: exchange, Transport: "rest", SourceTime: sourceTime,
		ReceivedAt: depth.ReceivedAt, Age: depth.ReceivedAt.Sub(sourceTime),
		Sequence: depth.LastUpdateID, SequenceOK: true, Reconciled: true,
	}
	sharedDepthStreams.mu.Unlock()
}

func newCoinAnkDepthConnector(exchange, symbol string, coinAnkExchange coinank_enum.Exchange) StreamConnector[*DepthSnapshot] {
	return func(ctx context.Context) (<-chan StreamEvent[*DepthSnapshot], <-chan error, error) {
		dialCtx, cancelDial := context.WithTimeout(ctx, coinAnkDepthDialTimeout)
		connection, err := coinank_api.DepthWsConn(dialCtx)
		cancelDial()
		if err != nil {
			return nil, nil, err
		}
		if err := connection.Subscribe(symbol, coinAnkExchange, "0.1"); err != nil {
			_ = connection.Close()
			return nil, nil, err
		}
		events := make(chan StreamEvent[*DepthSnapshot], 32)
		streamErrors := make(chan error, 1)
		go func() {
			defer close(events)
			defer close(streamErrors)
			defer connection.Close()
			book := newCoinAnkDepthBook()
			var lastSequence int64
			for {
				select {
				case <-ctx.Done():
					return
				case result, ok := <-connection.DepthV3Ch:
					if !ok {
						streamErrors <- errors.New("CoinAnk depth stream closed")
						return
					}
					if result == nil || !result.Success || result.Data.Ts == 0 {
						continue
					}
					sequence := int64(result.Data.Ts)
					fullSnapshot := strings.EqualFold(result.Data.Type, "all")
					if !fullSnapshot && sequence <= lastSequence {
						streamErrors <- fmt.Errorf("CoinAnk depth timestamp did not advance: previous=%d current=%d", lastSequence, sequence)
						return
					}
					depth, applyErr := book.apply(fullSnapshot, result.Data.Bids, result.Data.Asks)
					if applyErr != nil {
						continue
					}
					depth.LastUpdateID = sequence
					depth.EventTime = sequence
					depth.TransactionTime = sequence
					depth.Exchange = exchange
					depth.Transport = "coinank-websocket"
					depth.Fresh = true
					previousSequence := lastSequence
					if fullSnapshot {
						previousSequence = -1
					}
					lastSequence = sequence
					events <- StreamEvent[*DepthSnapshot]{
						Value: depth, Snapshot: fullSnapshot, Sequence: sequence, PreviousSequence: previousSequence,
						SourceTime: time.UnixMilli(sequence).UTC(), Exchange: exchange, Transport: "coinank-websocket",
					}
				}
			}
		}()
		return events, streamErrors, nil
	}
}

type coinAnkDepthBook struct {
	initialized bool
	bids        map[string][]string
	asks        map[string][]string
}

func newCoinAnkDepthBook() *coinAnkDepthBook {
	return &coinAnkDepthBook{bids: make(map[string][]string), asks: make(map[string][]string)}
}

func (book *coinAnkDepthBook) apply(full bool, bids, asks [][]string) (*DepthSnapshot, error) {
	if full {
		book.bids = make(map[string][]string)
		book.asks = make(map[string][]string)
		book.initialized = true
	} else if !book.initialized {
		return nil, ErrStreamNeedsSnapshot
	}
	applyCoinAnkDepthLevels(book.bids, bids)
	applyCoinAnkDepthLevels(book.asks, asks)
	return &DepthSnapshot{
		Bids: sortedCoinAnkDepthLevels(book.bids, true),
		Asks: sortedCoinAnkDepthLevels(book.asks, false),
	}, nil
}

func applyCoinAnkDepthLevels(book map[string][]string, levels [][]string) {
	for _, level := range normalizeDepthLevels(levels) {
		quantity, err := strconv.ParseFloat(level[1], 64)
		if err != nil {
			continue
		}
		if quantity == 0 {
			delete(book, level[0])
			continue
		}
		book[level[0]] = append([]string(nil), level...)
	}
}

func sortedCoinAnkDepthLevels(book map[string][]string, descending bool) [][]string {
	levels := make([][]string, 0, len(book))
	for _, level := range book {
		levels = append(levels, append([]string(nil), level...))
	}
	sort.Slice(levels, func(left, right int) bool {
		leftPrice, leftErr := strconv.ParseFloat(levels[left][0], 64)
		rightPrice, rightErr := strconv.ParseFloat(levels[right][0], 64)
		if leftErr != nil || rightErr != nil {
			return levels[left][0] < levels[right][0]
		}
		if descending {
			return leftPrice > rightPrice
		}
		return leftPrice < rightPrice
	})
	return levels
}

func toCoinAnkExchange(exchange string) (coinank_enum.Exchange, error) {
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "binance":
		return coinank_enum.Binance, nil
	case "okx":
		return coinank_enum.Okex, nil
	case "bitget":
		return coinank_enum.Bitget, nil
	case "bybit":
		return coinank_enum.Bybit, nil
	case "gate":
		return coinank_enum.Gate, nil
	case "hyperliquid":
		return coinank_enum.Hyperliquid, nil
	case "aster":
		return coinank_enum.Aster, nil
	default:
		return "", fmt.Errorf("unsupported CoinAnk stream exchange: %s", exchange)
	}
}

func cloneDepthSnapshot(depth *DepthSnapshot, limit int) *DepthSnapshot {
	clone := *depth
	clone.Bids = cloneDepthLevels(depth.Bids, limit)
	clone.Asks = cloneDepthLevels(depth.Asks, limit)
	return &clone
}

func cloneDepthLevels(levels [][]string, limit int) [][]string {
	if limit <= 0 || limit > len(levels) {
		limit = len(levels)
	}
	result := make([][]string, 0, limit)
	for _, level := range levels[:limit] {
		result = append(result, append([]string(nil), level...))
	}
	return result
}
