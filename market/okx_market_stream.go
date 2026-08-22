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
	okxPublicWebSocketURL      = "wss://ws.okx.com:8443/ws/v5/public"
	okxPriceFreshness          = 5 * time.Second
	okxFundingFreshness        = 120 * time.Second
	okxMarkFreshness           = 15 * time.Second
	okxFundingComponentMaxSkew = 120 * time.Second
	okxStreamDialTimeout       = 5 * time.Second
	okxStreamPingInterval      = 20 * time.Second
	okxStreamDemandWindow      = 60 * time.Second
	okxMarketRESTBudget        = 5 * time.Second
	okxMarketAttemptTimeout    = 2 * time.Second
)

type priceStreamSnapshot func(string) (float64, FreshnessProof, error)

type okxMarketStreamRegistry struct {
	mu                   sync.Mutex
	prices               map[string]*RedundantStream[float64]
	fundings             map[string]*RedundantStream[*FundingSnapshot]
	lastPriceRequested   map[string]time.Time
	lastFundingRequested map[string]time.Time
	priceRESTFallbacks   map[string]FreshnessProof
	fundingRESTFallbacks map[string]FreshnessProof
	priceRESTCounts      map[string]uint64
	fundingRESTCounts    map[string]uint64
	priceErrors          map[string]string
	fundingErrors        map[string]string
}

var sharedOKXStreams = okxMarketStreamRegistry{
	prices:               make(map[string]*RedundantStream[float64]),
	fundings:             make(map[string]*RedundantStream[*FundingSnapshot]),
	lastPriceRequested:   make(map[string]time.Time),
	lastFundingRequested: make(map[string]time.Time),
	priceRESTFallbacks:   make(map[string]FreshnessProof),
	fundingRESTFallbacks: make(map[string]FreshnessProof),
	priceRESTCounts:      make(map[string]uint64),
	fundingRESTCounts:    make(map[string]uint64),
	priceErrors:          make(map[string]string),
	fundingErrors:        make(map[string]string),
}

func streamedOKXPrice(symbol string) (float64, FreshnessProof, error) {
	normalized := NormalizeForExchange("okx", symbol)
	runtime := sharedOKXStreams.getPrice(normalized)
	price, proof, err := runtime.Snapshot(okxPriceFreshness)
	if err != nil || price <= 0 {
		if err == nil {
			err = errors.New("invalid streamed price")
		}
		return 0, FreshnessProof{}, fmt.Errorf("OKX %s live ticker not ready: %w", normalized, err)
	}
	sharedOKXStreams.mu.Lock()
	delete(sharedOKXStreams.priceErrors, normalized)
	sharedOKXStreams.mu.Unlock()
	return price, proof, nil
}

func streamedOKXFunding(symbol string) (*FundingSnapshot, FreshnessProof, error) {
	normalized := NormalizeForExchange("okx", symbol)
	runtime := sharedOKXStreams.getFunding(normalized)
	snapshot, proof, err := runtime.Snapshot(okxFundingFreshness)
	if err != nil || snapshot == nil || snapshot.Time <= 0 || !validOKXMarkPrice(snapshot.MarkPrice) {
		if err == nil {
			err = errors.New("incomplete streamed funding snapshot")
		}
		return nil, FreshnessProof{}, fmt.Errorf("OKX %s live funding not ready: %w", normalized, err)
	}
	sharedOKXStreams.mu.Lock()
	delete(sharedOKXStreams.fundingErrors, normalized)
	sharedOKXStreams.mu.Unlock()
	return snapshot, proof, nil
}

func (registry *okxMarketStreamRegistry) getPrice(symbol string) *RedundantStream[float64] {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.lastPriceRequested[symbol] = time.Now().UTC()
	if existing := registry.prices[symbol]; existing != nil {
		return existing
	}
	connector := newOKXPriceConnector(symbol, okxStreamInstID(symbol), okxPublicWebSocketURL)
	runtime := newRedundantStream(connector, connector)
	runtime.Start(context.Background())
	registry.prices[symbol] = runtime
	return runtime
}

func (registry *okxMarketStreamRegistry) getFunding(symbol string) *RedundantStream[*FundingSnapshot] {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.lastFundingRequested[symbol] = time.Now().UTC()
	if existing := registry.fundings[symbol]; existing != nil {
		return existing
	}
	connector := newOKXFundingConnector(symbol, okxStreamInstID(symbol), okxPublicWebSocketURL)
	runtime := newRedundantStream(connector, connector)
	runtime.Start(context.Background())
	registry.fundings[symbol] = runtime
	return runtime
}

