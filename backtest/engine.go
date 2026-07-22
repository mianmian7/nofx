package backtest

import (
	"context"
	"fmt"
	"math"
	"nofx/market"
)

// DecisionProvider is the only strategy-specific part of a replay. Production
// can use an AI model; tests can inject a deterministic provider. A decision
// made after candle i closes is never filled before candle i+1 opens.
type DecisionProvider interface {
	Decide(context.Context, Snapshot) (Decision, error)
}

type Decision struct {
	Action          string  `json:"action"`
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	Confidence      int     `json:"confidence,omitempty"`
	Reasoning       string  `json:"reasoning,omitempty"`
}

type Position struct {
	Side             string  `json:"side"`
	Quantity         float64 `json:"quantity"`
	EntryPrice       float64 `json:"entry_price"`
	Leverage         int     `json:"leverage"`
	Margin           float64 `json:"margin"`
	LiquidationPrice float64 `json:"liquidation_price,omitempty"`
	StopLoss         float64 `json:"stop_loss,omitempty"`
	TakeProfit       float64 `json:"take_profit,omitempty"`
	EntryTime        int64   `json:"entry_time"`
	EntryFee         float64 `json:"entry_fee"`
}

type Snapshot struct {
	Symbol   string         `json:"symbol"`
	Index    int            `json:"index"`
	Candles  []market.Kline `json:"candles"`
	Balance  float64        `json:"balance"`
	Equity   float64        `json:"equity"`
	Position *Position      `json:"position,omitempty"`
}

type Config struct {
	Symbol                 string  `json:"symbol"`
	InitialBalance         float64 `json:"initial_balance"`
	FeeBPS                 float64 `json:"fee_bps"`
	SlippageBPS            float64 `json:"slippage_bps"`
	WarmupBars             int     `json:"warmup_bars"`
	DecisionIntervalBars   int     `json:"decision_interval_bars"`
	MaxLeverage            int     `json:"max_leverage"`
	MaxMarginUsage         float64 `json:"max_margin_usage"`
	MaintenanceMarginRatio float64 `json:"maintenance_margin_ratio"`
}

type Trade struct {
	Side       string  `json:"side"`
	EntryTime  int64   `json:"entry_time"`
	ExitTime   int64   `json:"exit_time"`
	EntryPrice float64 `json:"entry_price"`
	ExitPrice  float64 `json:"exit_price"`
	Quantity   float64 `json:"quantity"`
	GrossPnL   float64 `json:"gross_pnl"`
	Fees       float64 `json:"fees"`
	NetPnL     float64 `json:"net_pnl"`
	ExitReason string  `json:"exit_reason"`
}

type EquityPoint struct {
	Time        int64   `json:"time"`
	Equity      float64 `json:"equity"`
	DrawdownPct float64 `json:"drawdown_pct"`
}

type Result struct {
	Symbol          string        `json:"symbol"`
	InitialBalance  float64       `json:"initial_balance"`
	FinalEquity     float64       `json:"final_equity"`
	TotalReturnPct  float64       `json:"total_return_pct"`
	MaxDrawdownPct  float64       `json:"max_drawdown_pct"`
	TradeCount      int           `json:"trade_count"`
	WinRatePct      float64       `json:"win_rate_pct"`
	ProfitFactor    *float64      `json:"profit_factor"`
	DecisionCalls   int           `json:"decision_calls"`
	Trades          []Trade       `json:"trades"`
	EquityCurve     []EquityPoint `json:"equity_curve"`
	ExecutionPolicy string        `json:"execution_policy"`
}

