package trader

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
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

type PaperDepthSource interface {
	GetDepth(symbol string, limit int) (*market.DepthSnapshot, error)
}

type PaperBrokerConfig struct {
	InitialBalance                 float64
	MakerFirst                     bool
	MakerFeeBPS                    float64
	TakerFeeBPS                    float64
	SlippageBPS                    float64
	MakerTimeout                   time.Duration
	MakerMaxReprices               int
	MaintenanceMarginRatio         float64
	MarkStaleTTL                   time.Duration
	FundingHistoryFallbackInterval time.Duration
	FundingSource                  PaperFundingSource
	Clock                          func() time.Time
}

// PaperCachedMarkWarning means an upstream mark refresh failed but the broker
// retained a previously successful mark that is still inside the configured
// stale TTL. Callers may continue read-only context construction, while still
// logging the degraded market-data condition.
type PaperCachedMarkWarning struct {
	Symbol string
	Age    time.Duration
	Cause  error
}

func (w *PaperCachedMarkWarning) Error() string {
	return fmt.Sprintf("refresh paper price %s: using cached mark age %s after upstream failure: %v", w.Symbol, w.Age.Round(time.Millisecond), w.Cause)
}

func (w *PaperCachedMarkWarning) Unwrap() error { return w.Cause }

func CanContinueWithCachedPaperMarks(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !CanContinueWithCachedPaperMarks(child) {
				return false
			}
		}
		return true
	}
	var warning *PaperCachedMarkWarning
	return errors.As(err, &warning)
}

func isValidPaperMarkPrice(price float64) bool {
	return price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}

func isFinitePaperNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

type PaperFundingSource interface {
	GetFundingHistory(symbol string, startTime, endTime int64) ([]market.FundingEvent, error)
	GetFundingSnapshot(symbol string) (*market.FundingSnapshot, error)
}

type PaperFundingPayment struct {
	ID          string    `json:"id"`
	Symbol      string    `json:"symbol"`
	Side        string    `json:"side"`
	Quantity    float64   `json:"quantity"`
	MarkPrice   float64   `json:"mark_price"`
	FundingRate float64   `json:"funding_rate"`
	FundingTime int64     `json:"funding_time"`
	Payment     float64   `json:"payment"`
	WalletDelta float64   `json:"wallet_delta"`
	AppliedAt   time.Time `json:"applied_at"`
}

type PaperFundingStatus struct {
	Symbol          string    `json:"symbol"`
	Rate            float64   `json:"funding_rate"`
	MarkPrice       float64   `json:"mark_price"`
	IndexPrice      float64   `json:"index_price"`
	NextFundingTime int64     `json:"next_funding_time"`
	UpdatedAt       time.Time `json:"updated_at"`
	LastAttemptAt   time.Time `json:"last_attempt_at,omitempty"`
	Stale           bool      `json:"stale,omitempty"`
	Warning         string    `json:"warning,omitempty"`
	HistoryPending  bool      `json:"history_pending,omitempty"`
}

type PaperFundingPollResult struct {
	SnapshotRequests int
	HistoryRequests  int
	PaymentsApplied  int
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

type PaperPendingOrder struct {
	OrderID           int64     `json:"order_id"`
	Symbol            string    `json:"symbol"`
	Action            string    `json:"action"`
	Side              string    `json:"side"`
	LimitPrice        float64   `json:"limit_price"`
	Quantity          float64   `json:"quantity"`
	FilledQuantity    float64   `json:"filled_quantity"`
	RemainingQuantity float64   `json:"remaining_quantity"`
	PositionSizeUSD   float64   `json:"position_size_usd"`
	Leverage          int       `json:"leverage"`
	StopLoss          float64   `json:"stop_loss,omitempty"`
	TakeProfit        float64   `json:"take_profit,omitempty"`
	ReduceOnly        bool      `json:"reduce_only"`
	Status            string    `json:"status"`
	RepriceCount      int       `json:"reprice_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type PaperOrderEvent struct {
	OrderID            int64     `json:"order_id"`
	ReplacementOrderID int64     `json:"replacement_order_id,omitempty"`
	Symbol             string    `json:"symbol"`
	Action             string    `json:"action"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	LimitPrice         float64   `json:"limit_price"`
	Quantity           float64   `json:"quantity"`
	FilledQuantity     float64   `json:"filled_quantity,omitempty"`
	IsMaker            bool      `json:"is_maker"`
	Time               time.Time `json:"time"`
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
	Status      string `json:"status,omitempty"`
	IsMaker     bool   `json:"is_maker,omitempty"`
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
	PendingOrders    int     `json:"pending_orders"`
	ClosedTrades     int     `json:"closed_trades"`
	Wins             int     `json:"wins"`
	WinRate          float64 `json:"win_rate"`
	MaxDrawdown      float64 `json:"max_drawdown"`
	FundingNet       float64 `json:"funding_net"`
	FundingPaid      float64 `json:"funding_paid"`
	FundingReceived  float64 `json:"funding_received"`
	MakerFees        float64 `json:"maker_fees"`
	TakerFees        float64 `json:"taker_fees"`
}

// PaperBroker owns only a virtual ledger and a read-only price source.
type PaperBroker struct {
	mu                      sync.RWMutex
	config                  PaperBrokerConfig
	prices                  PaperPriceSource
	initialBalance          float64
	balance                 float64
	positions               map[string]PaperPosition
	pendingOrders           map[int64]PaperPendingOrder
	orderEvents             []PaperOrderEvent
	fills                   []PaperFill
	marks                   map[string]float64
	markTimes               map[string]time.Time
	totalFees               float64
	closedTrades            int
	wins                    int
	peakEquity              float64
	maxDrawdown             float64
	nextID                  int64
	fundingSource           PaperFundingSource
	clock                   func() time.Time
	fundingPayments         []PaperFundingPayment
	lastFundingTimes        map[string]int64
	fundingStatuses         map[string]PaperFundingStatus
	fundingHistoryReady     map[string]bool
	fundingHistoryRetry     map[string]bool
	fundingHistoryCheckedAt map[string]time.Time
	// Quarantined historical state is retained for diagnostics but is never
	// included in margin, equity, or risk processing. This lets one malformed
	// legacy protection record fail closed without blocking the whole trader.
	quarantinedPositions map[string]PaperPosition
	quarantinedOrders    map[int64]PaperPendingOrder
	restoreWarnings      []string
	fundingPollMu        sync.Mutex
	ledger               PaperLedger
	traderID             string
	closeRecorder        PaperCloseRecorder
	pendingCloseRecords  []PaperClosedTrade
}

type PaperLedger interface {
	LoadPaperState(traderID string) ([]byte, bool, error)
	SavePaperState(traderID string, state []byte) error
}

// PaperCloseRecorder receives closed simulated positions so they can be
// persisted as completed trades (e.g. into trader_positions) and shown to the
// AI in its recent-trades context.
type PaperCloseRecorder interface {
	RecordPaperClose(close PaperClosedTrade, traderID string) error
}

// paperTradeRecorder persists only simulated closes into trader_positions so
// the AI's recent-trades context includes paper history. Runtime paper OPEN
// positions intentionally remain in paper_account_states.state_json; writing
// them here would make the exchange-position reconciliation path treat a
// virtual position as a live exchange position.
type paperTradeRecorder struct {
	store    *store.Store
	exchange string
}

func newPaperTradeRecorder(st *store.Store, exchange string) *paperTradeRecorder {
	return &paperTradeRecorder{store: st, exchange: exchange}
}

// NewPaperTradeRecorder is used by stopped-trader recovery paths.
func NewPaperTradeRecorder(st *store.Store, exchange string) PaperCloseRecorder {
	return newPaperTradeRecorder(st, exchange)
}

func (r *paperTradeRecorder) RecordPaperClose(close PaperClosedTrade, traderID string) error {
	if r.store == nil {
		return nil
	}
	nowMs := close.ExitTime.UnixMilli()
	pos := &store.TraderPosition{
		TraderID:           traderID,
		ExchangeID:         "paper",
		ExchangeType:       r.exchange,
		ExchangePositionID: fmt.Sprintf("paper_%s_%s_%d", close.Symbol, close.Side, close.ExitOrderID),
		Symbol:             close.Symbol,
		Side:               close.Side,
		Quantity:           close.Quantity,
		EntryPrice:         close.EntryPrice,
		EntryTime:          close.EntryTime.UnixMilli(),
		ExitPrice:          close.ExitPrice,
		ExitOrderID:        strconv.FormatInt(close.ExitOrderID, 10),
		ExitTime:           nowMs,
		RealizedPnL:        close.RealizedPnL,
		Fee:                close.Fee,
		Leverage:           close.Leverage,
		Status:             "CLOSED",
		CloseReason:        close.CloseReason,
		CreatedAt:          nowMs,
		UpdatedAt:          nowMs,
	}
	if pos.EntryQuantity == 0 {
		pos.EntryQuantity = close.Quantity
	}
	return r.store.Position().RecordClosedTrade(pos)
}

type paperBrokerState struct {
	InitialBalance          float64                       `json:"initial_balance"`
	Balance                 float64                       `json:"balance"`
	Positions               map[string]PaperPosition      `json:"positions"`
	PendingOrders           map[int64]PaperPendingOrder   `json:"pending_orders,omitempty"`
	OrderEvents             []PaperOrderEvent             `json:"order_events,omitempty"`
	Fills                   []PaperFill                   `json:"fills"`
	PendingCloseRecords     []PaperClosedTrade            `json:"pending_close_records,omitempty"`
	Marks                   map[string]float64            `json:"marks"`
	MarkTimes               map[string]time.Time          `json:"mark_times,omitempty"`
	TotalFees               float64                       `json:"total_fees"`
	ClosedTrades            int                           `json:"closed_trades"`
	Wins                    int                           `json:"wins"`
	PeakEquity              float64                       `json:"peak_equity"`
	MaxDrawdown             float64                       `json:"max_drawdown"`
	NextID                  int64                         `json:"next_id"`
	FundingPayments         []PaperFundingPayment         `json:"funding_payments,omitempty"`
	LastFundingTimes        map[string]int64              `json:"last_funding_times,omitempty"`
	FundingStatuses         map[string]PaperFundingStatus `json:"funding_statuses,omitempty"`
	FundingHistoryReady     map[string]bool               `json:"funding_history_ready,omitempty"`
	FundingHistoryRetry     map[string]bool               `json:"funding_history_retry,omitempty"`
	FundingHistoryCheckedAt map[string]time.Time          `json:"funding_history_checked_at,omitempty"`
	QuarantinedPositions    map[string]PaperPosition      `json:"quarantined_positions,omitempty"`
	QuarantinedOrders       map[int64]PaperPendingOrder   `json:"quarantined_orders,omitempty"`
	RestoreWarnings         []string                      `json:"restore_warnings,omitempty"`
}

func NewPaperBroker(config PaperBrokerConfig, prices PaperPriceSource) (*PaperBroker, error) {
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("paper initial balance must be greater than zero")
	}
	if prices == nil {
		return nil, fmt.Errorf("paper price source is required")
	}
	if config.MakerFeeBPS < 0 || config.TakerFeeBPS < 0 || config.SlippageBPS < 0 {
		return nil, fmt.Errorf("paper fee and slippage must not be negative")
	}
	if config.MakerTimeout <= 0 {
		config.MakerTimeout = 15 * time.Second
	}
	if config.MakerMaxReprices < 0 {
		return nil, fmt.Errorf("paper maker max reprices must not be negative")
	}
	if config.MaintenanceMarginRatio <= 0 {
		config.MaintenanceMarginRatio = 0.005
	}
	if config.MarkStaleTTL <= 0 {
		// The background paper monitor runs every 30s by default. Keeping a
		// mark for three monitor intervals lets one delayed/failed refresh
		// degrade to a cached mark without immediately blocking risk context
		// construction, while still failing closed after a bounded window.
		config.MarkStaleTTL = 90 * time.Second
	}
	if config.FundingHistoryFallbackInterval <= 0 {
		config.FundingHistoryFallbackInterval = 5 * time.Minute
	}
	return &PaperBroker{
		config:                  config,
		prices:                  prices,
		initialBalance:          config.InitialBalance,
		balance:                 config.InitialBalance,
		positions:               make(map[string]PaperPosition),
		pendingOrders:           make(map[int64]PaperPendingOrder),
		marks:                   make(map[string]float64),
		markTimes:               make(map[string]time.Time),
		peakEquity:              config.InitialBalance,
		nextID:                  1,
		fundingSource:           config.FundingSource,
		clock:                   config.Clock,
		lastFundingTimes:        make(map[string]int64),
		fundingStatuses:         make(map[string]PaperFundingStatus),
		fundingHistoryReady:     make(map[string]bool),
		fundingHistoryRetry:     make(map[string]bool),
		fundingHistoryCheckedAt: make(map[string]time.Time),
		quarantinedPositions:    make(map[string]PaperPosition),
		quarantinedOrders:       make(map[int64]PaperPendingOrder),
	}, nil
}

