export interface SystemStatus {
  trader_id: string
  trader_name: string
  ai_model: string
  is_running: boolean
  start_time: string
  runtime_minutes: number
  call_count: number
  last_persisted_cycle?: number
  initial_balance: number
  scan_interval: string
  cycle_timeout?: string
  stop_until: string
  last_reset_time: string
  ai_provider: string
  active_model_id?: string
  is_fallback?: boolean
  fallback_reason?: string
  fallback_since?: string
  execution_mode?: 'paper' | 'live'
  cycle_phase?: string
  cycle_started_at?: string
  last_cycle_completed_at?: string
  last_cycle_error?: string
  invert_signals?: boolean
  startup_delay_minutes?: number
  fallback_model_names?: string[]
  fallback_ai_model_ids?: string[]
  paper?: {
    balance: number
    equity: number
    available_balance: number
    used_margin: number
    realized_pnl: number
    unrealized_pnl: number
    fees: number
    maker_fees?: number
    taker_fees?: number
    open_positions: number
    pending_orders?: number
    closed_trades: number
    wins: number
    win_rate: number
    max_drawdown: number
    funding_net?: number
    funding_paid?: number
    funding_received?: number
  }
  paper_performance?: PaperPerformance
  paper_recent_funding?: PaperFundingPayment[]
  paper_funding_status?: PaperFundingStatus[]
  paper_pending_orders?: PaperPendingOrder[]
  paper_order_events?: PaperOrderEvent[]
  strategy_type?: 'ai_trading' | 'grid_trading'
  grid_symbol?: string
  /** Runtime health: true when AI failed repeatedly and no new positions open. */
  safe_mode?: boolean
  safe_mode_reason?: string
}

export interface PaperFundingPayment {
  id: string
  symbol: string
  side: string
  quantity: number
  mark_price: number
  funding_rate: number
  funding_time: number
  payment: number
  wallet_delta: number
  applied_at: string
}

export interface PaperFundingStatus {
  symbol: string
  funding_rate: number
  mark_price: number
  index_price: number
  next_funding_time: number
  updated_at: string
}

export interface PaperPendingOrder {
  order_id: number
  symbol: string
  action: string
  side: string
  limit_price: number
  quantity: number
  filled_quantity: number
  remaining_quantity: number
  position_size_usd: number
  leverage: number
  stop_loss?: number
  take_profit?: number
  reduce_only: boolean
  status: string
  reprice_count: number
  created_at: string
  updated_at: string
  expires_at: string
}

export interface PaperOrderEvent {
  order_id: number
  replacement_order_id?: number
  symbol: string
  action: string
  status: string
  reason?: string
  limit_price: number
  quantity: number
  filled_quantity?: number
  is_maker: boolean
  time: string
}

export interface PaperClosedTrade {
  entry_order_id: number
  exit_order_id: number
  symbol: string
  side: 'long' | 'short' | string
  quantity: number
  entry_price: number
  exit_price: number
  entry_time: string
  exit_time: string
  leverage: number
  entry_fee: number
  exit_fee: number
  fee: number
  realized_pnl: number
  close_reason: string
}

export interface PaperPerformance {
  total_trades: number
  win_trades: number
  loss_trades: number
  win_rate: number
  profit_factor: number
  sharpe_ratio: number
  total_pnl: number
  /** All Paper fills, including entry fees for positions that remain open. */
  total_fees: number
  maker_fees?: number
  taker_fees?: number
  /** Fees belonging only to completed Paper trades. */
  closed_trade_fees: number
  avg_win: number
  avg_loss: number
  max_drawdown_pct: number
  closed_trades: PaperClosedTrade[]
}

export interface AccountInfo {
  total_equity: number
  wallet_balance: number
  unrealized_profit: number // Unrealized PnL (official value from the exchange API)
  available_balance: number
  total_pnl: number
  total_pnl_pct: number
  initial_balance: number
  daily_pnl: number
  position_count: number
  margin_used: number
  margin_used_pct: number
}

export interface Position {
  symbol: string
  side: string
  entry_price: number
  mark_price: number
  quantity: number
  leverage: number
  unrealized_pnl: number
  unrealized_pnl_pct: number
  liquidation_price: number
  margin_used: number
}

export interface DecisionAction {
  action: string
  symbol: string
  quantity: number
  leverage: number
  price: number
  stop_loss?: number // Stop loss price
  take_profit?: number // Take profit price
  confidence?: number // AI confidence (0-100)
  reasoning?: string // Brief reasoning
  order_id: number
  timestamp: string
  success: boolean
  error?: string
}

export interface AccountSnapshot {
  total_balance: number
  available_balance: number
  total_unrealized_profit: number
  position_count: number
  margin_used_pct: number
}

export interface DecisionRecord {
  timestamp: string
  cycle_number: number
  system_prompt: string
  input_prompt: string
  cot_trace: string
  decision_json: string
  raw_response?: string
  account_state: AccountSnapshot
  positions: any[]
  candidate_coins: string[]
  decisions: DecisionAction[]
  execution_log: string[]
  success: boolean
  error_message?: string
}