func (registry *okxMarketStreamRegistry) recordPriceREST(symbol string, ticker *okxTickerRow, err error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err != nil {
		registry.priceErrors[symbol] = err.Error()
		return
	}
	delete(registry.priceErrors, symbol)
	if ticker == nil {
		return
	}
	timestamp := parseOptionalInt64(ticker.Ts)
	if timestamp <= 0 {
		return
	}
	receivedAt := time.Now().UTC()
	sourceTime := time.UnixMilli(timestamp).UTC()
	registry.priceRESTFallbacks[symbol] = FreshnessProof{Exchange: "okx", Transport: "rest", SourceTime: sourceTime, ReceivedAt: receivedAt, Age: clampedStreamAge(receivedAt, sourceTime), Sequence: timestamp, SequenceOK: true, Reconciled: true}
	registry.priceRESTCounts[symbol]++
}

func (registry *okxMarketStreamRegistry) recordFundingREST(symbol string, snapshot *FundingSnapshot, err error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err != nil {
		registry.fundingErrors[symbol] = err.Error()
		return
	}
	delete(registry.fundingErrors, symbol)
	if snapshot == nil || snapshot.Time <= 0 {
		return
	}
	receivedAt := time.Now().UTC()
	sourceTime := time.UnixMilli(snapshot.Time).UTC()
	registry.fundingRESTFallbacks[symbol] = FreshnessProof{Exchange: "okx", Transport: "rest", SourceTime: sourceTime, ReceivedAt: receivedAt, Age: clampedStreamAge(receivedAt, sourceTime), Sequence: snapshot.Time, SequenceOK: true, Reconciled: true}
	registry.fundingRESTCounts[symbol]++
}

func (registry *okxMarketStreamRegistry) priceHealth() map[string]PriceFeedHealth {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := make(map[string]PriceFeedHealth, len(registry.prices))
	now := time.Now().UTC()
	for key, runtime := range registry.prices {
		sources := runtime.Health()
		active := now.Sub(registry.lastPriceRequested[key]) <= okxStreamDemandWindow
		streamReady := false
		for _, source := range sources {
			if source.Ready && source.Reconciled && source.FreshnessAge >= 0 && source.FreshnessAge <= okxPriceFreshness {
				streamReady = true
				break
			}
		}
		var fallback *FreshnessProof
		fallbackReady := false
		if proof, found := registry.priceRESTFallbacks[key]; found {
			copy := proof
			copy.Age = clampedStreamAge(now, copy.SourceTime)
			fallback = &copy
			fallbackReady = copy.Age >= 0 && copy.Age <= okxStreamDemandWindow && now.Sub(copy.ReceivedAt) <= okxStreamDemandWindow
		}
		result[key] = PriceFeedHealth{Active: active, Ready: streamReady || fallbackReady || !active, RESTFallback: fallback, RESTCount: registry.priceRESTCounts[key], Sources: sources, LastError: registry.priceErrors[key]}
	}
	return result
}

func (registry *okxMarketStreamRegistry) fundingHealth() map[string]FundingFeedHealth {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := make(map[string]FundingFeedHealth, len(registry.fundings))
	now := time.Now().UTC()
	for key, runtime := range registry.fundings {
		sources := runtime.Health()
		active := now.Sub(registry.lastFundingRequested[key]) <= okxStreamDemandWindow
		streamReady := false
		for _, source := range sources {
			if source.Ready && source.Reconciled && source.FreshnessAge >= 0 && source.FreshnessAge <= okxFundingFreshness {
				streamReady = true
				break
			}
		}
		var fallback *FreshnessProof
		fallbackReady := false
		if proof, found := registry.fundingRESTFallbacks[key]; found {
			copy := proof
			copy.Age = clampedStreamAge(now, copy.SourceTime)
			fallback = &copy
			fallbackReady = copy.Age >= 0 && copy.Age <= okxFundingFreshness && now.Sub(copy.ReceivedAt) <= okxStreamDemandWindow
		}
		result[key] = FundingFeedHealth{Active: active, Ready: streamReady || fallbackReady || !active, RESTFallback: fallback, RESTCount: registry.fundingRESTCounts[key], Sources: sources, LastError: registry.fundingErrors[key]}
	}
	return result
}

func okxStreamInstID(symbol string) string {
	canonical := NormalizeForExchange("okx", symbol)
	if strings.HasSuffix(canonical, "USDT") {
		return strings.TrimSuffix(canonical, "USDT") + "-USDT-SWAP"
	}
	return canonical
}

func newOKXPriceConnector(symbol, instID, websocketURL string) StreamConnector[float64] {
	return func(ctx context.Context) (<-chan StreamEvent[float64], <-chan error, error) {
		connection, err := dialOKXPublicStream(ctx, websocketURL, []map[string]string{{"channel": "tickers", "instId": instID}})
		if err != nil {
			return nil, nil, err
		}
		events := make(chan StreamEvent[float64], 32)
		streamErrors := make(chan error, 1)
		go runOKXPriceConnection(ctx, connection, instID, events, streamErrors)
		return events, streamErrors, nil
	}
}

