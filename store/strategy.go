package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Hard limits to prevent token explosion in AI requests
const (
	MaxCandidateCoins       = 10
	MaxStaticCandidateCoins = 20
	MaxWatchlistAssets      = 50
	MaxPositions            = 20
	MaxTimeframes           = 4
	MinKlineCount           = 10
	MaxKlineCount           = 50
	MinLeverage             = 1
	MaxBTCETHLeverage       = 125
	MaxAltLeverage          = 125
	MinPositionRatio        = 0.5
	MaxPositionRatio        = 10.0
	MinPositionMarginRatio  = 0.01
	MaxPositionMarginRatio  = 1.0
	MinRiskReward           = 1.0
	MaxRiskReward           = 10.0
	MinMarginUsage          = 0.1
	MaxMarginUsage          = 1.0
	MinPositionSize         = 10.0
	MaxPositionSize         = 1000.0
	MinConfidence           = 1
	MaxConfidence           = 100
)

// ClampLimits enforces product-level limits on strategy config to prevent token overflow.
func (c *StrategyConfig) ClampLimits() {
	c.NormalizeProductSchema()

	// Clamp coin source limits
	if c.CoinSource.AI500Limit > MaxCandidateCoins {
		c.CoinSource.AI500Limit = MaxCandidateCoins
	}
	if c.CoinSource.OITopLimit > MaxCandidateCoins {
		c.CoinSource.OITopLimit = MaxCandidateCoins
	}
	if c.CoinSource.OILowLimit > MaxCandidateCoins {
		c.CoinSource.OILowLimit = MaxCandidateCoins
	}
	if c.CoinSource.VergexLimit > MaxCandidateCoins {
		c.CoinSource.VergexLimit = MaxCandidateCoins
	}
	if c.CoinSource.BinanceDynamicLimit > MaxCandidateCoins {
		c.CoinSource.BinanceDynamicLimit = MaxCandidateCoins
	}

	// Clamp static coins
	if len(c.CoinSource.StaticCoins) > MaxStaticCandidateCoins {
		c.CoinSource.StaticCoins = c.CoinSource.StaticCoins[:MaxStaticCandidateCoins]
	}
	if len(c.CoinSource.Watchlist) > MaxWatchlistAssets {
		c.CoinSource.Watchlist = c.CoinSource.Watchlist[:MaxWatchlistAssets]
	}

	// Clamp kline count
	if c.Indicators.Klines.PrimaryCount < MinKlineCount {
		c.Indicators.Klines.PrimaryCount = MinKlineCount
	}
	if c.Indicators.Klines.PrimaryCount > MaxKlineCount {
		c.Indicators.Klines.PrimaryCount = MaxKlineCount
	}
	if c.Indicators.Klines.LongerCount > MaxKlineCount {
		c.Indicators.Klines.LongerCount = MaxKlineCount
	}

	// Clamp timeframes
	if len(c.Indicators.Klines.SelectedTimeframes) > MaxTimeframes {
		c.Indicators.Klines.SelectedTimeframes = c.Indicators.Klines.SelectedTimeframes[:MaxTimeframes]
	}

	// Clamp max positions
	if c.RiskControl.MaxPositions < 1 {
		c.RiskControl.MaxPositions = 1
	}
	if c.RiskControl.MaxPositions > MaxPositions {
		c.RiskControl.MaxPositions = MaxPositions
	}

	// Clamp leverage limits to the same bounds as the manual config UI.
	if c.RiskControl.BTCETHMaxLeverage < MinLeverage {
		c.RiskControl.BTCETHMaxLeverage = MinLeverage
	}
	if c.RiskControl.BTCETHMaxLeverage > MaxBTCETHLeverage {
		c.RiskControl.BTCETHMaxLeverage = MaxBTCETHLeverage
	}
	if c.RiskControl.AltcoinMaxLeverage < MinLeverage {
		c.RiskControl.AltcoinMaxLeverage = MinLeverage
	}
	if c.RiskControl.AltcoinMaxLeverage > MaxAltLeverage {
		c.RiskControl.AltcoinMaxLeverage = MaxAltLeverage
	}

	// Clamp position value ratio limits.
	if c.RiskControl.BTCETHMaxPositionValueRatio < MinPositionRatio {
		c.RiskControl.BTCETHMaxPositionValueRatio = MinPositionRatio
	}
	if c.RiskControl.BTCETHMaxPositionValueRatio > MaxPositionRatio {
		c.RiskControl.BTCETHMaxPositionValueRatio = MaxPositionRatio
	}
	if c.RiskControl.AltcoinMaxPositionValueRatio < MinPositionRatio {
		c.RiskControl.AltcoinMaxPositionValueRatio = MinPositionRatio
	}
	if c.RiskControl.AltcoinMaxPositionValueRatio > MaxPositionRatio {
		c.RiskControl.AltcoinMaxPositionValueRatio = MaxPositionRatio
	}
	if c.RiskControl.PositionSizingMode == "margin_based" {
		if c.RiskControl.BTCETHMaxMarginRatio < MinPositionMarginRatio {
			c.RiskControl.BTCETHMaxMarginRatio = MinPositionMarginRatio
		}
		if c.RiskControl.BTCETHMaxMarginRatio > MaxPositionMarginRatio {
			c.RiskControl.BTCETHMaxMarginRatio = MaxPositionMarginRatio
		}
		if c.RiskControl.AltcoinMaxMarginRatio < MinPositionMarginRatio {
			c.RiskControl.AltcoinMaxMarginRatio = MinPositionMarginRatio
		}
		if c.RiskControl.AltcoinMaxMarginRatio > MaxPositionMarginRatio {
			c.RiskControl.AltcoinMaxMarginRatio = MaxPositionMarginRatio
		}
	}

	// Clamp risk parameters and entry requirements.
	if c.RiskControl.MinRiskRewardRatio < MinRiskReward {
		c.RiskControl.MinRiskRewardRatio = MinRiskReward
	}
	if c.RiskControl.MinRiskRewardRatio > MaxRiskReward {
		c.RiskControl.MinRiskRewardRatio = MaxRiskReward
	}
	if c.RiskControl.MaxMarginUsage < MinMarginUsage {
		c.RiskControl.MaxMarginUsage = MinMarginUsage
	}
	if c.RiskControl.MaxMarginUsage > MaxMarginUsage {
		c.RiskControl.MaxMarginUsage = MaxMarginUsage
	}
	if c.RiskControl.MinPositionSize < MinPositionSize {
		c.RiskControl.MinPositionSize = MinPositionSize
	}
	if c.RiskControl.MinPositionSize > MaxPositionSize {
		c.RiskControl.MinPositionSize = MaxPositionSize
	}
	if c.RiskControl.MinConfidence < MinConfidence {
		c.RiskControl.MinConfidence = MinConfidence
	}
	if c.RiskControl.MinConfidence > MaxConfidence {
		c.RiskControl.MinConfidence = MaxConfidence
	}
	if c.RiskControl.TradeThrottle != nil {
		c.RiskControl.TradeThrottle.ClampLimits()
	}
}

