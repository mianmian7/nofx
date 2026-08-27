package market

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	bitgetPublicBaseURL            = "https://api.bitget.com"
	bitgetPublicProductType        = "USDT-FUTURES"
	bitgetPublicTickerPath         = "/api/v2/mix/market/ticker"
	bitgetPublicTickersPath        = "/api/v2/mix/market/tickers"
	bitgetPublicCandlesPath        = "/api/v2/mix/market/candles"
	bitgetPublicOrderBookPath      = "/api/v2/mix/market/orderbook"
	bitgetPublicContractsPath      = "/api/v2/mix/market/contracts"
	bitgetPublicFundingPath        = "/api/v2/mix/market/current-fund-rate"
	bitgetPublicFundingHistoryPath = "/api/v2/mix/market/history-fund-rate"
	bitgetPublicSymbolPricePath    = "/api/v2/mix/market/symbol-price"
	bitgetPublicHistoryMarkPath    = "/api/v2/mix/market/history-mark-candles"
	bitgetPublicOpenInterestPath   = "/api/v2/mix/market/open-interest"
)

// BitgetMarketDataProvider reads public Bitget USDT-M perpetual data without
// credentials. Contract amounts are normalized to base-asset units whenever
// Bitget exposes a USD notional or contract multiplier.
type BitgetMarketDataProvider struct {
	httpClient                nativePublicHTTPClient
	fundingStream             fundingStreamSnapshot
	fundingRESTBudget         time.Duration
	fundingRESTAttemptTimeout time.Duration
}

func NewBitgetMarketDataProvider() *BitgetMarketDataProvider {
	return NewBitgetMarketDataProviderWithHTTPClient(bitgetPublicBaseURL, nil)
}

func NewBitgetMarketDataProviderWithHTTPClient(baseURL string, client *http.Client) *BitgetMarketDataProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = bitgetPublicBaseURL
	}
	provider := &BitgetMarketDataProvider{
		httpClient:                newNativePublicHTTPClient(baseURL, client),
		fundingRESTBudget:         bitgetFundingRESTBudget,
		fundingRESTAttemptTimeout: bitgetFundingAttemptTimeout,
	}
	if strings.Contains(strings.ToLower(baseURL), "bitget.com") {
		provider.fundingStream = streamedBitgetFunding
	}
	return provider
}

func (provider *BitgetMarketDataProvider) Exchange() string               { return "bitget" }
func (provider *BitgetMarketDataProvider) Capabilities() MarketCapability { return TradingCapabilities }

func (provider *BitgetMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange("bitget", symbol)
}

func (provider *BitgetMarketDataProvider) exchangeSymbol(symbol string) string {
	return provider.NormalizeSymbol(symbol)
}

func (provider *BitgetMarketDataProvider) GetCurrentPrice(symbol string) (float64, error) {
	return provider.GetCurrentPriceFresh(symbol)
}

func (provider *BitgetMarketDataProvider) GetCurrentPriceFresh(symbol string) (float64, error) {
	body, err := provider.httpClient.getFresh(bitgetPublicTickerPath, url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
	})
	if err != nil {
		return 0, err
	}
	var tickers []struct {
		Symbol string `json:"symbol"`
		LastPr string `json:"lastPr"`
	}
	if err := decodePublicEnvelope(body, "Bitget", &tickers); err != nil {
		return 0, err
	}
	if len(tickers) == 0 {
		return 0, fmt.Errorf("Bitget returned no ticker for %s", symbol)
	}
	price, err := parsePublicFloat(tickers[0].LastPr, "Bitget last price")
	if err != nil {
		return 0, fmt.Errorf("invalid Bitget price for %s: %w", symbol, err)
	}
	if price <= 0 {
		return 0, fmt.Errorf("invalid Bitget price for %s: %.8f", symbol, price)
	}
	return price, nil
}

func (provider *BitgetMarketDataProvider) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	return provider.GetKlinesFresh(symbol, interval, limit)
}

func (provider *BitgetMarketDataProvider) getKlines(symbol, interval string, limit int) ([]Kline, error) {
	return provider.getKlinesContext(context.Background(), symbol, interval, limit)
}