func newOKXFundingConnector(symbol, instID, websocketURL string) StreamConnector[*FundingSnapshot] {
	return func(ctx context.Context) (<-chan StreamEvent[*FundingSnapshot], <-chan error, error) {
		connection, err := dialOKXPublicStream(ctx, websocketURL, []map[string]string{
			{"channel": "funding-rate", "instId": instID},
			{"channel": "mark-price", "instId": instID},
		})
		if err != nil {
			return nil, nil, err
		}
		events := make(chan StreamEvent[*FundingSnapshot], 32)
		streamErrors := make(chan error, 1)
		go runOKXFundingConnection(ctx, connection, symbol, events, streamErrors)
		return events, streamErrors, nil
	}
}

func dialOKXPublicStream(ctx context.Context, websocketURL string, args []map[string]string) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, okxStreamDialTimeout)
	connection, _, err := websocket.DefaultDialer.DialContext(dialCtx, websocketURL, nil)
	cancel()
	if err != nil {
		return nil, err
	}
	if err := connection.WriteJSON(map[string]any{"op": "subscribe", "args": args}); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func runOKXPriceConnection(ctx context.Context, connection *websocket.Conn, instID string, events chan<- StreamEvent[float64], streamErrors chan<- error) {
	done := startOKXStreamLifecycle(ctx, connection)
	defer close(done)
	defer close(events)
	defer close(streamErrors)
	defer connection.Close()
	for {
		_, message, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				streamErrors <- fmt.Errorf("OKX ticker read: %w", err)
			}
			return
		}
		if strings.EqualFold(strings.TrimSpace(string(message)), "pong") {
			continue
		}
		price, sourceTime, ok, parseErr := parseOKXPriceStreamMessage(message, instID)
		if parseErr != nil {
			streamErrors <- parseErr
			return
		}
		if !ok {
			continue
		}
		event := StreamEvent[float64]{Value: price, Snapshot: true, Sequence: sourceTime.UnixMilli(), PreviousSequence: -1, SourceTime: sourceTime, Exchange: "okx", Transport: "okx-websocket"}
		select {
		case events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func runOKXFundingConnection(ctx context.Context, connection *websocket.Conn, symbol string, events chan<- StreamEvent[*FundingSnapshot], streamErrors chan<- error) {
	done := startOKXStreamLifecycle(ctx, connection)
	defer close(done)
	defer close(events)
	defer close(streamErrors)
	defer connection.Close()
	var assembler okxFundingAssembler
	for {
		_, message, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				streamErrors <- fmt.Errorf("OKX funding read: %w", err)
			}
			return
		}
		if strings.EqualFold(strings.TrimSpace(string(message)), "pong") {
			continue
		}
		snapshot, sourceTime, ok, parseErr := assembler.apply(message, symbol)
		if parseErr != nil {
			streamErrors <- parseErr
			return
		}
		if !ok {
			continue
		}
		event := StreamEvent[*FundingSnapshot]{Value: snapshot, Snapshot: true, Sequence: sourceTime.UnixMilli(), PreviousSequence: -1, SourceTime: sourceTime, Exchange: "okx", Transport: "okx-websocket"}
		select {
		case events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func startOKXStreamLifecycle(ctx context.Context, connection *websocket.Conn) chan struct{} {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	go func() {
		ticker := time.NewTicker(okxStreamPingInterval)
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
	return done
}

func parseOKXPriceStreamMessage(message []byte, expectedInstID string) (float64, time.Time, bool, error) {
	var payload struct {
		Event string `json:"event"`
		Code  string `json:"code"`
		Msg   string `json:"msg"`
		Arg   struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		} `json:"arg"`
		Data []struct {
			InstID string `json:"instId"`
			Last   string `json:"last"`
			Ts     string `json:"ts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(message, &payload); err != nil {
		return 0, time.Time{}, false, fmt.Errorf("decode OKX ticker: %w", err)
	}
	if strings.EqualFold(payload.Event, "error") || (payload.Code != "" && payload.Code != "0") {
		return 0, time.Time{}, false, fmt.Errorf("OKX ticker error %s: %s", payload.Code, payload.Msg)
	}
	if !strings.EqualFold(payload.Arg.Channel, "tickers") || len(payload.Data) == 0 {
		return 0, time.Time{}, false, nil
	}
	row := payload.Data[0]
	if expectedInstID != "" && !strings.EqualFold(row.InstID, expectedInstID) {
		return 0, time.Time{}, false, nil
	}
	price, err := parsePublicFloat(row.Last, "OKX streamed last price")
	if err != nil || price <= 0 {
		return 0, time.Time{}, false, fmt.Errorf("invalid OKX streamed price for %s", row.InstID)
	}
	timestamp := parseOptionalInt64(row.Ts)
	if timestamp <= 0 {
		return 0, time.Time{}, false, errors.New("OKX ticker has no source timestamp")
	}
	return price, time.UnixMilli(timestamp).UTC(), true, nil
}

type okxFundingAssembler struct {
	rate            float64
	nextFundingTime int64
	fundingTime     time.Time
	markPrice       float64
	markTime        time.Time
}

func (assembler *okxFundingAssembler) apply(message []byte, symbol string) (*FundingSnapshot, time.Time, bool, error) {
	var payload struct {
		Event string `json:"event"`
		Code  string `json:"code"`
		Msg   string `json:"msg"`
		Arg   struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		} `json:"arg"`
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(message, &payload); err != nil {
		return nil, time.Time{}, false, fmt.Errorf("decode OKX funding stream: %w", err)
	}
	if strings.EqualFold(payload.Event, "error") || (payload.Code != "" && payload.Code != "0") {
		return nil, time.Time{}, false, fmt.Errorf("OKX funding stream error %s: %s", payload.Code, payload.Msg)
	}
	if len(payload.Data) == 0 {
		return nil, time.Time{}, false, nil
	}
	expectedInstID := okxStreamInstID(symbol)
	if payload.Arg.InstID != "" && !strings.EqualFold(payload.Arg.InstID, expectedInstID) {
		return nil, time.Time{}, false, nil
	}
	switch strings.ToLower(payload.Arg.Channel) {
	case "funding-rate":
		var row struct {
			InstID          string `json:"instId"`
			FundingRate     string `json:"fundingRate"`
			NextFundingTime string `json:"nextFundingTime"`
			Ts              string `json:"ts"`
		}
		if err := json.Unmarshal(payload.Data[0], &row); err != nil {
			return nil, time.Time{}, false, err
		}
		if row.InstID != "" && !strings.EqualFold(row.InstID, expectedInstID) {
			return nil, time.Time{}, false, nil
		}
		rate, err := parsePublicFloat(row.FundingRate, "OKX streamed funding rate")
		if err != nil {
			return nil, time.Time{}, false, err
		}
		timestamp := parseOptionalInt64(row.Ts)
		if timestamp <= 0 {
			return nil, time.Time{}, false, errors.New("OKX funding stream has no source timestamp")
		}
		assembler.rate = rate
		assembler.nextFundingTime = parseOptionalInt64(row.NextFundingTime)
		assembler.fundingTime = time.UnixMilli(timestamp).UTC()
	case "mark-price":
		var row struct {
			InstID    string `json:"instId"`
			MarkPrice string `json:"markPx"`
			Ts        string `json:"ts"`
		}
		if err := json.Unmarshal(payload.Data[0], &row); err != nil {
			return nil, time.Time{}, false, err
		}
		if row.InstID != "" && !strings.EqualFold(row.InstID, expectedInstID) {
			return nil, time.Time{}, false, nil
		}
		markPrice, err := parsePublicFloat(row.MarkPrice, "OKX streamed mark price")
		if err != nil || !validOKXMarkPrice(markPrice) {
			return nil, time.Time{}, false, fmt.Errorf("invalid OKX streamed mark price")
		}
		timestamp := parseOptionalInt64(row.Ts)
		if timestamp <= 0 {
			return nil, time.Time{}, false, errors.New("OKX mark-price stream has no source timestamp")
		}
		assembler.markPrice = markPrice
		assembler.markTime = time.UnixMilli(timestamp).UTC()
	default:
		return nil, time.Time{}, false, nil
	}
	if assembler.fundingTime.IsZero() || assembler.markTime.IsZero() {
		return nil, time.Time{}, false, nil
	}
	componentSkew := assembler.fundingTime.Sub(assembler.markTime)
	if componentSkew < 0 {
		componentSkew = -componentSkew
	}
	if componentSkew > okxFundingComponentMaxSkew {
		return nil, time.Time{}, false, fmt.Errorf("OKX funding components differ by %s", componentSkew)
	}
	sourceTime := assembler.fundingTime
	if assembler.markTime.Before(sourceTime) {
		sourceTime = assembler.markTime
	}
	return &FundingSnapshot{
		Symbol: NormalizeForExchange("okx", symbol), MarkPrice: assembler.markPrice,
		Rate: assembler.rate, NextFundingTime: assembler.nextFundingTime,
		Time: assembler.fundingTime.UnixMilli(),
	}, sourceTime, true, nil
}
