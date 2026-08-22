package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	bitgetPublicWebSocketURL    = "wss://ws.bitget.com/v2/ws/public"
	bitgetFundingFreshness      = 5 * time.Second
	bitgetFundingDialTimeout    = 5 * time.Second
	bitgetFundingPingInterval   = 20 * time.Second
	fundingDemandActiveWindow   = 60 * time.Second
	bitgetFundingRESTBudget     = 5 * time.Second
	bitgetFundingAttemptTimeout = 2 * time.Second
)

type fundingStreamSnapshot func(string) (*FundingSnapshot, FreshnessProof, error)

type fundingStreamRegistry struct {
	mu            sync.Mutex
	streams       map[string]*RedundantStream[*FundingSnapshot]
	lastRequested map[string]time.Time
	restFallbacks map[string]FreshnessProof
	restCounts    map[string]uint64
	lastErrors    map[string]string
}

var sharedFundingStreams = fundingStreamRegistry{
	streams:       make(map[string]*RedundantStream[*FundingSnapshot]),
	lastRequested: make(map[string]time.Time),
	restFallbacks: make(map[string]FreshnessProof),
	restCounts:    make(map[string]uint64),
	lastErrors:    make(map[string]string),
}

func streamedBitgetFunding(symbol string) (*FundingSnapshot, FreshnessProof, error) {
	runtime := sharedFundingStreams.get(symbol)
	snapshot, proof, err := runtime.Snapshot(bitgetFundingFreshness)
	if err != nil {
		return nil, FreshnessProof{}, fmt.Errorf("Bitget %s live ticker not ready: %w", symbol, err)
	}
	if snapshot == nil || !validBitgetPrice(snapshot.MarkPrice) || !validBitgetPrice(snapshot.IndexPrice) || snapshot.Time <= 0 {
		return nil, FreshnessProof{}, fmt.Errorf("Bitget %s live ticker returned incomplete funding data", symbol)
	}
	sharedFundingStreams.mu.Lock()
	delete(sharedFundingStreams.lastErrors, NormalizeForExchange("bitget", symbol))
	sharedFundingStreams.mu.Unlock()
	return snapshot, proof, nil
}

func (registry *fundingStreamRegistry) get(symbol string) *RedundantStream[*FundingSnapshot] {
	normalizedSymbol := NormalizeForExchange("bitget", symbol)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.lastRequested[normalizedSymbol] = time.Now().UTC()
	if existing := registry.streams[normalizedSymbol]; existing != nil {
		return existing
	}
	connector := newBitgetFundingConnector(normalizedSymbol, bitgetPublicWebSocketURL)
	runtime := newRedundantStream(connector, connector)
	runtime.Start(context.Background())
	registry.streams[normalizedSymbol] = runtime
	return runtime
}

func (registry *fundingStreamRegistry) recordREST(symbol string, snapshot *FundingSnapshot, err error) {
	normalizedSymbol := NormalizeForExchange("bitget", symbol)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err != nil {
		registry.lastErrors[normalizedSymbol] = err.Error()
		return
	}
	delete(registry.lastErrors, normalizedSymbol)
	if snapshot == nil || snapshot.Time <= 0 {
		return
	}
	receivedAt := time.Now().UTC()
	sourceTime := time.UnixMilli(snapshot.Time).UTC()
	registry.restFallbacks[normalizedSymbol] = FreshnessProof{
		Exchange: "bitget", Transport: "rest", SourceTime: sourceTime,
		ReceivedAt: receivedAt, Age: clampedStreamAge(receivedAt, sourceTime),
		Sequence: snapshot.Time, SequenceOK: true, Reconciled: true,
	}
	registry.restCounts[normalizedSymbol]++
}

func newBitgetFundingConnector(symbol, websocketURL string) StreamConnector[*FundingSnapshot] {
	return func(ctx context.Context) (<-chan StreamEvent[*FundingSnapshot], <-chan error, error) {
		dialCtx, cancelDial := context.WithTimeout(ctx, bitgetFundingDialTimeout)
		connection, _, err := websocket.DefaultDialer.DialContext(dialCtx, websocketURL, nil)
		cancelDial()
		if err != nil {
			return nil, nil, err
		}
		subscription := map[string]any{
			"op": "subscribe",
			"args": []map[string]string{{
				"instType": bitgetPublicProductType,
				"channel":  "ticker",
				"instId":   symbol,
			}},
		}
		if err := connection.WriteJSON(subscription); err != nil {
			_ = connection.Close()
			return nil, nil, err
		}

		events := make(chan StreamEvent[*FundingSnapshot], 32)
		streamErrors := make(chan error, 1)
		go runBitgetFundingConnection(ctx, connection, symbol, events, streamErrors)
		return events, streamErrors, nil
	}
}