func NewPersistentPaperBroker(config PaperBrokerConfig, prices PaperPriceSource, ledger PaperLedger, traderID string, closeRecorder PaperCloseRecorder) (*PaperBroker, error) {
	if ledger == nil || traderID == "" {
		return nil, fmt.Errorf("paper ledger and trader ID are required")
	}
	broker, err := NewPaperBroker(config, prices)
	if err != nil {
		return nil, err
	}
	broker.ledger = ledger
	broker.traderID = traderID
	broker.closeRecorder = closeRecorder
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
		broker.syncHistoricalClosedTrades()
	}
	broker.flushPendingCloseRecords()
	return broker, nil
}

func (b *PaperBroker) ExecuteDecision(decision *kernel.Decision) (PaperFill, error) {
	if decision == nil {
		return PaperFill{}, fmt.Errorf("paper decision is required")
	}
	if decision.Action == "hold" || decision.Action == "wait" {
		return PaperFill{Symbol: decision.Symbol, Action: decision.Action, Time: time.Now().UTC()}, nil
	}
	if decision.Action == "update_position" {
		return b.updateProtection(decision)
	}
	if decision.Action == "close_long" || decision.Action == "close_short" {
		side := "long"
		if decision.Action == "close_short" {
			side = "short"
		}
		if b.config.MakerFirst {
			return b.placeMakerClose(decision.Symbol, decision.Action, side)
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
		b.flushPendingCloseRecordsLocked()
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
	if b.config.MakerFirst {
		return b.placeMakerOpen(decision)
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
	if err := validatePaperExitPrices(decision.Action, fillPrice, decision.StopLoss, decision.TakeProfit); err != nil {
		return PaperFill{}, err
	}
	quantity := decision.PositionSizeUSD / fillPrice
	fee := decision.PositionSizeUSD * b.config.TakerFeeBPS / 10_000
	requiredMargin := decision.PositionSizeUSD / float64(decision.Leverage)
	availableBalance := b.balance - b.usedMarginLocked() - b.reservedMarginLocked()
	if requiredMargin+fee > availableBalance {
		return PaperFill{}, fmt.Errorf("paper available balance is insufficient for initial margin and fee")
	}
	now := b.now()
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
	b.markTimes[decision.Symbol] = now
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

func (b *PaperBroker) updateProtection(decision *kernel.Decision) (PaperFill, error) {
	currentPrice, err := b.GetMarketPrice(decision.Symbol)
	if err != nil {
		return PaperFill{}, fmt.Errorf("paper current market price: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	var key string
	var position PaperPosition
	for candidateKey, candidate := range b.positions {
		if strings.EqualFold(candidate.Symbol, decision.Symbol) {
			if key != "" {
				return PaperFill{}, fmt.Errorf("paper position side is ambiguous for %s", decision.Symbol)
			}
			key, position = candidateKey, candidate
		}
	}
	if key == "" {
		return PaperFill{}, fmt.Errorf("paper position not found for %s", decision.Symbol)
	}
	newStop, newTakeProfit := position.StopLoss, position.TakeProfit
	if decision.NewStopLoss > 0 {
		newStop = decision.NewStopLoss
	}
	if decision.NewTakeProfit > 0 {
		newTakeProfit = decision.NewTakeProfit
	}
	if err := validatePaperUpdateExitPrices("open_"+strings.ToLower(position.Side), currentPrice, newStop, newTakeProfit); err != nil {
		return PaperFill{}, err
	}

	now := b.now()
	if decision.NewStopLoss > 0 {
		position.StopLoss = decision.NewStopLoss
	}
	if decision.NewTakeProfit > 0 {
		position.TakeProfit = decision.NewTakeProfit
		for orderID, order := range b.pendingOrders {
			if strings.EqualFold(order.Symbol, position.Symbol) && paperIsTakeProfitAction(order.Action) {
				delete(b.pendingOrders, orderID)
				order.UpdatedAt = now
				b.recordOrderEventLocked(order, "CANCELED", "protection_replaced", 0, 0)
			}
		}
		tp := PaperPendingOrder{
			OrderID: b.nextID, Symbol: position.Symbol, Action: paperTakeProfitAction(position.Side),
			Side: position.Side, LimitPrice: position.TakeProfit,
			Quantity: position.Quantity, RemainingQuantity: position.Quantity,
			Leverage: position.Leverage, ReduceOnly: true, Status: "NEW",
			CreatedAt: now, UpdatedAt: now,
		}
		b.nextID++
		b.pendingOrders[tp.OrderID] = tp
		b.recordOrderEventLocked(tp, "NEW", "protection_updated", 0, 0)
	}
	b.positions[key] = position
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	return PaperFill{Symbol: position.Symbol, Action: decision.Action, Side: position.Side, Time: now}, nil
}

func (b *PaperBroker) placeMakerClose(symbol, action, side string) (PaperFill, error) {
	depths, ok := b.prices.(PaperDepthSource)
	if !ok {
		return PaperFill{}, fmt.Errorf("paper maker-first requires a read-only depth source")
	}
	b.mu.RLock()
	position, exists := b.positions[symbol+":"+side]
	b.mu.RUnlock()
	if !exists {
		return PaperFill{}, fmt.Errorf("paper %s position not found", side)
	}
	depth, err := depths.GetDepth(symbol, 5)
	if err != nil {
		return PaperFill{}, fmt.Errorf("paper maker depth %s: %w", symbol, err)
	}
	template := PaperPendingOrder{Symbol: symbol, Action: action, Side: side}
	limitPrice, err := paperMakerRestingPrice(template, depth)
	if err != nil {
		return PaperFill{}, err
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pending := range b.pendingOrders {
		if pending.ReduceOnly && pending.Symbol == symbol && pending.Side == side && !paperIsTakeProfitAction(pending.Action) {
			return PaperFill{}, fmt.Errorf("paper maker close order already pending for %s %s", symbol, side)
		}
	}
	b.cancelPendingReduceOnlyLocked(symbol, side)
	order := PaperPendingOrder{
		OrderID: b.nextID, Symbol: symbol, Action: action, Side: side,
		LimitPrice: limitPrice, Quantity: position.Quantity,
		RemainingQuantity: position.Quantity, Leverage: position.Leverage,
		ReduceOnly: true, Status: "NEW", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(b.config.MakerTimeout),
	}
	b.nextID++
	b.pendingOrders[order.OrderID] = order
	b.recordOrderEventLocked(order, "NEW", "", 0, 0)
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	return PaperFill{
		OrderID: order.OrderID, Symbol: order.Symbol, Action: order.Action,
		Side: order.Side, Leverage: order.Leverage, Price: order.LimitPrice,
		Quantity: order.Quantity, Time: now, Status: order.Status, IsMaker: true,
	}, nil
}

func (b *PaperBroker) placeMakerOpen(decision *kernel.Decision) (PaperFill, error) {
	depths, ok := b.prices.(PaperDepthSource)
	if !ok {
		return PaperFill{}, fmt.Errorf("paper maker-first requires a read-only depth source")
	}
	depth, err := depths.GetDepth(decision.Symbol, 5)
	if err != nil {
		return PaperFill{}, fmt.Errorf("paper maker depth %s: %w", decision.Symbol, err)
	}
	if depth == nil {
		return PaperFill{}, fmt.Errorf("paper maker depth %s is empty", decision.Symbol)
	}
	side := "long"
	levels := depth.Bids
	if decision.Action == "open_short" {
		side = "short"
		levels = depth.Asks
	}
	limitPrice, err := firstPaperDepthPrice(levels)
	if err != nil {
		return PaperFill{}, fmt.Errorf("paper maker %s price: %w", decision.Symbol, err)
	}
	if err := validatePaperExitPrices(decision.Action, limitPrice, decision.StopLoss, decision.TakeProfit); err != nil {
		return PaperFill{}, err
	}
	quantity := decision.PositionSizeUSD / limitPrice
	requiredMargin := decision.PositionSizeUSD / float64(decision.Leverage)
	estimatedFee := decision.PositionSizeUSD * b.config.MakerFeeBPS / 10_000
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pending := range b.pendingOrders {
		if !pending.ReduceOnly && pending.Symbol == decision.Symbol && pending.Side == side &&
			(pending.Status == "NEW" || pending.Status == "PARTIALLY_FILLED") {
			return PaperFill{}, fmt.Errorf("paper maker entry order already pending for %s %s", decision.Symbol, side)
		}
	}
	availableBalance := b.balance - b.usedMarginLocked() - b.reservedMarginLocked()
	if requiredMargin+estimatedFee > availableBalance {
		return PaperFill{}, fmt.Errorf("paper available balance is insufficient for maker order margin and fee")
	}
	order := PaperPendingOrder{
		OrderID: b.nextID, Symbol: decision.Symbol, Action: decision.Action,
		Side: side, LimitPrice: limitPrice, Quantity: quantity,
		RemainingQuantity: quantity, PositionSizeUSD: decision.PositionSizeUSD,
		Leverage: decision.Leverage, StopLoss: decision.StopLoss,
		TakeProfit: decision.TakeProfit, Status: "NEW", CreatedAt: now,
		UpdatedAt: now, ExpiresAt: now.Add(b.config.MakerTimeout),
	}
	b.pendingOrders[order.OrderID] = order
	b.recordOrderEventLocked(order, "NEW", "", 0, 0)
	b.nextID++
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	return PaperFill{
		OrderID: order.OrderID, Symbol: order.Symbol, Action: order.Action,
		Side: order.Side, Leverage: order.Leverage, Price: order.LimitPrice,
		Quantity: order.Quantity, Time: now, Status: order.Status, IsMaker: true,
	}, nil
}

func firstPaperDepthPrice(levels [][]string) (float64, error) {
	if len(levels) == 0 || len(levels[0]) < 2 {
		return 0, fmt.Errorf("best level is unavailable")
	}
	price, err := strconv.ParseFloat(levels[0][0], 64)
	if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("best level price %q is invalid", levels[0][0])
	}
	return price, nil
}

func (b *PaperBroker) usedMarginLocked() float64 {
	used := 0.0
	for _, position := range b.positions {
		used += position.InitialMargin
	}
	return used
}

func (b *PaperBroker) reservedMarginLocked() float64 {
	reserved := 0.0
	for _, order := range b.pendingOrders {
		if !order.ReduceOnly && (order.Status == "NEW" || order.Status == "PARTIALLY_FILLED") && order.Leverage > 0 {
			reserved += order.RemainingQuantity * order.LimitPrice / float64(order.Leverage)
		}
	}
	return reserved
}

func (b *PaperBroker) now() time.Time {
	if b.clock != nil {
		return b.clock().UTC()
	}
	return time.Now().UTC()
}

func (b *PaperBroker) SettleFunding(now time.Time) error {
	_, err := b.pollFunding(now, true)
	return err
}

func (b *PaperBroker) PollFunding(now time.Time) (PaperFundingPollResult, error) {
	return b.pollFunding(now, false)
}

func (b *PaperBroker) CatchUpFunding(now time.Time) (PaperFundingPollResult, error) {
	return b.pollFunding(now, true)
}

func (b *PaperBroker) pollFunding(now time.Time, forceHistory bool) (PaperFundingPollResult, error) {
	b.fundingPollMu.Lock()
	defer b.fundingPollMu.Unlock()
	result := PaperFundingPollResult{}
	if b.fundingSource == nil {
		return result, nil
	}
	if now.IsZero() {
		now = b.now()
	}
	now = now.UTC()
	b.mu.RLock()
	positions := make(map[string]PaperPosition, len(b.positions))
	for key, position := range b.positions {
		positions[key] = position
	}
	b.mu.RUnlock()
	var settlementErrors []error
	for key, position := range positions {
		result.SnapshotRequests++
		fundingSnapshot, snapshotErr := b.fundingSource.GetFundingSnapshot(position.Symbol)
		if snapshotErr != nil {
			b.mu.Lock()
			status := b.fundingStatuses[position.Symbol]
			status.Symbol = position.Symbol
			status.LastAttemptAt = now
			status.Stale = true
			status.Warning = snapshotErr.Error()
			b.fundingStatuses[position.Symbol] = status
			if err := b.persistLocked(); err != nil {
				settlementErrors = append(settlementErrors, err)
			}
			b.mu.Unlock()
			settlementErrors = append(settlementErrors, fmt.Errorf("paper funding snapshot %s: %w", position.Symbol, snapshotErr))
		} else if fundingSnapshot != nil {
			b.mu.Lock()
			status := b.fundingStatuses[position.Symbol]
			status.Symbol = position.Symbol
			status.UpdatedAt = now
			status.LastAttemptAt = now
			status.NextFundingTime = fundingSnapshot.NextFundingTime
			var invalidSnapshotErr error
			if isValidPaperMarkPrice(fundingSnapshot.MarkPrice) {
				status.MarkPrice = fundingSnapshot.MarkPrice
				b.marks[position.Symbol] = fundingSnapshot.MarkPrice
				b.markTimes[position.Symbol] = now
			} else {
				invalidSnapshotErr = fmt.Errorf("paper funding snapshot %s has invalid mark price", position.Symbol)
			}
			if isFinitePaperNumber(fundingSnapshot.Rate) {
				status.Rate = fundingSnapshot.Rate
			} else if invalidSnapshotErr == nil {
				invalidSnapshotErr = fmt.Errorf("paper funding snapshot %s has invalid funding rate", position.Symbol)
			}
			if isFinitePaperNumber(fundingSnapshot.IndexPrice) {
				status.IndexPrice = fundingSnapshot.IndexPrice
			} else if invalidSnapshotErr == nil {
				invalidSnapshotErr = fmt.Errorf("paper funding snapshot %s has invalid index price", position.Symbol)
			}
			if invalidSnapshotErr != nil {
				status.Stale = true
				status.Warning = invalidSnapshotErr.Error()
			} else {
				status.Stale = false
				if !status.HistoryPending {
					status.Warning = ""
				}
			}
			b.fundingStatuses[position.Symbol] = status
			if err := b.persistLocked(); err != nil {
				settlementErrors = append(settlementErrors, err)
			}
			b.mu.Unlock()
			if invalidSnapshotErr != nil {
				settlementErrors = append(settlementErrors, invalidSnapshotErr)
			}
		}

		if !b.shouldQueryFundingHistory(key, position.Symbol, now, forceHistory) {
			continue
		}
		result.HistoryRequests++
		applied, err := b.settleFundingHistoryForPosition(key, position, now)
		result.PaymentsApplied += applied
		if err != nil {
			settlementErrors = append(settlementErrors, err)
		}
	}
	return result, errors.Join(settlementErrors...)
}

func (b *PaperBroker) settleFundingHistoryForPosition(key string, position PaperPosition, now time.Time) (int, error) {
	b.mu.RLock()
	lastApplied := b.lastFundingTimes[key]
	b.mu.RUnlock()
	startTime := position.EntryTime.UnixMilli()
	if lastApplied >= startTime {
		startTime = lastApplied + 1
	}
	events, err := b.fundingSource.GetFundingHistory(position.Symbol, startTime, now.UnixMilli())
	if err != nil {
		var resultErrors []error
		if persistErr := b.recordFundingHistoryResult(key, position.Symbol, now, err); persistErr != nil {
			resultErrors = append(resultErrors, persistErr)
		}
		resultErrors = append(resultErrors, fmt.Errorf("paper funding history %s: %w", position.Symbol, err))
		return 0, errors.Join(resultErrors...)
	}
	var resultErrors []error
	applied := 0
	for _, event := range events {
		if event.FundingTime <= position.EntryTime.UnixMilli() || event.FundingTime > now.UnixMilli() || event.FundingTime <= lastApplied {
			continue
		}
		if !isValidPaperMarkPrice(event.MarkPrice) {
			eventErr := fmt.Errorf("paper funding event %s at %d has invalid mark price", position.Symbol, event.FundingTime)
			resultErrors = append(resultErrors, eventErr)
			break
		}
		if !isFinitePaperNumber(event.Rate) {
			eventErr := fmt.Errorf("paper funding event %s at %d has invalid funding rate", position.Symbol, event.FundingTime)
			resultErrors = append(resultErrors, eventErr)
			break
		}
		payment := math.Abs(position.Quantity) * event.MarkPrice * event.Rate
		if !isFinitePaperNumber(payment) {
			eventErr := fmt.Errorf("paper funding event %s at %d has invalid payment", position.Symbol, event.FundingTime)
			resultErrors = append(resultErrors, eventErr)
			break
		}
		walletDelta := -payment
		if position.Side == "short" {
			walletDelta = payment
		}
		b.mu.Lock()
		current, exists := b.positions[key]
		if !exists || !current.EntryTime.Equal(position.EntryTime) || b.lastFundingTimes[key] >= event.FundingTime {
			b.mu.Unlock()
			continue
		}
		b.balance += walletDelta
		b.lastFundingTimes[key] = event.FundingTime
		b.fundingPayments = append(b.fundingPayments, PaperFundingPayment{
			ID:     fmt.Sprintf("%s:%s:%d", position.Symbol, position.Side, event.FundingTime),
			Symbol: position.Symbol, Side: position.Side, Quantity: math.Abs(position.Quantity),
			MarkPrice: event.MarkPrice, FundingRate: event.Rate, FundingTime: event.FundingTime,
			Payment: payment, WalletDelta: walletDelta, AppliedAt: now,
		})
		lastApplied = event.FundingTime
		applied++
		b.updateDrawdownLocked()
		if err := b.persistLocked(); err != nil {
			resultErrors = append(resultErrors, err)
			b.mu.Unlock()
			break
		}
		b.mu.Unlock()
	}
	if persistErr := b.recordFundingHistoryResult(key, position.Symbol, now, errors.Join(resultErrors...)); persistErr != nil {
		resultErrors = append(resultErrors, persistErr)
	}
	return applied, errors.Join(resultErrors...)
}

func (b *PaperBroker) shouldQueryFundingHistory(key, symbol string, now time.Time, force bool) bool {
	if force {
		return true
	}
	b.mu.RLock()
	ready := b.fundingHistoryReady[key]
	retry := b.fundingHistoryRetry[key]
	checkedAt := b.fundingHistoryCheckedAt[key]
	status := b.fundingStatuses[symbol]
	lastApplied := b.lastFundingTimes[key]
	b.mu.RUnlock()
	if !ready || retry {
		return true
	}
	if status.NextFundingTime > 0 {
		if lastApplied >= status.NextFundingTime {
			return false
		}
		return now.UnixMilli() >= status.NextFundingTime
	}
	return checkedAt.IsZero() || now.Sub(checkedAt) >= b.config.FundingHistoryFallbackInterval
}

func (b *PaperBroker) recordFundingHistoryResult(key, symbol string, now time.Time, historyErr error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := b.fundingStatuses[symbol]
	status.Symbol = symbol
	if historyErr != nil {
		b.fundingHistoryRetry[key] = true
		status.HistoryPending = true
		status.Warning = historyErr.Error()
	} else {
		b.fundingHistoryReady[key] = true
		b.fundingHistoryRetry[key] = false
		b.fundingHistoryCheckedAt[key] = now
		status.HistoryPending = false
		if !status.Stale {
			status.Warning = ""
		}
	}
	b.fundingStatuses[symbol] = status
	return b.persistLocked()
}

// SettleFundingBeforeRiskExit performs history-only settlement for a symbol
// when cached funding state says catch-up is pending or the next event is due.
// It makes no funding request before those conditions become true.
func (b *PaperBroker) SettleFundingBeforeRiskExit(symbol string, now time.Time) (PaperFundingPollResult, error) {
	result := PaperFundingPollResult{}
	if b.fundingSource == nil {
		return result, nil
	}
	if now.IsZero() {
		now = b.now()
	}
	now = now.UTC()
	b.fundingPollMu.Lock()
	defer b.fundingPollMu.Unlock()
	b.mu.RLock()
	positions := make(map[string]PaperPosition)
	for key, position := range b.positions {
		if position.Symbol == symbol {
			positions[key] = position
		}
	}
	b.mu.RUnlock()
	var settlementErrors []error
	for key, position := range positions {
		if !b.shouldQueryFundingHistory(key, symbol, now, false) {
			continue
		}
		result.HistoryRequests++
		applied, err := b.settleFundingHistoryForPosition(key, position, now)
		result.PaymentsApplied += applied
		if err != nil {
			settlementErrors = append(settlementErrors, err)
		}
	}
	return result, errors.Join(settlementErrors...)
}

func (b *PaperBroker) RecentFundingPayments(limit int) []PaperFundingPayment {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.fundingPayments) {
		limit = len(b.fundingPayments)
	}
	start := len(b.fundingPayments) - limit
	return append([]PaperFundingPayment(nil), b.fundingPayments[start:]...)
}

func (b *PaperBroker) FundingStatuses() []PaperFundingStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	statuses := make([]PaperFundingStatus, 0, len(b.positions))
	seen := make(map[string]bool)
	for _, position := range b.positions {
		if seen[position.Symbol] {
			continue
		}
		if status, ok := b.fundingStatuses[position.Symbol]; ok {
			statuses = append(statuses, status)
			seen[position.Symbol] = true
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Symbol < statuses[j].Symbol })
	return statuses
}

func (b *PaperBroker) RefreshOpenPositions() error {
	b.mu.RLock()
	symbolSet := make(map[string]struct{}, len(b.positions))
	for _, position := range b.positions {
		symbolSet[position.Symbol] = struct{}{}
	}
	b.mu.RUnlock()
	now := b.now()
	var refreshErrors []error
	for symbol := range symbolSet {
		price, err := b.prices.GetMarketPrice(symbol)
		if err != nil {
			if age, ok := b.freshCachedMarkAge(symbol, now); ok {
				refreshErrors = append(refreshErrors, &PaperCachedMarkWarning{Symbol: symbol, Age: age, Cause: err})
				continue
			}
			refreshErrors = append(refreshErrors, fmt.Errorf("refresh paper price %s: cached mark unavailable or expired: %w", symbol, err))
			continue
		}
		if b.riskExitTriggered(symbol, price) {
			// Funding history is best-effort. A failed settlement remains pending
			// for retry, but must never suppress SL/liquidation (or non-maker TP)
			// evaluation against this valid market price.
			if _, err := b.SettleFundingBeforeRiskExit(symbol, now); err != nil {
				refreshErrors = append(refreshErrors, fmt.Errorf("settle paper funding before risk exit %s: %w", symbol, err))
			}
		}
		if _, err := b.ProcessPrice(symbol, price, now); err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("process paper price %s: %w", symbol, err))
		}
	}
	return errors.Join(refreshErrors...)
}

func (b *PaperBroker) riskExitTriggered(symbol string, price float64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, position := range b.positions {
		if position.Symbol == symbol && b.paperRiskExitAction(position, price) != "" {
			return true
		}
	}
	return false
}

func (b *PaperBroker) paperRiskExitAction(position PaperPosition, price float64) string {
	action := paperRiskExitAction(position, price)
	if b.config.MakerFirst && action == "take_profit" {
		return ""
	}
	return action
}

func paperRiskExitAction(position PaperPosition, price float64) string {
	if position.LiquidationPrice > 0 && ((position.Side == "long" && price <= position.LiquidationPrice) || (position.Side == "short" && price >= position.LiquidationPrice)) {
		return "liquidation"
	}
	if position.StopLoss > 0 && ((position.Side == "long" && price <= position.StopLoss) || (position.Side == "short" && price >= position.StopLoss)) {
		return "stop_loss"
	}
	if position.TakeProfit > 0 && ((position.Side == "long" && price >= position.TakeProfit) || (position.Side == "short" && price <= position.TakeProfit)) {
		return "take_profit"
	}
	return ""
}

func (b *PaperBroker) freshCachedMarkAge(symbol string, now time.Time) (time.Duration, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	mark, markExists := b.marks[symbol]
	markedAt, timeExists := b.markTimes[symbol]
	if !markExists || !isValidPaperMarkPrice(mark) || !timeExists || markedAt.IsZero() || now.Before(markedAt) {
		return 0, false
	}
	age := now.Sub(markedAt)
	return age, age <= b.config.MarkStaleTTL
}

// ProcessPrice marks virtual positions and produces synthetic market fills
// when a configured take-profit level is crossed.
func (b *PaperBroker) ProcessPrice(symbol string, price float64, at time.Time) ([]PaperFill, error) {
	if !isValidPaperMarkPrice(price) {
		return nil, fmt.Errorf("paper market price for %s is invalid", symbol)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.marks[symbol] = price
	b.markTimes[symbol] = at.UTC()
	exits := make([]PaperFill, 0, 1)
	for key, position := range b.positions {
		if position.Symbol != symbol {
			continue
		}
		action := b.paperRiskExitAction(position, price)
		if action == "" {
			continue
		}
		fill := b.closePositionLocked(position, action, price, at)
		delete(b.positions, key)
		b.cancelPendingOrdersLocked(position.Symbol, position.Side, "risk_exit", at.UTC())
		exits = append(exits, fill)
	}
	b.updateDrawdownLocked()
	if err := b.persistLocked(); err != nil {
		return nil, err
	}
	b.flushPendingCloseRecordsLocked()
	return exits, nil
}

func (b *PaperBroker) cancelPendingReduceOnlyLocked(symbol, side string) {
	for orderID, order := range b.pendingOrders {
		if order.ReduceOnly && order.Symbol == symbol && order.Side == side {
			delete(b.pendingOrders, orderID)
		}
	}
}

func (b *PaperBroker) cancelPendingOrdersLocked(symbol, side, reason string, at time.Time) {
	for orderID, order := range b.pendingOrders {
		if order.Symbol != symbol || order.Side != side {
			continue
		}
		delete(b.pendingOrders, orderID)
		order.UpdatedAt = at
		b.recordOrderEventLocked(order, "CANCELED", reason, 0, 0)
	}
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
		Positions: b.positions, Fills: b.fills, Marks: b.marks, MarkTimes: b.markTimes,
		PendingOrders: b.pendingOrders, OrderEvents: b.orderEvents,
		TotalFees: b.totalFees, ClosedTrades: b.closedTrades, Wins: b.wins,
		PendingCloseRecords: b.pendingCloseRecords,
		PeakEquity:          b.peakEquity, MaxDrawdown: b.maxDrawdown, NextID: b.nextID,
		FundingPayments: b.fundingPayments, LastFundingTimes: b.lastFundingTimes,
		FundingStatuses:     b.fundingStatuses,
		FundingHistoryReady: b.fundingHistoryReady, FundingHistoryRetry: b.fundingHistoryRetry,
		FundingHistoryCheckedAt: b.fundingHistoryCheckedAt,
		QuarantinedPositions:    b.quarantinedPositions, QuarantinedOrders: b.quarantinedOrders,
		RestoreWarnings: b.restoreWarnings,
	}
}

func (b *PaperBroker) restoreState(state paperBrokerState) (bool, error) {
	b.initialBalance = state.InitialBalance
	b.balance = state.Balance
	b.positions = state.Positions
	if b.positions == nil {
		b.positions = make(map[string]PaperPosition)
	}
	b.pendingOrders = state.PendingOrders
	if b.pendingOrders == nil {
		b.pendingOrders = make(map[int64]PaperPendingOrder)
	}
	b.orderEvents = state.OrderEvents
	b.quarantinedPositions = state.QuarantinedPositions
	if b.quarantinedPositions == nil {
		b.quarantinedPositions = make(map[string]PaperPosition)
	}
	b.quarantinedOrders = state.QuarantinedOrders
	if b.quarantinedOrders == nil {
		b.quarantinedOrders = make(map[int64]PaperPendingOrder)
	}
	b.restoreWarnings = append([]string(nil), state.RestoreWarnings...)
	migrated := false
	for key, position := range b.positions {
		if err := validateRestoredPaperPosition(position); err != nil {
			warning := fmt.Sprintf("restore paper position %s: quarantined invalid historical state: %v", key, err)
			b.quarantinedPositions[key] = position
			delete(b.positions, key)
			b.restoreWarnings = append(b.restoreWarnings, warning)
			migrated = true
			continue
		}
		if position.InitialMargin <= 0 {
			position.InitialMargin = position.EntryPrice * position.Quantity / float64(position.Leverage)
			b.positions[key] = position
			migrated = true
		}
	}
	for orderID, order := range b.pendingOrders {
		if order.ReduceOnly {
			if !paperIsTakeProfitAction(order.Action) {
				continue
			}
			position, ok := b.positions[order.Symbol+":"+strings.ToLower(order.Side)]
			if !ok {
				continue
			}
			if err := validatePaperExitPrices("open_"+strings.ToLower(position.Side), position.EntryPrice, 0, order.LimitPrice); err != nil {
				warning := fmt.Sprintf("restore paper pending take-profit %d: quarantined invalid historical state: %v", orderID, err)
				b.quarantinedOrders[orderID] = order
				delete(b.pendingOrders, orderID)
				b.restoreWarnings = append(b.restoreWarnings, warning)
				migrated = true
			}
			continue
		}
		if err := validatePaperExitPrices(order.Action, order.LimitPrice, order.StopLoss, order.TakeProfit); err != nil {
			warning := fmt.Sprintf("restore paper pending entry %d: quarantined invalid historical state: %v", orderID, err)
			b.quarantinedOrders[orderID] = order
			delete(b.pendingOrders, orderID)
			b.restoreWarnings = append(b.restoreWarnings, warning)
			migrated = true
		}
	}
	b.fills = state.Fills
	b.pendingCloseRecords = state.PendingCloseRecords
	b.marks = state.Marks
	if b.marks == nil {
		b.marks = make(map[string]float64)
	}
	b.markTimes = state.MarkTimes
	if b.markTimes == nil {
		b.markTimes = make(map[string]time.Time)
	}
	b.totalFees = state.TotalFees
	b.closedTrades = state.ClosedTrades
	b.wins = state.Wins
	b.peakEquity = state.PeakEquity
	b.maxDrawdown = state.MaxDrawdown
	b.nextID = state.NextID
	b.fundingPayments = state.FundingPayments
	b.lastFundingTimes = state.LastFundingTimes
	if b.lastFundingTimes == nil {
		b.lastFundingTimes = make(map[string]int64)
	}
	b.fundingStatuses = state.FundingStatuses
	if b.fundingStatuses == nil {
		b.fundingStatuses = make(map[string]PaperFundingStatus)
	}
	b.fundingHistoryReady = state.FundingHistoryReady
	if b.fundingHistoryReady == nil {
		b.fundingHistoryReady = make(map[string]bool)
	}
	b.fundingHistoryRetry = state.FundingHistoryRetry
	if b.fundingHistoryRetry == nil {
		b.fundingHistoryRetry = make(map[string]bool)
	}
	b.fundingHistoryCheckedAt = state.FundingHistoryCheckedAt
	if b.fundingHistoryCheckedAt == nil {
		b.fundingHistoryCheckedAt = make(map[string]time.Time)
	}
	if b.nextID <= 0 {
		b.nextID = 1
	}
	if b.config.MakerFirst {
		now := b.now()
		for _, position := range b.positions {
			if position.TakeProfit <= 0 || b.hasPendingTakeProfitLocked(position.Symbol, position.Side) {
				continue
			}
			order := PaperPendingOrder{
				OrderID: b.nextID, Symbol: position.Symbol,
				Action: paperTakeProfitAction(position.Side), Side: position.Side,
				LimitPrice: position.TakeProfit, Quantity: position.Quantity,
				RemainingQuantity: position.Quantity, Leverage: position.Leverage,
				ReduceOnly: true, Status: "NEW", CreatedAt: now, UpdatedAt: now,
			}
			b.nextID++
			b.pendingOrders[order.OrderID] = order
			b.recordOrderEventLocked(order, "NEW", "restored_position", 0, 0)
			migrated = true
		}
	}
	return migrated, nil
}

func (b *PaperBroker) hasPendingTakeProfitLocked(symbol, side string) bool {
	for _, order := range b.pendingOrders {
		if order.Symbol == symbol && order.Side == side && paperIsTakeProfitAction(order.Action) {
			return true
		}
	}
	return false
}

func validateRestoredPaperPosition(position PaperPosition) error {
	if position.Side != "long" && position.Side != "short" {
		return fmt.Errorf("unsupported position side %q", position.Side)
	}
	if !isFinitePositive(position.Quantity) {
		return fmt.Errorf("quantity must be a finite positive value, got %.8f", position.Quantity)
	}
	if position.Leverage <= 0 {
		return fmt.Errorf("leverage must be greater than zero")
	}
	return validatePaperExitPrices("open_"+position.Side, position.EntryPrice, position.StopLoss, position.TakeProfit)
}

// RestoreWarnings reports historical records that were quarantined during
// startup. Quarantined records are preserved in paper state for diagnostics,
// but are not active positions or orders and therefore cannot create an
// unprotected risk path.
func (b *PaperBroker) RestoreWarnings() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]string(nil), b.restoreWarnings...)
}

