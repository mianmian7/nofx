import { describe, expect, it } from 'vitest'
import { buildUpdateTraderRequest } from './traderRequest'

describe('buildUpdateTraderRequest', () => {
  it('preserves existing configuration when editing a trader', () => {
    const request = buildUpdateTraderRequest({
      name: 'Inverse trader',
      ai_model_id: 'model-1',
      primary_model_name: 'gpt-5.6-terra',
      exchange_id: 'exchange-1',
      strategy_id: 'strategy-1',
      execution_mode: 'paper',
      initial_balance: 100,
      invert_signals: true,
      trading_symbols: 'BTCUSDT,ETHUSDT',
      custom_prompt: 'Keep the existing risk rules.',
      override_base_prompt: true,
    })

    expect(request.invert_signals).toBe(true)
    expect(request.primary_model_name).toBe('gpt-5.6-terra')
    expect(request.trading_symbols).toBe('BTCUSDT,ETHUSDT')
    expect(request.custom_prompt).toBe('Keep the existing risk rules.')
    expect(request.override_base_prompt).toBe(true)
  })
})
