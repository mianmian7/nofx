package trader

import (
	"math"
	"sort"
	"strings"
	"time"
)

type PaperClosedTrade struct {
	EntryOrderID int64     `json:"entry_order_id"`
	ExitOrderID  int64     `json:"exit_order_id"`
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"`
	Quantity     float64   `json:"quantity"`
	EntryPrice   float64   `json:"entry_price"`
	ExitPrice    float64   `json:"exit_price"`
	EntryTime    time.Time `json:"entry_time"`
	ExitTime     time.Time `json:"exit_time"`
	Leverage     int       `json:"leverage"`
	EntryFee     float64   `json:"entry_fee"`
	ExitFee      float64   `json:"exit_fee"`
	Fee          float64   `json:"fee"`
	RealizedPnL  float64   `json:"realized_pnl"`
	CloseReason  string    `json:"close_reason"`
}

type PaperPerformance struct {
	TotalTrades     int                `json:"total_trades"`
	WinTrades       int                `json:"win_trades"`
	LossTrades      int                `json:"loss_trades"`
	WinRate         float64            `json:"win_rate"`
	ProfitFactor    float64            `json:"profit_factor"`
	SharpeRatio     float64            `json:"sharpe_ratio"`
	TotalPnL        float64            `json:"total_pnl"`
	TotalFees       float64            `json:"total_fees"`
	ClosedTradeFees float64            `json:"closed_trade_fees"`
	AvgWin          float64            `json:"avg_win"`
	AvgLoss         float64            `json:"avg_loss"`
	MaxDrawdownPct  float64            `json:"max_drawdown_pct"`
	ClosedTrades    []PaperClosedTrade `json:"closed_trades"`
}

func (b *PaperBroker) Performance() PaperPerformance {
	b.mu.RLock()
	fills := append([]PaperFill(nil), b.fills...)
	initialBalance := b.initialBalance
	b.mu.RUnlock()
	return reconstructPaperPerformance(fills, initialBalance)
}

func reconstructPaperPerformance(fills []PaperFill, initialBalance float64) PaperPerformance {
	sort.SliceStable(fills, func(i, j int) bool {
		if fills[i].Time.Equal(fills[j].Time) {
			return fills[i].OrderID < fills[j].OrderID
		}
		return fills[i].Time.Before(fills[j].Time)
	})
	pending := make(map[string][]PaperFill)
	closedChronological := make([]PaperClosedTrade, 0)
	performance := PaperPerformance{}
	for _, fill := range fills {
		performance.TotalFees += fill.Fee
		side, opening := paperOpenSide(fill.Action)
		if opening {
			if fill.Side != "" {
				side = strings.ToLower(fill.Side)
			}
			key := fill.Symbol + ":" + side
			pending[key] = append(pending[key], fill)
			continue
		}
		side, closing := paperCloseSide(fill.Action)
		if !closing {
			continue
		}
		if fill.Side != "" {
			side = strings.ToLower(fill.Side)
		} else if side == "" {
			side = solePendingPaperSide(pending, fill.Symbol)
		}
		if side == "" {
			continue
		}
		key := fill.Symbol + ":" + side
		entries := pending[key]
		if len(entries) == 0 {
			continue
		}
		entry := entries[0]
		pending[key] = entries[1:]
		trade := PaperClosedTrade{
			EntryOrderID: entry.OrderID, ExitOrderID: fill.OrderID,
			Symbol: fill.Symbol, Side: side, Quantity: fill.Quantity,
			EntryPrice: entry.Price, ExitPrice: fill.Price,
			EntryTime: entry.Time, ExitTime: fill.Time,
			Leverage: entry.Leverage,
			EntryFee: entry.Fee, ExitFee: fill.Fee, Fee: entry.Fee + fill.Fee,
			RealizedPnL: fill.RealizedPnL, CloseReason: fill.Action,
		}
		closedChronological = append(closedChronological, trade)
	}
	populatePaperPerformance(&performance, closedChronological, initialBalance)
	performance.ClosedTrades = make([]PaperClosedTrade, len(closedChronological))
	for i := range closedChronological {
		performance.ClosedTrades[len(closedChronological)-1-i] = closedChronological[i]
	}
	return performance
}

func paperOpenSide(action string) (string, bool) {
	switch strings.ToLower(action) {
	case "open_long":
		return "long", true
	case "open_short":
		return "short", true
	default:
		return "", false
	}
}

func paperCloseSide(action string) (string, bool) {
	switch strings.ToLower(action) {
	case "close_long":
		return "long", true
	case "close_short":
		return "short", true
	case "take_profit", "stop_loss", "liquidation":
		return "", true
	default:
		return "", false
	}
}

func solePendingPaperSide(pending map[string][]PaperFill, symbol string) string {
	found := ""
	for _, side := range []string{"long", "short"} {
		if len(pending[symbol+":"+side]) == 0 {
			continue
		}
		if found != "" {
			return ""
		}
		found = side
	}
	return found
}

func populatePaperPerformance(performance *PaperPerformance, trades []PaperClosedTrade, initialBalance float64) {
	performance.TotalTrades = len(trades)
	var totalWin, totalLoss float64
	pnls := make([]float64, 0, len(trades))
	for _, trade := range trades {
		performance.TotalPnL += trade.RealizedPnL
		performance.ClosedTradeFees += trade.Fee
		pnls = append(pnls, trade.RealizedPnL)
		if trade.RealizedPnL > 0 {
			performance.WinTrades++
			totalWin += trade.RealizedPnL
		} else if trade.RealizedPnL < 0 {
			performance.LossTrades++
			totalLoss += -trade.RealizedPnL
		}
	}
	if performance.TotalTrades > 0 {
		performance.WinRate = float64(performance.WinTrades) / float64(performance.TotalTrades) * 100
	}
	if totalLoss > 0 {
		performance.ProfitFactor = totalWin / totalLoss
	}
	if performance.WinTrades > 0 {
		performance.AvgWin = totalWin / float64(performance.WinTrades)
	}
	if performance.LossTrades > 0 {
		performance.AvgLoss = totalLoss / float64(performance.LossTrades)
	}
	performance.SharpeRatio = paperSharpe(pnls)
	performance.MaxDrawdownPct = paperClosedTradeDrawdown(pnls, initialBalance)
}

func paperSharpe(pnls []float64) float64 {
	if len(pnls) < 2 {
		return 0
	}
	var sum float64
	for _, pnl := range pnls {
		sum += pnl
	}
	mean := sum / float64(len(pnls))
	var variance float64
	for _, pnl := range pnls {
		variance += (pnl - mean) * (pnl - mean)
	}
	stdDev := math.Sqrt(variance / float64(len(pnls)-1))
	if stdDev == 0 {
		return 0
	}
	return mean / stdDev
}

func paperClosedTradeDrawdown(pnls []float64, initialBalance float64) float64 {
	if initialBalance <= 0 {
		initialBalance = 10_000
	}
	equity, peak, maxDrawdown := initialBalance, initialBalance, 0.0
	for _, pnl := range pnls {
		equity += pnl
		if equity > peak {
			peak = equity
		}
		if peak > 0 {
			drawdown := (peak - equity) / peak * 100
			if drawdown > maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	return maxDrawdown
}