func (b *PaperBroker) QuarantinedPositions() map[string]PaperPosition {
	b.mu.RLock()
	defer b.mu.RUnlock()
	positions := make(map[string]PaperPosition, len(b.quarantinedPositions))
	for key, position := range b.quarantinedPositions {
		positions[key] = position
	}
	return positions
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
		RealizedPnL: netTradePnL, Time: at, Status: "FILLED", IsMaker: false,
	}
	b.nextID++
	b.fills = append(b.fills, fill)
	b.recordCloseLocked(position, fill, at, netTradePnL, exitFee)
	return fill
}

// recordCloseLocked notifies the close recorder (if any) that a simulated
// position was fully closed, so it can be persisted as a completed trade.
func (b *PaperBroker) recordCloseLocked(position PaperPosition, fill PaperFill, at time.Time, realizedPnL, fee float64) {
	rec := PaperClosedTrade{
		Symbol:      position.Symbol,
		Side:        position.Side,
		Quantity:    position.Quantity,
		EntryPrice:  position.EntryPrice,
		ExitOrderID: fill.OrderID,
		ExitPrice:   fill.Price,
		EntryTime:   position.EntryTime,
		ExitTime:    at,
		RealizedPnL: realizedPnL,
		Fee:         fee,
		Leverage:    position.Leverage,
		CloseReason: "paper",
	}
	b.pendingCloseRecords = append(b.pendingCloseRecords, rec)
}