func runBitgetFundingConnection(ctx context.Context, connection *websocket.Conn, symbol string, events chan<- StreamEvent[*FundingSnapshot], streamErrors chan<- error) {
	done := make(chan struct{})
	defer close(done)
	defer close(events)
	defer close(streamErrors)
	defer connection.Close()

	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	go func() {
		ticker := time.NewTicker(bitgetFundingPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := connection.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
					_ = connection.Close()
					return
				}
			}
		}
	}()

	for {
		_, message, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				streamErrors <- fmt.Errorf("Bitget funding ticker read: %w", err)
			}
			return
		}
		if strings.EqualFold(strings.TrimSpace(string(message)), "pong") {
			continue
		}
		snapshot, sourceTime, ok, parseErr := parseBitgetFundingTickerMessage(message, symbol)
		if parseErr != nil {
			streamErrors <- parseErr
			return
		}
		if !ok {
			continue
		}
		sequence := sourceTime.UnixMilli()
		event := StreamEvent[*FundingSnapshot]{
			Value: snapshot, Snapshot: true, Sequence: sequence, PreviousSequence: -1,
			SourceTime: sourceTime, Exchange: "bitget", Transport: "bitget-websocket",
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func parseBitgetFundingTickerMessage(message []byte, expectedSymbol string) (*FundingSnapshot, time.Time, bool, error) {
	var payload struct {
		Event  string          `json:"event"`
		Code   string          `json:"code"`
		Msg    string          `json:"msg"`
		Action string          `json:"action"`
		Ts     json.RawMessage `json:"ts"`
		Arg    struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		} `json:"arg"`
		Data []struct {
			InstID          string          `json:"instId"`
			Symbol          string          `json:"symbol"`
			FundingRate     json.RawMessage `json:"fundingRate"`
			NextFundingTime json.RawMessage `json:"nextFundingTime"`
			MarkPrice       json.RawMessage `json:"markPrice"`
			IndexPrice      json.RawMessage `json:"indexPrice"`
			Ts              json.RawMessage `json:"ts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(message, &payload); err != nil {
		return nil, time.Time{}, false, fmt.Errorf("decode Bitget funding ticker: %w", err)
	}
	if strings.EqualFold(payload.Event, "error") || (payload.Code != "" && payload.Code != "0" && payload.Code != "00000") {
		return nil, time.Time{}, false, fmt.Errorf("Bitget funding ticker error %s: %s", payload.Code, payload.Msg)
	}
	if len(payload.Data) == 0 {
		return nil, time.Time{}, false, nil
	}
	if payload.Arg.Channel != "" && !strings.EqualFold(payload.Arg.Channel, "ticker") {
		return nil, time.Time{}, false, nil
	}
	row := payload.Data[0]
	symbol := row.InstID
	if symbol == "" {
		symbol = row.Symbol
	}
	if symbol == "" {
		symbol = payload.Arg.InstID
	}
	symbol = NormalizeForExchange("bitget", symbol)
	expected := NormalizeForExchange("bitget", expectedSymbol)
	if expected != "" && symbol != expected {
		return nil, time.Time{}, false, nil
	}
	rate, err := parseRawFloat(row.FundingRate, "Bitget ticker funding rate")
	if err != nil {
		return nil, time.Time{}, false, err
	}
	markPrice, err := parseRawFloat(row.MarkPrice, "Bitget ticker mark price")
	if err != nil || !validBitgetPrice(markPrice) {
		return nil, time.Time{}, false, fmt.Errorf("invalid Bitget ticker mark price for %s", symbol)
	}
	indexPrice, err := parseRawFloat(row.IndexPrice, "Bitget ticker index price")
	if err != nil || !validBitgetPrice(indexPrice) {
		return nil, time.Time{}, false, fmt.Errorf("invalid Bitget ticker index price for %s", symbol)
	}
	sourceTimeMillis := parseOptionalRawInt64(row.Ts)
	if sourceTimeMillis <= 0 {
		sourceTimeMillis = parseOptionalRawInt64(payload.Ts)
	}
	if sourceTimeMillis <= 0 {
		return nil, time.Time{}, false, errors.New("Bitget funding ticker has no source timestamp")
	}
	nextFundingTime := parseOptionalRawInt64(row.NextFundingTime)
	return &FundingSnapshot{
		Symbol: symbol, MarkPrice: markPrice, IndexPrice: indexPrice, Rate: rate,
		NextFundingTime: nextFundingTime, Time: sourceTimeMillis,
	}, time.UnixMilli(sourceTimeMillis).UTC(), true, nil
}
