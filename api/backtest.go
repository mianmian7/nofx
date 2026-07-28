package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"nofx/backtest"
	"nofx/kernel"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type backtestJob struct {
	ID               string           `json:"id"`
	UserID           string           `json:"-"`
	StrategyID       string           `json:"strategy_id"`
	Mode             string           `json:"mode"`
	Status           string           `json:"status"`
	Stage            string           `json:"stage"`
	DecisionProgress int              `json:"decision_progress"`
	MaxAICalls       int              `json:"max_ai_calls"`
	Error            string           `json:"error,omitempty"`
	Result           *backtest.Result `json:"result,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type backtestJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*backtestJob
}

func newBacktestJobStore() *backtestJobStore {
	return &backtestJobStore{jobs: make(map[string]*backtestJob)}
}

func (s *backtestJobStore) put(job *backtestJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
}

func (s *backtestJobStore) update(id string, mutate func(*backtestJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil {
		mutate(job)
		job.UpdatedAt = time.Now().UTC()
	}
}

func (s *backtestJobStore) get(id string) *backtestJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job := s.jobs[id]
	if job == nil {
		return nil
	}
	copyValue := *job
	return &copyValue
}

type startBacktestRequest struct {
	Mode           string                `json:"mode"`
	AIModelID      string                `json:"ai_model_id"`
	Symbol         string                `json:"symbol"`
	Timeframe      string                `json:"timeframe"`
	StartTime      string                `json:"start_time"`
	EndTime        string                `json:"end_time"`
	InitialBalance float64               `json:"initial_balance"`
	FeeBPS         float64               `json:"fee_bps"`
	SlippageBPS    float64               `json:"slippage_bps"`
	MaxAICalls     int                   `json:"max_ai_calls"`
	ConfirmAICalls bool                  `json:"confirm_ai_calls"`
	TrendConfig    *backtest.TrendConfig `json:"trend_config,omitempty"`
}

func (s *Server) handleStartStrategyBacktest(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req startBacktestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	req.Mode = strings.TrimSpace(req.Mode)
	if req.Mode == "" {
		req.Mode = "trend_v1"
	}
	if req.Mode != "trend_v1" && req.Mode != "ai_replay" {
		SafeBadRequest(c, "mode must be trend_v1 or ai_replay")
		return
	}
	if req.Mode == "ai_replay" {
		if strings.TrimSpace(req.AIModelID) == "" {
			SafeBadRequest(c, "ai_model_id is required for ai_replay")
			return
		}
		if !req.ConfirmAICalls {
			SafeBadRequest(c, "confirm_ai_calls must be true because AI replay invokes the selected model repeatedly")
			return
		}
		if req.MaxAICalls <= 0 {
			req.MaxAICalls = 20
		}
		if req.MaxAICalls > 50 {
			SafeBadRequest(c, "max_ai_calls must be between 1 and 50")
			return
		}
	} else {
		trendConfig := backtest.DefaultTrendConfig()
		if req.TrendConfig != nil {
			trendConfig = *req.TrendConfig
		}
		if _, err := backtest.NewTrendProvider(trendConfig); err != nil {
			SafeBadRequest(c, err.Error())
			return
		}
		req.TrendConfig = &trendConfig
		req.MaxAICalls = 0
	}
	if req.InitialBalance <= 0 {
		req.InitialBalance = 1000
	}
	if req.FeeBPS < 0 || req.SlippageBPS < 0 {
		SafeBadRequest(c, "fee_bps and slippage_bps must not be negative")
		return
	}
	if req.FeeBPS == 0 {
		req.FeeBPS = 5
	}
	if req.SlippageBPS == 0 {
		req.SlippageBPS = 2
	}

	strategy, err := s.store.Strategy().Get(userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found"})
		return
	}
	var config store.StrategyConfig
	if err := json.Unmarshal([]byte(strategy.Config), &config); err != nil {
		SafeInternalError(c, "Failed to decode strategy", err)
		return
	}
	if config.StrategyType == "grid_trading" {
		SafeBadRequest(c, "AI historical replay currently supports ai_trading strategies only")
		return
	}
	if err := validateHistoricalReplaySupport(&config); err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	if req.Mode == "ai_replay" {
		if _, _, err := s.newConfiguredAIClient(userID, req.AIModelID); err != nil {
			SafeBadRequest(c, err.Error())
			return
		}
	}

	if s.backtestJobs == nil {
		s.backtestJobs = newBacktestJobStore()
	}
	now := time.Now().UTC()
	job := &backtestJob{
		ID:         uuid.NewString(),
		UserID:     userID,
		StrategyID: strategy.ID,
		Mode:       req.Mode,
		Status:     "queued",
		Stage:      "waiting",
		MaxAICalls: req.MaxAICalls,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.backtestJobs.put(job)

	go s.runStrategyBacktestJob(job.ID, userID, req, config)
	c.JSON(http.StatusAccepted, job)
}

func validateHistoricalReplaySupport(config *store.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("strategy config is required")
	}
	switch config.CoinSource.SourceType {
	case "", "static", "binance_dynamic":
	default:
		return fmt.Errorf("historical replay cannot reconstruct past %s candidate rankings; use a static or Binance local-dynamic strategy", config.CoinSource.SourceType)
	}
	indicators := config.Indicators
	if indicators.EnableOI || indicators.EnableFundingRate || indicators.EnableQuantData ||
		indicators.EnableQuantOI || indicators.EnableQuantNetflow || indicators.EnableOIRanking ||
		indicators.EnableNetFlowRanking || indicators.EnablePriceRanking {
		return fmt.Errorf("historical replay requires external live-only indicators to be disabled")
	}
	return nil
}

func (s *Server) handleGetStrategyBacktest(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if s.backtestJobs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backtest job not found"})
		return
	}
	job := s.backtestJobs.get(c.Param("job_id"))
	if job == nil || job.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backtest job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (s *Server) runStrategyBacktestJob(jobID, userID string, req startBacktestRequest, config store.StrategyConfig) {
	fail := func(err error) {
		s.backtestJobs.update(jobID, func(job *backtestJob) {
			job.Status = "failed"
			job.Stage = "failed"
			job.Error = err.Error()
		})
	}

	symbol := market.Normalize(req.Symbol)
	if symbol == "" {
		symbol = "BTCUSDT"
	}
	timeframe := strings.TrimSpace(req.Timeframe)
	if timeframe == "" {
		timeframe = config.Indicators.Klines.PrimaryTimeframe
	}
	if timeframe == "" {
		timeframe = "15m"
	}
	end := time.Now().UTC()
	if req.EndTime != "" {
		parsed, err := time.Parse(time.RFC3339, req.EndTime)
		if err != nil {
			fail(fmt.Errorf("invalid end_time: %w", err))
			return
		}
		end = parsed
	}
	start := end.Add(-7 * 24 * time.Hour)
	if req.StartTime != "" {
		parsed, err := time.Parse(time.RFC3339, req.StartTime)
		if err != nil {
			fail(fmt.Errorf("invalid start_time: %w", err))
			return
		}
		start = parsed
	}

	s.backtestJobs.update(jobID, func(job *backtestJob) {
		job.Status = "running"
		job.Stage = "fetching_candles"
	})
	candles, err := market.GetKlinesRange(symbol, timeframe, start, end)
	if err != nil {
		fail(fmt.Errorf("failed to fetch historical candles: %w", err))
		return
	}
	candles = market.FilterClosedKlines(candles, time.Now().UTC())
	if req.Mode == "ai_replay" && len(candles) > 500 {
		fail(fmt.Errorf("AI replay range contains %d candles; reduce it to 500 or fewer", len(candles)))
		return
	}
	if len(candles) > 20_000 {
		fail(fmt.Errorf("replay range contains %d candles; reduce it to 20000 or fewer", len(candles)))
		return
	}
	if len(candles) < 22 {
		fail(fmt.Errorf("not enough historical candles: got %d, need at least 22", len(candles)))
		return
	}

	warmup := config.Indicators.Klines.PrimaryCount
	if warmup < 20 {
		warmup = 20
	}
	if warmup > 100 {
		warmup = 100
	}
	interval := 1
	var provider backtest.DecisionProvider
	if req.Mode == "ai_replay" {
		aiClient, _, err := s.newConfiguredAIClient(userID, req.AIModelID)
		if err != nil {
			fail(err)
			return
		}
		available := len(candles) - warmup - 1
		if available > req.MaxAICalls {
			interval = int(math.Ceil(float64(available) / float64(req.MaxAICalls)))
		}
		s.backtestJobs.update(jobID, func(job *backtestJob) { job.Stage = "calling_ai" })
		provider = &historicalAIProvider{
			client:    aiClient,
			engine:    kernel.NewStrategyEngine(&config),
			symbol:    symbol,
			timeframe: timeframe,
			barCount:  warmup,
			onCall: func() {
				s.backtestJobs.update(jobID, func(job *backtestJob) { job.DecisionProgress++ })
			},
		}
	} else {
		trendProvider, err := backtest.NewTrendProvider(*req.TrendConfig)
		if err != nil {
			fail(err)
			return
		}
		warmup = max(req.TrendConfig.SlowEMAPeriod, req.TrendConfig.BreakoutPeriod+1, req.TrendConfig.ATRPeriod+1)
		s.backtestJobs.update(jobID, func(job *backtestJob) { job.Stage = "running_trend_benchmark" })
		provider = trendProvider
	}
	result, err := backtest.Run(context.Background(), backtest.Config{
		Symbol:                 symbol,
		InitialBalance:         req.InitialBalance,
		FeeBPS:                 req.FeeBPS,
		SlippageBPS:            req.SlippageBPS,
		WarmupBars:             warmup,
		DecisionIntervalBars:   interval,
		MaxLeverage:            config.RiskControl.MaxLeverage,
		MaxMarginUsage:         config.RiskControl.MaxMarginUsage,
		MaintenanceMarginRatio: 0.005,
	}, candles, provider)
	if err != nil {
		fail(err)
		return
	}
	s.backtestJobs.update(jobID, func(job *backtestJob) {
		job.Status = "completed"
		job.Stage = "completed"
		job.Result = result
	})
}

type historicalAIProvider struct {
	client    mcp.AIClient
	engine    *kernel.StrategyEngine
	symbol    string
	timeframe string
	barCount  int
	onCall    func()
}

func (p *historicalAIProvider) Decide(ctx context.Context, snapshot backtest.Snapshot) (backtest.Decision, error) {
	if p.onCall != nil {
		p.onCall()
	}
	marketData := market.BuildHistoricalData(p.symbol, p.timeframe, snapshot.Candles, p.barCount)
	positions := make([]kernel.PositionInfo, 0, 1)
	if snapshot.Position != nil {
		mark := snapshot.Candles[len(snapshot.Candles)-1].Close
		unrealized := (mark - snapshot.Position.EntryPrice) * snapshot.Position.Quantity
		if snapshot.Position.Side == "short" {
			unrealized = -unrealized
		}
		positions = append(positions, kernel.PositionInfo{
			Symbol:        p.symbol,
			Side:          snapshot.Position.Side,
			EntryPrice:    snapshot.Position.EntryPrice,
			MarkPrice:     mark,
			Quantity:      snapshot.Position.Quantity,
			Leverage:      snapshot.Position.Leverage,
			UnrealizedPnL: unrealized,
		})
	}
	kernelContext := &kernel.Context{
		CurrentTime:    time.UnixMilli(snapshot.Candles[len(snapshot.Candles)-1].CloseTime).UTC().Format("2006-01-02 15:04:05 UTC"),
		RuntimeMinutes: snapshot.Index,
		CallCount:      snapshot.Index + 1,
		Account: kernel.AccountInfo{
			TotalEquity:      snapshot.Equity,
			AvailableBalance: snapshot.Balance,
			PositionCount:    len(positions),
		},
		Positions:      positions,
		CandidateCoins: []kernel.CandidateCoin{{Symbol: p.symbol, Sources: []string{"historical_replay"}}},
		MarketDataMap:  map[string]*market.Data{p.symbol: marketData},
		OITopDataMap:   map[string]*kernel.OITopData{},
		QuantDataMap:   map[string]*kernel.QuantData{},
	}
	full, err := kernel.GetFullDecisionWithStrategy(kernelContext, p.client, p.engine, "")
	if err != nil {
		return backtest.Decision{}, err
	}
	for _, decision := range full.Decisions {
		if market.Normalize(decision.Symbol) != p.symbol {
			continue
		}
		return backtest.Decision{
			Action:          decision.Action,
			Leverage:        decision.Leverage,
			PositionSizeUSD: decision.PositionSizeUSD,
			StopLoss:        decision.StopLoss,
			TakeProfit:      decision.TakeProfit,
			Confidence:      decision.Confidence,
			Reasoning:       decision.Reasoning,
		}, nil
	}
	return backtest.Decision{Action: "hold", Reasoning: "model returned no decision for replay symbol"}, nil
}