func (b *PaperBroker) flushPendingCloseRecords() {
	if b.closeRecorder == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushPendingCloseRecordsLocked()
}

func (b *PaperBroker) flushPendingCloseRecordsLocked() {
	if b.closeRecorder == nil {
		return
	}
	for len(b.pendingCloseRecords) > 0 {
		rec := b.pendingCloseRecords[0]
		if err := b.closeRecorder.RecordPaperClose(rec, b.traderID); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
				fmt.Printf("paper close recorder failed for %s %s: %v\n", rec.Symbol, rec.Side, err)
				return
			}
		}
		b.pendingCloseRecords = b.pendingCloseRecords[1:]
		if err := b.persistLocked(); err != nil {
			b.pendingCloseRecords = append([]PaperClosedTrade{rec}, b.pendingCloseRecords...)
			fmt.Printf("paper close recorder state update failed for %s %s: %v\n", rec.Symbol, rec.Side, err)
			return
		}
	}
}

// syncHistoricalClosedTrades repairs the derived position history from the
// paper ledger. Fills are intentionally interpreted by the same performance
// reconstruction used by the runtime; opening fills are never recorded.
func (b *PaperBroker) syncHistoricalClosedTrades() {
	if b.closeRecorder == nil {
		return
	}
	b.mu.RLock()
	fills := append([]PaperFill(nil), b.fills...)
	initialBalance := b.initialBalance
	b.mu.RUnlock()
	for _, closed := range reconstructPaperPerformance(fills, initialBalance).ClosedTrades {
		if closed.ExitOrderID <= 0 {
			b.mu.Lock()
			b.restoreWarnings = append(b.restoreWarnings, fmt.Sprintf("paper historical close %s %s skipped: missing exit order ID", closed.Symbol, closed.Side))
			b.mu.Unlock()
			continue
		}
		if err := b.closeRecorder.RecordPaperClose(closed, b.traderID); err != nil {
			b.mu.Lock()
			b.restoreWarnings = append(b.restoreWarnings, fmt.Sprintf("paper historical close %s %s not recorded: %v", closed.Symbol, closed.Side, err))
			b.mu.Unlock()
		}
	}
}

