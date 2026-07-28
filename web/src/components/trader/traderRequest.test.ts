import { describe, expect, it } from 'vitest'
import { buildUpdateTraderRequest } from './traderRequest'

describe('buildUpdateTraderRequest', () => {
  it('preserves inverse-signal mode when editing a trader', () => {
    const request = buildUpdateTraderRequest({
      name: 'Inverse trader',
      ai_model_id: 'model-1',
      exchange_id: 'exchange-1',
      strategy_id: 'strategy-1',
      execution_mode: 'paper',
      initial_balance: 100,
      invert_signals: true,
    })

    expect(request.invert_signals).toBe(true)
  })
})
