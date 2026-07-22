import type { Strategy, StrategyConfig } from '../../types'
import { API_BASE, httpClient } from './helpers'

export interface BacktestTrade {
  side: 'long' | 'short'
  entry_time: number
  exit_time: number
  entry_price: number
  exit_price: number
  net_pnl: number
  exit_reason: string
}

export interface BacktestResult {
  symbol: string
  initial_balance: number
  final_equity: number
  total_return_pct: number
  max_drawdown_pct: number
  trade_count: number
  win_rate_pct: number
  profit_factor: number | null
  decision_calls: number
  execution_policy: string
  trades: BacktestTrade[]
}

export interface BacktestJob {
  id: string
  strategy_id: string
  mode: 'trend_v1' | 'ai_replay'
  status: 'queued' | 'running' | 'completed' | 'failed'
  stage: string
  decision_progress: number
  max_ai_calls: number
  error?: string
  result?: BacktestResult
}

export const strategyApi = {
  async getStrategies(): Promise<Strategy[]> {
    const result = await httpClient.get<{ strategies: Strategy[] }>(
      `${API_BASE}/strategies`
    )
    if (!result.success) throw new Error('Failed to fetch strategy list')
    const strategies = result.data?.strategies
    return Array.isArray(strategies) ? strategies : []
  },

  async getStrategy(strategyId: string): Promise<Strategy> {
    const result = await httpClient.get<Strategy>(
      `${API_BASE}/strategies/${strategyId}`
    )
    if (!result.success) throw new Error('Failed to fetch strategy')
    return result.data!
  },

  async getActiveStrategy(): Promise<Strategy> {
    const result = await httpClient.get<Strategy>(
      `${API_BASE}/strategies/active`
    )
    if (!result.success) throw new Error('Failed to fetch active strategy')
    return result.data!
  },

  async getDefaultStrategyConfig(): Promise<StrategyConfig> {
    const result = await httpClient.get<StrategyConfig>(
      `${API_BASE}/strategies/default-config`
    )
    if (!result.success)
      throw new Error('Failed to fetch default strategy config')
    return result.data!
  },

  async createStrategy(data: {
    name: string
    description: string
    config: StrategyConfig
  }): Promise<Strategy> {
    const result = await httpClient.post<Strategy>(
      `${API_BASE}/strategies`,
      data
    )
    if (!result.success) throw new Error('Failed to create strategy')
    return result.data!
  },

  async updateStrategy(
    strategyId: string,
    data: {
      name?: string
      description?: string
      config?: StrategyConfig
    }
  ): Promise<Strategy> {
    const result = await httpClient.put<Strategy>(
      `${API_BASE}/strategies/${strategyId}`,
      data
    )
    if (!result.success) throw new Error('Failed to update strategy')
    return result.data!
  },

  async deleteStrategy(strategyId: string): Promise<void> {
    const result = await httpClient.delete(
      `${API_BASE}/strategies/${strategyId}`
    )
    if (!result.success) throw new Error('Failed to delete strategy')
  },

  async activateStrategy(strategyId: string): Promise<Strategy> {
    const result = await httpClient.post<Strategy>(
      `${API_BASE}/strategies/${strategyId}/activate`
    )
    if (!result.success) throw new Error('Failed to activate strategy')
    return result.data!
  },

  async duplicateStrategy(strategyId: string): Promise<Strategy> {
    const result = await httpClient.post<Strategy>(
      `${API_BASE}/strategies/${strategyId}/duplicate`
    )
    if (!result.success) throw new Error('Failed to duplicate strategy')
    return result.data!
  },

  async startStrategyBacktest(
    strategyId: string,
    data: {
      mode: 'trend_v1' | 'ai_replay'
      ai_model_id?: string
      symbol: string
      timeframe: string
      start_time: string
      end_time: string
      initial_balance: number
      fee_bps: number
      slippage_bps: number
      max_ai_calls: number
      confirm_ai_calls: boolean
    }
  ): Promise<BacktestJob> {
    const result = await httpClient.post<BacktestJob>(
      `${API_BASE}/strategies/${strategyId}/backtests`,
      data
    )
    if (!result.success) throw new Error('Failed to start historical replay')
    return result.data!
  },

  async getStrategyBacktest(jobId: string): Promise<BacktestJob> {
    const result = await httpClient.get<BacktestJob>(
      `${API_BASE}/strategy-backtests/${jobId}`
    )
    if (!result.success) throw new Error('Failed to get historical replay')
    return result.data!
  },
}
