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
	okxPublicBaseURL            = "https://www.okx.com"
	okxPublicTickerPath         = "/api/v5/market/ticker"
	okxPublicTickersPath        = "/api/v5/market/tickers"
	okxPublicCandlesPath        = "/api/v5/market/candles"
	okxPublicBooksPath          = "/api/v5/market/books"
	okxPublicInstrumentsPath    = "/api/v5/public/instruments"
	okxPublicFundingPath        = "/api/v5/public/funding-rate"
	okxPublicFundingHistoryPath = "/api/v5/public/funding-rate-history"
	okxPublicOpenInterestPath   = "/api/v5/public/open-interest"
	okxPublicMarkPricePath      = "/api/v5/public/mark-price"
	okxPublicHistoryMarkPath    = "/api/v5/market/history-mark-price-candles"
)

const (
	okxFundingHistoryPageLimit = 100
	okxFundingHistoryMaxPages  = 100
	okxMarkCandlePageLimit     = 100
	okxMarkCandleMaxPages      = 100
)

// OKXMarketDataProvider reads public OKX USDT perpetual market data. It does
// not need API credentials and deliberately uses the native OKX endpoints so
// the AI, paper broker, and UI all observe the same venue.
type OKXMarketDataProvider struct {
	httpClient               nativePublicHTTPClient
	streamDepth              bool
	redundantKlines          bool
	priceStream              priceStreamSnapshot
	fundingStream            fundingStreamSnapshot
	marketRESTBudget         time.Duration
	marketRESTAttemptTimeout time.Duration
}

func NewOKXMarketDataProvider() *OKXMarketDataProvider {
	return NewOKXMarketDataProviderWithHTTPClient(okxPublicBaseURL, nil)
}

func NewOKXMarketDataProviderWithHTTPClient(baseURL string, client *http.Client) *OKXMarketDataProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = okxPublicBaseURL
	}
	provider := &OKXMarketDataProvider{
		httpClient:               newNativePublicHTTPClient(baseURL, client),
		streamDepth:              strings.Contains(strings.ToLower(baseURL), "okx.com"),
		redundantKlines:          strings.Contains(strings.ToLower(baseURL), "okx.com"),
		marketRESTBudget:         okxMarketRESTBudget,
		marketRESTAttemptTimeout: okxMarketAttemptTimeout,
	}
	if strings.Contains(strings.ToLower(baseURL), "okx.com") {
		provider.priceStream = streamedOKXPrice
		provider.fundingStream = streamedOKXFunding
	}
	return provider
}

func (provider *OKXMarketDataProvider) Exchange() string               { return "okx" }
func (provider *OKXMarketDataProvider) Capabilities() MarketCapability { return TradingCapabilities }

func (provider *OKXMarketDataProvider) NormalizeSymbol(symbol string) string {
	return NormalizeForExchange("okx", symbol)
}

func (provider *OKXMarketDataProvider) exchangeSymbol(symbol string) string {
	canonicalSymbol := provider.NormalizeSymbol(symbol)
	if strings.HasSuffix(canonicalSymbol, "USDT") {
		return strings.TrimSuffix(canonicalSymbol, "USDT") + "-USDT-SWAP"
	}
	return canonicalSymbol
}

func (provider *OKXMarketDataProvider) GetCurrentPrice(symbol string) (float64, error) {
	return provider.GetCurrentPriceFresh(symbol)
}

