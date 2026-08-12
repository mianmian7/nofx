import { describe, expect, it } from 'vitest'
import type { CompetitionTraderData } from '../../types'
import {
  buildComparisonDisplayData,
  mapEquityHistoriesByTraderId,
  rebaseVisibleComparisonData,
} from './ComparisonChart'

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

describe('rebaseVisibleComparisonData', () => {
  it('rebases every short-period series from its first visible point', () => {
    const traders = [trader('alpha'), trader('beta')]
    const visibleData = [
      { alpha_pnl_pct: 12, alpha_equity: 112 },
      {
        alpha_pnl_pct: 15,
        alpha_equity: 115,
        beta_pnl_pct: -4,
        beta_equity: 96,
      },
      {
        alpha_pnl_pct: 10,
        alpha_equity: 110,
        beta_pnl_pct: 1,
        beta_equity: 101,
      },
    ]

    expect(rebaseVisibleComparisonData(visibleData, traders, 24)).toEqual([
      { alpha_pnl_pct: 0, alpha_equity: 112 },
      { alpha_pnl_pct: 3, alpha_equity: 115, beta_pnl_pct: 0, beta_equity: 96 },
      {
        alpha_pnl_pct: -2,
        alpha_equity: 110,
        beta_pnl_pct: 5,
        beta_equity: 101,
      },
    ])
  })

  it('preserves lifetime PnL values for All', () => {
    const traders = [trader('alpha')]
    const visibleData = [
      { alpha_pnl_pct: 12, alpha_equity: 112 },
      { alpha_pnl_pct: 15, alpha_equity: 115 },
    ]

    expect(rebaseVisibleComparisonData(visibleData, traders, 0)).toEqual(
      visibleData
    )
  })
})

describe('buildComparisonDisplayData', () => {
  it('uses the first point after the visible-point limit as the short-period baseline', () => {
    const alpha = trader('alpha')
    const combinedData = [10, 12, 15].map((pnl) => ({ alpha_pnl_pct: pnl }))

    expect(buildComparisonDisplayData(combinedData, [alpha], 24, 2)).toEqual([
      { alpha_pnl_pct: 0 },
      { alpha_pnl_pct: 3 },
    ])
  })

  it('keeps the limited All view on its lifetime baseline', () => {
    const alpha = trader('alpha')
    const combinedData = [10, 12, 15].map((pnl) => ({ alpha_pnl_pct: pnl }))

    expect(buildComparisonDisplayData(combinedData, [alpha], 0, 2)).toEqual([
      { alpha_pnl_pct: 12 },
      { alpha_pnl_pct: 15 },
    ])
  })
})
