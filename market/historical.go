package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

const (
	binanceFuturesKlinesURL = "https://fapi.binance.com/fapi/v1/klines"
	binanceMaxKlineLimit    = 1500
	maxKlineResponseBytes   = 2 << 20
	// DefaultHistoricalKlineLimit bounds memory and upstream request usage for
	// callers that do not provide a narrower replay-specific limit.
	DefaultHistoricalKlineLimit = 20_000
)

// GetKlinesRange fetches K-line series within specified time range (closed interval), returns data sorted by time in ascending order.
func GetKlinesRange(symbol string, timeframe string, start, end time.Time) ([]Kline, error) {
	return GetKlinesRangeContext(context.Background(), symbol, timeframe, start, end, DefaultHistoricalKlineLimit)
}

// GetKlinesRangeContext is the cancellable, bounded variant used by API jobs.
// maxCandles is enforced while fetching, before the accumulated slice can grow
// beyond the caller's budget.
func GetKlinesRangeContext(ctx context.Context, symbol string, timeframe string, start, end time.Time, maxCandles int) ([]Kline, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	symbol = NormalizeForExchange("binance", symbol)
	normTF, err := NormalizeTimeframe(timeframe)
	if err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, fmt.Errorf("end time must be after start time")
	}
	if maxCandles <= 0 {
		maxCandles = DefaultHistoricalKlineLimit
	}
	interval, err := TFDuration(normTF)
	if err != nil {
		return nil, err
	}
	requestedCandles := int64(end.Sub(start) / interval)
	if end.Sub(start)%interval != 0 {
		requestedCandles++
	}
	if requestedCandles > int64(maxCandles) {
		return nil, fmt.Errorf("historical range requests %d candles; maximum is %d", requestedCandles, maxCandles)
	}

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	var all []Kline
	cursor := startMs

	client := &http.Client{Timeout: 15 * time.Second}

	for cursor < endMs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, binanceFuturesKlinesURL, nil)
		if err != nil {
			return nil, err
		}

		q := req.URL.Query()
		q.Set("symbol", symbol)
		q.Set("interval", normTF)
		q.Set("limit", fmt.Sprintf("%d", binanceMaxKlineLimit))
		q.Set("startTime", fmt.Sprintf("%d", cursor))
		q.Set("endTime", fmt.Sprintf("%d", endMs))
		req.URL.RawQuery = q.Encode()

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxKlineResponseBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > maxKlineResponseBytes {
			return nil, fmt.Errorf("binance kline response exceeds %d bytes", maxKlineResponseBytes)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance klines api returned status %d: %s", resp.StatusCode, string(body))
		}

		var raw [][]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		if len(all)+len(raw) > maxCandles {
			return nil, fmt.Errorf("historical range returned more than %d candles", maxCandles)
		}

		batch := make([]Kline, len(raw))
		for i, item := range raw {
			if len(item) < 7 {
				return nil, fmt.Errorf("binance kline row %d has %d fields; expected at least 7", i, len(item))
			}
			openTimeValue, err := parseFloat(item[0])
			if err != nil || math.IsNaN(openTimeValue) || math.IsInf(openTimeValue, 0) {
				return nil, fmt.Errorf("invalid kline open time at row %d", i)
			}
			closeTimeValue, err := parseFloat(item[6])
			if err != nil || math.IsNaN(closeTimeValue) || math.IsInf(closeTimeValue, 0) {
				return nil, fmt.Errorf("invalid kline close time at row %d", i)
			}
			open, err := parseFloat(item[1])
			if err != nil {
				return nil, fmt.Errorf("invalid kline open price at row %d: %w", i, err)
			}
			high, err := parseFloat(item[2])
			if err != nil {
				return nil, fmt.Errorf("invalid kline high price at row %d: %w", i, err)
			}
			low, err := parseFloat(item[3])
			if err != nil {
				return nil, fmt.Errorf("invalid kline low price at row %d: %w", i, err)
			}
			close, err := parseFloat(item[4])
			if err != nil {
				return nil, fmt.Errorf("invalid kline close price at row %d: %w", i, err)
			}
			volume, err := parseFloat(item[5])
			if err != nil {
				return nil, fmt.Errorf("invalid kline volume at row %d: %w", i, err)
			}

			batch[i] = Kline{
				OpenTime:  int64(openTimeValue),
				Open:      open,
				High:      high,
				Low:       low,
				Close:     close,
				Volume:    volume,
				CloseTime: int64(closeTimeValue),
			}
		}

		all = append(all, batch...)

		last := batch[len(batch)-1]
		nextCursor := last.CloseTime + 1
		if nextCursor <= cursor {
			return nil, fmt.Errorf("binance kline cursor did not advance")
		}
		cursor = nextCursor

		// If returned quantity is less than request limit, reached the end, can exit early.
		if len(batch) < binanceMaxKlineLimit {
			break
		}
	}

	return all, nil
}

// BuildHistoricalData converts an already-fetched, closed-candle prefix into
// the same market.Data shape used by the live prompt builder. It performs no
// network calls, which is essential for a look-ahead-free historical replay.
func BuildHistoricalData(symbol, timeframe string, klines []Kline, count int) *Data {
	data := &Data{
		Symbol:        Normalize(symbol),
		TimeframeData: make(map[string]*TimeframeSeriesData),
	}
	if len(klines) == 0 {
		return data
	}
	if count <= 0 || count > len(klines) {
		count = len(klines)
	}
	data.CurrentPrice = klines[len(klines)-1].Close
	data.CurrentEMA20 = calculateEMA(klines, 20)
	data.CurrentMACD = calculateMACD(klines)
	data.CurrentRSI7 = calculateRSI(klines, 7)
	data.TimeframeData[timeframe] = calculateTimeframeSeries(klines, timeframe, count)
	return data
}

// FilterClosedKlines removes a currently-forming candle. Historical replays
// must never expose a high, low, close, or volume value that was not known at
// the decision timestamp.
func FilterClosedKlines(klines []Kline, cutoff time.Time) []Kline {
	cutoffMs := cutoff.UnixMilli()
	closed := make([]Kline, 0, len(klines))
	for _, kline := range klines {
		if kline.CloseTime <= cutoffMs {
			closed = append(closed, kline)
		}
	}
	return closed
}
