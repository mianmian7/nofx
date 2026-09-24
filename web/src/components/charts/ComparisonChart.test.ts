import { describe, expect, it } from 'vitest'
import type { CompetitionTraderData } from '../../types'
import {
  buildComparisonDisplayData,
  calculateComparisonTicks,
  downsampleData,
  formatComparisonTick,
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

describe('downsampleData', () => {
  it('returns original data if length is within maxPoints limit', () => {
    const data = [1, 2, 3]
    expect(downsampleData(data, 5)).toEqual([1, 2, 3])
  })

  it('samples evenly preserving both the very first and very last points', () => {
    const data = [10, 20, 30, 40, 50]
    const sampled = downsampleData(data, 3)
    expect(sampled).toEqual([10, 30, 50])
  })

  it('downsamples large time series without discarding earlier historical range', () => {
    const timestamps = Array.from({ length: 1000 }, (_, i) => ({
      ts: i,
    }))
    const sampled = downsampleData(timestamps, 100)
    expect(sampled).toHaveLength(100)
    expect(sampled[0].ts).toBe(0)
    expect(sampled[sampled.length - 1].ts).toBe(999)
  })
})

describe('buildComparisonDisplayData', () => {
  it('preserves the full time window from the start of the period and rebases from the period start', () => {
    const alpha = trader('alpha')
    // Combined data over a multi-day period: start is 10, middle is 12, end is 15
    const combinedData = [10, 12, 15].map((pnl) => ({ alpha_pnl_pct: pnl }))

    // When downsampling to 2 points, first (10) and last (15) are kept; rebased: 10-10=0, 15-10=5
    expect(buildComparisonDisplayData(combinedData, [alpha], 24, 2)).toEqual([
      { alpha_pnl_pct: 0 },
      { alpha_pnl_pct: 5 },
    ])
  })

  it('keeps the downsampled All view on its lifetime baseline', () => {
    const alpha = trader('alpha')
    const combinedData = [10, 12, 15].map((pnl) => ({ alpha_pnl_pct: pnl }))

    expect(buildComparisonDisplayData(combinedData, [alpha], 0, 2)).toEqual([
      { alpha_pnl_pct: 10 },
      { alpha_pnl_pct: 15 },
    ])
  })
})

describe('calculateComparisonTicks', () => {
  it('returns all timestamps when data points are fewer than target count', () => {
    const data = [
      { timestamp: '2026-09-13T08:00:00Z' },
      { timestamp: '2026-09-13T12:00:00Z' },
    ]
    expect(calculateComparisonTicks(data, 5)).toEqual([
      '2026-09-13T08:00:00Z',
      '2026-09-13T12:00:00Z',
    ])
  })

  it('samples evenly including first and last timestamps without duplicates', () => {
    const data = [
      { timestamp: '2026-09-13T00:00:00Z' },
      { timestamp: '2026-09-13T06:00:00Z' },
      { timestamp: '2026-09-13T12:00:00Z' },
      { timestamp: '2026-09-13T18:00:00Z' },
      { timestamp: '2026-09-14T00:00:00Z' },
    ]
    const ticks = calculateComparisonTicks(data, 3)
    expect(ticks).toEqual([
      '2026-09-13T00:00:00Z',
      '2026-09-13T12:00:00Z',
      '2026-09-14T00:00:00Z',
    ])
  })

  it('handles empty data gracefully', () => {
    expect(calculateComparisonTicks([], 5)).toEqual([])
  })
})

describe('formatComparisonTick', () => {
  it('formats as HH:mm for short spans (<= 26 hours)', () => {
    const ts = '2026-09-13T08:30:00.000Z'
    const formatted = formatComparisonTick(ts, 24)
    expect(formatted).toMatch(/^\d{2}:\d{2}$/)
  })

  it('formats as M/D HH:mm when span <= 96 hours or includeHours is true', () => {
    const ts = '2026-09-13T08:30:00.000Z'
    const formatted = formatComparisonTick(ts, 48)
    expect(formatted).toMatch(/^\d{1,2}\/\d{1,2} \d{2}:\d{2}$/)

    const longWithHours = formatComparisonTick(ts, 120, true)
    expect(longWithHours).toMatch(/^\d{1,2}\/\d{1,2} \d{2}:\d{2}$/)
  })

  it('formats as M/D for long spans (> 96 hours) without duplicates', () => {
    const ts = '2026-09-13T08:30:00.000Z'
    const formatted = formatComparisonTick(ts, 168, false)
    expect(formatted).toMatch(/^\d{1,2}\/\d{1,2}$/)
  })
})