func (b *PaperBroker) Snapshot() PaperSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshotLocked()
}

// HasActiveExecution reports whether the paper broker has any state that
// requires exchange-backed execution or risk refreshes. It is intentionally a
// cheap, read-only check used by the background monitor to avoid touching
// Binance while a paper trader is idle.
func (b *PaperBroker) HasActiveExecution() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.positions) > 0 || len(b.pendingOrders) > 0
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

func (b *PaperBroker) PendingOrders() []PaperPendingOrder {
	b.mu.RLock()
	defer b.mu.RUnlock()
	orders := make([]PaperPendingOrder, 0, len(b.pendingOrders))
	for _, order := range b.pendingOrders {
		orders = append(orders, order)
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].OrderID < orders[j].OrderID })
	return orders
}

func (b *PaperBroker) RecentOrderEvents(limit int) []PaperOrderEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.orderEvents) {
		limit = len(b.orderEvents)
	}
	start := len(b.orderEvents) - limit
	return append([]PaperOrderEvent(nil), b.orderEvents[start:]...)
}

func (b *PaperBroker) RefreshMakerOrders(at time.Time) ([]PaperFill, error) {
	if !b.config.MakerFirst {
		return nil, nil
	}
	depths, ok := b.prices.(PaperDepthSource)
	if !ok {
		return nil, fmt.Errorf("paper maker-first requires a read-only depth source")
	}
	if at.IsZero() {
		at = b.now()
	}
	orders := b.PendingOrders()
	fills := make([]PaperFill, 0)
	for _, order := range orders {
		depth, err := depths.GetDepth(order.Symbol, 20)
		if err != nil {
			return nil, fmt.Errorf("refresh paper maker depth %s: %w", order.Symbol, err)
		}
		available, err := paperMakerAvailableQuantity(order, depth)
		if err != nil {
			return nil, err
		}
		if available <= 0 {
			if !paperIsTakeProfitAction(order.Action) && !at.Before(order.ExpiresAt) {
				fallback, err := b.expireOrRepriceMakerOrder(order.OrderID, depth, at.UTC())
				if err != nil {
					return nil, err
				}
				if fallback != nil {
					price, priceErr := b.prices.GetMarketPrice(fallback.Symbol)
					if priceErr != nil {
						return nil, fmt.Errorf("paper maker close fallback price %s: %w", fallback.Symbol, priceErr)
					}
					fill, fillErr := b.applyTakerCloseFallback(*fallback, price, at.UTC())
					if fillErr != nil {
						return nil, fillErr
					}
					fills = append(fills, fill)
				}
			}
			continue
		}
		fill, err := b.applyMakerFill(order.OrderID, math.Min(available, order.RemainingQuantity), depth, at.UTC())
		if err != nil {
			return nil, err
		}
		fills = append(fills, fill)
	}
	return fills, nil
}

