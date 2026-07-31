package binance

import (
	"context"
	"fmt"
	"math"
	"nofx/logger"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

// GetPositions gets all positions (with cache)
func (t *FuturesTrader) GetPositions() ([]map[string]interface{}, error) {
	// First check if cache is valid
	t.positionsCacheMutex.RLock()
	if t.cachedPositions != nil && time.Since(t.positionsCacheTime) < t.cacheDuration {
		cacheAge := time.Since(t.positionsCacheTime)
		t.positionsCacheMutex.RUnlock()
		logger.Infof("✓ Using cached position information (cache age: %.1f seconds ago)", cacheAge.Seconds())
		return t.cachedPositions, nil
	}
	t.positionsCacheMutex.RUnlock()

	// AI cycles, risk checks, and order synchronization may all observe an
	// expired cache together. Coalesce the refresh and re-check after waiting so
	// one expiry produces one Binance position request per trader.
	t.positionsFetchMutex.Lock()
	defer t.positionsFetchMutex.Unlock()
	t.positionsCacheMutex.RLock()
	if t.cachedPositions != nil && time.Since(t.positionsCacheTime) < t.cacheDuration {
		cacheAge := time.Since(t.positionsCacheTime)
		t.positionsCacheMutex.RUnlock()
		logger.Infof("✓ Using cached position information after coalescing (cache age: %.1f seconds ago)", cacheAge.Seconds())
		return t.cachedPositions, nil
	}
	t.positionsCacheMutex.RUnlock()

	// Cache expired or doesn't exist, call API
	logger.Infof("🔄 Cache expired, calling Binance API to get position information...")
	positions, err := t.client.NewGetPositionRiskService().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		posAmt, _ := strconv.ParseFloat(pos.PositionAmt, 64)
		if posAmt == 0 {
			continue // Skip positions with zero amount
		}

		posMap := make(map[string]interface{})
		posMap["symbol"] = pos.Symbol
		posMap["positionAmt"], _ = strconv.ParseFloat(pos.PositionAmt, 64)
		posMap["entryPrice"], _ = strconv.ParseFloat(pos.EntryPrice, 64)
		posMap["markPrice"], _ = strconv.ParseFloat(pos.MarkPrice, 64)
		posMap["unRealizedProfit"], _ = strconv.ParseFloat(pos.UnRealizedProfit, 64)
		posMap["leverage"], _ = strconv.ParseFloat(pos.Leverage, 64)
		posMap["liquidationPrice"], _ = strconv.ParseFloat(pos.LiquidationPrice, 64)
		// Note: Binance SDK doesn't expose updateTime field, will fallback to local tracking

		// Determine direction
		if posAmt > 0 {
			posMap["side"] = "long"
		} else {
			posMap["side"] = "short"
		}

		result = append(result, posMap)
	}

	// Update cache
	t.positionsCacheMutex.Lock()
	t.cachedPositions = result
	t.positionsCacheTime = time.Now()
	t.positionsCacheMutex.Unlock()

	return result, nil
}

// SetMarginMode sets margin mode
func (t *FuturesTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	var marginType futures.MarginType
	if isCrossMargin {
		marginType = futures.MarginTypeCrossed
	} else {
		marginType = futures.MarginTypeIsolated
	}

	// Try to set margin mode
	err := t.client.NewChangeMarginTypeService().
		Symbol(symbol).
		MarginType(marginType).
		Do(context.Background())

	marginModeStr := "Cross Margin"
	if !isCrossMargin {
		marginModeStr = "Isolated Margin"
	}

	if err != nil {
		// If error message contains "No need to change", margin mode is already set to target value
		if contains(err.Error(), "No need to change margin type") {
			logger.Infof("  ✓ %s margin mode is already %s", symbol, marginModeStr)
			return nil
		}
		// If there is an open position, margin mode cannot be changed, but this doesn't affect trading
		if contains(err.Error(), "Margin type cannot be changed if there exists position") {
			logger.Infof("  ⚠️ %s has open positions, cannot change margin mode, continuing with current mode", symbol)
			return nil
		}
		// Detect Multi-Assets mode (error code -4168)
		if contains(err.Error(), "Multi-Assets mode") || contains(err.Error(), "-4168") || contains(err.Error(), "4168") {
			logger.Infof("  ⚠️ %s detected Multi-Assets mode, forcing Cross Margin mode", symbol)
			logger.Infof("  💡 Tip: To use Isolated Margin mode, please disable Multi-Assets mode in Binance")
			return nil
		}
		// Detect Unified Account API (Portfolio Margin)
		if contains(err.Error(), "unified") || contains(err.Error(), "portfolio") || contains(err.Error(), "Portfolio") {
			logger.Infof("  ❌ %s detected Unified Account API, unable to trade futures", symbol)
			return fmt.Errorf("please use 'Spot & Futures Trading' API permission, do not use 'Unified Account API'")
		}
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Don't return error, let trading continue
		return nil
	}

	logger.Infof("  ✓ %s margin mode set to %s", symbol, marginModeStr)
	return nil
}

