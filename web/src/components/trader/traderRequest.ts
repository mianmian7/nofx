import type { CreateTraderRequest } from '../../types'

export function buildUpdateTraderRequest(
  traderConfig: CreateTraderRequest
): CreateTraderRequest {
  return {
    name: traderConfig.name,
    ai_model_id: traderConfig.ai_model_id,
    primary_model_name: traderConfig.primary_model_name,
    exchange_id: traderConfig.exchange_id,
    strategy_id: traderConfig.strategy_id,
    scan_interval_minutes: traderConfig.scan_interval_minutes,
    is_cross_margin: traderConfig.is_cross_margin,
    show_in_competition: traderConfig.show_in_competition,
    invert_signals: traderConfig.invert_signals,
    trading_symbols: traderConfig.trading_symbols,
    custom_prompt: traderConfig.custom_prompt,
    override_base_prompt: traderConfig.override_base_prompt,
    execution_mode: traderConfig.execution_mode,
    initial_balance: traderConfig.initial_balance,
    reset_paper_account: traderConfig.reset_paper_account,
    startup_delay_minutes: traderConfig.startup_delay_minutes,
    fallback_model_names: traderConfig.fallback_model_names,
    fallback_ai_model_ids: traderConfig.fallback_ai_model_ids,
  }
}