func (b *PaperBroker) expireOrRepriceMakerOrder(orderID int64, depth *market.BinanceDepthSnapshot, at time.Time) (*PaperPendingOrder, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	order, ok := b.pendingOrders[orderID]
	if !ok {
		return nil, nil
	}
	delete(b.pendingOrders, orderID)
	if order.RepriceCount >= b.config.MakerMaxReprices {
		order.UpdatedAt = at
		b.recordOrderEventLocked(order, "CANCELED", "max_reprices", 0, 0)
		if err := b.persistLocked(); err != nil {
			return nil, err
		}
		if order.ReduceOnly && (order.Action == "close_long" || order.Action == "close_short") {
			return &order, nil
		}
		return nil, nil
	}
	price, err := paperMakerRestingPrice(order, depth)
	if err != nil {
		b.pendingOrders[orderID] = order
		return nil, err
	}
	replacement := order
	replacement.OrderID = b.nextID
	replacement.LimitPrice = price
	replacement.Quantity = order.RemainingQuantity
	replacement.FilledQuantity = 0
	replacement.Status = "NEW"
	replacement.RepriceCount++
	replacement.CreatedAt = at
	replacement.UpdatedAt = at
	replacement.ExpiresAt = at.Add(b.config.MakerTimeout)
	b.nextID++
	b.pendingOrders[replacement.OrderID] = replacement
	b.recordOrderEventLocked(order, "CANCELED", "reprice", replacement.OrderID, 0)
	b.recordOrderEventLocked(replacement, "NEW", "reprice", 0, 0)
	return nil, b.persistLocked()
}

func (b *PaperBroker) recordOrderEventLocked(order PaperPendingOrder, status, reason string, replacementOrderID int64, filledQuantity float64) {
	b.orderEvents = append(b.orderEvents, PaperOrderEvent{
		OrderID: order.OrderID, ReplacementOrderID: replacementOrderID,
		Symbol: order.Symbol, Action: order.Action, Status: status, Reason: reason,
		LimitPrice: order.LimitPrice, Quantity: order.Quantity,
		FilledQuantity: filledQuantity, IsMaker: true, Time: order.UpdatedAt,
	})
}

