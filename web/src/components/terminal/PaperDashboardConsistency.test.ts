import { describe, expect, it } from 'vitest'
import type {
  PositionHistoryResponse,
  SystemStatus,
  TraderFullStats,
} from '../../types'
import { resolveDashboardPerformance } from './TerminalDashboard'

describe('Paper dashboard performance source', () => {
  it('uses persistent Paper trades instead of contradictory zero live-table stats', () => {
    const liveStats: TraderFullStats = {
      total_trades: 0,
      win_trades: 0,
      loss_trades: 0,
      win_rate: 0,
      profit_factor: 0,
      sharpe_ratio: 0,
      total_pnl: 0,
      total_fee: 0,
      avg_win: 0,
      avg_loss: 0,
      max_drawdown_pct: 0,
    }
    const liveHistory: PositionHistoryResponse = {
      positions: [],
      stats: liveStats,
      symbol_stats: [],
      direction_stats: [],
    }
    const status = {
      execution_mode: 'paper',
      paper_performance: {
        total_trades: 3,
        win_trades: 1,
        loss_trades: 2,
        win_rate: 100 / 3,
        profit_factor: 1.2075,
        sharpe_ratio: 0.1,
        total_pnl: Number('13.518084153188568'),
        total_fees: Number('12.541468107008955'),
        closed_trade_fees: Number('11.039368107008955'),
        avg_win: Number('78.63929016543888'),
        avg_loss: Number('32.560603006125155'),
        max_drawdown_pct: Number('0.4761142556929577'),
        closed_trades: [
          {
            entry_order_id: 5,
            exit_order_id: 6,
            symbol: 'SNDKUSDT',
            side: 'long',
            quantity: Number('2.4525190322681176'),
            entry_price: 1630.97613,
            exit_price: 1625.464842,
            entry_time: '2026-07-21T21:08:00.02886671Z',
            exit_time: '2026-07-21T22:22:47.518006514Z',
            leverage: 0,
            entry_fee: 2,
            exit_fee: Number('1.9932417306438446'),
            fee: Number('3.9932417306438446'),
            realized_pnl: Number('-17.509780442954543'),
            close_reason: 'close_long',
          },
          {
            entry_order_id: 3,
            exit_order_id: 4,
            symbol: 'SNDKUSDT',
            side: 'long',
            quantity: Number('2.1894945734517948'),
            entry_price: 1590.778092,
            exit_price: 1628.304274,
            entry_time: '2026-07-21T20:03:25.837961979Z',
            exit_time: '2026-07-21T21:01:36.31170213Z',
            leverage: 0,
            entry_fee: 1.7415,
            exit_fee: Number('1.7825816859256822'),
            fee: Number('3.524081685925682'),
            realized_pnl: Number('78.63929016543888'),
            close_reason: 'take_profit',
          },
          {
            entry_order_id: 1,
            exit_order_id: 2,
            symbol: 'SOXLUSDT',
            side: 'short',
            quantity: Number('22.345845397855264'),
            entry_price: 156.628668,
            exit_price: 158.601714,
            entry_time: '2026-07-21T19:29:49.893241334Z',
            exit_time: '2026-07-21T19:46:10.755796447Z',
            leverage: 0,
            entry_fee: 1.75,
            exit_fee: Number('1.772044690439428'),
            fee: Number('3.522044690439428'),
            realized_pnl: Number('-47.61142556929577'),
            close_reason: 'close_short',
          },
        ],
      },
    } as SystemStatus

    const resolved = resolveDashboardPerformance(status, liveStats, liveHistory)
    expect(resolved.fullStats.total_trades).toBe(3)
    expect(resolved.fullStats.total_pnl).toBeCloseTo(13.518084153188568)
    expect(resolved.fullStats.total_fee).toBeCloseTo(12.541468107008955)
    expect(resolved.history.positions).toHaveLength(3)
    expect(resolved.history.positions[0].close_reason).toBe('close_long')
    expect(resolved.history.symbol_stats).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ symbol: 'SNDKUSDT', total_trades: 2 }),
        expect.objectContaining({ symbol: 'SOXLUSDT', total_trades: 1 }),
      ])
    )
  })
})