// SetLeverage sets leverage (with smart detection and cooldown period)
func (t *FuturesTrader) SetLeverage(symbol string, leverage int) error {
	return t.setLeverage(symbol, leverage)
}

// SetLeverageForNotional sets leverage using the bracket that applies to the
// intended order notional. This prevents a small-position leverage tier from
// being reused for a larger order.
func (t *FuturesTrader) SetLeverageForNotional(symbol string, leverage int, notional float64) error {
	maxLeverage, err := t.GetMaxLeverageForNotional(symbol, notional)
	if err != nil {
		return fmt.Errorf("failed to get leverage bracket for %s: %w", symbol, err)
	}
	return t.setLeverageWithLimit(symbol, leverage, maxLeverage)
}

func (t *FuturesTrader) setLeverage(symbol string, leverage int) error {
	maxLeverage, err := t.GetMaxLeverage(symbol)
	if err != nil {
		return fmt.Errorf("failed to get leverage bracket for %s: %w", symbol, err)
	}
	return t.setLeverageWithLimit(symbol, leverage, maxLeverage)
}

func (t *FuturesTrader) setLeverageWithLimit(symbol string, leverage, maxLeverage int) error {
	if maxLeverage <= 0 {
		return fmt.Errorf("exchange returned invalid leverage limit %d for %s", maxLeverage, symbol)
	}
	if leverage > maxLeverage {
		logger.Infof("  ⚠️ %s requested leverage %dx exceeds exchange maximum %dx; reducing to %dx", symbol, leverage, maxLeverage, maxLeverage)
		leverage = maxLeverage
	}

	// First try to get current leverage (from position information)
	currentLeverage := 0
	positions, err := t.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == symbol {
				if lev, ok := pos["leverage"].(float64); ok {
					currentLeverage = int(lev)
					break
				}
			}
		}
	}

	// If current leverage is already the target leverage, skip
	if currentLeverage == leverage && currentLeverage > 0 {
		logger.Infof("  ✓ %s leverage is already %dx, no need to change", symbol, leverage)
		return nil
	}

	// Change leverage
	_, err = t.client.NewChangeLeverageService().
		Symbol(symbol).
		Leverage(leverage).
		Do(context.Background())

	if err != nil {
		// If error message contains "No need to change", leverage is already the target value
		if contains(err.Error(), "No need to change") {
			logger.Infof("  ✓ %s leverage is already %dx", symbol, leverage)
			return nil
		}
		return fmt.Errorf("failed to set leverage: %w", err)
	}

	logger.Infof("  ✓ %s leverage changed to %dx", symbol, leverage)

	// Wait 5 seconds after changing leverage (to avoid cooldown period errors)
	logger.Infof("  ⏱ Waiting 5 seconds for cooldown period...")
	time.Sleep(5 * time.Second)

	return nil
}

// GetMaxLeverage returns the highest initial leverage currently allowed by
// Binance's signed per-symbol leverage bracket endpoint. Callers that know the
// intended order notional should use GetMaxLeverageForNotional instead.
func (t *FuturesTrader) GetMaxLeverage(symbol string) (int, error) {
	brackets, err := t.getLeverageBrackets(symbol)
	if err != nil {
		return 0, err
	}
	maxLeverage := 0
	for _, bracket := range brackets {
		if bracket.InitialLeverage > maxLeverage {
			maxLeverage = bracket.InitialLeverage
		}
	}
	if maxLeverage <= 0 {
		return 0, fmt.Errorf("no valid leverage bracket returned for %s", symbol)
	}
	return maxLeverage, nil
}