func (provider *BitgetMarketDataProvider) getKlinesContext(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	granularity, err := bitgetGranularity(interval)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
		"granularity": {granularity},
		"limit":       {strconv.Itoa(limit)},
	}
	body, err := provider.httpClient.getFreshContext(ctx, bitgetPublicCandlesPath, query, 0)
	if err != nil {
		return nil, err
	}
	var rows [][]json.RawMessage
	if err := decodePublicEnvelope(body, "Bitget", &rows); err != nil {
		return nil, err
	}
	klines := make([]Kline, 0, len(rows))
	for rowIndex, row := range rows {
		if len(row) < 5 {
			return nil, fmt.Errorf("Bitget candle %d has %d fields, want at least 5", rowIndex, len(row))
		}
		openTime, err := parseRawInt64(row[0], "Bitget candle timestamp")
		if err != nil {
			return nil, err
		}
		open, err := parseRawFloat(row[1], "Bitget candle open")
		if err != nil {
			return nil, err
		}
		high, err := parseRawFloat(row[2], "Bitget candle high")
		if err != nil {
			return nil, err
		}
		low, err := parseRawFloat(row[3], "Bitget candle low")
		if err != nil {
			return nil, err
		}
		closePrice, err := parseRawFloat(row[4], "Bitget candle close")
		if err != nil {
			return nil, err
		}
		volume := 0.0
		if len(row) > 5 {
			volume, _ = parseRawFloat(row[5], "Bitget candle volume")
		}
		quoteVolume := 0.0
		if len(row) > 6 {
			quoteVolume, _ = parseRawFloat(row[6], "Bitget candle quote volume")
		}
		klines = append(klines, Kline{
			OpenTime:    openTime,
			Open:        open,
			High:        high,
			Low:         low,
			Close:       closePrice,
			Volume:      volume,
			CloseTime:   openTime + bitgetIntervalDuration(granularity).Milliseconds() - 1,
			QuoteVolume: quoteVolume,
		})
	}
	sort.SliceStable(klines, func(left, right int) bool { return klines[left].OpenTime < klines[right].OpenTime })
	if err := validateFreshPublicKlines("Bitget", provider.NormalizeSymbol(symbol), granularity, klines, bitgetIntervalDuration(granularity)); err != nil {
		return nil, err
	}
	return klines, nil
}

func (provider *BitgetMarketDataProvider) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return provider.GetKlinesFreshContext(context.Background(), symbol, interval, limit)
}

func (provider *BitgetMarketDataProvider) GetKlinesFreshContext(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	normalizedSymbol := provider.NormalizeSymbol(symbol)
	klines, err := provider.getKlinesContext(ctx, normalizedSymbol, interval, limit)
	recordKlineHealth(provider.Exchange(), provider.NormalizeSymbol(symbol), interval, proofFromKlines(provider.Exchange(), "primary-rest", klines), err)
	return klines, err
}

func (provider *BitgetMarketDataProvider) GetDepth(symbol string, limit int) (*DepthSnapshot, error) {
	return provider.GetDepthFresh(symbol, limit)
}

func (provider *BitgetMarketDataProvider) GetDepthFresh(symbol string, limit int) (*DepthSnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 150 {
		limit = 150
	}
	body, err := provider.httpClient.getFresh(bitgetPublicOrderBookPath, url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
		"limit":       {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}
	var book struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
		Ts   string     `json:"ts"`
	}
	if err := decodePublicEnvelope(body, "Bitget", &book); err != nil {
		return nil, err
	}
	depth := &DepthSnapshot{
		EventTime:       parseOptionalInt64(book.Ts),
		TransactionTime: parseOptionalInt64(book.Ts),
		Bids:            normalizeDepthLevels(book.Bids),
		Asks:            normalizeDepthLevels(book.Asks),
		Exchange:        "bitget",
		Transport:       "rest",
		ReceivedAt:      time.Now().UTC(),
		Fresh:           true,
	}
	recordDepthRESTFallback(provider.Exchange(), symbol, depth)
	return depth, nil
}

func (provider *BitgetMarketDataProvider) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	normalizedSymbol := provider.NormalizeSymbol(symbol)
	var streamErr error
	if provider.fundingStream != nil {
		snapshot, _, err := provider.fundingStream(normalizedSymbol)
		if err == nil {
			return snapshot, nil
		}
		streamErr = err
	}
	budget := provider.fundingRESTBudget
	if budget <= 0 {
		budget = bitgetFundingRESTBudget
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	snapshot, err := provider.getFundingSnapshotREST(ctx, normalizedSymbol)
	sharedFundingStreams.recordREST(normalizedSymbol, snapshot, err)
	if err != nil && streamErr != nil {
		return nil, fmt.Errorf("Bitget funding sources unavailable for %s (stream: %v; REST: %w)", normalizedSymbol, streamErr, err)
	}
	return snapshot, err
}

