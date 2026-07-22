package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"nofx/backtest"
	"nofx/market"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type windowResult struct {
	Symbol         string   `json:"symbol"`
	Window         string   `json:"window"`
	CandleCount    int      `json:"candle_count"`
	SnapshotSHA256 string   `json:"snapshot_sha256"`
	ReturnPct      float64  `json:"return_pct"`
	MaxDrawdownPct float64  `json:"max_drawdown_pct"`
	TradeCount     int      `json:"trade_count"`
	WinRatePct     float64  `json:"win_rate_pct"`
	ProfitFactor   *float64 `json:"profit_factor"`
	Sharpe         float64  `json:"sharpe"`
}

type report struct {
	GeneratedAt       string               `json:"generated_at"`
	Timeframe         string               `json:"timeframe"`
	FeeBPS            float64              `json:"fee_bps"`
	SlippageBPS       float64              `json:"slippage_bps"`
	TrendConfig       backtest.TrendConfig `json:"trend_config"`
	Windows           []windowResult       `json:"windows"`
	PositiveWindowPct float64              `json:"positive_window_pct"`
	AggregateTrades   int                  `json:"aggregate_trades"`
	MedianReturnPct   float64              `json:"median_return_pct"`
	WorstDrawdownPct  float64              `json:"worst_drawdown_pct"`
	StabilityGate     bool                 `json:"stability_gate"`
	GateExplanation   string               `json:"gate_explanation"`
}

func main() {
	symbolsFlag := flag.String("symbols", "BTCUSDT,ETHUSDT,SOLUSDT", "comma-separated Binance futures symbols")
	timeframe := flag.String("timeframe", "4h", "Binance kline timeframe")
	startFlag := flag.String("start", "2021-01-01", "UTC start date")
	endFlag := flag.String("end", time.Now().UTC().Format("2006-01-02"), "UTC end date")
	feeBPS := flag.Float64("fee-bps", 5, "one-way fee in basis points")
	slippageBPS := flag.Float64("slippage-bps", 2, "one-way slippage in basis points")
	outputDir := flag.String("output-dir", "", "optional directory for immutable candle snapshots")
	inputDir := flag.String("input-dir", "", "optional directory containing frozen candle snapshots")
	fast := flag.Int("fast", 20, "fast EMA period")
	slow := flag.Int("slow", 100, "slow EMA period")
	breakout := flag.Int("breakout", 55, "Donchian breakout period")
	atrPeriod := flag.Int("atr", 20, "ATR period")
	stopATR := flag.Float64("stop-atr", 2.5, "ATR stop multiple")
	rewardRisk := flag.Float64("reward-risk", 3, "take-profit reward/risk")
	riskPct := flag.Float64("risk-pct", 1, "equity risk per trade percentage")
	maxExposure := flag.Float64("max-exposure", 1, "max notional / equity")
	leverage := flag.Int("leverage", 1, "leverage")
	allowShort := flag.Bool("allow-short", false, "allow short entries")
	summaryOnly := flag.Bool("summary-only", false, "omit per-window details from JSON output")
	flag.Parse()

	start, err := time.Parse("2006-01-02", *startFlag)
	must(err)
	end, err := time.Parse("2006-01-02", *endFlag)
	must(err)
	end = end.Add(24*time.Hour - time.Millisecond)

	trendConfig := backtest.TrendConfig{
		FastEMAPeriod:   *fast,
		SlowEMAPeriod:   *slow,
		BreakoutPeriod:  *breakout,
		ATRPeriod:       *atrPeriod,
		ATRStopMultiple: *stopATR,
		RewardRiskRatio: *rewardRisk,
		RiskPerTradePct: *riskPct,
		MaxExposure:     *maxExposure,
		Leverage:        *leverage,
		AllowShort:      *allowShort,
	}
	_, err = backtest.NewTrendProvider(trendConfig)
	must(err)

	resultReport := report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339), Timeframe: *timeframe,
		FeeBPS: *feeBPS, SlippageBPS: *slippageBPS, TrendConfig: trendConfig,
	}
	for _, rawSymbol := range strings.Split(*symbolsFlag, ",") {
		symbol := market.Normalize(rawSymbol)
		var candles []market.Kline
		var encoded []byte
		if *inputDir != "" {
			pattern := filepath.Join(*inputDir, fmt.Sprintf("%s-%s-%s-%s-*.json", symbol, *timeframe, *startFlag, *endFlag))
			matches, err := filepath.Glob(pattern)
			must(err)
			if len(matches) != 1 {
				panic(fmt.Errorf("expected one frozen snapshot for %s, found %d", symbol, len(matches)))
			}
			encoded, err = os.ReadFile(matches[0])
			must(err)
			must(json.Unmarshal(encoded, &candles))
		} else {
			candles, err = market.GetKlinesRange(symbol, *timeframe, start, end)
			must(err)
			candles = market.FilterClosedKlines(candles, time.Now().UTC())
			encoded, err = json.Marshal(candles)
			must(err)
		}
		if len(candles) == 0 {
			continue
		}
		digest := sha256.Sum256(encoded)
		hash := hex.EncodeToString(digest[:])
		if *outputDir != "" {
			must(os.MkdirAll(*outputDir, 0o755))
			name := fmt.Sprintf("%s-%s-%s-%s-%s.json", symbol, *timeframe, *startFlag, *endFlag, hash[:12])
			must(os.WriteFile(filepath.Join(*outputDir, name), encoded, 0o644))
		}

		for year := start.Year(); year <= end.Year(); year++ {
			windowStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
			windowEnd := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
			window := selectWindow(candles, windowStart, windowEnd)
			warmup := max(trendConfig.SlowEMAPeriod, trendConfig.BreakoutPeriod+1, trendConfig.ATRPeriod+1)
			if len(window) <= warmup+2 {
				continue
			}
			windowProvider, err := backtest.NewTrendProvider(trendConfig)
			must(err)
			result, err := backtest.Run(context.Background(), backtest.Config{
				Symbol: symbol, InitialBalance: 10_000, FeeBPS: *feeBPS, SlippageBPS: *slippageBPS,
				WarmupBars: warmup, DecisionIntervalBars: 1, MaxLeverage: trendConfig.Leverage,
				MaxMarginUsage:         math.Min(1, trendConfig.MaxExposure/float64(trendConfig.Leverage)),
				MaintenanceMarginRatio: 0.005,
			}, window, windowProvider)
			must(err)
			resultReport.Windows = append(resultReport.Windows, windowResult{
				Symbol: symbol, Window: fmt.Sprintf("%d", year), CandleCount: len(window), SnapshotSHA256: hash,
				ReturnPct: result.TotalReturnPct, MaxDrawdownPct: result.MaxDrawdownPct,
				TradeCount: result.TradeCount, WinRatePct: result.WinRatePct,
				ProfitFactor: result.ProfitFactor, Sharpe: annualizedSharpe(result.EquityCurve, *timeframe),
			})
		}
	}

	returns := make([]float64, 0, len(resultReport.Windows))
	positive := 0
	for _, window := range resultReport.Windows {
		returns = append(returns, window.ReturnPct)
		resultReport.AggregateTrades += window.TradeCount
		resultReport.WorstDrawdownPct = math.Max(resultReport.WorstDrawdownPct, window.MaxDrawdownPct)
		if window.ReturnPct > 0 {
			positive++
		}
	}
	resultReport.MedianReturnPct = median(returns)
	if len(resultReport.Windows) > 0 {
		resultReport.PositiveWindowPct = float64(positive) / float64(len(resultReport.Windows)) * 100
	}
	resultReport.StabilityGate = resultReport.PositiveWindowPct >= 70 &&
		resultReport.AggregateTrades >= 100 && resultReport.MedianReturnPct > 0 && resultReport.WorstDrawdownPct <= 25
	resultReport.GateExplanation = "positive in >=70% symbol-year windows; >=100 aggregate trades; positive median return; worst max drawdown <=25%; all after configured fees/slippage"
	if *summaryOnly {
		resultReport.Windows = nil
	}

	output, err := json.MarshalIndent(resultReport, "", "  ")
	must(err)
	fmt.Println(string(output))
}

