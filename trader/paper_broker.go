package trader

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"nofx/kernel"
	tradertypes "nofx/trader/types"
)

type ExecutionMode string

const (
	ExecutionModePaper ExecutionMode = "paper"
	ExecutionModeLive  ExecutionMode = "live"
)

// PaperPriceSource is deliberately read-only. PaperBroker has no field or
// constructor parameter capable of carrying exchange credentials or writes.
type PaperPriceSource interface {
	GetMarketPrice(symbol string) (float64, error)
}

type PaperBrokerConfig struct {
	InitialBalance         float64
	TakerFeeBPS            float64
	SlippageBPS            float64
	MaintenanceMarginRatio float64
}

type PaperPosition struct {
	Symbol           string
	Side             string
	Quantity         float64
	EntryPrice       float64
	Leverage         int
	StopLoss         float64
	TakeProfit       float64
	EntryTime        time.Time
	EntryFee         float64
	InitialMargin    float64
	LiquidationPrice float64
}

type PaperFill struct {
	OrderID     int64
	Symbol      string
	Action      string
	Side        string `json:"side,omitempty"`
	Leverage    int    `json:"leverage,omitempty"`
	Price       float64
	Quantity    float64
	Fee         float64
	RealizedPnL float64
	Time        time.Time
}

type PaperSnapshot struct {
	Balance          float64 `json:"balance"`
	Equity           float64 `json:"equity"`
	AvailableBalance float64 `json:"available_balance"`
	UsedMargin       float64 `json:"used_margin"`
	RealizedPnL      float64 `json:"realized_pnl"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	Fees             float64 `json:"fees"`
	OpenPositions    int     `json:"open_positions"`
	ClosedTrades     int     `json:"closed_trades"`
	Wins             int     `json:"wins"`
	WinRate          float64 `json:"win_rate"`
	MaxDrawdown      float64 `json:"max_drawdown"`
}

// PaperBroker owns only a virtual ledger and a read-only price source.
type PaperBroker struct {
	mu             sync.RWMutex
	config         PaperBrokerConfig
	prices         PaperPriceSource
	initialBalance float64
	balance        float64
	positions      map[string]PaperPosition
	fills          []PaperFill
	marks          map[string]float64
	totalFees      float64
	closedTrades   int
	wins           int
	peakEquity     float64
	maxDrawdown    float64
	nextID         int64
	ledger         PaperLedger
	traderID       string
}

type PaperLedger interface {
	LoadPaperState(traderID string) ([]byte, bool, error)
	SavePaperState(traderID string, state []byte) error
}

type paperBrokerState struct {
	InitialBalance float64                  `json:"initial_balance"`
	Balance        float64                  `json:"balance"`
	Positions      map[string]PaperPosition `json:"positions"`
	Fills          []PaperFill              `json:"fills"`
	Marks          map[string]float64       `json:"marks"`
	TotalFees      float64                  `json:"total_fees"`
	ClosedTrades   int                      `json:"closed_trades"`
	Wins           int                      `json:"wins"`
	PeakEquity     float64                  `json:"peak_equity"`
	MaxDrawdown    float64                  `json:"max_drawdown"`
	NextID         int64                    `json:"next_id"`
}

func NewPaperBroker(config PaperBrokerConfig, prices PaperPriceSource) (*PaperBroker, error) {
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("paper initial balance must be greater than zero")
	}
	if prices == nil {
		return nil, fmt.Errorf("paper price source is required")
	}
	if config.TakerFeeBPS < 0 || config.SlippageBPS < 0 {
		return nil, fmt.Errorf("paper fee and slippage must not be negative")
	}
	if config.MaintenanceMarginRatio <= 0 {
		config.MaintenanceMarginRatio = 0.005
	}
	return &PaperBroker{
		config:         config,
		prices:         prices,
		initialBalance: config.InitialBalance,
		balance:        config.InitialBalance,
		positions:      make(map[string]PaperPosition),
		marks:          make(map[string]float64),
		peakEquity:     config.InitialBalance,
		nextID:         1,
	}, nil
}

func NewPersistentPaperBroker(config PaperBrokerConfig, prices PaperPriceSource, ledger PaperLedger, traderID string) (*PaperBroker, error) {
	if ledger == nil || traderID == "" {
		return nil, fmt.Errorf("paper ledger and trader ID are required")
	}
	broker, err := NewPaperBroker(config, prices)
	if err != nil {
		return nil, err
	}
	broker.ledger = ledger
	broker.traderID = traderID
	raw, found, err := ledger.LoadPaperState(traderID)
	if err != nil {
		return nil, fmt.Errorf("load paper state: %w", err)
	}
	if found {
		var state paperBrokerState
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, fmt.Errorf("decode paper state: %w", err)
		}
		migrated, err := broker.restoreState(state)
		if err != nil {
			return nil, err
		}
		if migrated {
			if err := broker.persistLocked(); err != nil {
				return nil, fmt.Errorf("persist migrated paper state: %w", err)
			}
		}
	}
	return broker, nil
}

func (b *PaperBroker) ExecuteDecision(decision *kernel.Decision) (PaperFill, error) {
	if decision == nil {
		return PaperFill{}, fmt.Errorf("paper decision is required")
	}
	if decision.Action == "hold" || decision.Action == "wait" {
		return PaperFill{Symbol: decision.Symbol, Action: decision.Action, Time: time.Now().UTC()}, nil
	}
	if decision.Action == "close_long" || decision.Action == "close_short" {
		side := "long"
		if decision.Action == "close_short" {
			side = "short"
		}
		price, err := b.prices.GetMarketPrice(decision.Symbol)
		if err != nil {
			return PaperFill{}, err
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		key := decision.Symbol + ":" + side
		position, ok := b.positions[key]
		if !ok {
			return PaperFill{}, fmt.Errorf("paper %s position not found", side)
		}
		fill := b.closePositionLocked(position, decision.Action, price, time.Now().UTC())
		delete(b.positions, key)
		b.updateDrawdownLocked()
		if err := b.persistLocked(); err != nil {
			return PaperFill{}, err
		}
		return fill, nil
	}
	if decision.Action != "open_long" && decision.Action != "open_short" {
		return PaperFill{}, fmt.Errorf("paper action %q is not implemented", decision.Action)
	}
	if decision.PositionSizeUSD <= 0 {
		return PaperFill{}, fmt.Errorf("paper position size must be greater than zero")
	}
	if decision.Leverage <= 0 {
		return PaperFill{}, fmt.Errorf("paper leverage must be greater than zero")
	}
	marketPrice, err := b.prices.GetMarketPrice(decision.Symbol)
	if err != nil {
		return PaperFill{}, fmt.Errorf("paper market price: %w", err)
	}
	if marketPrice <= 0 {
		return PaperFill{}, fmt.Errorf("paper market price for %s is invalid", decision.Symbol)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	side := "long"
	slippage := b.config.SlippageBPS / 10_000
	fillPrice := marketPrice * (1 + slippage)
	if decision.Action == "open_short" {
		side = "short"
		fillPrice = marketPrice * (1 - slippage)
	}
	quantity := decision.PositionSizeUSD / fillPrice
	fee := decision.PositionSizeUSD * b.config.TakerFeeBPS / 10_000
	requiredMargin := decision.PositionSizeUSD / float64(decision.Leverage)
	availableBalance := b.balance - b.usedMarginLocked()
	if requiredMargin+fee > availableBalance {
		return PaperFill{}, fmt.Errorf("paper available balance is insufficient for initial margin and fee")
	}
	now := time.Now().UTC()
	fill := PaperFill{
		OrderID:  b.nextID,
		Symbol:   decision.Symbol,
		Action:   decision.Action,
		Side:     side,
		Leverage: decision.Leverage,
		Price:    fillPrice,
		Quantity: quantity,
		Fee:      fee,
		Time:     now,
	}
	b.nextID++
	b.balance -= fee
	b.totalFees += fee
	b.marks[decision.Symbol] = marketPrice
	leverage := decision.Leverage
	liquidationPrice := 0.0
	if leverage > 1 {
		if side == "short" {
			liquidationPrice = fillPrice * (1 + 1/float64(leverage) - b.config.MaintenanceMarginRatio)
		} else {
			liquidationPrice = math.Max(0, fillPrice*(1-1/float64(leverage)+b.config.MaintenanceMarginRatio))
		}
	}
	b.positions[decision.Symbol+":"+side] = PaperPosition{
		Symbol: decision.Symbol, Side: side, Quantity: quantity,
		EntryPrice: fillPrice, Leverage: decision.Leverage,
		StopLoss: decision.StopLoss, TakeProfit: decision.TakeProfit,
		EntryTime: now, EntryFee: fee, InitialMargin: requiredMargin,
		LiquidationPrice: liquidationPrice,
	}
	b.fills = append(b.fills, fill)
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	return fill, nil
}

func (b *PaperBroker) usedMarginLocked() float64 {
	used := 0.0
	for _, position := range b.positions {
		used += position.InitialMargin
	}
	return used
}

func (b *PaperBroker) RefreshOpenPositions() error {
	b.mu.RLock()
	symbolSet := make(map[string]struct{}, len(b.positions))
	for _, position := range b.positions {
		symbolSet[position.Symbol] = struct{}{}
	}
	b.mu.RUnlock()
	var refreshErrors []error
	for symbol := range symbolSet {
		price, err := b.prices.GetMarketPrice(symbol)
		if err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("refresh paper price %s: %w", symbol, err))
			continue
		}
		if _, err := b.ProcessPrice(symbol, price, time.Now().UTC()); err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("process paper price %s: %w", symbol, err))
		}
	}
	return errors.Join(refreshErrors...)
}

// ProcessPrice marks virtual positions and produces synthetic market fills
// when a configured take-profit level is crossed.
func (b *PaperBroker) ProcessPrice(symbol string, price float64, at time.Time) ([]PaperFill, error) {
	if price <= 0 {
		return nil, fmt.Errorf("paper market price for %s is invalid", symbol)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.marks[symbol] = price
	exits := make([]PaperFill, 0, 1)
	for key, position := range b.positions {
		if position.Symbol != symbol {
			continue
		}
		action := ""
		if position.LiquidationPrice > 0 && ((position.Side == "long" && price <= position.LiquidationPrice) || (position.Side == "short" && price >= position.LiquidationPrice)) {
			action = "liquidation"
		} else if position.StopLoss > 0 && ((position.Side == "long" && price <= position.StopLoss) || (position.Side == "short" && price >= position.StopLoss)) {
			action = "stop_loss"
		} else if position.TakeProfit > 0 && ((position.Side == "long" && price >= position.TakeProfit) || (position.Side == "short" && price <= position.TakeProfit)) {
			action = "take_profit"
		}
		if action == "" {
			continue
		}
		fill := b.closePositionLocked(position, action, price, at)
		delete(b.positions, key)
		exits = append(exits, fill)
	}
	b.updateDrawdownLocked()
	if err := b.persistLocked(); err != nil {
		return nil, err
	}
	return exits, nil
}

func (b *PaperBroker) persistLocked() error {
	if b.ledger == nil {
		return nil
	}
	raw, err := json.Marshal(b.stateLocked())
	if err != nil {
		return fmt.Errorf("encode paper state: %w", err)
	}
	if err := b.ledger.SavePaperState(b.traderID, raw); err != nil {
		return fmt.Errorf("save paper state: %w", err)
	}
	return nil
}

func (b *PaperBroker) stateLocked() paperBrokerState {
	return paperBrokerState{
		InitialBalance: b.initialBalance, Balance: b.balance,
		Positions: b.positions, Fills: b.fills, Marks: b.marks,
		TotalFees: b.totalFees, ClosedTrades: b.closedTrades, Wins: b.wins,
		PeakEquity: b.peakEquity, MaxDrawdown: b.maxDrawdown, NextID: b.nextID,
	}
}

func (b *PaperBroker) restoreState(state paperBrokerState) (bool, error) {
	b.initialBalance = state.InitialBalance
	b.balance = state.Balance
	b.positions = state.Positions
	if b.positions == nil {
		b.positions = make(map[string]PaperPosition)
	}
	migrated := false
	for key, position := range b.positions {
		if position.Leverage <= 0 {
			return false, fmt.Errorf("restore paper position %s: leverage must be greater than zero", key)
		}
		if position.InitialMargin <= 0 {
			position.InitialMargin = position.EntryPrice * position.Quantity / float64(position.Leverage)
			b.positions[key] = position
			migrated = true
		}
	}
	b.fills = state.Fills
	b.marks = state.Marks
	if b.marks == nil {
		b.marks = make(map[string]float64)
	}
	b.totalFees = state.TotalFees
	b.closedTrades = state.ClosedTrades
	b.wins = state.Wins
	b.peakEquity = state.PeakEquity
	b.maxDrawdown = state.MaxDrawdown
	b.nextID = state.NextID
	if b.nextID <= 0 {
		b.nextID = 1
	}
	return migrated, nil
}

func (b *PaperBroker) closePositionLocked(position PaperPosition, action string, marketPrice float64, at time.Time) PaperFill {
	slippage := b.config.SlippageBPS / 10_000
	fillPrice := marketPrice * (1 - slippage)
	grossPnL := (fillPrice - position.EntryPrice) * position.Quantity
	if position.Side == "short" {
		fillPrice = marketPrice * (1 + slippage)
		grossPnL = (position.EntryPrice - fillPrice) * position.Quantity
	}
	exitFee := fillPrice * position.Quantity * b.config.TakerFeeBPS / 10_000
	netTradePnL := grossPnL - position.EntryFee - exitFee
	b.balance += grossPnL - exitFee
	b.totalFees += exitFee
	b.closedTrades++
	if netTradePnL > 0 {
		b.wins++
	}
	fill := PaperFill{
		OrderID: b.nextID, Symbol: position.Symbol, Action: action,
		Side: position.Side, Leverage: position.Leverage,
		Price: fillPrice, Quantity: position.Quantity, Fee: exitFee,
		RealizedPnL: netTradePnL, Time: at,
	}
	b.nextID++
	b.fills = append(b.fills, fill)
	return fill
}

func (b *PaperBroker) Snapshot() PaperSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshotLocked()
}

func (b *PaperBroker) RecentFills(limit int) []PaperFill {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.fills) {
		limit = len(b.fills)
	}
	start := len(b.fills) - limit
	return append([]PaperFill(nil), b.fills[start:]...)
}

func (b *PaperBroker) snapshotLocked() PaperSnapshot {
	unrealized := 0.0
	usedMargin := b.usedMarginLocked()
	for _, position := range b.positions {
		mark := b.marks[position.Symbol]
		if mark == 0 {
			mark = position.EntryPrice
		}
		pnl := (mark - position.EntryPrice) * position.Quantity
		if position.Side == "short" {
			pnl = (position.EntryPrice - mark) * position.Quantity
		}
		unrealized += pnl
	}
	winRate := 0.0
	if b.closedTrades > 0 {
		winRate = float64(b.wins) / float64(b.closedTrades) * 100
	}
	return PaperSnapshot{
		Balance: b.balance, Equity: b.balance + unrealized,
		AvailableBalance: b.balance - usedMargin, UsedMargin: usedMargin,
		RealizedPnL:   b.balance - b.initialBalance,
		UnrealizedPnL: unrealized, Fees: b.totalFees,
		OpenPositions: len(b.positions), ClosedTrades: b.closedTrades,
		Wins: b.wins, WinRate: winRate, MaxDrawdown: b.maxDrawdown,
	}
}

func (b *PaperBroker) updateDrawdownLocked() {
	equity := b.snapshotLocked().Equity
	if equity > b.peakEquity {
		b.peakEquity = equity
	}
	if b.peakEquity <= 0 {
		return
	}
	drawdown := (b.peakEquity - equity) / b.peakEquity * 100
	if drawdown > b.maxDrawdown {
		b.maxDrawdown = drawdown
	}
}

func (b *PaperBroker) GetPositions() ([]map[string]interface{}, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]map[string]interface{}, 0, len(b.positions))
	for _, position := range b.positions {
		mark := b.marks[position.Symbol]
		if mark == 0 {
			mark = position.EntryPrice
		}
		unrealized := (mark - position.EntryPrice) * position.Quantity
		positionAmt := position.Quantity
		if position.Side == "short" {
			unrealized = (position.EntryPrice - mark) * position.Quantity
			positionAmt = -position.Quantity
		}
		result = append(result, map[string]interface{}{
			"symbol": position.Symbol, "side": position.Side,
			"quantity": position.Quantity, "entry_price": position.EntryPrice,
			"entryPrice": position.EntryPrice, "markPrice": mark,
			"positionAmt": positionAmt, "unRealizedProfit": unrealized,
			"liquidationPrice": position.LiquidationPrice, "leverage": float64(position.Leverage),
			"initial_margin": position.InitialMargin, "margin_used": position.InitialMargin,
			"stop_loss": position.StopLoss, "take_profit": position.TakeProfit,
		})
	}
	return result, nil
}

func (b *PaperBroker) GetBalance() (map[string]interface{}, error) {
	s := b.Snapshot()
	return map[string]interface{}{
		"totalWalletBalance":    s.Balance,
		"totalUnrealizedProfit": s.UnrealizedPnL,
		"availableBalance":      s.AvailableBalance,
		"totalInitialMargin":    s.UsedMargin,
		"totalEquity":           s.Equity,
	}, nil
}

func (b *PaperBroker) GetMarketPrice(symbol string) (float64, error) {
	price, err := b.prices.GetMarketPrice(symbol)
	if err != nil {
		return 0, err
	}
	_, err = b.ProcessPrice(symbol, price, time.Now().UTC())
	return price, err
}

func (b *PaperBroker) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return b.openFromQuantity(symbol, "open_long", quantity, leverage)
}

func (b *PaperBroker) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return b.openFromQuantity(symbol, "open_short", quantity, leverage)
}

func (b *PaperBroker) openFromQuantity(symbol, action string, quantity float64, leverage int) (map[string]interface{}, error) {
	price, err := b.prices.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}
	fill, err := b.ExecuteDecision(&kernel.Decision{Symbol: symbol, Action: action, PositionSizeUSD: quantity * price, Leverage: leverage})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"orderId": fill.OrderID, "avgPrice": fill.Price, "executedQty": fill.Quantity, "status": "FILLED"}, nil
}

func (b *PaperBroker) CloseLong(symbol string, _ float64) (map[string]interface{}, error) {
	return b.closeBySide(symbol, "long", "close_long")
}

func (b *PaperBroker) CloseShort(symbol string, _ float64) (map[string]interface{}, error) {
	return b.closeBySide(symbol, "short", "close_short")
}

func (b *PaperBroker) closeBySide(symbol, side, action string) (map[string]interface{}, error) {
	price, err := b.prices.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := symbol + ":" + side
	position, ok := b.positions[key]
	if !ok {
		return nil, fmt.Errorf("paper %s position not found", side)
	}
	fill := b.closePositionLocked(position, action, price, time.Now().UTC())
	delete(b.positions, key)
	b.updateDrawdownLocked()
	if err := b.persistLocked(); err != nil {
		return nil, err
	}
	return map[string]interface{}{"orderId": fill.OrderID, "avgPrice": fill.Price, "executedQty": fill.Quantity, "status": "FILLED"}, nil
}

func (b *PaperBroker) SetLeverage(string, int) error       { return nil }
func (b *PaperBroker) SetMarginMode(string, bool) error    { return nil }
func (b *PaperBroker) CancelAllOrders(string) error        { return nil }
func (b *PaperBroker) CancelStopOrders(string) error       { return nil }
func (b *PaperBroker) CancelStopLossOrders(string) error   { return nil }
func (b *PaperBroker) CancelTakeProfitOrders(string) error { return nil }
func (b *PaperBroker) FormatQuantity(_ string, quantity float64) (string, error) {
	return fmt.Sprintf("%.8f", quantity), nil
}

func (b *PaperBroker) SetStopLoss(symbol, side string, _ float64, stop float64) error {
	return b.setExitLevel(symbol, side, stop, true)
}

func (b *PaperBroker) SetTakeProfit(symbol, side string, _ float64, target float64) error {
	return b.setExitLevel(symbol, side, target, false)
}

func (b *PaperBroker) setExitLevel(symbol, side string, value float64, stop bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := symbol + ":" + strings.ToLower(side)
	position, ok := b.positions[key]
	if !ok {
		return fmt.Errorf("paper position not found")
	}
	if stop {
		position.StopLoss = value
	} else {
		position.TakeProfit = value
	}
	b.positions[key] = position
	return b.persistLocked()
}

func (b *PaperBroker) GetOrderStatus(_ string, orderID string) (map[string]interface{}, error) {
	return map[string]interface{}{"orderId": orderID, "status": "FILLED"}, nil
}
func (b *PaperBroker) GetClosedPnL(time.Time, int) ([]tradertypes.ClosedPnLRecord, error) {
	return nil, nil
}
func (b *PaperBroker) GetOpenOrders(string) ([]tradertypes.OpenOrder, error) { return nil, nil }

var _ Trader = (*PaperBroker)(nil)