func Run(ctx context.Context, cfg Config, candles []market.Kline, provider DecisionProvider) (*Result, error) {
	if provider == nil {
		return nil, fmt.Errorf("decision provider is required")
	}
	if len(candles) < 2 {
		return nil, fmt.Errorf("at least two candles are required")
	}
	if cfg.InitialBalance <= 0 {
		return nil, fmt.Errorf("initial balance must be positive")
	}
	if cfg.FeeBPS < 0 || cfg.SlippageBPS < 0 {
		return nil, fmt.Errorf("fee and slippage must not be negative")
	}
	if cfg.DecisionIntervalBars <= 0 {
		cfg.DecisionIntervalBars = 1
	}
	if cfg.WarmupBars < 0 {
		cfg.WarmupBars = 0
	}
	if cfg.MaxLeverage <= 0 {
		cfg.MaxLeverage = 20
	}
	if cfg.MaxMarginUsage <= 0 || cfg.MaxMarginUsage > 1 {
		cfg.MaxMarginUsage = 0.5
	}
	if cfg.MaintenanceMarginRatio <= 0 || cfg.MaintenanceMarginRatio >= 0.5 {
		cfg.MaintenanceMarginRatio = 0.005
	}

	feeRate := cfg.FeeBPS / 10000
	slippageRate := cfg.SlippageBPS / 10000
	balance := cfg.InitialBalance
	peakEquity := cfg.InitialBalance
	maxDrawdown := 0.0
	var position *Position
	var pending *Decision
	trades := make([]Trade, 0)
	curve := make([]EquityPoint, 0, len(candles))
	decisionCalls := 0

	closePosition := func(rawPrice float64, exitTime int64, reason string) {
		if position == nil {
			return
		}
		exitPrice := adverseFill(rawPrice, position.Side, false, slippageRate)
		gross := unrealized(position, exitPrice)
		exitFee := math.Abs(position.Quantity*exitPrice) * feeRate
		balance += gross - exitFee
		if reason == "liquidation" && balance < 0 {
			balance = 0
		}
		trades = append(trades, Trade{
			Side:       position.Side,
			EntryTime:  position.EntryTime,
			ExitTime:   exitTime,
			EntryPrice: position.EntryPrice,
			ExitPrice:  exitPrice,
			Quantity:   position.Quantity,
			GrossPnL:   gross,
			Fees:       position.EntryFee + exitFee,
			NetPnL:     gross - position.EntryFee - exitFee,
			ExitReason: reason,
		})
		position = nil
	}

	for i, candle := range candles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if pending != nil {
			action := pending.Action
			switch action {
			case "open_long", "open_short":
				if position == nil && pending.PositionSizeUSD > 0 {
					side := "long"
					if action == "open_short" {
						side = "short"
					}
					leverage := pending.Leverage
					if leverage <= 0 {
						leverage = 1
					}
					if leverage > cfg.MaxLeverage {
						leverage = cfg.MaxLeverage
					}
					maxNotional := balance * cfg.MaxMarginUsage * float64(leverage)
					notional := math.Min(pending.PositionSizeUSD, maxNotional)
					entryPrice := adverseFill(candle.Open, side, true, slippageRate)
					entryFee := notional * feeRate
					if entryPrice > 0 && notional > 0 && entryFee < balance {
						stopLoss, takeProfit := validExitLevels(side, entryPrice, pending.StopLoss, pending.TakeProfit)
						balance -= entryFee
						position = &Position{
							Side:             side,
							Quantity:         notional / entryPrice,
							EntryPrice:       entryPrice,
							Leverage:         leverage,
							Margin:           notional / float64(leverage),
							LiquidationPrice: liquidationPrice(side, entryPrice, leverage, cfg.MaintenanceMarginRatio),
							StopLoss:         stopLoss,
							TakeProfit:       takeProfit,
							EntryTime:        candle.OpenTime,
							EntryFee:         entryFee,
						}
					}
				}
			case "close_long":
				if position != nil && position.Side == "long" {
					closePosition(candle.Open, candle.OpenTime, "decision")
				}
			case "close_short":
				if position != nil && position.Side == "short" {
					closePosition(candle.Open, candle.OpenTime, "decision")
				}
			}
			pending = nil
		}

		// When stop and target are both touched in one candle, use the stop.
		// That conservative ordering avoids claiming an unknowable intrabar path.
		if position != nil {
			if position.Side == "long" {
				if position.LiquidationPrice > 0 && candle.Low <= position.LiquidationPrice {
					closePosition(position.LiquidationPrice, candle.CloseTime, "liquidation")
				} else if position.StopLoss > 0 && candle.Low <= position.StopLoss {
					closePosition(position.StopLoss, candle.CloseTime, "stop_loss")
				} else if position.TakeProfit > 0 && candle.High >= position.TakeProfit {
					closePosition(position.TakeProfit, candle.CloseTime, "take_profit")
				}
			} else {
				if position.LiquidationPrice > 0 && candle.High >= position.LiquidationPrice {
					closePosition(position.LiquidationPrice, candle.CloseTime, "liquidation")
				} else if position.StopLoss > 0 && candle.High >= position.StopLoss {
					closePosition(position.StopLoss, candle.CloseTime, "stop_loss")
				} else if position.TakeProfit > 0 && candle.Low <= position.TakeProfit {
					closePosition(position.TakeProfit, candle.CloseTime, "take_profit")
				}
			}
		}

		equity := balance
		if position != nil {
			equity += unrealized(position, candle.Close)
		}
		if equity > peakEquity {
			peakEquity = equity
		}
		worstEquity := equity
		if position != nil {
			worstPrice := candle.Low
			if position.Side == "short" {
				worstPrice = candle.High
			}
			worstEquity = math.Min(worstEquity, balance+unrealized(position, worstPrice))
		}
		drawdown := 0.0
		if peakEquity > 0 {
			drawdown = (peakEquity - worstEquity) / peakEquity * 100
		}
		maxDrawdown = math.Max(maxDrawdown, drawdown)
		curve = append(curve, EquityPoint{Time: candle.CloseTime, Equity: equity, DrawdownPct: drawdown})

		if i >= cfg.WarmupBars && (i-cfg.WarmupBars)%cfg.DecisionIntervalBars == 0 && i < len(candles)-1 {
			var clonedPosition *Position
			if position != nil {
				copyValue := *position
				clonedPosition = &copyValue
			}
			decision, err := provider.Decide(ctx, Snapshot{
				Symbol:   cfg.Symbol,
				Index:    i,
				Candles:  candles[:i+1],
				Balance:  balance,
				Equity:   equity,
				Position: clonedPosition,
			})
			if err != nil {
				return nil, fmt.Errorf("decision at candle %d: %w", i, err)
			}
			decisionCalls++
			pending = &decision
		}
	}

	if position != nil {
		last := candles[len(candles)-1]
		closePosition(last.Close, last.CloseTime, "end_of_replay")
		lastPoint := &curve[len(curve)-1]
		lastPoint.Equity = balance
		if balance > peakEquity {
			peakEquity = balance
		}
		lastPoint.DrawdownPct = (peakEquity - balance) / peakEquity * 100
		maxDrawdown = math.Max(maxDrawdown, lastPoint.DrawdownPct)
	}

	wins := 0
	grossProfit := 0.0
	grossLoss := 0.0
	for _, trade := range trades {
		if trade.NetPnL > 0 {
			wins++
			grossProfit += trade.NetPnL
		} else if trade.NetPnL < 0 {
			grossLoss += -trade.NetPnL
		}
	}
	winRate := 0.0
	if len(trades) > 0 {
		winRate = float64(wins) / float64(len(trades)) * 100
	}
	var profitFactor *float64
	if grossLoss > 0 {
		value := grossProfit / grossLoss
		profitFactor = &value
	}

	return &Result{
		Symbol:          cfg.Symbol,
		InitialBalance:  cfg.InitialBalance,
		FinalEquity:     balance,
		TotalReturnPct:  (balance - cfg.InitialBalance) / cfg.InitialBalance * 100,
		MaxDrawdownPct:  maxDrawdown,
		TradeCount:      len(trades),
		WinRatePct:      winRate,
		ProfitFactor:    profitFactor,
		DecisionCalls:   decisionCalls,
		Trades:          trades,
		EquityCurve:     curve,
		ExecutionPolicy: "decision_at_close_fill_next_open; conservative_stop_first; fees_and_slippage_included",
	}, nil
}

func adverseFill(price float64, side string, opening bool, slippageRate float64) float64 {
	if (side == "long" && opening) || (side == "short" && !opening) {
		return price * (1 + slippageRate)
	}
	return price * (1 - slippageRate)
}

func unrealized(position *Position, price float64) float64 {
	if position.Side == "short" {
		return (position.EntryPrice - price) * position.Quantity
	}
	return (price - position.EntryPrice) * position.Quantity
}

func validExitLevels(side string, entry, stopLoss, takeProfit float64) (float64, float64) {
	if side == "long" {
		if stopLoss >= entry {
			stopLoss = 0
		}
		if takeProfit <= entry {
			takeProfit = 0
		}
	} else {
		if stopLoss <= entry {
			stopLoss = 0
		}
		if takeProfit >= entry {
			takeProfit = 0
		}
	}
	return stopLoss, takeProfit
}

func liquidationPrice(side string, entry float64, leverage int, maintenanceMarginRatio float64) float64 {
	if leverage <= 1 {
		return 0
	}
	if side == "short" {
		return entry * (1 + 1/float64(leverage) - maintenanceMarginRatio)
	}
	return math.Max(0, entry*(1-1/float64(leverage)+maintenanceMarginRatio))
}