func (provider *BitgetMarketDataProvider) getFundingSnapshotREST(ctx context.Context, symbol string) (*FundingSnapshot, error) {
	attemptTimeout := provider.fundingRESTAttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = bitgetFundingAttemptTimeout
	}
	body, err := provider.httpClient.getFreshContext(ctx, bitgetPublicFundingPath, url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
	}, attemptTimeout)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Symbol          string          `json:"symbol"`
		FundingRate     json.RawMessage `json:"fundingRate"`
		NextUpdate      json.RawMessage `json:"nextUpdate"`
		NextFundingTime json.RawMessage `json:"nextFundingTime"`
		Ts              json.RawMessage `json:"ts"`
	}
	if err := decodePublicEnvelope(body, "Bitget", &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("Bitget returned no funding rate for %s", symbol)
	}
	if !strings.EqualFold(rows[0].Symbol, provider.exchangeSymbol(symbol)) {
		return nil, fmt.Errorf("Bitget funding %s returned symbol %s", symbol, rows[0].Symbol)
	}
	rate, err := parseRawFloat(rows[0].FundingRate, "Bitget funding rate")
	if err != nil {
		return nil, err
	}
	nextFundingTime := parseOptionalRawInt64(rows[0].NextUpdate)
	if nextFundingTime == 0 {
		nextFundingTime = parseOptionalRawInt64(rows[0].NextFundingTime)
	}
	fundingTime := parseOptionalRawInt64(rows[0].Ts)

	priceBody, err := provider.httpClient.getFreshContext(ctx, bitgetPublicSymbolPricePath, url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
	}, attemptTimeout)
	if err != nil {
		return nil, fmt.Errorf("Bitget funding %s symbol price: %w", symbol, err)
	}
	var prices []struct {
		Symbol     string          `json:"symbol"`
		MarkPrice  json.RawMessage `json:"markPrice"`
		IndexPrice json.RawMessage `json:"indexPrice"`
		Ts         json.RawMessage `json:"ts"`
	}
	if err := decodePublicEnvelope(priceBody, "Bitget", &prices); err != nil {
		return nil, fmt.Errorf("Bitget funding %s symbol price: %w", symbol, err)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("Bitget returned no symbol price for %s", symbol)
	}
	if !strings.EqualFold(prices[0].Symbol, provider.exchangeSymbol(symbol)) {
		return nil, fmt.Errorf("Bitget funding price %s returned symbol %s", symbol, prices[0].Symbol)
	}
	markPrice, err := parseRawFloat(prices[0].MarkPrice, "Bitget mark price")
	if err != nil {
		return nil, fmt.Errorf("invalid Bitget mark price for %s: %w", symbol, err)
	}
	if !validBitgetPrice(markPrice) {
		return nil, fmt.Errorf("invalid Bitget mark price for %s: %.8f", symbol, markPrice)
	}
	indexPrice, err := parseRawFloat(prices[0].IndexPrice, "Bitget index price")
	if err != nil {
		return nil, fmt.Errorf("invalid Bitget index price for %s: %w", symbol, err)
	}
	if !validBitgetPrice(indexPrice) {
		return nil, fmt.Errorf("invalid Bitget index price for %s: %.8f", symbol, indexPrice)
	}
	if fundingTime == 0 {
		if priceTime := parseOptionalRawInt64(prices[0].Ts); priceTime > 0 {
			fundingTime = priceTime
		}
	}
	if _, err := validateFreshSourceTimestamp("Bitget", symbol, "funding", fundingTime, bitgetFundingFreshness); err != nil {
		return nil, err
	}
	return &FundingSnapshot{
		Symbol:          provider.NormalizeSymbol(symbol),
		MarkPrice:       markPrice,
		IndexPrice:      indexPrice,
		Rate:            rate,
		NextFundingTime: nextFundingTime,
		Time:            fundingTime,
	}, nil
}