func (b *PaperBroker) applyTakerCloseFallback(order PaperPendingOrder, marketPrice float64, at time.Time) (PaperFill, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := order.Symbol + ":" + order.Side
	position, ok := b.positions[key]
	if !ok {
		return PaperFill{}, fmt.Errorf("paper %s position not found for taker fallback", order.Side)
	}
	fill := b.closePositionLocked(position, order.Action, marketPrice, at)
	delete(b.positions, key)
	b.cancelPendingReduceOnlyLocked(order.Symbol, order.Side)
	b.updateDrawdownLocked()
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	b.flushPendingCloseRecordsLocked()
	return fill, nil
}

func paperMakerRestingPrice(order PaperPendingOrder, depth *market.BinanceDepthSnapshot) (float64, error) {
	if depth == nil {
		return 0, fmt.Errorf("paper maker depth %s is empty", order.Symbol)
	}
	if order.Action == "open_long" || order.Action == "close_short" || order.Action == "take_profit_short" {
		return firstPaperDepthPrice(depth.Bids)
	}
	return firstPaperDepthPrice(depth.Asks)
}

func paperTakeProfitAction(side string) string {
	if side == "short" {
		return "take_profit_short"
	}
	return "take_profit"
}

func paperIsTakeProfitAction(action string) bool {
	return action == "take_profit" || action == "take_profit_short"
}

func paperMakerAvailableQuantity(order PaperPendingOrder, depth *market.BinanceDepthSnapshot) (float64, error) {
	if depth == nil {
		return 0, fmt.Errorf("paper maker depth %s is empty", order.Symbol)
	}
	levels := depth.Bids
	buy := order.Action == "open_long" || order.Action == "close_short" || order.Action == "take_profit_short"
	if buy {
		levels = depth.Asks
	}
	available := 0.0
	for _, level := range levels {
		if len(level) < 2 {
			continue
		}
		price, priceErr := strconv.ParseFloat(level[0], 64)
		quantity, quantityErr := strconv.ParseFloat(level[1], 64)
		if priceErr != nil || quantityErr != nil || price <= 0 || quantity <= 0 {
			continue
		}
		crossed := price >= order.LimitPrice
		if buy {
			crossed = price <= order.LimitPrice
		}
		if crossed {
			available += quantity
		}
	}
	return available, nil
}

func (b *PaperBroker) applyMakerFill(orderID int64, quantity float64, depth *market.BinanceDepthSnapshot, at time.Time) (PaperFill, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	order, ok := b.pendingOrders[orderID]
	if !ok || quantity <= 0 {
		return PaperFill{}, fmt.Errorf("paper maker order %d is unavailable", orderID)
	}
	if order.ReduceOnly {
		return b.applyMakerReduceOnlyFillLocked(order, quantity, at)
	}
	if quantity > order.RemainingQuantity {
		quantity = order.RemainingQuantity
	}
	notional := quantity * order.LimitPrice
	fee := notional * b.config.MakerFeeBPS / 10_000
	key := order.Symbol + ":" + order.Side
	position := b.positions[key]
	oldQuantity := position.Quantity
	newQuantity := oldQuantity + quantity
	if oldQuantity == 0 {
		position = PaperPosition{
			Symbol: order.Symbol, Side: order.Side, Leverage: order.Leverage,
			StopLoss: order.StopLoss, TakeProfit: order.TakeProfit,
			EntryTime: at,
		}
	}
	position.EntryPrice = (position.EntryPrice*oldQuantity + order.LimitPrice*quantity) / newQuantity
	position.Quantity = newQuantity
	position.EntryFee += fee
	position.InitialMargin += notional / float64(order.Leverage)
	position.LiquidationPrice = paperLiquidationPrice(position.Side, position.EntryPrice, position.Leverage, b.config.MaintenanceMarginRatio)
	b.positions[key] = position
	b.balance -= fee
	b.totalFees += fee
	order.FilledQuantity += quantity
	order.RemainingQuantity = math.Max(0, order.Quantity-order.FilledQuantity)
	order.UpdatedAt = at
	status := "PARTIALLY_FILLED"
	if order.RemainingQuantity <= 1e-12 {
		status = "FILLED"
		delete(b.pendingOrders, orderID)
		if !order.ReduceOnly && order.TakeProfit > 0 {
			tp := PaperPendingOrder{
				OrderID: b.nextID, Symbol: order.Symbol, Action: paperTakeProfitAction(order.Side),
				Side: order.Side, LimitPrice: order.TakeProfit,
				Quantity: position.Quantity, RemainingQuantity: position.Quantity,
				Leverage: order.Leverage, ReduceOnly: true, Status: "NEW",
				CreatedAt: at, UpdatedAt: at,
			}
			b.nextID++
			b.pendingOrders[tp.OrderID] = tp
			b.recordOrderEventLocked(tp, "NEW", "entry_filled", 0, 0)
		}
	} else {
		order.Status = status
		b.pendingOrders[orderID] = order
	}
	mark := order.LimitPrice
	if bid, bidErr := firstPaperDepthPrice(depth.Bids); bidErr == nil {
		if ask, askErr := firstPaperDepthPrice(depth.Asks); askErr == nil {
			mark = (bid + ask) / 2
		}
	}
	b.marks[order.Symbol] = mark
	b.markTimes[order.Symbol] = at
	fill := PaperFill{
		OrderID: order.OrderID, Symbol: order.Symbol, Action: order.Action,
		Side: order.Side, Leverage: order.Leverage, Price: order.LimitPrice,
		Quantity: quantity, Fee: fee, Time: at, Status: status, IsMaker: true,
	}
	b.fills = append(b.fills, fill)
	b.recordOrderEventLocked(order, status, "", 0, quantity)
	b.updateDrawdownLocked()
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	return fill, nil
}

func (b *PaperBroker) applyMakerReduceOnlyFillLocked(order PaperPendingOrder, quantity float64, at time.Time) (PaperFill, error) {
	key := order.Symbol + ":" + order.Side
	position, ok := b.positions[key]
	if !ok {
		delete(b.pendingOrders, order.OrderID)
		if err := b.persistLocked(); err != nil {
			return PaperFill{}, err
		}
		return PaperFill{}, fmt.Errorf("paper %s position not found for maker reduce-only order", order.Side)
	}
	quantity = math.Min(quantity, math.Min(order.RemainingQuantity, position.Quantity))
	if quantity <= 0 {
		return PaperFill{}, fmt.Errorf("paper maker reduce-only quantity is unavailable")
	}
	ratio := quantity / position.Quantity
	entryFeePortion := position.EntryFee * ratio
	initialMarginPortion := position.InitialMargin * ratio
	grossPnL := (order.LimitPrice - position.EntryPrice) * quantity
	if position.Side == "short" {
		grossPnL = (position.EntryPrice - order.LimitPrice) * quantity
	}
	exitFee := order.LimitPrice * quantity * b.config.MakerFeeBPS / 10_000
	netTradePnL := grossPnL - entryFeePortion - exitFee
	b.balance += grossPnL - exitFee
	b.totalFees += exitFee
	position.Quantity -= quantity
	position.EntryFee -= entryFeePortion
	position.InitialMargin -= initialMarginPortion
	order.FilledQuantity += quantity
	order.RemainingQuantity = math.Max(0, order.Quantity-order.FilledQuantity)
	order.UpdatedAt = at
	status := "PARTIALLY_FILLED"
	if order.RemainingQuantity <= 1e-12 || position.Quantity <= 1e-12 {
		status = "FILLED"
		delete(b.pendingOrders, order.OrderID)
		delete(b.positions, key)
		b.closedTrades++
		if netTradePnL > 0 {
			b.wins++
		}
		for pendingID, pending := range b.pendingOrders {
			if pending.ReduceOnly && pending.Symbol == order.Symbol && pending.Side == order.Side {
				delete(b.pendingOrders, pendingID)
			}
		}
	} else {
		order.Status = status
		b.pendingOrders[order.OrderID] = order
		b.positions[key] = position
	}
	fill := PaperFill{
		OrderID: order.OrderID, Symbol: order.Symbol, Action: order.Action,
		Side: order.Side, Leverage: order.Leverage, Price: order.LimitPrice,
		Quantity: quantity, Fee: exitFee, RealizedPnL: netTradePnL,
		Time: at, Status: status, IsMaker: true,
	}
	b.fills = append(b.fills, fill)
	b.recordOrderEventLocked(order, status, "", 0, quantity)
	b.updateDrawdownLocked()
	if err := b.persistLocked(); err != nil {
		return PaperFill{}, err
	}
	return fill, nil
}

func paperLiquidationPrice(side string, entryPrice float64, leverage int, maintenanceMarginRatio float64) float64 {
	if leverage <= 1 {
		return 0
	}
	if side == "short" {
		return entryPrice * (1 + 1/float64(leverage) - maintenanceMarginRatio)
	}
	return math.Max(0, entryPrice*(1-1/float64(leverage)+maintenanceMarginRatio))
}

func paperMakerOrderCrossed(order PaperPendingOrder, depth *market.BinanceDepthSnapshot) (bool, error) {
	available, err := paperMakerAvailableQuantity(order, depth)
	return available > 0, err
}