// NormalizeProductSchema keeps saved strategy JSON aligned with the product
// editor schema. LLMs may emit user-facing labels such as "AI500"; persistence
// must use the exact frontend/backend enum values.
func (c *StrategyConfig) NormalizeProductSchema() {
	if c.RiskControl.PositionSizingMode != "margin_based" {
		c.RiskControl.PositionSizingMode = "notional_based"
	}
	c.StrategyType = normalizeStrategyType(c.StrategyType)
	c.CoinSource.StaticCoins = normalizeSymbols(c.CoinSource.StaticCoins)
	c.CoinSource.Watchlist = normalizeSymbols(c.CoinSource.Watchlist)
	c.CoinSource.ExcludedCoins = normalizeSymbols(c.CoinSource.ExcludedCoins)
	if c.CoinSource.UseWatchlist {
		c.CoinSource.SourceType = "static"
		if len(c.CoinSource.StaticCoins) == 0 {
			c.CoinSource.StaticCoins = binanceWatchlistCandidates(c.CoinSource.Watchlist)
		}
	}
	c.CoinSource.SourceType = normalizeCoinSourceType(c.CoinSource.SourceType)
	if c.CoinSource.SourceType == "" {
		c.CoinSource.SourceType = inferCoinSourceType(c.CoinSource)
	}
	if c.CoinSource.SourceType == "static" {
		c.CoinSource.StaticCoins = binanceUSDTContracts(c.CoinSource.StaticCoins)
		if len(c.CoinSource.StaticCoins) == 0 {
			c.CoinSource.SourceType = "binance_dynamic"
		}
	}

	switch c.CoinSource.SourceType {
	case "binance_dynamic":
		c.CoinSource.UseWatchlist = false
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		c.CoinSource.HyperMainLimit = 0
		c.CoinSource.HyperRankCategory = ""
		c.CoinSource.HyperRankDirection = ""
		c.CoinSource.HyperRankLimit = 0
		c.CoinSource.VergexLimit = 0
		c.CoinSource.VergexMarketType = ""
		c.CoinSource.VergexChain = ""
		c.CoinSource.VergexLiqBand = ""
		if c.CoinSource.BinanceDynamicLimit <= 0 {
			c.CoinSource.BinanceDynamicLimit = MaxCandidateCoins
		}
	case "ai500":
		c.CoinSource.UseAI500 = true
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		if c.CoinSource.AI500Limit <= 0 {
			c.CoinSource.AI500Limit = 3
		}
	case "oi_top":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = true
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		if c.CoinSource.OITopLimit <= 0 {
			c.CoinSource.OITopLimit = 3
		}
	case "oi_low":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = true
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		if c.CoinSource.OILowLimit <= 0 {
			c.CoinSource.OILowLimit = 3
		}
	case "static":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
	case "hyper_all":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = true
		c.CoinSource.UseHyperMain = false
	case "hyper_main":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = true
		if c.CoinSource.HyperMainLimit <= 0 {
			c.CoinSource.HyperMainLimit = 30
		}
	case "hyper_rank":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		if c.CoinSource.HyperRankCategory == "" {
			c.CoinSource.HyperRankCategory = "stock"
		}
		if c.CoinSource.HyperRankDirection == "" {
			c.CoinSource.HyperRankDirection = "gainers"
		}
		if c.CoinSource.HyperRankLimit <= 0 {
			c.CoinSource.HyperRankLimit = 5
		}
	case "vergex_signal":
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		minLimit := 10
		if len(c.CoinSource.StaticCoins) > 0 {
			minLimit = len(c.CoinSource.StaticCoins)
			if minLimit > MaxCandidateCoins {
				minLimit = MaxCandidateCoins
			}
		}
		if c.CoinSource.VergexLimit < minLimit {
			c.CoinSource.VergexLimit = minLimit
		}
		if c.CoinSource.VergexMarketType == "" {
			c.CoinSource.VergexMarketType = "all"
		}
		if c.CoinSource.VergexChain == "" {
			c.CoinSource.VergexChain = "hyperliquid"
		}
	default:
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
	}

	c.Indicators.Klines.PrimaryTimeframe = normalizeTimeframe(c.Indicators.Klines.PrimaryTimeframe)
	c.Indicators.Klines.LongerTimeframe = normalizeTimeframe(c.Indicators.Klines.LongerTimeframe)
	c.Indicators.Klines.SelectedTimeframes = normalizeTimeframes(c.Indicators.Klines.SelectedTimeframes)
	if len(c.Indicators.Klines.SelectedTimeframes) > 0 {
		c.Indicators.Klines.EnableMultiTimeframe = true
	}
}

func normalizeStrategyType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "grid", "grid_strategy", "grid-trading", "grid trading", "grid_trading", "grid strategy":
		return "grid_trading"
	case "", "ai", "ai_strategy", "ai-trading", "ai trading", "ai_trading", "ai strategy", "ai smart strategy":
		return "ai_trading"
	default:
		return value
	}
}

func normalizeCoinSourceType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	compact := strings.NewReplacer(" ", "", "_", "", "-", "", "datasource", "", "coinselection", "", "coin", "").Replace(value)
	switch {
	case compact == "":
		return ""
	case strings.Contains(compact, "ai500"):
		return "ai500"
	case strings.Contains(compact, "oitop") || strings.Contains(value, "oi top") || strings.Contains(value, "highest open interest") || strings.Contains(value, "top open interest"):
		return "oi_top"
	case strings.Contains(compact, "oilow") || strings.Contains(value, "oi low") || strings.Contains(value, "lowest open interest") || strings.Contains(value, "low open interest"):
		return "oi_low"
	case strings.Contains(compact, "hyperrank"):
		return "binance_dynamic"
	case strings.Contains(compact, "binance") && (strings.Contains(compact, "dynamic") || strings.Contains(compact, "local")):
		return "binance_dynamic"
	case strings.Contains(compact, "localdynamic") || strings.Contains(value, "local dynamic"):
		return "binance_dynamic"
	case strings.Contains(compact, "vergex") || strings.Contains(compact, "claw402") || strings.Contains(compact, "dynamicranking") || strings.Contains(value, "dynamic board") || strings.Contains(value, "gainers board") || strings.Contains(value, "signal board"):
		return "binance_dynamic"
	case strings.Contains(compact, "hyperall"):
		return "binance_dynamic"
	case strings.Contains(compact, "hypermain"):
		return "binance_dynamic"
	case strings.Contains(value, "static") || strings.Contains(value, "fixed"):
		return "static"
	default:
		return value
	}
}

func inferCoinSourceType(source CoinSourceConfig) string {
	switch {
	case len(source.StaticCoins) > 0:
		return "static"
	case source.UseAI500:
		return "ai500"
	case source.UseOITop:
		return "oi_top"
	case source.UseOILow:
		return "oi_low"
	case source.UseHyperAll:
		return "binance_dynamic"
	case source.UseHyperMain:
		return "binance_dynamic"
	case source.VergexLimit > 0 || source.VergexMarketType != "" || source.VergexChain != "" || source.VergexLiqBand != "":
		return "binance_dynamic"
	case source.HyperRankCategory != "" || source.HyperRankDirection != "" || source.HyperRankLimit > 0:
		return "binance_dynamic"
	default:
		return "binance_dynamic"
	}
}

func normalizeSymbols(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range splitLooseStringList(values) {
		value = strings.ToUpper(strings.TrimSpace(value))
		value = strings.Trim(value, "，,;； ")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func binanceWatchlistCandidates(values []string) []string {
	values = normalizeSymbols(values)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "XYZ:") || strings.HasSuffix(value, "-USDC") || strings.ContainsAny(value, ":-_/ ") {
			continue
		}
		if !strings.HasSuffix(value, "USDT") {
			value += "USDT"
		}
		if isBinanceUSDTContract(value) {
			out = append(out, value)
		}
	}
	return out
}

func binanceUSDTContracts(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if isBinanceUSDTContract(value) {
			out = append(out, strings.ToUpper(strings.TrimSpace(value)))
		}
	}
	return out
}

func isBinanceUSDTContract(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !strings.HasSuffix(value, "USDT") || strings.HasPrefix(value, "XYZ:") || strings.ContainsAny(value, ":-_/ ") {
		return false
	}
	base := strings.TrimSuffix(value, "USDT")
	if base == "" {
		return false
	}
	for _, r := range base {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func normalizeTimeframes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range splitLooseStringList(values) {
		tf := normalizeTimeframe(value)
		if tf == "" || seen[tf] {
			continue
		}
		seen[tf] = true
		out = append(out, tf)
	}
	return out
}

func splitLooseStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	joined := strings.TrimSpace(strings.Join(values, ","))
	if strings.HasPrefix(joined, "[") && strings.HasSuffix(joined, "]") {
		var parsed []string
		if err := json.Unmarshal([]byte(joined), &parsed); err == nil {
			return parsed
		}
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			var parsed []string
			if err := json.Unmarshal([]byte(value), &parsed); err == nil {
				parts = append(parts, parsed...)
				continue
			}
		}
		value = strings.Trim(value, "[]")
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '，' || r == ';' || r == '；' || r == '\n'
		}) {
			part = strings.Trim(strings.TrimSpace(part), "\"'")
			if part != "" {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func normalizeTimeframe(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, "\"', . ")
	if value == "" {
		return ""
	}
	aliases := map[string]string{
		"1 minute":  "1m",
		"3 minute":  "3m",
		"5 minute":  "5m",
		"15 minute": "15m",
		"30 minute": "30m",
		"1 hour":    "1h",
		"2 hour":    "2h",
		"4 hour":    "4h",
		"6 hour":    "6h",
		"8 hour":    "8h",
		"12 hour":   "12h",
		"1 day":     "1d",
		"3 day":     "3d",
		"1 week":    "1w",
	}
	if alias, ok := aliases[value]; ok {
		return alias
	}
	allowed := map[string]bool{
		"1m": true, "3m": true, "5m": true, "15m": true, "30m": true,
		"1h": true, "2h": true, "4h": true, "6h": true, "8h": true, "12h": true,
		"1d": true, "3d": true, "1w": true,
	}
	if !allowed[value] {
		return ""
	}
	return value
}

// MergeStrategyConfig applies a partial JSON-style patch onto a full strategy config.
// Nested objects are merged recursively so omitted fields keep their previous values.
func MergeStrategyConfig(base StrategyConfig, patch map[string]any) (StrategyConfig, error) {
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return StrategyConfig{}, err
	}

	var mergedMap map[string]any
	if err := json.Unmarshal(baseJSON, &mergedMap); err != nil {
		return StrategyConfig{}, err
	}

	normalizeStrategyConfigPatch(patch)
	if fmt.Sprint(patch["strategy_type"]) == "grid_trading" {
		ensureDefaultGridConfigMap(mergedMap)
	}
	mergeJSONMaps(mergedMap, patch)

	mergedJSON, err := json.Marshal(mergedMap)
	if err != nil {
		return StrategyConfig{}, err
	}

	var merged StrategyConfig
	if err := json.Unmarshal(mergedJSON, &merged); err != nil {
		return StrategyConfig{}, err
	}
	return merged, nil
}