export interface Statistics {
  total_cycles: number
  successful_cycles: number
  failed_cycles: number
  total_open_positions: number
  total_close_positions: number
}

// Full trade-quality metrics from GET /api/statistics/full (store.TraderStats).
// Derived from CLOSED positions — the same numbers the AI sees each cycle.
export interface TraderFullStats {
  total_trades: number
  win_trades: number
  loss_trades: number
  win_rate: number // percentage, 0-100
  profit_factor: number
  sharpe_ratio: number
  total_pnl: number
  total_fee: number
  avg_win: number
  avg_loss: number
  /** Percent, not a fraction: 18.5 means -18.5% peak drawdown. */
  max_drawdown_pct: number
}

// AI Trading related types
export interface TraderInfo {
  trader_id: string
  trader_name: string
  ai_model: string
  primary_model_name?: string
  exchange_id?: string
  exchange_type?: string
  is_running?: boolean
  execution_mode?: 'paper' | 'live'
  startup_warning?: string
  show_in_competition?: boolean
  strategy_id?: string
  strategy_name?: string
  custom_prompt?: string
  system_prompt_template?: string
  invert_signals?: boolean
  startup_delay_minutes?: number
  fallback_model_names?: string[]
  fallback_ai_model_ids?: string[]
}

// Competition related types
export interface CompetitionTraderData {
  trader_id: string
  trader_name: string
  ai_model: string
  exchange: string
  total_equity: number
  total_pnl: number
  total_pnl_pct: number
  position_count: number
  margin_used_pct: number
  is_running: boolean
  invert_signals?: boolean
}

export interface CompetitionData {
  traders: CompetitionTraderData[]
  count: number
}

// Public trader configuration returned by the competition endpoint.
export interface PublicTraderConfigData {
  trader_id: string
  trader_name: string
  ai_model: string
  exchange: string
  is_running: boolean
  invert_signals?: boolean
  ai_provider?: string
  start_time?: string
}

// Trader Configuration Data for View Modal
export interface TraderConfigData {
  trader_id?: string
  trader_name: string
  ai_model: string
  primary_model_name?: string
  exchange_id: string
  exchange_type?: string
  strategy_id?: string // Strategy ID
  strategy_name?: string // Strategy name
  is_cross_margin: boolean
  show_in_competition: boolean // Whether to show in the competition arena
  invert_signals?: boolean // Whether to invert AI trading decisions
  scan_interval_minutes: number
  startup_delay_minutes?: number
  fallback_model_names?: string[]
  fallback_ai_model_ids?: string[]
  initial_balance: number
  is_running: boolean
  execution_mode?: 'paper' | 'live'
  max_leverage?: number
  max_positions?: number
  trading_symbols?: string
  custom_prompt?: string
  override_base_prompt?: boolean
  system_prompt_template?: string
}

// Position History Types
export interface HistoricalPosition {
  id: number
  trader_id: string
  exchange_id: string
  exchange_type: string
  symbol: string
  side: string
  quantity: number
  entry_quantity: number
  entry_price: number
  entry_order_id: string
  /** Epoch milliseconds. */
  entry_time: number
  exit_price: number
  exit_order_id: string
  /** Epoch milliseconds. */
  exit_time: number
  realized_pnl: number
  fee: number
  leverage: number
  status: string
  close_reason: string
  created_at: string
  updated_at: string
}

// Matches Go TraderStats struct exactly
export interface TraderStats {
  total_trades: number
  win_trades: number
  loss_trades: number
  win_rate: number
  profit_factor: number
  sharpe_ratio: number
  total_pnl: number
  total_fee: number
  avg_win: number
  avg_loss: number
  max_drawdown_pct: number
}

// Matches Go SymbolStats struct exactly
export interface SymbolStats {
  symbol: string
  total_trades: number
  win_trades: number
  win_rate: number
  total_pnl: number
  avg_pnl: number
  avg_hold_mins: number
}

// Matches Go DirectionStats struct exactly
export interface DirectionStats {
  side: string
  trade_count: number
  win_rate: number
  total_pnl: number
  avg_pnl: number
}

export interface PositionHistoryResponse {
  positions: HistoricalPosition[]
  stats: TraderStats | null
  symbol_stats: SymbolStats[]
  direction_stats: DirectionStats[]
}

// Grid Risk Information for frontend display
export interface GridRiskInfo {
  // Leverage info
  current_leverage: number
  effective_leverage: number
  recommended_leverage: number

  // Position info
  current_position: number
  max_position: number
  position_percent: number

  // Liquidation info
  liquidation_price: number
  liquidation_distance: number

  // Market state
  regime_level: string

  // Box state
  short_box_upper: number
  short_box_lower: number
  mid_box_upper: number
  mid_box_lower: number
  long_box_upper: number
  long_box_lower: number
  current_price: number

  // Breakout state
  breakout_level: string
  breakout_direction: string
}