func (b *PaperBroker) snapshotLocked() PaperSnapshot {
	unrealized := 0.0
	usedMargin := b.usedMarginLocked()
	reservedMargin := b.reservedMarginLocked()
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
	fundingNet, fundingPaid, fundingReceived := 0.0, 0.0, 0.0
	for _, payment := range b.fundingPayments {
		fundingNet += payment.WalletDelta
		if payment.WalletDelta < 0 {
			fundingPaid += -payment.WalletDelta
		} else {
			fundingReceived += payment.WalletDelta
		}
	}
	makerFees, takerFees := 0.0, 0.0
	for _, fill := range b.fills {
		if fill.IsMaker {
			makerFees += fill.Fee
		} else {
			takerFees += fill.Fee
		}
	}
	return PaperSnapshot{
		Balance: b.balance, Equity: b.balance + unrealized,
		AvailableBalance: b.balance - usedMargin - reservedMargin, UsedMargin: usedMargin,
		RealizedPnL:   b.balance - b.initialBalance,
		UnrealizedPnL: unrealized, Fees: b.totalFees,
		OpenPositions: len(b.positions), PendingOrders: len(b.pendingOrders), ClosedTrades: b.closedTrades,
		Wins: b.wins, WinRate: winRate, MaxDrawdown: b.maxDrawdown,
		FundingNet: fundingNet, FundingPaid: fundingPaid, FundingReceived: fundingReceived,
		MakerFees: makerFees, TakerFees: takerFees,
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

// GetProtectionBreakevenCosts uses the paper ledger's actual entry fee plus
// configured taker/slippage rates and funding settlements applied since this
// position opened. This is read-only and does not alter PaperBroker execution
// or protection replacement semantics.
func (b *PaperBroker) GetProtectionBreakevenCosts(symbol, side string, entryPrice, quantity float64) (protectionBreakevenCosts, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var position *PaperPosition
	for _, candidate := range b.positions {
		candidate := candidate
		if candidate.Symbol == symbol && strings.EqualFold(candidate.Side, side) {
			if position != nil {
				return protectionBreakevenCosts{}, fmt.Errorf("paper position side is ambiguous for %s", symbol)
			}
			position = &candidate
		}
	}
	if position == nil {
		return protectionBreakevenCosts{}, fmt.Errorf("paper position not found for %s %s", symbol, side)
	}
	fundingNet := 0.0
	for _, payment := range b.fundingPayments {
		if payment.Symbol == symbol && strings.EqualFold(payment.Side, side) && !payment.AppliedAt.Before(position.EntryTime) {
			fundingNet += payment.WalletDelta
		}
	}
	return protectionBreakevenCosts{
		EntryFeeQuote:    position.EntryFee,
		ExitFeeRate:      b.config.TakerFeeBPS / 10_000,
		FundingCostQuote: -fundingNet,
		SlippageRate:     b.config.SlippageBPS / 10_000,
		ProfitBufferRate: 0,
		Source:           "paper ledger actual entry fee + configured taker/slippage + settled funding since entry",
	}, nil
}

func (b *PaperBroker) GetBalance() (map[string]interface{}, error) {
	s := b.Snapshot()
	return map[string]interface{}{
		"totalWalletBalance":          s.Balance,
		"totalUnrealizedProfit":       s.UnrealizedPnL,
		"availableBalance":            s.AvailableBalance,
		"totalInitialMargin":          s.UsedMargin,
		"totalPositionInitialMargin":  s.UsedMargin,
		"totalOpenOrderInitialMargin": math.Max(0, s.Balance-s.UsedMargin-s.AvailableBalance),
		"totalEquity":                 s.Equity,
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
	b.flushPendingCloseRecordsLocked()
	return map[string]interface{}{"orderId": fill.OrderID, "avgPrice": fill.Price, "executedQty": fill.Quantity, "status": "FILLED"}, nil
}

func (b *PaperBroker) SetLeverage(string, int) error    { return nil }
func (b *PaperBroker) SetMarginMode(string, bool) error { return nil }
func (b *PaperBroker) CancelAllOrders(symbol string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for orderID, order := range b.pendingOrders {
		if symbol != "" && !strings.EqualFold(order.Symbol, symbol) {
			continue
		}
		delete(b.pendingOrders, orderID)
		order.UpdatedAt = now
		b.recordOrderEventLocked(order, "CANCELED", "cancel_all", 0, 0)
	}
	return b.persistLocked()
}
func (b *PaperBroker) CancelStopOrders(symbol string) error {
	return b.cancelOrdersMatching(symbol, func(order PaperPendingOrder) bool { return order.ReduceOnly })
}
func (b *PaperBroker) CancelStopLossOrders(symbol string) error {
	return b.cancelOrdersMatching(symbol, func(order PaperPendingOrder) bool { return strings.Contains(order.Action, "stop_loss") })
}
func (b *PaperBroker) CancelTakeProfitOrders(symbol string) error {
	return b.cancelOrdersMatching(symbol, func(order PaperPendingOrder) bool { return strings.Contains(order.Action, "take_profit") })
}

func (b *PaperBroker) cancelOrdersMatching(symbol string, matches func(PaperPendingOrder) bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for orderID, order := range b.pendingOrders {
		if (symbol == "" || strings.EqualFold(order.Symbol, symbol)) && matches(order) {
			delete(b.pendingOrders, orderID)
			order.UpdatedAt = now
			b.recordOrderEventLocked(order, "CANCELED", "cancel_requested", 0, 0)
		}
	}
	return b.persistLocked()
}
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
	newStop, newTakeProfit := position.StopLoss, position.TakeProfit
	if stop {
		newStop = value
	} else {
		newTakeProfit = value
	}
	if err := validatePaperExitPrices("open_"+strings.ToLower(position.Side), position.EntryPrice, newStop, newTakeProfit); err != nil {
		return err
	}
	if stop {
		position.StopLoss = value
	} else {
		position.TakeProfit = value
		if b.config.MakerFirst {
			for orderID, order := range b.pendingOrders {
				if strings.EqualFold(order.Symbol, position.Symbol) && order.Side == position.Side && paperIsTakeProfitAction(order.Action) {
					delete(b.pendingOrders, orderID)
					order.UpdatedAt = b.now()
					b.recordOrderEventLocked(order, "CANCELED", "protection_replaced", 0, 0)
				}
			}
			if value > 0 {
				now := b.now()
				order := PaperPendingOrder{
					OrderID: b.nextID, Symbol: position.Symbol,
					Action: paperTakeProfitAction(position.Side), Side: position.Side,
					LimitPrice: value, Quantity: position.Quantity,
					RemainingQuantity: position.Quantity, Leverage: position.Leverage,
					ReduceOnly: true, Status: "NEW", CreatedAt: now, UpdatedAt: now,
				}
				b.nextID++
				b.pendingOrders[order.OrderID] = order
				b.recordOrderEventLocked(order, "NEW", "protection_set", 0, 0)
			}
		}
	}
	b.positions[key] = position
	return b.persistLocked()
}

func (b *PaperBroker) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	id, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid paper order id %q: %w", orderID, err)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if order, ok := b.pendingOrders[id]; ok && (symbol == "" || strings.EqualFold(symbol, order.Symbol)) {
		return paperOrderStatusMap(order), nil
	}
	for i := len(b.orderEvents) - 1; i >= 0; i-- {
		event := b.orderEvents[i]
		if event.OrderID != id || (symbol != "" && !strings.EqualFold(symbol, event.Symbol)) {
			continue
		}
		return map[string]interface{}{
			"orderId": strconv.FormatInt(id, 10), "status": event.Status,
			"avgPrice": event.LimitPrice, "executedQty": event.FilledQuantity,
			"commission": 0.0, "reason": event.Reason,
		}, nil
	}
	return nil, fmt.Errorf("paper order %s not found", orderID)
}
func (b *PaperBroker) GetClosedPnL(time.Time, int) ([]tradertypes.ClosedPnLRecord, error) {
	return nil, nil
}
func (b *PaperBroker) GetOpenOrders(symbol string) ([]tradertypes.OpenOrder, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	orders := make([]tradertypes.OpenOrder, 0, len(b.pendingOrders))
	for _, order := range b.pendingOrders {
		if symbol != "" && !strings.EqualFold(symbol, order.Symbol) {
			continue
		}
		orders = append(orders, tradertypes.OpenOrder{
			OrderID: strconv.FormatInt(order.OrderID, 10), Symbol: order.Symbol,
			Side: paperPendingExchangeSide(order), PositionSide: strings.ToUpper(order.Side),
			Type: "LIMIT", Price: order.LimitPrice, Quantity: order.Quantity, Status: order.Status,
		})
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].OrderID < orders[j].OrderID })
	return orders, nil
}

func paperOrderStatusMap(order PaperPendingOrder) map[string]interface{} {
	return map[string]interface{}{
		"orderId": strconv.FormatInt(order.OrderID, 10), "status": order.Status,
		"avgPrice": order.LimitPrice, "executedQty": order.FilledQuantity,
		"origQty": order.Quantity, "commission": 0.0,
	}
}

func paperPendingExchangeSide(order PaperPendingOrder) string {
	if order.Action == "open_long" || order.Action == "close_short" || order.Action == "take_profit_short" {
		return "BUY"
	}
	return "SELL"
}

var _ Trader = (*PaperBroker)(nil)