func DefaultGridStrategyConfig() GridStrategyConfig {
	return GridStrategyConfig{
		Symbol:                "BTCUSDT",
		GridCount:             10,
		TotalInvestment:       1000,
		Leverage:              5,
		UpperPrice:            0,
		LowerPrice:            0,
		UseATRBounds:          true,
		ATRMultiplier:         2.0,
		Distribution:          "gaussian",
		MaxDrawdownPct:        15,
		StopLossPct:           5,
		DailyLossLimitPct:     10,
		UseMakerOnly:          true,
		EnableDirectionAdjust: false,
		DirectionBiasRatio:    0.7,
	}
}

func ensureDefaultGridConfigMap(config map[string]any) {
	if config == nil {
		return
	}
	if existing, ok := config["grid_config"].(map[string]any); ok && len(existing) > 0 {
		return
	}
	defaultGrid := DefaultGridStrategyConfig()
	raw, err := json.Marshal(defaultGrid)
	if err != nil {
		return
	}
	var gridMap map[string]any
	if err := json.Unmarshal(raw, &gridMap); err != nil {
		return
	}
	config["grid_config"] = gridMap
}

func normalizeStrategyConfigPatch(patch map[string]any) {
	if patch == nil {
		return
	}

	if gridConfig, hasGrid := patch["grid_config"]; hasGrid && gridConfig != nil {
		if _, hasType := patch["strategy_type"]; !hasType {
			patch["strategy_type"] = "grid_trading"
		}
	}

	aiKeys := []string{"coin_source", "indicators", "risk_control", "prompt_sections", "custom_prompt"}
	for _, key := range aiKeys {
		value, ok := patch[key]
		if !ok {
			continue
		}
		aiConfig, _ := patch["ai_config"].(map[string]any)
		if aiConfig == nil {
			aiConfig = map[string]any{}
			patch["ai_config"] = aiConfig
		}
		aiConfig[key] = value
		delete(patch, key)
	}

	if fmt.Sprint(patch["strategy_type"]) == "grid_trading" {
		delete(patch, "ai_config")
	}

	if _, hasType := patch["strategy_type"]; hasType {
		return
	}
	if gridConfig, hasGrid := patch["grid_config"]; hasGrid && gridConfig != nil {
		patch["strategy_type"] = "grid_trading"
	}
}

func mergeJSONMaps(dst, src map[string]any) {
	for key, srcVal := range src {
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dst[key].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeJSONMaps(dstMap, srcMap)
			continue
		}
		dst[key] = srcVal
	}
}

func StrategyClampWarnings(before, after StrategyConfig, lang string) []string {
	if lang != "zh" {
		lang = "en"
	}
	warnings := make([]string, 0, 8)
	appendInt := func(labelZH, labelEN string, from, to int) {
		if from == to {
			return
		}
		if lang == "zh" {
			warnings = append(warnings, fmt.Sprintf("%s adjusted from %d to %d", labelZH, from, to))
			return
		}
		warnings = append(warnings, fmt.Sprintf("%s adjusted from %d to %d", labelEN, from, to))
	}
	appendFloat := func(labelZH, labelEN string, from, to float64) {
		if from == to {
			return
		}
		if lang == "zh" {
			warnings = append(warnings, fmt.Sprintf("%s adjusted from %.2f to %.2f", labelZH, from, to))
			return
		}
		warnings = append(warnings, fmt.Sprintf("%s adjusted from %.2f to %.2f", labelEN, from, to))
	}

	appendInt("Max Positions", "max_positions", before.RiskControl.MaxPositions, after.RiskControl.MaxPositions)
	appendInt("BTC/ETH Max Leverage", "btc_eth_max_leverage", before.RiskControl.BTCETHMaxLeverage, after.RiskControl.BTCETHMaxLeverage)
	appendInt("Altcoin Max Leverage", "altcoin_max_leverage", before.RiskControl.AltcoinMaxLeverage, after.RiskControl.AltcoinMaxLeverage)
	appendFloat("BTC/ETH Max Position Value Ratio", "btc_eth_max_position_value_ratio", before.RiskControl.BTCETHMaxPositionValueRatio, after.RiskControl.BTCETHMaxPositionValueRatio)
	appendFloat("Altcoin Max Position Value Ratio", "altcoin_max_position_value_ratio", before.RiskControl.AltcoinMaxPositionValueRatio, after.RiskControl.AltcoinMaxPositionValueRatio)
	appendFloat("Min Risk/Reward Ratio", "min_risk_reward_ratio", before.RiskControl.MinRiskRewardRatio, after.RiskControl.MinRiskRewardRatio)
	appendFloat("Max Margin Usage", "max_margin_usage", before.RiskControl.MaxMarginUsage, after.RiskControl.MaxMarginUsage)
	appendFloat("Min Position Size", "min_position_size", before.RiskControl.MinPositionSize, after.RiskControl.MinPositionSize)
	appendInt("Min Confidence", "min_confidence", before.RiskControl.MinConfidence, after.RiskControl.MinConfidence)
	return warnings
}

// StrategyStore strategy storage
type StrategyStore struct {
	db *gorm.DB
}