func (provider *OKXMarketDataProvider) GetCurrentPriceFresh(symbol string) (float64, error) {
	normalized := provider.NormalizeSymbol(symbol)
	var streamErr error
	if provider.priceStream != nil {
		price, _, err := provider.priceStream(normalized)
		if err == nil {
			return price, nil
		}
		streamErr = err
	}
	budget := provider.marketRESTBudget
	if budget <= 0 {
		budget = okxMarketRESTBudget
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	data, err := provider.requestTickerContext(ctx, normalized)
	if err != nil {
		sharedOKXStreams.recordPriceREST(normalized, nil, err)
		if streamErr != nil {
			return 0, fmt.Errorf("OKX price sources unavailable for %s (stream: %v; REST: %w)", normalized, streamErr, err)
		}
		return 0, err
	}
	if len(data) == 0 {
		err := fmt.Errorf("OKX returned no ticker for %s", provider.NormalizeSymbol(symbol))
		sharedOKXStreams.recordPriceREST(normalized, nil, err)
		return 0, err
	}
	if !strings.EqualFold(data[0].InstID, provider.exchangeSymbol(normalized)) {
		err := fmt.Errorf("OKX ticker %s returned instrument %s", normalized, data[0].InstID)
		sharedOKXStreams.recordPriceREST(normalized, nil, err)
		return 0, err
	}
	if _, timestampErr := validateFreshSourceTimestamp("OKX", normalized, "ticker", parseOptionalInt64(data[0].Ts), okxPriceFreshness); timestampErr != nil {
		err := timestampErr
		sharedOKXStreams.recordPriceREST(normalized, nil, err)
		return 0, err
	}
	price, err := parsePublicFloat(data[0].Last, "OKX last price")
	if err != nil {
		err = fmt.Errorf("invalid OKX price for %s: %w", symbol, err)
		sharedOKXStreams.recordPriceREST(normalized, nil, err)
		return 0, err
	}
	if price <= 0 {
		err := fmt.Errorf("invalid OKX price for %s: %.8f", symbol, price)
		sharedOKXStreams.recordPriceREST(normalized, nil, err)
		return 0, err
	}
	sharedOKXStreams.recordPriceREST(normalized, &data[0], nil)
	return price, nil
}

func (provider *OKXMarketDataProvider) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	return provider.GetKlinesFresh(symbol, interval, limit)
}

func (provider *OKXMarketDataProvider) getKlines(symbol, interval string, limit int) ([]Kline, error) {
	bar, err := okxBarInterval(interval)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 300 {
		limit = 300
	}
	query := url.Values{
		"instId": {provider.exchangeSymbol(symbol)},
		"bar":    {bar},
		"limit":  {strconv.Itoa(limit)},
	}
	body, err := provider.httpClient.getFresh(okxPublicCandlesPath, query)
	if err != nil {
		return nil, err
	}
	var rows [][]json.RawMessage
	if err := decodePublicEnvelope(body, "OKX", &rows); err != nil {
		return nil, err
	}
	klines := make([]Kline, 0, len(rows))
	for rowIndex, row := range rows {
		if len(row) < 6 {
			return nil, fmt.Errorf("OKX candle %d has %d fields, want at least 6", rowIndex, len(row))
		}
		openTime, err := parseRawInt64(row[0], "OKX candle timestamp")
		if err != nil {
			return nil, err
		}
		open, err := parseRawFloat(row[1], "OKX candle open")
		if err != nil {
			return nil, err
		}
		high, err := parseRawFloat(row[2], "OKX candle high")
		if err != nil {
			return nil, err
		}
		low, err := parseRawFloat(row[3], "OKX candle low")
		if err != nil {
			return nil, err
		}
		closePrice, err := parseRawFloat(row[4], "OKX candle close")
		if err != nil {
			return nil, err
		}
		volume, err := parseRawFloat(row[5], "OKX candle volume")
		if err != nil {
			return nil, err
		}
		quoteVolume := 0.0
		if len(row) > 7 {
			quoteVolume, _ = parseRawFloat(row[7], "OKX candle quote volume")
		}
		klines = append(klines, Kline{
			OpenTime:    openTime,
			Open:        open,
			High:        high,
			Low:         low,
			Close:       closePrice,
			Volume:      volume,
			CloseTime:   openTime + okxIntervalDuration(bar).Milliseconds() - 1,
			QuoteVolume: quoteVolume,
		})
	}
	sort.SliceStable(klines, func(left, right int) bool { return klines[left].OpenTime < klines[right].OpenTime })
	if err := validateFreshPublicKlines("OKX", provider.NormalizeSymbol(symbol), bar, klines, okxIntervalDuration(bar)); err != nil {
		return nil, err
	}
	return klines, nil
}

func (provider *OKXMarketDataProvider) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	if provider.redundantKlines {
		normalizedSymbol := provider.NormalizeSymbol(symbol)
		klines, proof, err := hedgedFreshKlines(context.Background(), provider.Exchange(), normalizedSymbol, interval,
			func(context.Context) ([]Kline, error) { return provider.getKlines(normalizedSymbol, interval, limit) },
			func(ctx context.Context) ([]Kline, error) {
				return getKlinesFromCoinAnkFreshContext(ctx, normalizedSymbol, interval, provider.Exchange(), limit)
			},
		)
		recordKlineHealth(provider.Exchange(), normalizedSymbol, interval, proof, err)
		return klines, err
	}
	klines, err := provider.getKlines(symbol, interval, limit)
	recordKlineHealth(provider.Exchange(), provider.NormalizeSymbol(symbol), interval, proofFromKlines(provider.Exchange(), "primary-rest", klines), err)
	return klines, err
}