func selectWindow(candles []market.Kline, start, end int64) []market.Kline {
	selected := make([]market.Kline, 0)
	for _, candle := range candles {
		if candle.OpenTime >= start && candle.OpenTime < end {
			selected = append(selected, candle)
		}
	}
	return selected
}

func annualizedSharpe(curve []backtest.EquityPoint, timeframe string) float64 {
	if len(curve) < 3 {
		return 0
	}
	returns := make([]float64, 0, len(curve)-1)
	for i := 1; i < len(curve); i++ {
		if curve[i-1].Equity > 0 {
			returns = append(returns, curve[i].Equity/curve[i-1].Equity-1)
		}
	}
	mean := average(returns)
	variance := 0.0
	for _, value := range returns {
		variance += (value - mean) * (value - mean)
	}
	variance /= float64(len(returns) - 1)
	if variance <= 0 {
		return 0
	}
	periodsPerYear := map[string]float64{"15m": 35040, "1h": 8760, "4h": 2190, "1d": 365}[timeframe]
	if periodsPerYear == 0 {
		periodsPerYear = 365
	}
	return mean / math.Sqrt(variance) * math.Sqrt(periodsPerYear)
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	for i := 0; i < len(copyValues); i++ {
		for j := i + 1; j < len(copyValues); j++ {
			if copyValues[j] < copyValues[i] {
				copyValues[i], copyValues[j] = copyValues[j], copyValues[i]
			}
		}
	}
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 0 {
		return (copyValues[middle-1] + copyValues[middle]) / 2
	}
	return copyValues[middle]
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