// GetMaxLeverageForNotional selects the leverage tier whose notional floor and
// cap contain the intended order value. It fails closed when Binance returns
// no matching tier instead of falling back to the global maximum.
func (t *FuturesTrader) GetMaxLeverageForNotional(symbol string, notional float64) (int, error) {
	if math.IsNaN(notional) || math.IsInf(notional, 0) || notional < 0 {
		return 0, fmt.Errorf("notional must be a finite non-negative value")
	}
	brackets, err := t.getLeverageBrackets(symbol)
	if err != nil {
		return 0, err
	}
	if notional == 0 {
		return t.GetMaxLeverage(symbol)
	}

	matchedFloor := -1.0
	matchedLeverage := 0
	for _, bracket := range brackets {
		if bracket.InitialLeverage <= 0 || notional < bracket.NotionalFloor {
			continue
		}
		if bracket.NotionalCap > 0 && notional > bracket.NotionalCap {
			continue
		}
		if bracket.NotionalFloor >= matchedFloor {
			matchedFloor = bracket.NotionalFloor
			matchedLeverage = bracket.InitialLeverage
		}
	}
	if matchedLeverage <= 0 {
		return 0, fmt.Errorf("no leverage bracket covers notional %.4f for %s", notional, symbol)
	}
	return matchedLeverage, nil
}

func (t *FuturesTrader) getLeverageBrackets(symbol string) ([]futures.Bracket, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	t.maxLeverageMutex.RLock()
	entry, ok := t.maxLeverageCache[symbol]
	t.maxLeverageMutex.RUnlock()
	if ok && len(entry.brackets) > 0 && time.Now().Before(entry.expiresAt) {
		return append([]futures.Bracket(nil), entry.brackets...), nil
	}

	// Leverage bracket lookup is also shared by concurrent order paths. Avoid
	// sending duplicate signed requests when the one-minute cache expires.
	t.maxLeverageFetchMutex.Lock()
	defer t.maxLeverageFetchMutex.Unlock()
	t.maxLeverageMutex.RLock()
	entry, ok = t.maxLeverageCache[symbol]
	t.maxLeverageMutex.RUnlock()
	if ok && len(entry.brackets) > 0 && time.Now().Before(entry.expiresAt) {
		return append([]futures.Bracket(nil), entry.brackets...), nil
	}

	brackets, err := t.client.NewGetLeverageBracketService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("query Binance leverage bracket: %w", err)
	}
	var selected []futures.Bracket
	for _, item := range brackets {
		if item == nil || !strings.EqualFold(item.Symbol, symbol) {
			continue
		}
		selected = append(selected, item.Brackets...)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no valid leverage bracket returned for %s", symbol)
	}

	t.maxLeverageMutex.Lock()
	if t.maxLeverageCache == nil {
		t.maxLeverageCache = make(map[string]maxLeverageCacheEntry)
	}
	t.maxLeverageCache[symbol] = maxLeverageCacheEntry{
		brackets:  append([]futures.Bracket(nil), selected...),
		expiresAt: time.Now().Add(time.Minute),
	}
	t.maxLeverageMutex.Unlock()
	return selected, nil
}

// GetMarketPrice gets market price
func (t *FuturesTrader) GetMarketPrice(symbol string) (float64, error) {
	prices, err := t.client.NewListPricesService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get price: %w", err)
	}

	if len(prices) == 0 {
		return 0, fmt.Errorf("price not found")
	}

	price, err := strconv.ParseFloat(prices[0].Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

// CalculatePositionSize calculates position size
func (t *FuturesTrader) CalculatePositionSize(balance, riskPercent, price float64, leverage int) float64 {
	riskAmount := balance * (riskPercent / 100.0)
	positionValue := riskAmount * float64(leverage)
	quantity := positionValue / price
	return quantity
}

// GetMinNotional gets minimum notional value (Binance requirement)
func (t *FuturesTrader) GetMinNotional(symbol string) float64 {
	// Use conservative default value of 10 USDT to ensure order passes exchange validation
	return 10.0
}

// CheckMinNotional checks if order meets minimum notional value requirement
func (t *FuturesTrader) CheckMinNotional(symbol string, quantity float64) error {
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return fmt.Errorf("failed to get market price: %w", err)
	}
	return t.CheckMinNotionalAtPrice(symbol, quantity, price)
}

