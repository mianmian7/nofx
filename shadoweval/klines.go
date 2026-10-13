package shadoweval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// PriceSource returns the open price of the 1m bar starting at minuteMs.
// ok=false with err=nil means the bar does not exist (yet) or the symbol is unknown.
type PriceSource interface {
	OpenAt(ctx context.Context, symbol string, minuteMs int64) (price float64, ok bool, err error)
}

const chunkMinutes = 1000

// BinanceKlines reads public USD-M futures 1m klines and caches them in
// fixed-size chunks. It holds no credentials.
type BinanceKlines struct {
	BaseURL string
	Client  *http.Client
	mu      sync.Mutex
	cache   map[string]map[int64]float64 // symbol|chunkStart -> minute -> open
	unknown map[string]bool
}

func NewBinanceKlines(baseURL string) *BinanceKlines {
	if baseURL == "" {
		baseURL = "https://fapi.binance.com"
	}
	return &BinanceKlines{BaseURL: baseURL, Client: &http.Client{Timeout: 15 * time.Second},
		cache: map[string]map[int64]float64{}, unknown: map[string]bool{}}
}

// Reset drops cached chunks (called once per fill pass to bound memory and
// pick up newly closed bars).
func (b *BinanceKlines) Reset() {
	b.mu.Lock()
	b.cache = map[string]map[int64]float64{}
	b.mu.Unlock()
}

func (b *BinanceKlines) OpenAt(ctx context.Context, symbol string, minuteMs int64) (float64, bool, error) {
	minuteMs = minuteMs / 60000 * 60000
	chunk := minuteMs / (chunkMinutes * 60000) * (chunkMinutes * 60000)
	key := symbol + "|" + strconv.FormatInt(chunk, 10)
	b.mu.Lock()
	if b.unknown[symbol] {
		b.mu.Unlock()
		return 0, false, nil
	}
	m, ok := b.cache[key]
	b.mu.Unlock()
	if !ok {
		var err error
		m, err = b.fetch(ctx, symbol, chunk)
		if err != nil {
			return 0, false, err
		}
		b.mu.Lock()
		b.cache[key] = m
		b.mu.Unlock()
	}
	p, ok := m[minuteMs]
	return p, ok, nil
}

func (b *BinanceKlines) fetch(ctx context.Context, symbol string, start int64) (map[int64]float64, error) {
	url := fmt.Sprintf("%s/fapi/v1/klines?symbol=%s&interval=1m&startTime=%d&limit=%d", b.BaseURL, symbol, start, chunkMinutes)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		b.mu.Lock()
		b.unknown[symbol] = true
		b.mu.Unlock()
		return map[int64]float64{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("klines %s: http %d", symbol, resp.StatusCode)
	}
	var raw [][]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[int64]float64, len(raw))
	now := time.Now().UnixMilli()
	for _, r := range raw {
		if len(r) < 7 {
			continue
		}
		ot, _ := r[0].(float64)
		ct, _ := r[6].(float64)
		if int64(ct) > now { // only closed bars; the open of a live bar is fine but keep it simple
			continue
		}
		s, _ := r[1].(string)
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			out[int64(ot)] = v
		}
	}
	return out, nil
}