// Strategy strategy configuration
type Strategy struct {
	ID            string    `gorm:"primaryKey" json:"id"`
	UserID        string    `gorm:"column:user_id;not null;default:'';index" json:"user_id"`
	Name          string    `gorm:"not null" json:"name"`
	Description   string    `gorm:"default:''" json:"description"`
	IsActive      bool      `gorm:"column:is_active;default:false;index" json:"is_active"`
	IsDefault     bool      `gorm:"column:is_default;default:false" json:"is_default"`
	IsPublic      bool      `gorm:"column:is_public;default:false;index" json:"is_public"`    // whether visible in strategy market
	ConfigVisible bool      `gorm:"column:config_visible;default:true" json:"config_visible"` // whether config details are visible
	Config        string    `gorm:"not null;default:'{}'" json:"config"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Strategy) TableName() string { return "strategies" }

// StrategyConfig strategy configuration details (JSON structure)
type StrategyConfig struct {
	// Strategy type: "ai_trading" (default) or "grid_trading"
	StrategyType string `json:"strategy_type,omitempty"`

	// language setting: "zh" for Chinese, "en" for English
	// This determines the language used for data formatting and prompt generation
	Language string `json:"language,omitempty"`
	// AI trading configuration fields are kept on the Go struct for engine
	// compatibility, but JSON persistence nests them under ai_config.
	CoinSource     CoinSourceConfig     `json:"-"`
	Indicators     IndicatorConfig      `json:"-"`
	CustomPrompt   string               `json:"-"`
	RiskControl    RiskControlConfig    `json:"-"`
	PromptSections PromptSectionsConfig `json:"-"`

	// Grid trading configuration (only used when StrategyType == "grid_trading")
	GridConfig *GridStrategyConfig `json:"grid_config,omitempty"`

	// Publish settings are shared by AI and grid strategies. The database still
	// stores the authoritative booleans on Strategy, but config JSON may carry
	// this object for agent/frontend schema consistency.
	PublishConfig *PublishStrategyConfig `json:"publish_config,omitempty"`
}

// AIStrategyConfig contains fields only used by AI trading strategies.
type AIStrategyConfig struct {
	CoinSource     CoinSourceConfig     `json:"coin_source"`
	Indicators     IndicatorConfig      `json:"indicators"`
	CustomPrompt   string               `json:"custom_prompt,omitempty"`
	RiskControl    RiskControlConfig    `json:"risk_control"`
	PromptSections PromptSectionsConfig `json:"prompt_sections,omitempty"`
}

// PublishStrategyConfig contains settings shared by all strategy types.
type PublishStrategyConfig struct {
	IsPublic      bool `json:"is_public"`
	ConfigVisible bool `json:"config_visible"`
}

// MarshalJSON writes the product-facing strategy schema:
// strategy_type + grid_config or ai_config + shared publish_config.
func (c StrategyConfig) MarshalJSON() ([]byte, error) {
	strategyType := strings.TrimSpace(c.StrategyType)
	if strategyType == "" {
		strategyType = "ai_trading"
	}

	out := struct {
		StrategyType  string                 `json:"strategy_type"`
		Language      string                 `json:"language,omitempty"`
		AIConfig      *AIStrategyConfig      `json:"ai_config,omitempty"`
		GridConfig    *GridStrategyConfig    `json:"grid_config,omitempty"`
		PublishConfig *PublishStrategyConfig `json:"publish_config,omitempty"`
	}{
		StrategyType:  strategyType,
		Language:      c.Language,
		PublishConfig: c.PublishConfig,
	}

	if strategyType == "grid_trading" {
		out.GridConfig = c.GridConfig
	} else {
		out.AIConfig = &AIStrategyConfig{
			CoinSource:     c.CoinSource,
			Indicators:     c.Indicators,
			CustomPrompt:   c.CustomPrompt,
			RiskControl:    c.RiskControl,
			PromptSections: c.PromptSections,
		}
	}

	return json.Marshal(out)
}

// UnmarshalJSON accepts both the new nested schema and old flat configs. Old
// top-level AI fields are normalized into the Go compatibility fields.
func (c *StrategyConfig) UnmarshalJSON(data []byte) error {
	type rawStrategyConfig struct {
		StrategyType  string                 `json:"strategy_type"`
		Language      string                 `json:"language"`
		AIConfig      *AIStrategyConfig      `json:"ai_config"`
		GridConfig    *GridStrategyConfig    `json:"grid_config"`
		PublishConfig *PublishStrategyConfig `json:"publish_config"`

		CoinSource     *CoinSourceConfig     `json:"coin_source"`
		Indicators     *IndicatorConfig      `json:"indicators"`
		CustomPrompt   *string               `json:"custom_prompt"`
		RiskControl    *RiskControlConfig    `json:"risk_control"`
		PromptSections *PromptSectionsConfig `json:"prompt_sections"`
	}

	var raw rawStrategyConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	c.StrategyType = raw.StrategyType
	c.Language = raw.Language
	c.GridConfig = raw.GridConfig
	c.PublishConfig = raw.PublishConfig

	if raw.AIConfig != nil {
		c.CoinSource = raw.AIConfig.CoinSource
		c.Indicators = raw.AIConfig.Indicators
		c.CustomPrompt = raw.AIConfig.CustomPrompt
		c.RiskControl = raw.AIConfig.RiskControl
		c.PromptSections = raw.AIConfig.PromptSections
	} else {
		if raw.CoinSource != nil {
			c.CoinSource = *raw.CoinSource
		}
		if raw.Indicators != nil {
			c.Indicators = *raw.Indicators
		}
		if raw.CustomPrompt != nil {
			c.CustomPrompt = *raw.CustomPrompt
		}
		if raw.RiskControl != nil {
			c.RiskControl = *raw.RiskControl
		}
		if raw.PromptSections != nil {
			c.PromptSections = *raw.PromptSections
		}
	}

	if strings.TrimSpace(c.StrategyType) == "" && c.GridConfig != nil {
		c.StrategyType = "grid_trading"
	}
	return nil
}

// GridStrategyConfig grid trading specific configuration
type GridStrategyConfig struct {
	// Trading pair (e.g., "BTCUSDT")
	Symbol string `json:"symbol"`
	// Number of grid levels (5-50)
	GridCount int `json:"grid_count"`
	// Total investment in USDT
	TotalInvestment float64 `json:"total_investment"`
	// Leverage (1-20)
	Leverage int `json:"leverage"`
	// Upper price boundary (0 = auto-calculate from ATR)
	UpperPrice float64 `json:"upper_price"`
	// Lower price boundary (0 = auto-calculate from ATR)
	LowerPrice float64 `json:"lower_price"`
	// Use ATR to auto-calculate bounds
	UseATRBounds bool `json:"use_atr_bounds"`
	// ATR multiplier for bound calculation (default 2.0)
	ATRMultiplier float64 `json:"atr_multiplier"`
	// Position distribution: "uniform" | "gaussian" | "pyramid"
	Distribution string `json:"distribution"`
	// Maximum drawdown percentage before emergency exit
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
	// Stop loss percentage per position
	StopLossPct float64 `json:"stop_loss_pct"`
	// Daily loss limit percentage
	DailyLossLimitPct float64 `json:"daily_loss_limit_pct"`
	// Use maker-only orders for lower fees
	UseMakerOnly bool `json:"use_maker_only"`
	// Enable automatic grid direction adjustment based on box breakouts
	EnableDirectionAdjust bool `json:"enable_direction_adjust"`
	// Direction bias ratio for long_bias/short_bias modes (default 0.7 = 70%/30%)
	DirectionBiasRatio float64 `json:"direction_bias_ratio"`
}

// PromptSectionsConfig editable sections of System Prompt
type PromptSectionsConfig struct {
	// role definition (title + description)
	RoleDefinition string `json:"role_definition,omitempty"`
	// trading frequency awareness
	TradingFrequency string `json:"trading_frequency,omitempty"`
	// entry standards
	EntryStandards string `json:"entry_standards,omitempty"`
	// decision process
	DecisionProcess string `json:"decision_process,omitempty"`
}

// CoinSourceConfig coin source configuration
type CoinSourceConfig struct {
	// source type shown in the product editor. binance_dynamic is the local,
	// public-market default and does not use paid signal providers.
	SourceType string `json:"source_type"`
	// Binance public-market candidate count, ranked by 24h quote volume.
	BinanceDynamicLimit int `json:"binance_dynamic_limit,omitempty"`
	// static coin list (used when source_type = "static")
	StaticCoins []string `json:"static_coins,omitempty"`
	// User-selected Binance USDⓈ-M symbols. When UseWatchlist is true these are
	// normalized to USDT contracts and become the strategy's static AI candidate pool.
	Watchlist    []string `json:"watchlist,omitempty"`
	UseWatchlist bool     `json:"use_watchlist,omitempty"`
	// excluded coins list (filtered out from all sources)
	ExcludedCoins []string `json:"excluded_coins,omitempty"`
	// whether to use AI500 coin pool
	UseAI500 bool `json:"use_ai500"`
	// AI500 coin pool maximum count
	AI500Limit int `json:"ai500_limit,omitempty"`
	// whether to use OI Top (OI increase ranking, suitable for long positions)
	UseOITop bool `json:"use_oi_top"`
	// OI Top maximum count
	OITopLimit int `json:"oi_top_limit,omitempty"`
	// whether to use OI Low (OI decrease ranking, suitable for short positions)
	UseOILow bool `json:"use_oi_low"`
	// OI Low maximum count
	OILowLimit int `json:"oi_low_limit,omitempty"`
	// whether to use Hyperliquid All coins (all available perp pairs)
	UseHyperAll bool `json:"use_hyper_all"`
	// whether to use Hyperliquid Main coins (top N by 24h volume)
	UseHyperMain bool `json:"use_hyper_main"`
	// Hyperliquid Main maximum count (default 20)
	HyperMainLimit int `json:"hyper_main_limit,omitempty"`
	// Hyperliquid dynamic ranking category: stock, commodity, index, forex, pre_ipo, crypto, all
	HyperRankCategory string `json:"hyper_rank_category,omitempty"`
	// Hyperliquid dynamic ranking direction: gainers, losers, volume
	HyperRankDirection string `json:"hyper_rank_direction,omitempty"`
	// Hyperliquid dynamic ranking maximum count. Defaults to 5 and is hard capped at 10 for AI context safety.
	HyperRankLimit int `json:"hyper_rank_limit,omitempty"`
	// Vergex signal-ranking maximum count. Defaults to 5 and is hard capped at 10.
	VergexLimit int `json:"vergex_limit,omitempty"`
	// Vergex market type for detail endpoints, e.g. hip3_perp for Hyperliquid TradeFi perps.
	VergexMarketType string `json:"vergex_market_type,omitempty"`
	// Vergex chain query parameter. Defaults to hyperliquid.
	VergexChain string `json:"vergex_chain,omitempty"`
	// Vergex liquidation band query parameter.
	VergexLiqBand string `json:"vergex_liq_band,omitempty"`
	// Note: API URLs are now built automatically using NofxOSAPIKey from IndicatorConfig
}

// IndicatorConfig indicator configuration
type IndicatorConfig struct {
	// K-line configuration
	Klines KlineConfig `json:"klines"`
	// raw kline data (OHLCV) - always enabled, required for AI analysis
	EnableRawKlines bool `json:"enable_raw_klines"`
	// technical indicator switches
	EnableEMA         bool `json:"enable_ema"`
	EnableMACD        bool `json:"enable_macd"`
	EnableRSI         bool `json:"enable_rsi"`
	EnableATR         bool `json:"enable_atr"`
	EnableBOLL        bool `json:"enable_boll"` // Bollinger Bands
	EnableVolume      bool `json:"enable_volume"`
	EnableOI          bool `json:"enable_oi"`           // open interest
	EnableFundingRate bool `json:"enable_funding_rate"` // funding rate
	// EMA period configuration
	EMAPeriods []int `json:"ema_periods,omitempty"` // default [20, 50]
	// RSI period configuration
	RSIPeriods []int `json:"rsi_periods,omitempty"` // default [7, 14]
	// ATR period configuration
	ATRPeriods []int `json:"atr_periods,omitempty"` // default [14]
	// BOLL period configuration (period, standard deviation multiplier is fixed at 2)
	BOLLPeriods []int `json:"boll_periods,omitempty"` // default [20] - can select multiple timeframes
	// external data sources
	ExternalDataSources []ExternalDataSource `json:"external_data_sources,omitempty"`

	// ========== NofxOS Unified API Configuration ==========
	// Unified API Key for all NofxOS data sources
	NofxOSAPIKey string `json:"nofxos_api_key,omitempty"`

	// quantitative data sources (capital flow, position changes, price changes)
	EnableQuantData    bool `json:"enable_quant_data"`    // whether to enable quantitative data
	EnableQuantOI      bool `json:"enable_quant_oi"`      // whether to show OI data
	EnableQuantNetflow bool `json:"enable_quant_netflow"` // whether to show Netflow data

	// OI ranking data (market-wide open interest increase/decrease rankings)
	EnableOIRanking   bool   `json:"enable_oi_ranking"`             // whether to enable OI ranking data
	OIRankingDuration string `json:"oi_ranking_duration,omitempty"` // duration: 1h, 4h, 24h
	OIRankingLimit    int    `json:"oi_ranking_limit,omitempty"`    // number of entries (default 10)

	// NetFlow ranking data (market-wide fund flow rankings - institution/personal)
	EnableNetFlowRanking   bool   `json:"enable_netflow_ranking"`             // whether to enable NetFlow ranking data
	NetFlowRankingDuration string `json:"netflow_ranking_duration,omitempty"` // duration: 1h, 4h, 24h
	NetFlowRankingLimit    int    `json:"netflow_ranking_limit,omitempty"`    // number of entries (default 10)

	// Price ranking data (market-wide gainers/losers)
	EnablePriceRanking   bool   `json:"enable_price_ranking"`             // whether to enable price ranking data
	PriceRankingDuration string `json:"price_ranking_duration,omitempty"` // durations: "1h" or "1h,4h,24h"
	PriceRankingLimit    int    `json:"price_ranking_limit,omitempty"`    // number of entries per ranking (default 10)
}

// KlineConfig K-line configuration
type KlineConfig struct {
	// primary timeframe: "1m", "3m", "5m", "15m", "1h", "4h"
	PrimaryTimeframe string `json:"primary_timeframe"`
	// primary timeframe K-line count
	PrimaryCount int `json:"primary_count"`
	// longer timeframe
	LongerTimeframe string `json:"longer_timeframe,omitempty"`
	// longer timeframe K-line count
	LongerCount int `json:"longer_count,omitempty"`
	// whether to enable multi-timeframe analysis
	EnableMultiTimeframe bool `json:"enable_multi_timeframe"`
	// selected timeframe list (new: supports multi-timeframe selection)
	SelectedTimeframes []string `json:"selected_timeframes,omitempty"`
}

// ExternalDataSource external data source configuration
type ExternalDataSource struct {
	Name        string            `json:"name"`   // data source name
	Type        string            `json:"type"`   // type: "api" | "webhook"
	URL         string            `json:"url"`    // API URL
	Method      string            `json:"method"` // HTTP method
	Headers     map[string]string `json:"headers,omitempty"`
	DataPath    string            `json:"data_path,omitempty"`    // JSON data path
	RefreshSecs int               `json:"refresh_secs,omitempty"` // refresh interval (seconds)
}

// TradeThrottleConfig controls AI-managed trade frequency and noise exits for
// one strategy. Zero values mean "use the legacy fallback" so old persisted
// strategy JSON remains backward compatible.
type TradeThrottleConfig struct {
	MinHoldMinutes                int     `json:"min_hold_minutes,omitempty"`
	NoiseCloseHoldMinutes         int     `json:"noise_close_hold_minutes,omitempty"`
	ReentryCooldownMinutes        int     `json:"reentry_cooldown_minutes,omitempty"`
	MaxOpensPerHour               int     `json:"max_opens_per_hour,omitempty"`
	MaxOpensPerCycle              int     `json:"max_opens_per_cycle,omitempty"`
	EarlyCloseStopLossBypassPct   float64 `json:"early_close_stop_loss_bypass_pct,omitempty"`
	EarlyCloseTakeProfitBypassPct float64 `json:"early_close_take_profit_bypass_pct,omitempty"`
	NoiseCloseLossFloorPct        float64 `json:"noise_close_loss_floor_pct,omitempty"`
	NoiseCloseProfitCeilingPct    float64 `json:"noise_close_profit_ceiling_pct,omitempty"`
}

// DefaultTradeThrottleConfig returns the compatibility profile used when an
// existing strategy has no throttle settings.
func DefaultTradeThrottleConfig() TradeThrottleConfig {
	return TradeThrottleConfig{
		MinHoldMinutes:                60,
		NoiseCloseHoldMinutes:         90,
		ReentryCooldownMinutes:        30,
		MaxOpensPerHour:               30,
		MaxOpensPerCycle:              6,
		EarlyCloseStopLossBypassPct:   -2.5,
		EarlyCloseTakeProfitBypassPct: 5,
		NoiseCloseLossFloorPct:        -1,
		NoiseCloseProfitCeilingPct:    2,
	}
}

// BigMoveTradeThrottleConfig is the opt-in profile derived from
// 39eac5ac. It reduces fee-heavy churn while remaining scoped to the strategy
// that stores it instead of changing every AutoTrader globally.
func BigMoveTradeThrottleConfig() TradeThrottleConfig {
	return TradeThrottleConfig{
		MinHoldMinutes:                4 * 60,
		NoiseCloseHoldMinutes:         8 * 60,
		ReentryCooldownMinutes:        3 * 60,
		MaxOpensPerHour:               3,
		MaxOpensPerCycle:              2,
		EarlyCloseStopLossBypassPct:   -5,
		EarlyCloseTakeProfitBypassPct: 12,
		NoiseCloseLossFloorPct:        -4,
		NoiseCloseProfitCeilingPct:    6,
	}
}

// Effective returns a complete, safe configuration. Partial configs from API
// clients inherit only the missing fields from the compatibility profile.
func (c TradeThrottleConfig) Effective() TradeThrottleConfig {
	d := DefaultTradeThrottleConfig()
	if c.MinHoldMinutes > 0 {
		d.MinHoldMinutes = c.MinHoldMinutes
	}
	if c.NoiseCloseHoldMinutes > 0 {
		d.NoiseCloseHoldMinutes = c.NoiseCloseHoldMinutes
	}
	if c.ReentryCooldownMinutes > 0 {
		d.ReentryCooldownMinutes = c.ReentryCooldownMinutes
	}
	if c.MaxOpensPerHour > 0 {
		d.MaxOpensPerHour = c.MaxOpensPerHour
	}
	if c.MaxOpensPerCycle > 0 {
		d.MaxOpensPerCycle = c.MaxOpensPerCycle
	}
	if c.EarlyCloseStopLossBypassPct != 0 {
		d.EarlyCloseStopLossBypassPct = c.EarlyCloseStopLossBypassPct
	}
	if c.EarlyCloseTakeProfitBypassPct != 0 {
		d.EarlyCloseTakeProfitBypassPct = c.EarlyCloseTakeProfitBypassPct
	}
	if c.NoiseCloseLossFloorPct != 0 {
		d.NoiseCloseLossFloorPct = c.NoiseCloseLossFloorPct
	}
	if c.NoiseCloseProfitCeilingPct != 0 {
		d.NoiseCloseProfitCeilingPct = c.NoiseCloseProfitCeilingPct
	}
	if d.NoiseCloseHoldMinutes < d.MinHoldMinutes {
		d.NoiseCloseHoldMinutes = d.MinHoldMinutes
	}
	return d
}

// ClampLimits keeps user-provided throttle values finite and bounded without
// materializing defaults into legacy strategy JSON.
func (c *TradeThrottleConfig) ClampLimits() {
	if c == nil {
		return
	}
	if c.MinHoldMinutes < 0 {
		c.MinHoldMinutes = 0
	}
	if c.MinHoldMinutes > 7*24*60 {
		c.MinHoldMinutes = 7 * 24 * 60
	}
	if c.NoiseCloseHoldMinutes < 0 {
		c.NoiseCloseHoldMinutes = 0
	}
	if c.NoiseCloseHoldMinutes > 14*24*60 {
		c.NoiseCloseHoldMinutes = 14 * 24 * 60
	}
	if c.ReentryCooldownMinutes < 0 {
		c.ReentryCooldownMinutes = 0
	}
	if c.ReentryCooldownMinutes > 7*24*60 {
		c.ReentryCooldownMinutes = 7 * 24 * 60
	}
	if c.MaxOpensPerHour < 0 {
		c.MaxOpensPerHour = 0
	}
	if c.MaxOpensPerHour > 1000 {
		c.MaxOpensPerHour = 1000
	}
	if c.MaxOpensPerCycle < 0 {
		c.MaxOpensPerCycle = 0
	}
	if c.MaxOpensPerCycle > 100 {
		c.MaxOpensPerCycle = 100
	}
	if c.EarlyCloseStopLossBypassPct < -100 {
		c.EarlyCloseStopLossBypassPct = -100
	}
	if c.EarlyCloseStopLossBypassPct > 100 {
		c.EarlyCloseStopLossBypassPct = 100
	}
	if c.EarlyCloseTakeProfitBypassPct < -100 {
		c.EarlyCloseTakeProfitBypassPct = -100
	}
	if c.EarlyCloseTakeProfitBypassPct > 100 {
		c.EarlyCloseTakeProfitBypassPct = 100
	}
	if c.NoiseCloseLossFloorPct < -100 {
		c.NoiseCloseLossFloorPct = -100
	}
	if c.NoiseCloseLossFloorPct > 100 {
		c.NoiseCloseLossFloorPct = 100
	}
	if c.NoiseCloseProfitCeilingPct < -100 {
		c.NoiseCloseProfitCeilingPct = -100
	}
	if c.NoiseCloseProfitCeilingPct > 100 {
		c.NoiseCloseProfitCeilingPct = 100
	}
}

// RiskControlConfig risk control configuration
type RiskControlConfig struct {
	// Max number of coins held simultaneously (CODE ENFORCED)
	MaxPositions int `json:"max_positions"`
	// Versioned: legacy strategies remain notional_based.
	PositionSizingMode string `json:"position_sizing_mode,omitempty"`

	// BTC/ETH exchange leverage for opening positions (AI guided)
	BTCETHMaxLeverage int `json:"btc_eth_max_leverage"`
	// Altcoin exchange leverage for opening positions (AI guided)
	AltcoinMaxLeverage int `json:"altcoin_max_leverage"`

	// BTC/ETH single position max value = equity × this ratio (CODE ENFORCED, default: 5)
	BTCETHMaxPositionValueRatio float64 `json:"btc_eth_max_position_value_ratio"`
	// Altcoin single position max value = equity × this ratio (CODE ENFORCED, default: 1)
	AltcoinMaxPositionValueRatio float64 `json:"altcoin_max_position_value_ratio"`
	// Legacy per-position margin ratios retained for config compatibility. Live
	// margin-based execution sizes from current available margin instead.
	BTCETHMaxMarginRatio  float64 `json:"btc_eth_max_margin_ratio,omitempty"`
	AltcoinMaxMarginRatio float64 `json:"altcoin_max_margin_ratio,omitempty"`

	// Max margin utilization (e.g. 0.9 = 90%) (CODE ENFORCED)
	MaxMarginUsage float64 `json:"max_margin_usage"`
	// Min position size in USDT (CODE ENFORCED)
	MinPositionSize float64 `json:"min_position_size"`

	// Min take_profit / stop_loss ratio (AI guided)
	MinRiskRewardRatio float64 `json:"min_risk_reward_ratio"`
	// Min AI confidence to open position (AI guided)
	MinConfidence int `json:"min_confidence"`
	// Strategy-scoped AI trade frequency and noise-exit policy.
	TradeThrottle *TradeThrottleConfig `json:"trade_throttle,omitempty"`
}

func (r RiskControlConfig) IsMarginBased() bool { return r.PositionSizingMode == "margin_based" }

func (r RiskControlConfig) EffectiveTradeThrottle() TradeThrottleConfig {
	if r.TradeThrottle == nil {
		return DefaultTradeThrottleConfig()
	}
	return r.TradeThrottle.Effective()
}

// MaxPositionNotional returns the configured/reference notional amount. Legacy
// notional-based strategies use it as a hard cap. Live margin-based execution
// no longer treats the stored margin ratio as a hard per-position cap; it uses
// the account's current available margin at execution time.
func (r RiskControlConfig) MaxPositionNotional(equity float64, leverage int, major bool) float64 {
	if equity <= 0 || leverage <= 0 {
		return 0
	}
	notionalRatio, marginRatio := r.AltcoinMaxPositionValueRatio, r.AltcoinMaxMarginRatio
	if major {
		notionalRatio, marginRatio = r.BTCETHMaxPositionValueRatio, r.BTCETHMaxMarginRatio
	}
	notionalCap := equity * notionalRatio
	if !r.IsMarginBased() {
		return notionalCap
	}
	return equity * marginRatio * float64(leverage)
}

// NewStrategyStore creates a new StrategyStore
func NewStrategyStore(db *gorm.DB) *StrategyStore {
	return &StrategyStore{db: db}
}

func (s *StrategyStore) initTables() error {
	// AutoMigrate will add missing columns without dropping existing data
	return s.db.AutoMigrate(&Strategy{})
}

func (s *StrategyStore) initDefaultData() error {
	// No longer pre-populate strategies - create on demand when user configures
	return nil
}

// GetDefaultStrategyConfig returns the default strategy configuration for the given language
func GetDefaultStrategyConfig(lang string) StrategyConfig {
	// Normalize language to "zh" or "en"
	normalizedLang := "en"
	if lang == "zh" {
		normalizedLang = "zh"
	}

	config := StrategyConfig{
		Language: normalizedLang,
		CoinSource: CoinSourceConfig{
			SourceType:          "binance_dynamic",
			BinanceDynamicLimit: MaxCandidateCoins,
			UseAI500:            false,
			AI500Limit:          3,
			UseOITop:            false,
			OITopLimit:          3,
			UseOILow:            false,
			OILowLimit:          3,
			UseHyperAll:         false,
			UseHyperMain:        false,
			HyperMainLimit:      30,
			HyperRankCategory:   "all",
		},
		Indicators: IndicatorConfig{
			Klines: KlineConfig{
				PrimaryTimeframe:     "15m",
				PrimaryCount:         30,
				LongerTimeframe:      "",
				LongerCount:          0,
				EnableMultiTimeframe: false,
				SelectedTimeframes:   []string{"15m"},
			},
			EnableRawKlines:   true, // Required - raw OHLCV data for AI analysis
			EnableEMA:         false,
			EnableMACD:        false,
			EnableRSI:         false,
			EnableATR:         false,
			EnableBOLL:        false,
			EnableVolume:      false,
			EnableOI:          false,
			EnableFundingRate: false,
			EMAPeriods:        []int{20, 50},
			RSIPeriods:        []int{7, 14},
			ATRPeriods:        []int{14},
			BOLLPeriods:       []int{20},
			// External ranking datasets are opt-in. The default local dynamic
			// strategy uses exchange public data and raw candles only.
			NofxOSAPIKey:           "",
			EnableQuantData:        false,
			EnableQuantOI:          false,
			EnableQuantNetflow:     false,
			EnableOIRanking:        false,
			OIRankingDuration:      "1h",
			OIRankingLimit:         10,
			EnableNetFlowRanking:   false,
			NetFlowRankingDuration: "1h",
			NetFlowRankingLimit:    10,
			EnablePriceRanking:     false,
			PriceRankingDuration:   "1h,4h,24h",
			PriceRankingLimit:      10,
		},
		RiskControl: RiskControlConfig{
			MaxPositions:                 3,
			PositionSizingMode:           "margin_based",
			BTCETHMaxLeverage:            3,
			AltcoinMaxLeverage:           3,
			BTCETHMaxPositionValueRatio:  1.0,
			AltcoinMaxPositionValueRatio: 0.5,
			BTCETHMaxMarginRatio:         0.15,
			AltcoinMaxMarginRatio:        0.15,
			MaxMarginUsage:               0.5,
			MinPositionSize:              12,
			MinRiskRewardRatio:           2.0,
			MinConfidence:                75,
		},
	}
	tradeThrottle := BigMoveTradeThrottleConfig()
	config.RiskControl.TradeThrottle = &tradeThrottle

	if lang == "zh" {
		config.PromptSections = PromptSectionsConfig{
			RoleDefinition: `# 你是 NOFX 交易决策助手

只分析本轮程序提供的候选交易对和已有持仓。候选池默认来自 Binance 公开行情的本地动态筛选；候选排名只定义分析范围，不构成开仓理由。`,
			TradingFrequency: `# 交易频率

- 优先等待高质量机会，不需要每个周期都交易。
- 先管理已有持仓，再考虑新开仓。
- 不要在同一周期反复开平同一交易对。`,
			EntryStandards: `# 入场标准

只有在趋势、动量、成交量、风险收益比和原始 K 线结构相互支持时才开仓。关键数据缺失或互相矛盾时默认等待。`,
			DecisionProcess: `# 决策流程

1. 先检查已有持仓：决定止盈、止损或继续持有。
2. 逐个分析本轮候选交易对的多周期行情和原始 K 线。
3. 明确入场依据、止损、止盈和仓位风险。
4. 输出简洁理由和严格 JSON。`,
		}
	} else {
		config.PromptSections = PromptSectionsConfig{
			RoleDefinition: `# You are the NOFX trading decision assistant

Analyze only the candidate symbols and existing positions supplied for this cycle. By default the candidate pool is built locally from public Binance market data; ranking defines the analysis universe and is never an entry reason by itself.`,
			TradingFrequency: `# Trading Frequency

- Wait for quality; you do not need to trade every cycle.
- Manage existing positions before opening new ones.
- Do not churn in and out of the same symbol in one cycle.`,
			EntryStandards: `# Entry Standards

Open only when trend, momentum, volume, risk/reward and raw-candle structure broadly agree. Wait when key data is missing or contradictory.`,
			DecisionProcess: `# Decision Process

1. Check current positions first: take profit, stop loss or hold.
2. Analyze each candidate using the supplied multi-timeframe market data and raw candles.
3. Confirm entry, stop, target and position risk.
4. Output concise reasoning and strict JSON.`,
		}
	}

	return config
}

// Create create a strategy
func (s *StrategyStore) Create(strategy *Strategy) error {
	return s.db.Create(strategy).Error
}

// Update update a strategy
func (s *StrategyStore) Update(strategy *Strategy) error {
	return s.db.Model(&Strategy{}).
		Where("id = ? AND user_id = ?", strategy.ID, strategy.UserID).
		Updates(map[string]interface{}{
			"name":           strategy.Name,
			"description":    strategy.Description,
			"config":         strategy.Config,
			"is_public":      strategy.IsPublic,
			"config_visible": strategy.ConfigVisible,
			"updated_at":     time.Now().UTC(),
		}).Error
}

// Delete delete a strategy
func (s *StrategyStore) Delete(userID, id string) error {
	// do not allow deleting system default strategy
	var st Strategy
	if err := s.db.Where("id = ?", id).First(&st).Error; err == nil {
		if st.IsDefault {
			return fmt.Errorf("cannot delete system default strategy")
		}
		if st.IsActive {
			return fmt.Errorf("cannot delete active strategy")
		}
	}

	// Check if any trader references this strategy
	var count int64
	if err := s.db.Model(&Trader{}).
		Where("user_id = ? AND strategy_id = ?", userID, id).
		Count(&count).Error; err == nil && count > 0 {
		return fmt.Errorf("cannot delete strategy in use by %d trader(s) - reassign those traders first", count)
	}

	return s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&Strategy{}).Error
}

// List get user's strategy list
func (s *StrategyStore) List(userID string) ([]*Strategy, error) {
	var strategies []*Strategy
	err := s.db.Where("user_id = ? OR is_default = ?", userID, true).
		Order("is_default DESC, created_at DESC").
		Find(&strategies).Error
	if err != nil {
		return nil, err
	}
	return strategies, nil
}

// ListPublic get all public strategies for the strategy market
func (s *StrategyStore) ListPublic() ([]*Strategy, error) {
	var strategies []*Strategy
	err := s.db.Where("is_public = ?", true).
		Order("created_at DESC").
		Find(&strategies).Error
	if err != nil {
		return nil, err
	}
	return strategies, nil
}

// Get get a single strategy
func (s *StrategyStore) Get(userID, id string) (*Strategy, error) {
	var st Strategy
	err := s.db.Where("id = ? AND (user_id = ? OR is_default = ?)", id, userID, true).
		First(&st).Error
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// GetActive get user's currently active strategy
func (s *StrategyStore) GetActive(userID string) (*Strategy, error) {
	var st Strategy
	err := s.db.Where("user_id = ? AND is_active = ?", userID, true).First(&st).Error
	if err == gorm.ErrRecordNotFound {
		// no active strategy, return system default strategy
		return s.GetDefault()
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// GetDefault get system default strategy
func (s *StrategyStore) GetDefault() (*Strategy, error) {
	var st Strategy
	err := s.db.Where("is_default = ?", true).First(&st).Error
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// SetActive set active strategy (will first deactivate other strategies)
func (s *StrategyStore) SetActive(userID, strategyID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// first deactivate all strategies for the user
		if err := tx.Model(&Strategy{}).Where("user_id = ?", userID).
			Update("is_active", false).Error; err != nil {
			return err
		}

		// activate specified strategy
		return tx.Model(&Strategy{}).
			Where("id = ? AND (user_id = ? OR is_default = ?)", strategyID, userID, true).
			Update("is_active", true).Error
	})
}

// Duplicate duplicate a strategy (used to create custom strategy based on default strategy)
func (s *StrategyStore) Duplicate(userID, sourceID, newID, newName string) error {
	// get source strategy
	source, err := s.Get(userID, sourceID)
	if err != nil {
		return fmt.Errorf("failed to get source strategy: %w", err)
	}

	// create new strategy
	newStrategy := &Strategy{
		ID:          newID,
		UserID:      userID,
		Name:        newName,
		Description: "Created based on [" + source.Name + "]",
		IsActive:    false,
		IsDefault:   false,
		Config:      source.Config,
	}

	return s.Create(newStrategy)
}

// ParseConfig parse strategy configuration JSON
func (s *Strategy) ParseConfig() (*StrategyConfig, error) {
	var config StrategyConfig
	if err := json.Unmarshal([]byte(s.Config), &config); err != nil {
		return nil, fmt.Errorf("failed to parse strategy configuration: %w", err)
	}
	return &config, nil
}

// SetConfig set strategy configuration
func (s *Strategy) SetConfig(config *StrategyConfig) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to serialize strategy configuration: %w", err)
	}
	s.Config = string(data)
	return nil
}

// ============================================================================
// Token Estimation
// ============================================================================

// TokenEstimate holds the result of token estimation
type TokenEstimate struct {
	Total       int            `json:"total"`
	Breakdown   TokenBreakdown `json:"breakdown"`
	ModelLimits []ModelLimit   `json:"model_limits"`
	Suggestions []string       `json:"suggestions"`
}

// TokenBreakdown shows estimated tokens per component
type TokenBreakdown struct {
	SystemPrompt  int `json:"system_prompt"`
	MarketData    int `json:"market_data"`
	RankingData   int `json:"ranking_data"`
	QuantData     int `json:"quant_data"`
	FixedOverhead int `json:"fixed_overhead"`
}

// ModelLimit shows token usage against a specific model's context limit
type ModelLimit struct {
	Name         string `json:"name"`
	ContextLimit int    `json:"context_limit"`
	UsagePct     int    `json:"usage_pct"`
	Level        string `json:"level"` // "ok" | "warning" | "danger"
}

// Context window sizes (tokens) for each model family
const (
	contextLimitDeepSeek = 131_072   // 128K
	contextLimitOpenAI   = 128_000   // 128K
	contextLimitClaude   = 200_000   // 200K
	contextLimitQwen     = 131_072   // 128K
	contextLimitGemini   = 1_000_000 // 1M
	contextLimitGrok     = 131_072   // 128K
	contextLimitKimi     = 131_072   // 128K
	contextLimitMinimax  = 1_000_000 // 1M
)

// ModelContextLimits maps provider names to their context window sizes (in tokens)
var ModelContextLimits = map[string]int{
	"deepseek": contextLimitDeepSeek,
	"openai":   contextLimitOpenAI,
	"claude":   contextLimitClaude,
	"qwen":     contextLimitQwen,
	"gemini":   contextLimitGemini,
	"grok":     contextLimitGrok,
	"kimi":     contextLimitKimi,
	"minimax":  contextLimitMinimax,
}

// GetContextLimit returns the context limit for a given provider
func GetContextLimit(provider string) int {
	if limit, ok := ModelContextLimits[provider]; ok {
		return limit
	}
	return contextLimitDeepSeek // safe default
}

// GetContextLimitForClient returns context limit for a provider+model pair.
// For claw402, the underlying model is inferred from the model name prefix.
func GetContextLimitForClient(provider, model string) int {
	if provider == "claw402" {
		switch {
		case strings.HasPrefix(model, "claude"):
			return ModelContextLimits["claude"]
		case strings.HasPrefix(model, "gpt"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"):
			return ModelContextLimits["openai"]
		case strings.HasPrefix(model, "gemini"):
			return ModelContextLimits["gemini"]
		case strings.HasPrefix(model, "grok"):
			return ModelContextLimits["grok"]
		case strings.HasPrefix(model, "kimi"):
			return ModelContextLimits["kimi"]
		case strings.HasPrefix(model, "qwen"):
			return ModelContextLimits["qwen"]
		case strings.HasPrefix(model, "minimax"):
			return ModelContextLimits["minimax"]
		case strings.HasPrefix(model, "deepseek"):
			return ModelContextLimits["deepseek"]
		default:
			return ModelContextLimits["deepseek"]
		}
	}
	return GetContextLimit(provider)
}

// EstimateTokens estimates the total token count for a strategy configuration.
// This is a pure computation based on config fields — no network calls.
func (c *StrategyConfig) EstimateTokens() TokenEstimate {
	breakdown := TokenBreakdown{}

	// --- System Prompt ---
	// Base system prompt: schema + role + rules + output format
	baseChars := 4000 // English default
	if c.Language == "zh" {
		baseChars = 3000
	}
	// Add prompt sections
	baseChars += len(c.PromptSections.RoleDefinition)
	baseChars += len(c.PromptSections.TradingFrequency)
	baseChars += len(c.PromptSections.EntryStandards)
	baseChars += len(c.PromptSections.DecisionProcess)
	baseChars += len(c.CustomPrompt)

	if c.Language == "zh" {
		breakdown.SystemPrompt = baseChars / 2 // CJK: ~2 chars per token
	} else {
		breakdown.SystemPrompt = baseChars / 4 // English: ~4 chars per token
	}

	// --- Fixed Overhead ---
	// Time, BTC price, account info, section headers
	breakdown.FixedOverhead = 800 / 4 // ~200 tokens

	// --- Market Data ---
	numCoins := c.getEffectiveCoinCount()
	numTimeframes := c.getEffectiveTimeframeCount()
	klineCount := c.Indicators.Klines.PrimaryCount
	if klineCount <= 0 {
		klineCount = 20
	}

	// Per coin per timeframe: kline OHLCV rows
	charsPerCoinTF := klineCount * 80 // each OHLCV line ~80 chars

	// Add enabled indicator overhead per timeframe
	indicatorCharsPerLine := 0
	if c.Indicators.EnableEMA {
		indicatorCharsPerLine += 20 // EMA values appended
	}
	if c.Indicators.EnableMACD {
		indicatorCharsPerLine += 30
	}
	if c.Indicators.EnableRSI {
		indicatorCharsPerLine += 15
	}
	if c.Indicators.EnableATR {
		indicatorCharsPerLine += 15
	}
	if c.Indicators.EnableBOLL {
		indicatorCharsPerLine += 25
	}
	if c.Indicators.EnableVolume {
		indicatorCharsPerLine += 10
	}
	charsPerCoinTF += klineCount * indicatorCharsPerLine

	totalMarketChars := numCoins * numTimeframes * charsPerCoinTF

	// OI + Funding per coin
	if c.Indicators.EnableOI || c.Indicators.EnableFundingRate {
		totalMarketChars += numCoins * 100
	}

	breakdown.MarketData = totalMarketChars / 4 // numeric data: ~4 chars per token

	// --- Quant Data ---
	if c.Indicators.EnableQuantData {
		quantCharsPerCoin := 0
		if c.Indicators.EnableQuantOI {
			quantCharsPerCoin += 300
		}
		if c.Indicators.EnableQuantNetflow {
			quantCharsPerCoin += 300
		}
		breakdown.QuantData = (numCoins * quantCharsPerCoin) / 4
	}

	// --- Ranking Data ---
	rankingChars := 0
	if c.Indicators.EnableOIRanking {
		limit := c.Indicators.OIRankingLimit
		if limit <= 0 {
			limit = 10
		}
		rankingChars += limit * 60
	}
	if c.Indicators.EnableNetFlowRanking {
		limit := c.Indicators.NetFlowRankingLimit
		if limit <= 0 {
			limit = 10
		}
		rankingChars += limit * 80
	}
	if c.Indicators.EnablePriceRanking {
		limit := c.Indicators.PriceRankingLimit
		if limit <= 0 {
			limit = 10
		}
		// Count durations (comma-separated)
		numDurations := 1
		if c.Indicators.PriceRankingDuration != "" {
			numDurations = len(strings.Split(c.Indicators.PriceRankingDuration, ","))
		}
		rankingChars += limit * numDurations * 40
	}
	breakdown.RankingData = rankingChars / 4

	// --- Total with 15% safety margin ---
	subtotal := breakdown.SystemPrompt + breakdown.MarketData + breakdown.RankingData + breakdown.QuantData + breakdown.FixedOverhead
	total := subtotal * 115 / 100

	// --- Model limits ---
	modelLimits := make([]ModelLimit, 0, len(ModelContextLimits))
	for name, limit := range ModelContextLimits {
		pct := total * 100 / limit
		level := "ok"
		if pct >= 100 {
			level = "danger"
		} else if pct >= 80 {
			level = "warning"
		}
		modelLimits = append(modelLimits, ModelLimit{
			Name:         name,
			ContextLimit: limit,
			UsagePct:     pct,
			Level:        level,
		})
	}

	// Sort by usage_pct desc, then name asc for deterministic order
	sort.Slice(modelLimits, func(i, j int) bool {
		if modelLimits[i].UsagePct != modelLimits[j].UsagePct {
			return modelLimits[i].UsagePct > modelLimits[j].UsagePct
		}
		return modelLimits[i].Name < modelLimits[j].Name
	})

	// --- Suggestions ---
	var suggestions []string
	// Find the strictest model (smallest context)
	minLimit := 0
	for _, limit := range ModelContextLimits {
		if minLimit == 0 || limit < minLimit {
			minLimit = limit
		}
	}
	if minLimit > 0 && total > minLimit {
		if numTimeframes > 1 {
			savedPerTF := (numCoins * klineCount * (80 + indicatorCharsPerLine)) / 4 * 115 / 100
			suggestions = append(suggestions, fmt.Sprintf("Reduce 1 timeframe to save ~%d tokens", savedPerTF))
		}
		if numCoins > 1 {
			savedPerCoin := (numTimeframes * klineCount * (80 + indicatorCharsPerLine)) / 4 * 115 / 100
			suggestions = append(suggestions, fmt.Sprintf("Reduce 1 coin to save ~%d tokens", savedPerCoin))
		}
		if klineCount > 15 {
			suggestions = append(suggestions, "Reduce K-line count to 15 to save tokens")
		}
	}

	return TokenEstimate{
		Total:       total,
		Breakdown:   breakdown,
		ModelLimits: modelLimits,
		Suggestions: suggestions,
	}
}

// getEffectiveCoinCount returns the estimated number of coins that will be analyzed
func (c *StrategyConfig) getEffectiveCoinCount() int {
	count := 0
	switch c.CoinSource.SourceType {
	case "static":
		count = len(c.CoinSource.StaticCoins)
	case "ai500":
		count = c.CoinSource.AI500Limit
	case "oi_top":
		count = c.CoinSource.OITopLimit
	case "oi_low":
		count = c.CoinSource.OILowLimit
	case "hyper_rank":
		count = c.CoinSource.HyperRankLimit
	case "vergex_signal":
		count = c.CoinSource.VergexLimit
	case "hyper_main":
		count = c.CoinSource.HyperMainLimit
	case "hyper_all":
		count = c.CoinSource.HyperMainLimit
	default:
		count = c.CoinSource.HyperRankLimit
	}
	if count <= 0 {
		count = 3
	}
	return count
}

// getEffectiveTimeframeCount returns the number of timeframes that will be used
func (c *StrategyConfig) getEffectiveTimeframeCount() int {
	if len(c.Indicators.Klines.SelectedTimeframes) > 0 {
		return len(c.Indicators.Klines.SelectedTimeframes)
	}
	count := 1
	if c.Indicators.Klines.LongerTimeframe != "" {
		count++
	}
	return count
}