func (provider *OKXMarketDataProvider) GetDepth(symbol string, limit int) (*DepthSnapshot, error) {
	return provider.GetDepthFresh(symbol, limit)
}

func (provider *OKXMarketDataProvider) GetDepthFresh(symbol string, limit int) (*DepthSnapshot, error) {
	if provider.streamDepth {
		if depth, err := streamedDepth(provider.Exchange(), provider.NormalizeSymbol(symbol), limit); err == nil {
			return depth, nil
		}
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 400 {
		limit = 400
	}
	body, err := provider.httpClient.getFresh(okxPublicBooksPath, url.Values{
		"instId": {provider.exchangeSymbol(symbol)},
		"sz":     {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}
	var books []struct {
		Asks [][]string      `json:"asks"`
		Bids [][]string      `json:"bids"`
		Ts   json.RawMessage `json:"ts"`
		Seq  json.RawMessage `json:"seqId"`
	}
	if err := decodePublicEnvelope(body, "OKX", &books); err != nil {
		return nil, err
	}
	if len(books) == 0 {
		return nil, fmt.Errorf("OKX returned no order book for %s", symbol)
	}
	depth := &DepthSnapshot{
		LastUpdateID:    parseOptionalRawInt64(books[0].Seq),
		EventTime:       parseOptionalRawInt64(books[0].Ts),
		TransactionTime: parseOptionalRawInt64(books[0].Ts),
		Bids:            normalizeDepthLevels(books[0].Bids),
		Asks:            normalizeDepthLevels(books[0].Asks),
		Exchange:        "okx",
		Transport:       "rest",
		ReceivedAt:      time.Now().UTC(),
		Fresh:           true,
	}
	recordDepthRESTFallback(provider.Exchange(), symbol, depth)
	return depth, nil
}

func (provider *OKXMarketDataProvider) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	normalized := provider.NormalizeSymbol(symbol)
	var streamErr error
	if provider.fundingStream != nil {
		snapshot, _, err := provider.fundingStream(normalized)
		if err == nil {
			return snapshot, nil
		}
		streamErr = err
	}
	budget := provider.marketRESTBudget
	if budget <= 0 {
		budget = okxMarketRESTBudget
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	snapshot, err := provider.getFundingSnapshotREST(ctx, normalized)
	sharedOKXStreams.recordFundingREST(normalized, snapshot, err)
	if err != nil && streamErr != nil {
		return nil, fmt.Errorf("OKX funding sources unavailable for %s (stream: %v; REST: %w)", normalized, streamErr, err)
	}
	return snapshot, err
}

func (provider *OKXMarketDataProvider) getFundingSnapshotREST(ctx context.Context, symbol string) (*FundingSnapshot, error) {
	instID := provider.exchangeSymbol(symbol)
	attemptTimeout := provider.marketRESTAttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = okxMarketAttemptTimeout
	}
	body, err := provider.httpClient.getFreshContext(ctx, okxPublicFundingPath, url.Values{"instId": {instID}}, attemptTimeout)
	if err != nil {
		return nil, err
	}
	var funding []struct {
		InstID          string `json:"instId"`
		FundingRate     string `json:"fundingRate"`
		NextFundingTime string `json:"nextFundingTime"`
		Ts              string `json:"ts"`
	}
	if err := decodePublicEnvelope(body, "OKX", &funding); err != nil {
		return nil, err
	}
	if len(funding) == 0 {
		return nil, fmt.Errorf("OKX returned no funding rate for %s", symbol)
	}
	rate, err := parsePublicFloat(funding[0].FundingRate, "OKX funding rate")
	if err != nil {
		return nil, err
	}
	markBody, markErr := provider.httpClient.getFreshContext(ctx, okxPublicMarkPricePath, url.Values{
		"instType": {"SWAP"},
		"instId":   {instID},
	}, attemptTimeout)
	if markErr != nil {
		return nil, fmt.Errorf("OKX funding mark price for %s: %w", symbol, markErr)
	}
	var marks []struct {
		InstID    string `json:"instId"`
		MarkPrice string `json:"markPx"`
		Ts        string `json:"ts"`
	}
	if err := decodePublicEnvelope(markBody, "OKX", &marks); err != nil {
		return nil, fmt.Errorf("OKX funding mark price for %s: %w", symbol, err)
	}
	if len(marks) == 0 {
		return nil, fmt.Errorf("OKX returned no mark price for %s", symbol)
	}
	if !strings.EqualFold(funding[0].InstID, instID) {
		return nil, fmt.Errorf("OKX funding %s returned instrument %s", symbol, funding[0].InstID)
	}
	if !strings.EqualFold(marks[0].InstID, instID) {
		return nil, fmt.Errorf("OKX funding mark price %s returned instrument %s", symbol, marks[0].InstID)
	}
	markPrice, err := parsePublicFloat(marks[0].MarkPrice, "OKX mark price")
	if err != nil || !validOKXMarkPrice(markPrice) {
		if err != nil {
			return nil, fmt.Errorf("invalid OKX mark price for %s: %w", symbol, err)
		}
		return nil, fmt.Errorf("invalid OKX mark price for %s: %.8f", symbol, markPrice)
	}
	fundingTime := parseOptionalInt64(funding[0].Ts)
	if _, err := validateFreshSourceTimestamp("OKX", symbol, "funding", fundingTime, okxFundingFreshness); err != nil {
		return nil, err
	}
	markTime := parseOptionalInt64(marks[0].Ts)
	if _, err := validateFreshSourceTimestamp("OKX", symbol, "mark price", markTime, okxMarkFreshness); err != nil {
		return nil, err
	}
	componentSkew := time.Duration(fundingTime-markTime) * time.Millisecond
	if componentSkew < 0 {
		componentSkew = -componentSkew
	}
	if componentSkew > okxFundingComponentMaxSkew {
		return nil, fmt.Errorf("OKX funding components for %s differ by %s", symbol, componentSkew)
	}
	return &FundingSnapshot{
		Symbol:          provider.NormalizeSymbol(symbol),
		MarkPrice:       markPrice,
		Rate:            rate,
		NextFundingTime: parseOptionalInt64(funding[0].NextFundingTime),
		Time:            fundingTime,
	}, nil
}

func (provider *OKXMarketDataProvider) GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error) {
	canonicalSymbol := provider.NormalizeSymbol(symbol)
	instID := provider.exchangeSymbol(symbol)
	type fundingHistoryRow struct {
		InstID      string          `json:"instId"`
		FundingRate json.RawMessage `json:"fundingRate"`
		FundingTime json.RawMessage `json:"fundingTime"`
	}

	rows := make([]fundingHistoryRow, 0, okxFundingHistoryPageLimit)
	var after int64
	for page := 0; page < okxFundingHistoryMaxPages; page++ {
		query := url.Values{
			"instId": {instID},
			"limit":  {strconv.Itoa(okxFundingHistoryPageLimit)},
		}
		if after > 0 {
			query.Set("after", strconv.FormatInt(after, 10))
		}
		body, err := provider.httpClient.getFresh(okxPublicFundingHistoryPath, query)
		if err != nil {
			return nil, fmt.Errorf("OKX funding history request for %s: %w", canonicalSymbol, err)
		}
		var pageRows []fundingHistoryRow
		if err := decodePublicEnvelope(body, "OKX", &pageRows); err != nil {
			return nil, fmt.Errorf("OKX funding history response for %s: %w", canonicalSymbol, err)
		}
		if len(pageRows) == 0 {
			break
		}
		var oldest int64
		for _, row := range pageRows {
			fundingTime, parseErr := parseRawInt64(row.FundingTime, "OKX funding history time")
			if parseErr != nil {
				return nil, fmt.Errorf("OKX funding history %s has invalid funding time: %w", canonicalSymbol, parseErr)
			}
			if fundingTime <= 0 {
				return nil, fmt.Errorf("OKX funding history returned invalid funding time %d for %s", fundingTime, canonicalSymbol)
			}
			if oldest == 0 || fundingTime < oldest {
				oldest = fundingTime
			}
		}
		if after > 0 && oldest >= after {
			return nil, fmt.Errorf("OKX funding history pagination made no progress for %s after %d", canonicalSymbol, after)
		}
		rows = append(rows, pageRows...)
		if startTime > 0 && oldest <= startTime {
			break
		}
		if len(pageRows) < okxFundingHistoryPageLimit {
			break
		}
		if oldest == 0 {
			break
		}
		after = oldest
	}

	events := make([]FundingEvent, 0, len(rows))
	for _, row := range rows {
		fundingTime, parseErr := parseRawInt64(row.FundingTime, "OKX funding history time")
		if parseErr != nil {
			return nil, fmt.Errorf("OKX funding history %s at invalid time: %w", canonicalSymbol, parseErr)
		}
		if (startTime > 0 && fundingTime < startTime) || (endTime > 0 && fundingTime > endTime) {
			continue
		}
		rate, parseErr := parseRawFloat(row.FundingRate, "OKX funding history rate")
		if parseErr != nil {
			return nil, fmt.Errorf("OKX funding history %s at %d: %w", canonicalSymbol, fundingTime, parseErr)
		}
		events = append(events, FundingEvent{
			Symbol:      canonicalSymbol,
			Rate:        rate,
			FundingTime: fundingTime,
		})
	}
	sort.SliceStable(events, func(left, right int) bool { return events[left].FundingTime < events[right].FundingTime })
	for index := range events {
		markPrice, markErr := provider.getHistoricalMarkPrice(events[index].Symbol, events[index].FundingTime)
		if markErr != nil {
			return nil, markErr
		}
		events[index].MarkPrice = markPrice
	}
	return events, nil
}

func (provider *OKXMarketDataProvider) getHistoricalMarkPrice(symbol string, fundingTime int64) (float64, error) {
	if fundingTime <= 0 {
		return 0, fmt.Errorf("OKX historical mark price for %s at %d: invalid funding time", symbol, fundingTime)
	}

	// OKX's `after` cursor returns candles older than the requested timestamp.
	// Adding one millisecond keeps a candle whose opening timestamp equals the
	// funding timestamp eligible for selection.
	after := fundingTime
	if after < math.MaxInt64 {
		after++
	}
	var (
		found         bool
		selectedTime  int64
		selectedPrice float64
	)
	for page := 0; page < okxMarkCandleMaxPages; page++ {
		query := url.Values{
			"instId": {provider.exchangeSymbol(symbol)},
			"bar":    {"1m"},
			"limit":  {strconv.Itoa(okxMarkCandlePageLimit)},
			"after":  {strconv.FormatInt(after, 10)},
		}
		body, err := provider.httpClient.getFresh(okxPublicHistoryMarkPath, query)
		if err != nil {
			return 0, fmt.Errorf("OKX historical mark price for %s at %d: request failed: %w", symbol, fundingTime, err)
		}
		var rows [][]json.RawMessage
		if err := decodePublicEnvelope(body, "OKX", &rows); err != nil {
			return 0, fmt.Errorf("OKX historical mark price for %s at %d: decode failed: %w", symbol, fundingTime, err)
		}
		if len(rows) == 0 {
			break
		}

		oldest := int64(0)
		for rowIndex, row := range rows {
			if len(row) < 5 {
				return 0, fmt.Errorf("OKX historical mark price for %s at %d: candle %d has %d fields, want at least 5", symbol, fundingTime, rowIndex, len(row))
			}
			candleTime, parseErr := parseRawInt64(row[0], "OKX historical mark candle timestamp")
			if parseErr != nil {
				return 0, fmt.Errorf("OKX historical mark price for %s at %d: %w", symbol, fundingTime, parseErr)
			}
			if candleTime <= 0 {
				return 0, fmt.Errorf("OKX historical mark price for %s at %d: invalid candle timestamp %d", symbol, fundingTime, candleTime)
			}
			markPrice, parseErr := parseRawFloat(row[4], "OKX historical mark candle close")
			if parseErr != nil {
				return 0, fmt.Errorf("OKX historical mark price for %s at %d: %w", symbol, fundingTime, parseErr)
			}
			if !validOKXMarkPrice(markPrice) {
				return 0, fmt.Errorf("OKX historical mark price for %s at %d: invalid candle price %.8f", symbol, fundingTime, markPrice)
			}
			if oldest == 0 || candleTime < oldest {
				oldest = candleTime
			}
			if candleTime <= fundingTime && (!found || candleTime > selectedTime) {
				found = true
				selectedTime = candleTime
				selectedPrice = markPrice
			}
		}
		if found {
			return selectedPrice, nil
		}
		if len(rows) < okxMarkCandlePageLimit || oldest == 0 || oldest >= after {
			break
		}
		after = oldest
	}
	if !found {
		return 0, fmt.Errorf("OKX historical mark price for %s at %d: no valid candle at or before funding time", symbol, fundingTime)
	}
	return selectedPrice, nil
}

func validOKXMarkPrice(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (provider *OKXMarketDataProvider) GetOpenInterest(symbol string) (*OIData, error) {
	body, err := provider.httpClient.getFresh(okxPublicOpenInterestPath, url.Values{
		"instType": {"SWAP"},
		"instId":   {provider.exchangeSymbol(symbol)},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		OI        string `json:"oi"`
		OICcy     string `json:"oiCcy"`
		OIUSD     string `json:"oiUsd"`
		Timestamp string `json:"ts"`
	}
	if err := decodePublicEnvelope(body, "OKX", &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("OKX returned no open interest for %s", symbol)
	}
	latest, err := parsePublicFloat(rows[0].OICcy, "OKX open interest currency")
	if err != nil || latest == 0 {
		latest, err = parsePublicFloat(rows[0].OI, "OKX open interest contracts")
		if err != nil {
			return nil, err
		}
	}
	notionalUSD, _ := parsePublicFloat(rows[0].OIUSD, "OKX open interest USD")
	return &OIData{Latest: latest, Average: latest, Unit: "base", NotionalUSD: notionalUSD}, nil
}

func (provider *OKXMarketDataProvider) GetContractSpec(symbol string) (*ContractSpec, error) {
	canonicalSymbol := provider.NormalizeSymbol(symbol)
	body, err := provider.httpClient.getFresh(okxPublicInstrumentsPath, url.Values{
		"instType": {"SWAP"},
		"instId":   {provider.exchangeSymbol(canonicalSymbol)},
	})
	if err != nil {
		return nil, err
	}
	var instruments []struct {
		InstID    string `json:"instId"`
		InstType  string `json:"instType"`
		BaseCcy   string `json:"baseCcy"`
		QuoteCcy  string `json:"quoteCcy"`
		SettleCcy string `json:"settleCcy"`
		CtVal     string `json:"ctVal"`
		CtMult    string `json:"ctMult"`
		LotSz     string `json:"lotSz"`
		MinSz     string `json:"minSz"`
		MaxLmtSz  string `json:"maxLmtSz"`
		TickSz    string `json:"tickSz"`
		State     string `json:"state"`
	}
	if err := decodePublicEnvelope(body, "OKX", &instruments); err != nil {
		return nil, err
	}
	if len(instruments) == 0 {
		return nil, fmt.Errorf("OKX contract %s not found", canonicalSymbol)
	}
	item := instruments[0]
	contractValue, _ := parsePublicFloat(item.CtVal, "OKX contract value")
	contractMultiplier, _ := parsePublicFloat(item.CtMult, "OKX contract multiplier")
	if contractMultiplier == 0 {
		contractMultiplier = 1
	}
	priceTick, _ := parsePublicFloat(item.TickSz, "OKX price tick")
	quantityStep, _ := parsePublicFloat(item.LotSz, "OKX quantity step")
	minQuantity, _ := parsePublicFloat(item.MinSz, "OKX minimum quantity")
	maxQuantity, _ := parsePublicFloat(item.MaxLmtSz, "OKX maximum quantity")
	return &ContractSpec{
		Symbol:             canonicalSymbol,
		ExchangeSymbol:     item.InstID,
		BaseAsset:          item.BaseCcy,
		QuoteAsset:         item.SettleCcy,
		ContractType:       item.InstType,
		Status:             item.State,
		ContractMultiplier: contractValue * contractMultiplier,
		PriceTick:          priceTick,
		QuantityStep:       quantityStep,
		MinQuantity:        minQuantity,
		MaxQuantity:        maxQuantity,
		QuantityUnit:       "contract",
	}, nil
}

func (provider *OKXMarketDataProvider) ListPerpetualSymbols(limit int) ([]string, error) {
	body, err := provider.httpClient.getFresh(okxPublicTickersPath, url.Values{"instType": {"SWAP"}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		InstID    string `json:"instId"`
		Volume24h string `json:"volCcy24h"`
	}
	if err := decodePublicEnvelope(body, "OKX", &rows); err != nil {
		return nil, err
	}
	type rankedSymbol struct {
		symbol string
		volume float64
	}
	ranked := make([]rankedSymbol, 0, len(rows))
	for _, row := range rows {
		if !strings.HasSuffix(row.InstID, "-USDT-SWAP") {
			continue
		}
		volume, _ := parsePublicFloat(row.Volume24h, "OKX 24h volume")
		ranked = append(ranked, rankedSymbol{symbol: provider.NormalizeSymbol(row.InstID), volume: volume})
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

func (provider *OKXMarketDataProvider) ValidateMarketAvailability(symbol string) (*MarketAvailability, error) {
	canonicalSymbol := provider.NormalizeSymbol(symbol)
	body, err := provider.httpClient.getFresh(okxPublicInstrumentsPath, url.Values{
		"instType": {"SWAP"},
		"instId":   {provider.exchangeSymbol(canonicalSymbol)},
	})
	if err != nil {
		return nil, err
	}
	var instruments []struct {
		InstID string `json:"instId"`
		State  string `json:"state"`
		Settle string `json:"settleCcy"`
	}
	if err := decodePublicEnvelope(body, "OKX", &instruments); err != nil {
		return nil, err
	}
	if len(instruments) == 0 || !strings.EqualFold(instruments[0].State, "live") || !strings.EqualFold(instruments[0].Settle, "USDT") {
		return nil, fmt.Errorf("OKX contract %s is unavailable or not live", canonicalSymbol)
	}
	price, err := provider.GetCurrentPrice(canonicalSymbol)
	if err != nil {
		return nil, err
	}
	return &MarketAvailability{Symbol: canonicalSymbol, Price: price, CheckedAt: time.Now().UTC()}, nil
}

type okxTickerRow struct {
	InstID string `json:"instId"`
	Last   string `json:"last"`
	Ts     string `json:"ts"`
}

func (provider *OKXMarketDataProvider) requestTicker(symbol string) ([]okxTickerRow, error) {
	return provider.requestTickerContext(context.Background(), symbol)
}

func (provider *OKXMarketDataProvider) requestTickerContext(ctx context.Context, symbol string) ([]okxTickerRow, error) {
	attemptTimeout := provider.marketRESTAttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = okxMarketAttemptTimeout
	}
	body, err := provider.httpClient.getFreshContext(ctx, okxPublicTickerPath, url.Values{"instId": {provider.exchangeSymbol(symbol)}}, attemptTimeout)
	if err != nil {
		return nil, err
	}
	var tickers []okxTickerRow
	if err := decodePublicEnvelope(body, "OKX", &tickers); err != nil {
		return nil, err
	}
	return tickers, nil
}

func okxBarInterval(interval string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(interval)) {
	case "1m", "3m", "5m", "15m", "30m":
		return strings.ToLower(strings.TrimSpace(interval)), nil
	case "1h":
		return "1H", nil
	case "2h":
		return "2H", nil
	case "4h":
		return "4H", nil
	case "6h":
		return "6H", nil
	case "12h":
		return "12H", nil
	case "1d":
		return "1D", nil
	case "2d":
		return "2D", nil
	case "3d":
		return "3D", nil
	case "5d":
		return "5D", nil
	case "1w":
		return "1W", nil
	case "1M", "1mo", "1month":
		return "1M", nil
	default:
		return "", fmt.Errorf("unsupported OKX candle interval: %s", interval)
	}
}

func okxIntervalDuration(bar string) time.Duration {
	minutes := map[string]int{
		"1m": 1, "3m": 3, "5m": 5, "15m": 15, "30m": 30,
		"1h": 60, "1H": 60,
		"2h": 120, "2H": 120,
		"4h": 240, "4H": 240,
		"6h": 360, "6H": 360,
		"12h": 720, "12H": 720,
		"1d": 1440, "1D": 1440,
		"2d": 2880, "2D": 2880,
		"3d": 4320, "3D": 4320,
		"5d": 7200, "5D": 7200,
		"1w": 10080, "1W": 10080,
		"1M": 43200,
	}
	if d, ok := minutes[bar]; ok {
		return time.Duration(d) * time.Minute
	}
	return 15 * time.Minute
}
