import type { CreateTraderRequest } from '../../types'

export function buildUpdateTraderRequest(
  traderConfig: CreateTraderRequest
): CreateTraderRequest {
  return {
    name: traderConfig.name,
    ai_model_id: traderConfig.ai_model_id,
    exchange_id: traderConfig.exchange_id,
    strategy_id: traderConfig.strategy_id,
    scan_interval_minutes: traderConfig.scan_interval_minutes,
    is_cross_margin: traderConfig.is_cross_margin,
    show_in_competition: traderConfig.show_in_competition,
    invert_signals: traderConfig.invert_signals,
    execution_mode: traderConfig.execution_mode,
    initial_balance: traderConfig.initial_balance,
    reset_paper_account: traderConfig.reset_paper_account,
  }
}