func (provider *BitgetMarketDataProvider) GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error) {
	type fundingHistoryRow struct {
		Symbol      string          `json:"symbol"`
		FundingRate json.RawMessage `json:"fundingRate"`
		FundingTime json.RawMessage `json:"fundingTime"`
	}
	const pageSize = 100
	rows := make([]fundingHistoryRow, 0, pageSize)
	for pageNo := 1; ; pageNo++ {
		body, err := provider.httpClient.getFresh(bitgetPublicFundingHistoryPath, url.Values{
			"symbol":      {provider.exchangeSymbol(symbol)},
			"productType": {bitgetPublicProductType},
			"pageSize":    {strconv.Itoa(pageSize)},
			"pageNo":      {strconv.Itoa(pageNo)},
		})
		if err != nil {
			return nil, err
		}
		var pageRows []fundingHistoryRow
		if err := decodePublicEnvelope(body, "Bitget", &pageRows); err != nil {
			return nil, err
		}
		rows = append(rows, pageRows...)
		if len(pageRows) < pageSize {
			break
		}
	}
	events := make([]FundingEvent, 0, len(rows))
	for _, row := range rows {
		fundingTime, parseErr := parseRawInt64(row.FundingTime, "Bitget funding history time")
		if parseErr != nil {
			return nil, parseErr
		}
		if fundingTime <= 0 {
			return nil, fmt.Errorf("Bitget funding history returned invalid funding time %d for %s", fundingTime, symbol)
		}
		if (startTime > 0 && fundingTime < startTime) || (endTime > 0 && fundingTime > endTime) {
			continue
		}
		rate, parseErr := parseRawFloat(row.FundingRate, "Bitget funding history rate")
		if parseErr != nil {
			return nil, parseErr
		}
		events = append(events, FundingEvent{
			Symbol:      provider.NormalizeSymbol(symbol),
			Rate:        rate,
			FundingTime: fundingTime,
		})
	}
	sort.SliceStable(events, func(left, right int) bool { return events[left].FundingTime < events[right].FundingTime })
	for index := range events {
		markPrice, markErr := provider.getHistoricalMarkPrice(events[index].Symbol, events[index].FundingTime)
		if markErr != nil {
			return nil, fmt.Errorf("Bitget funding history %s at %d: %w", symbol, events[index].FundingTime, markErr)
		}
		events[index].MarkPrice = markPrice
	}
	return events, nil
}

func (provider *BitgetMarketDataProvider) getHistoricalMarkPrice(symbol string, fundingTime int64) (float64, error) {
	const candleDuration = time.Minute
	window := candleDuration.Milliseconds()
	startTime := fundingTime - window
	if startTime < 0 {
		startTime = 0
	}
	body, err := provider.httpClient.getFresh(bitgetPublicHistoryMarkPath, url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
		"granularity": {"1m"},
		"startTime":   {strconv.FormatInt(startTime, 10)},
		"endTime":     {strconv.FormatInt(fundingTime+window, 10)},
		"limit":       {"5"},
	})
	if err != nil {
		return 0, fmt.Errorf("request historical mark candles: %w", err)
	}
	var rows [][]json.RawMessage
	if err := decodePublicEnvelope(body, "Bitget", &rows); err != nil {
		return 0, fmt.Errorf("decode historical mark candles: %w", err)
	}
	var (
		found         bool
		selectedOpen  int64
		selectedPrice float64
	)
	for rowIndex, row := range rows {
		if len(row) < 5 {
			return 0, fmt.Errorf("historical mark candle %d has %d fields, want at least 5", rowIndex, len(row))
		}
		openTime, parseErr := parseRawInt64(row[0], "Bitget historical mark candle timestamp")
		if parseErr != nil {
			return 0, parseErr
		}
		if openTime > fundingTime {
			continue
		}
		markPrice, parseErr := parseRawFloat(row[4], "Bitget historical mark candle close")
		if parseErr != nil {
			return 0, parseErr
		}
		if !validBitgetPrice(markPrice) {
			return 0, fmt.Errorf("historical mark candle at %d has invalid mark price %.8f", openTime, markPrice)
		}
		if !found || openTime > selectedOpen {
			found = true
			selectedOpen = openTime
			selectedPrice = markPrice
		}
	}
	if !found {
		return 0, fmt.Errorf("no historical mark candle covering funding time %d", fundingTime)
	}
	return selectedPrice, nil
}

