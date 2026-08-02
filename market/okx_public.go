package market

import (
	"encoding/json"
	"fmt"
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
)

// OKXMarketDataProvider reads public OKX USDT perpetual market data. It does
// not need API credentials and deliberately uses the native OKX endpoints so
// the AI, paper broker, and UI all observe the same venue.
type OKXMarketDataProvider struct {
	httpClient nativePublicHTTPClient
}

func NewOKXMarketDataProvider() *OKXMarketDataProvider {
	return NewOKXMarketDataProviderWithHTTPClient(okxPublicBaseURL, nil)
}

func NewOKXMarketDataProviderWithHTTPClient(baseURL string, client *http.Client) *OKXMarketDataProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = okxPublicBaseURL
	}
	return &OKXMarketDataProvider{httpClient: newNativePublicHTTPClient(baseURL, client)}
}

func (provider *OKXMarketDataProvider) Exchange() string { return "okx" }

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
	data, err := provider.requestTicker(symbol)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("OKX returned no ticker for %s", provider.NormalizeSymbol(symbol))
	}
	price, err := parsePublicFloat(data[0].Last, "OKX last price")
	if err != nil {
		return 0, fmt.Errorf("invalid OKX price for %s: %w", symbol, err)
	}
	if price <= 0 {
		return 0, fmt.Errorf("invalid OKX price for %s: %.8f", symbol, price)
	}
	return price, nil
}

func (provider *OKXMarketDataProvider) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	return provider.getKlines(symbol, interval, limit, false)
}

func (provider *OKXMarketDataProvider) getKlines(symbol, interval string, limit int, requireFresh bool) ([]Kline, error) {
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
	var body []byte
	if requireFresh {
		body, err = provider.httpClient.getFresh(okxPublicCandlesPath, query)
	} else {
		body, err = provider.httpClient.get(okxPublicCandlesPath, query)
	}
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
	if requireFresh {
		if err := validateFreshPublicKlines("OKX", provider.NormalizeSymbol(symbol), bar, klines, okxIntervalDuration(bar)); err != nil {
			return nil, err
		}
	}
	return klines, nil
}

func (provider *OKXMarketDataProvider) GetKlinesFresh(symbol, interval string, limit int) ([]Kline, error) {
	return provider.getKlines(symbol, interval, limit, true)
}

func (provider *OKXMarketDataProvider) GetDepth(symbol string, limit int) (*DepthSnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 400 {
		limit = 400
	}
	body, err := provider.httpClient.get(okxPublicBooksPath, url.Values{
		"instId": {provider.exchangeSymbol(symbol)},
		"sz":     {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}
	var books []struct {
		Asks [][]string `json:"asks"`
		Bids [][]string `json:"bids"`
		Ts   string     `json:"ts"`
		Seq  string     `json:"seqId"`
	}
	if err := decodePublicEnvelope(body, "OKX", &books); err != nil {
		return nil, err
	}
	if len(books) == 0 {
		return nil, fmt.Errorf("OKX returned no order book for %s", symbol)
	}
	return &DepthSnapshot{
		LastUpdateID:    parseOptionalInt64(books[0].Seq),
		EventTime:       parseOptionalInt64(books[0].Ts),
		TransactionTime: parseOptionalInt64(books[0].Ts),
		Bids:            normalizeDepthLevels(books[0].Bids),
		Asks:            normalizeDepthLevels(books[0].Asks),
	}, nil
}

func (provider *OKXMarketDataProvider) GetFundingSnapshot(symbol string) (*FundingSnapshot, error) {
	instID := provider.exchangeSymbol(symbol)
	body, err := provider.httpClient.get(okxPublicFundingPath, url.Values{"instId": {instID}})
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
	markPrice := 0.0
	if markBody, markErr := provider.httpClient.get(okxPublicMarkPricePath, url.Values{
		"instType": {"SWAP"},
		"instId":   {instID},
	}); markErr == nil {
		var marks []struct {
			MarkPrice string `json:"markPx"`
		}
		if decodePublicEnvelope(markBody, "OKX", &marks) == nil && len(marks) > 0 {
			markPrice, _ = parsePublicFloat(marks[0].MarkPrice, "OKX mark price")
		}
	}
	return &FundingSnapshot{
		Symbol:          provider.NormalizeSymbol(symbol),
		MarkPrice:       markPrice,
		Rate:            rate,
		NextFundingTime: parseOptionalInt64(funding[0].NextFundingTime),
		Time:            parseOptionalInt64(funding[0].Ts),
	}, nil
}

func (provider *OKXMarketDataProvider) GetFundingHistory(symbol string, startTime, endTime int64) ([]FundingEvent, error) {
	body, err := provider.httpClient.get(okxPublicFundingHistoryPath, url.Values{
		"instId": {provider.exchangeSymbol(symbol)},
		"limit":  {"100"},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		InstID      string `json:"instId"`
		FundingRate string `json:"fundingRate"`
		FundingTime string `json:"fundingTime"`
	}
	if err := decodePublicEnvelope(body, "OKX", &rows); err != nil {
		return nil, err
	}
	events := make([]FundingEvent, 0, len(rows))
	for _, row := range rows {
		fundingTime := parseOptionalInt64(row.FundingTime)
		if (startTime > 0 && fundingTime < startTime) || (endTime > 0 && fundingTime > endTime) {
			continue
		}
		rate, parseErr := parsePublicFloat(row.FundingRate, "OKX funding history rate")
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
	return events, nil
}

func (provider *OKXMarketDataProvider) GetOpenInterest(symbol string) (*OIData, error) {
	body, err := provider.httpClient.get(okxPublicOpenInterestPath, url.Values{
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
	body, err := provider.httpClient.get(okxPublicInstrumentsPath, url.Values{
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
	body, err := provider.httpClient.get(okxPublicTickersPath, url.Values{"instType": {"SWAP"}})
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
	body, err := provider.httpClient.get(okxPublicInstrumentsPath, url.Values{
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

func (provider *OKXMarketDataProvider) requestTicker(symbol string) ([]struct {
	InstID string `json:"instId"`
	Last   string `json:"last"`
}, error) {
	body, err := provider.httpClient.get(okxPublicTickerPath, url.Values{"instId": {provider.exchangeSymbol(symbol)}})
	if err != nil {
		return nil, err
	}
	var tickers []struct {
		InstID string `json:"instId"`
		Last   string `json:"last"`
	}
	if err := decodePublicEnvelope(body, "OKX", &tickers); err != nil {
		return nil, err
	}
	return tickers, nil
}

func okxBarInterval(interval string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(interval)) {
	case "1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "12h", "1d", "3d", "1w":
		return strings.ToLower(strings.TrimSpace(interval)), nil
	default:
		return "", fmt.Errorf("unsupported OKX candle interval: %s", interval)
	}
}

func okxIntervalDuration(bar string) time.Duration {
	minutes := map[string]int{"1m": 1, "3m": 3, "5m": 5, "15m": 15, "30m": 30, "1h": 60, "2h": 120, "4h": 240, "6h": 360, "12h": 720, "1d": 1440, "3d": 4320, "1w": 10080}
	return time.Duration(minutes[bar]) * time.Minute
}
