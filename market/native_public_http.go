package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativePublicHTTPClient struct {
	client  *http.Client
	baseURL string
	cache   *nativePublicResponseCache
}

type nativePublicResponseCache struct {
	mu      sync.Mutex
	entries map[string]nativePublicCacheEntry
}

type nativePublicCacheEntry struct {
	body      []byte
	fetchedAt time.Time
}

const nativePublicResponseCacheTTL = time.Second

func newNativePublicHTTPClient(baseURL string, client *http.Client) nativePublicHTTPClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return nativePublicHTTPClient{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		cache:   &nativePublicResponseCache{entries: make(map[string]nativePublicCacheEntry)},
	}
}

func (client nativePublicHTTPClient) get(path string, query url.Values) ([]byte, error) {
	return client.getWithCache(path, query, true)
}

func (client nativePublicHTTPClient) getFresh(path string, query url.Values) ([]byte, error) {
	return client.getWithCache(path, query, false)
}

func (client nativePublicHTTPClient) getWithCache(path string, query url.Values, allowCache bool) ([]byte, error) {
	cacheKey := path
	if encodedQuery := query.Encode(); encodedQuery != "" {
		cacheKey += "?" + encodedQuery
	}
	if allowCache && client.cache != nil {
		client.cache.mu.Lock()
		entry, found := client.cache.entries[cacheKey]
		if found && time.Since(entry.fetchedAt) < nativePublicResponseCacheTTL {
			body := append([]byte(nil), entry.body...)
			client.cache.mu.Unlock()
			return body, nil
		}
		client.cache.mu.Unlock()
	}

	requestURL := client.baseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("public market request %s: %w", path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read public market response %s: %w", path, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 300 {
			message = message[:300] + "..."
		}
		return nil, fmt.Errorf("public market request %s failed with HTTP %d: %s", path, response.StatusCode, message)
	}
	if allowCache && client.cache != nil {
		client.cache.mu.Lock()
		client.cache.entries[cacheKey] = nativePublicCacheEntry{
			body:      append([]byte(nil), body...),
			fetchedAt: time.Now(),
		}
		client.cache.mu.Unlock()
	}
	return body, nil
}

func validateFreshPublicKlines(exchange, symbol, interval string, klines []Kline, intervalDuration time.Duration) error {
	if len(klines) == 0 {
		return fmt.Errorf("%s returned no K-lines for %s", exchange, symbol)
	}
	if intervalDuration <= 0 {
		return fmt.Errorf("%s has no freshness duration for interval %s", exchange, interval)
	}
	latestOpenTime := klines[len(klines)-1].OpenTime
	if latestOpenTime <= 0 {
		return fmt.Errorf("%s returned an invalid latest K-line timestamp for %s", exchange, symbol)
	}
	latestAge := time.Since(time.UnixMilli(latestOpenTime))
	if latestAge < -intervalDuration {
		return fmt.Errorf("%s returned a future K-line for %s", exchange, symbol)
	}
	maxAge := 2*intervalDuration + 30*time.Second
	if latestAge > maxAge {
		return fmt.Errorf("%s latest %s K-line for %s is stale by %s", exchange, interval, symbol, latestAge.Round(time.Second))
	}
	return nil
}

func decodePublicEnvelope(body []byte, exchange string, target any) error {
	var envelope struct {
		Code json.RawMessage `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode %s public response: %w", exchange, err)
	}

	var code string
	if len(envelope.Code) > 0 {
		if err := json.Unmarshal(envelope.Code, &code); err != nil {
			var numericCode int
			if numericErr := json.Unmarshal(envelope.Code, &numericCode); numericErr == nil {
				code = fmt.Sprintf("%d", numericCode)
			}
		}
	}
	if code != "" && code != "0" && code != "00000" {
		return fmt.Errorf("%s public API error: code=%s, msg=%s", exchange, code, strings.TrimSpace(envelope.Msg))
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("%s public API returned empty data", exchange)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("decode %s public data: %w", exchange, err)
	}
	return nil
}

func parsePublicFloat(raw string, fieldName string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", fieldName, raw, err)
	}
	return value, nil
}

func parseRawFloat(raw json.RawMessage, fieldName string) (float64, error) {
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return parsePublicFloat(stringValue, fieldName)
	}
	var numericValue float64
	if err := json.Unmarshal(raw, &numericValue); err != nil {
		return 0, fmt.Errorf("parse %s: %w", fieldName, err)
	}
	return numericValue, nil
}

func parseRawInt64(raw json.RawMessage, fieldName string) (int64, error) {
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		value, parseErr := strconv.ParseInt(strings.TrimSpace(stringValue), 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("parse %s %q: %w", fieldName, stringValue, parseErr)
		}
		return value, nil
	}
	var numericValue int64
	if err := json.Unmarshal(raw, &numericValue); err != nil {
		return 0, fmt.Errorf("parse %s: %w", fieldName, err)
	}
	return numericValue, nil
}

func parseOptionalInt64(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func normalizeDepthLevels(levels [][]string) [][]string {
	normalized := make([][]string, 0, len(levels))
	for _, level := range levels {
		if len(level) < 2 {
			continue
		}
		normalized = append(normalized, []string{level[0], level[1]})
	}
	return normalized
}