func validBitgetPrice(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (provider *BitgetMarketDataProvider) GetOpenInterest(symbol string) (*OIData, error) {
	body, err := provider.httpClient.getFresh(bitgetPublicOpenInterestPath, url.Values{
		"symbol":      {provider.exchangeSymbol(symbol)},
		"productType": {bitgetPublicProductType},
	})
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := decodePublicEnvelope(body, "Bitget", &raw); err != nil {
		return nil, err
	}
	var object struct {
		OpenInterestList []struct {
			OpenInterest    string `json:"openInterest"`
			OpenInterestUSD string `json:"openInterestUsd"`
			Size            string `json:"size"`
		} `json:"openInterestList"`
	}
	if json.Unmarshal(raw, &object) == nil && len(object.OpenInterestList) > 0 {
		item := object.OpenInterestList[0]
		return bitgetOpenInterestResult(firstNonEmpty(item.OpenInterest, item.Size), item.OpenInterestUSD, symbol)
	}
	var rows []struct {
		OpenInterest    string `json:"openInterest"`
		OpenInterestUSD string `json:"openInterestUsd"`
		Size            string `json:"size"`
	}
	if json.Unmarshal(raw, &rows) == nil && len(rows) > 0 {
		return bitgetOpenInterestResult(firstNonEmpty(rows[0].OpenInterest, rows[0].Size), rows[0].OpenInterestUSD, symbol)
	}
	return nil, fmt.Errorf("Bitget returned an unsupported open-interest response for %s", symbol)
}

func (provider *BitgetMarketDataProvider) GetContractSpec(symbol string) (*ContractSpec, error) {
	canonicalSymbol := provider.NormalizeSymbol(symbol)
	body, err := provider.httpClient.getFresh(bitgetPublicContractsPath, url.Values{
		"productType": {bitgetPublicProductType},
		"symbol":      {provider.exchangeSymbol(canonicalSymbol)},
	})
	if err != nil {
		return nil, err
	}
	var contracts []struct {
		Symbol         string `json:"symbol"`
		BaseCoin       string `json:"baseCoin"`
		QuoteCoin      string `json:"quoteCoin"`
		SettleCoin     string `json:"settleCoin"`
		SymbolStatus   string `json:"symbolStatus"`
		MinTradeNum    string `json:"minTradeNum"`
		MaxTradeNum    string `json:"maxTradeNum"`
		SizeMultiplier string `json:"sizeMultiplier"`
		PricePlace     string `json:"pricePlace"`
		VolumePlace    string `json:"volumePlace"`
		PriceEndStep   string `json:"priceEndStep"`
	}
	if err := decodePublicEnvelope(body, "Bitget", &contracts); err != nil {
		return nil, err
	}
	if len(contracts) == 0 {
		return nil, fmt.Errorf("Bitget contract %s not found", canonicalSymbol)
	}
	item := contracts[0]
	minimumQuantity, _ := parsePublicFloat(item.MinTradeNum, "Bitget minimum quantity")
	maximumQuantity, _ := parsePublicFloat(item.MaxTradeNum, "Bitget maximum quantity")
	contractMultiplier, _ := parsePublicFloat(item.SizeMultiplier, "Bitget contract multiplier")
	if contractMultiplier == 0 {
		contractMultiplier = 1
	}
	priceDecimals, _ := strconv.Atoi(strings.TrimSpace(item.PricePlace))
	priceTick := pow10Negative(priceDecimals)
	if endStep, parseErr := strconv.ParseFloat(strings.TrimSpace(item.PriceEndStep), 64); parseErr == nil && endStep > 0 {
		priceTick *= endStep
	}
	quantityStep := pow10Negative(parseIntOrZero(item.VolumePlace))
	quoteAsset := item.SettleCoin
	if quoteAsset == "" {
		quoteAsset = item.QuoteCoin
	}
	return &ContractSpec{
		Symbol:             canonicalSymbol,
		ExchangeSymbol:     item.Symbol,
		BaseAsset:          item.BaseCoin,
		QuoteAsset:         quoteAsset,
		ContractType:       "perpetual",
		Status:             item.SymbolStatus,
		ContractMultiplier: contractMultiplier,
		PriceTick:          priceTick,
		QuantityStep:       quantityStep,
		MinQuantity:        minimumQuantity,
		MaxQuantity:        maximumQuantity,
		QuantityUnit:       "contract",
	}, nil
}

func bitgetOpenInterestResult(rawInterest, rawUSD, symbol string) (*OIData, error) {
	interest, err := parsePublicFloat(rawInterest, "Bitget open interest")
	if err != nil {
		return nil, err
	}
	notionalUSD, _ := parsePublicFloat(rawUSD, "Bitget open interest USD")
	return &OIData{Latest: interest, Average: interest, Unit: "base", NotionalUSD: notionalUSD}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (provider *BitgetMarketDataProvider) ListPerpetualSymbols(limit int) ([]string, error) {
	body, err := provider.httpClient.getFresh(bitgetPublicTickersPath, url.Values{"productType": {bitgetPublicProductType}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Symbol      string `json:"symbol"`
		QuoteVolume string `json:"quoteVolume"`
		USDTVolume  string `json:"usdtVolume"`
		Status      string `json:"symbolStatus"`
	}
	if err := decodePublicEnvelope(body, "Bitget", &rows); err != nil {
		return nil, err
	}
	type rankedSymbol struct {
		symbol string
		volume float64
	}
	ranked := make([]rankedSymbol, 0, len(rows))
	for _, row := range rows {
		if row.Status != "" && !strings.EqualFold(row.Status, "normal") {
			continue
		}
		volume, _ := parsePublicFloat(row.USDTVolume, "Bitget 24h USDT volume")
		if volume == 0 {
			volume, _ = parsePublicFloat(row.QuoteVolume, "Bitget 24h quote volume")
		}
		ranked = append(ranked, rankedSymbol{symbol: provider.NormalizeSymbol(row.Symbol), volume: volume})
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].volume == ranked[right].volume {
			return ranked[left].symbol < ranked[right].symbol
		}
		return ranked[left].volume > ranked[right].volume
	})
	if limit <= 0 || limit > len(ranked) {
		limit = len(ranked)
	}
	symbols := make([]string, 0, limit)
	for _, item := range ranked[:limit] {
		symbols = append(symbols, item.symbol)
	}
	return symbols, nil
}

func (provider *BitgetMarketDataProvider) ValidateMarketAvailability(symbol string) (*MarketAvailability, error) {
	canonicalSymbol := provider.NormalizeSymbol(symbol)
	body, err := provider.httpClient.getFresh(bitgetPublicContractsPath, url.Values{
		"productType": {bitgetPublicProductType},
		"symbol":      {provider.exchangeSymbol(canonicalSymbol)},
	})
	if err != nil {
		return nil, err
	}
	var contracts []struct {
		Symbol       string `json:"symbol"`
		SymbolStatus string `json:"symbolStatus"`
		QuoteCoin    string `json:"quoteCoin"`
	}
	if err := decodePublicEnvelope(body, "Bitget", &contracts); err != nil {
		return nil, err
	}
	if len(contracts) == 0 || (contracts[0].SymbolStatus != "" && !strings.EqualFold(contracts[0].SymbolStatus, "normal")) || (contracts[0].QuoteCoin != "" && !strings.EqualFold(contracts[0].QuoteCoin, "USDT")) {
		return nil, fmt.Errorf("Bitget contract %s is unavailable or not normal", canonicalSymbol)
	}
	price, err := provider.GetCurrentPrice(canonicalSymbol)
	if err != nil {
		return nil, err
	}
	return &MarketAvailability{Symbol: canonicalSymbol, Price: price, CheckedAt: time.Now().UTC()}, nil
}

func bitgetGranularity(interval string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(interval)) {
	case "1m", "3m", "5m", "15m", "30m":
		return strings.ToLower(strings.TrimSpace(interval)), nil
	case "1h", "2h", "4h", "6h", "12h":
		return strings.ToUpper(strings.TrimSpace(interval)), nil
	case "1d", "3d", "1w":
		return strings.ToUpper(strings.TrimSpace(interval)), nil
	default:
		return "", fmt.Errorf("unsupported Bitget candle interval: %s", interval)
	}
}

func bitgetIntervalDuration(granularity string) time.Duration {
	normalized := strings.ToLower(granularity)
	minutes := map[string]int{"1m": 1, "3m": 3, "5m": 5, "15m": 15, "30m": 30, "1h": 60, "2h": 120, "4h": 240, "6h": 360, "12h": 720, "1d": 1440, "3d": 4320, "1w": 10080}
	return time.Duration(minutes[normalized]) * time.Minute
}

func parseIntOrZero(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func pow10Negative(decimalPlaces int) float64 {
	if decimalPlaces <= 0 {
		return 1
	}
	return 1 / float64Pow10(decimalPlaces)
}

func float64Pow10(decimalPlaces int) float64 {
	value := 1.0
	for index := 0; index < decimalPlaces; index++ {
		value *= 10
	}
	return value
}
