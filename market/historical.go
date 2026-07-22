package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	binanceFuturesKlinesURL = "https://fapi.binance.com/fapi/v1/klines"
	binanceMaxKlineLimit    = 1500
)

// GetKlinesRange fetches K-line series within specified time range (closed interval), returns data sorted by time in ascending order.
func GetKlinesRange(symbol string, timeframe string, start, end time.Time) ([]Kline, error) {
	symbol = NormalizeForExchange("binance", symbol)
	normTF, err := NormalizeTimeframe(timeframe)
	if err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, fmt.Errorf("end time must be after start time")
	}

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	var all []Kline
	cursor := startMs

	client := &http.Client{Timeout: 15 * time.Second}

	for cursor < endMs {
		req, err := http.NewRequest("GET", binanceFuturesKlinesURL, nil)
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

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
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

		batch := make([]Kline, len(raw))
		for i, item := range raw {
			openTime := int64(item[0].(float64))
			open, _ := parseFloat(item[1])
			high, _ := parseFloat(item[2])
			low, _ := parseFloat(item[3])
			close, _ := parseFloat(item[4])
			volume, _ := parseFloat(item[5])
			closeTime := int64(item[6].(float64))

			batch[i] = Kline{
				OpenTime:  openTime,
				Open:      open,
				High:      high,
				Low:       low,
				Close:     close,
				Volume:    volume,
				CloseTime: closeTime,
			}
		}

		all = append(all, batch...)

		last := batch[len(batch)-1]
		cursor = last.CloseTime + 1

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