// CheckMinNotionalAtPrice validates an order against a caller-provided price,
// allowing order placement to reuse the same price for leverage-tier checks.
func (t *FuturesTrader) CheckMinNotionalAtPrice(symbol string, quantity, price float64) error {
	if quantity <= 0 || price <= 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) || math.IsNaN(price) || math.IsInf(price, 0) {
		return fmt.Errorf("quantity and price must be finite positive values")
	}

	notionalValue := quantity * price
	minNotional := t.GetMinNotional(symbol)

	if notionalValue < minNotional {
		return fmt.Errorf(
			"order amount %.2f USDT is below minimum requirement %.2f USDT (quantity: %.4f, price: %.4f)",
			notionalValue, minNotional, quantity, price,
		)
	}

	return nil
}

func (t *FuturesTrader) getExchangeInfo() (*futures.ExchangeInfo, error) {
	t.exchangeInfoMutex.RLock()
	if t.exchangeInfoCache != nil && time.Since(t.exchangeInfoFetchedAt) < 15*time.Minute {
		info := t.exchangeInfoCache
		t.exchangeInfoMutex.RUnlock()
		return info, nil
	}
	t.exchangeInfoMutex.RUnlock()

	// Precision lookups are called by several order paths. Coalesce the
	// relatively expensive exchangeInfo request and keep it for 15 minutes.
	t.exchangeInfoFetchMu.Lock()
	defer t.exchangeInfoFetchMu.Unlock()
	t.exchangeInfoMutex.RLock()
	if t.exchangeInfoCache != nil && time.Since(t.exchangeInfoFetchedAt) < 15*time.Minute {
		info := t.exchangeInfoCache
		t.exchangeInfoMutex.RUnlock()
		return info, nil
	}
	t.exchangeInfoMutex.RUnlock()

	info, err := t.client.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get trading rules: %w", err)
	}
	t.exchangeInfoMutex.Lock()
	t.exchangeInfoCache = info
	t.exchangeInfoFetchedAt = time.Now()
	t.exchangeInfoMutex.Unlock()
	return info, nil
}

// GetSymbolPrecision gets the quantity precision for a trading pair
func (t *FuturesTrader) GetSymbolPrecision(symbol string) (int, error) {
	exchangeInfo, err := t.getExchangeInfo()
	if err != nil {
		return 0, err
	}

	for _, s := range exchangeInfo.Symbols {
		if s.Symbol == symbol {
			// Get precision from LOT_SIZE filter
			for _, filter := range s.Filters {
				if filter["filterType"] == "LOT_SIZE" {
					stepSize := filter["stepSize"].(string)
					precision := calculatePrecision(stepSize)
					logger.Infof("  %s quantity precision: %d (stepSize: %s)", symbol, precision, stepSize)
					return precision, nil
				}
			}
		}
	}

	logger.Infof("  ⚠ %s precision information not found, using default precision 3", symbol)
	return 3, nil // Default precision is 3
}

// FormatQuantity formats quantity to correct precision
func (t *FuturesTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	precision, err := t.GetSymbolPrecision(symbol)
	if err != nil {
		// If retrieval fails, use default format
		return fmt.Sprintf("%.3f", quantity), nil
	}

	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, quantity), nil
}

// GetSymbolPricePrecision gets the price precision for a trading pair
func (t *FuturesTrader) GetSymbolPricePrecision(symbol string) (int, error) {
	exchangeInfo, err := t.getExchangeInfo()
	if err != nil {
		return 0, err
	}

	for _, s := range exchangeInfo.Symbols {
		if s.Symbol == symbol {
			// Get precision from PRICE_FILTER filter
			for _, filter := range s.Filters {
				if filter["filterType"] == "PRICE_FILTER" {
					tickSize := filter["tickSize"].(string)
					precision := calculatePrecision(tickSize)
					return precision, nil
				}
			}
		}
	}

	// Default to 2 decimal places for price
	return 2, nil
}

// FormatPrice formats price to correct precision
func (t *FuturesTrader) FormatPrice(symbol string, price float64) (string, error) {
	precision, err := t.GetSymbolPricePrecision(symbol)
	if err != nil {
		// If retrieval fails, use default format
		return fmt.Sprintf("%.2f", price), nil
	}

	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, price), nil
}
