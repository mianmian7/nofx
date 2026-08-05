import { describe, expect, it } from 'vitest'
import type { CompetitionTraderData } from '../../types'
import { mapEquityHistoriesByTraderId } from './ComparisonChart'

const trader = (trader_id: string): CompetitionTraderData => ({
  trader_id,
  trader_name: trader_id,
  ai_model: 'openai',
  exchange: 'binance',
  total_equity: 10,
  total_pnl: 0,
  total_pnl_pct: 0,
  position_count: 0,
  margin_used_pct: 0,
  is_running: false,
})

describe('mapEquityHistoriesByTraderId', () => {
  it('keeps each history attached to its trader when ranking order changes', () => {
    const gemini = trader('gemini')
    const autopilot = trader('autopilot')
    const histories = {
      gemini: [{ total_pnl_pct: 64.59 }],
      autopilot: [{ total_pnl_pct: 64.76 }],
    }

    const firstOrder = mapEquityHistoriesByTraderId(
      [autopilot, gemini],
      histories
    )
    const reordered = mapEquityHistoriesByTraderId(
      [gemini, autopilot],
      histories
    )

    expect(firstOrder.autopilot[0].total_pnl_pct).toBe(64.76)
    expect(firstOrder.gemini[0].total_pnl_pct).toBe(64.59)
    expect(reordered.gemini[0].total_pnl_pct).toBe(64.59)
    expect(reordered.autopilot[0].total_pnl_pct).toBe(64.76)
  })
})
