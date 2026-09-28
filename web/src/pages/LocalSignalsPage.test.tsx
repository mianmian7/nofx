import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { SWRConfig } from 'swr'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { LocalSignalsPage } from './LocalSignalsPage'
import { signalIsCurrent, type LocalSignal } from '../lib/api/localSignals'

const mocks = vi.hoisted(() => ({
  snapshots: vi.fn(),
  history: vi.fn(),
  create: vi.fn(),
  defaults: vi.fn(),
}))
vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'zh' }),
}))
vi.mock('../lib/api', () => ({
  api: {
    getDefaultStrategyConfig: mocks.defaults,
    createStrategy: mocks.create,
  },
}))
vi.mock('../lib/api/localSignals', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api/localSignals')>()),
  getLocalSignals: mocks.snapshots,
  getLocalSignalHistory: mocks.history,
}))
function observation(now = Date.now()): LocalSignal {
  return {
    exchange: 'binance',
    symbol: 'BTCUSDT',
    version: 'local-trend-1',
    direction: 'long',
    score: 80,
    confirmed: true,
    observed_at: now,
    closed_at: Math.floor(now / 900000) * 900000 - 1,
    frames: ['15m', '1h', '4h'].map((timeframe, index) => ({
      timeframe,
      closed_at:
        Math.floor(now / [900000, 3600000, 14400000][index]) *
          [900000, 3600000, 14400000][index] -
        1,
      price: 100,
      ema20: 102,
      ema50: 98,
      rsi: 65,
      atr_percent: 1,
      volume_ratio: 1.2,
      trend: 1,
      momentum: 0.6,
      breakout: 0,
    })),
    funding_rate: null,
    open_interest: null,
    warnings: [],
  }
}
function view() {
  return render(
    <SWRConfig
      value={{
        provider: () => new Map(),
        dedupingInterval: 0,
        shouldRetryOnError: false,
      }}
    >
      <MemoryRouter initialEntries={['/analysis']}>
        <Routes>
          <Route path="/analysis" element={<LocalSignalsPage />} />
          <Route path="/strategy" element={<div>策略设置页面</div>} />
        </Routes>
      </MemoryRouter>
    </SWRConfig>
  )
}
describe('local market analysis', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.snapshots.mockResolvedValue({
      exchange: 'binance',
      observed_at: Date.now(),
      expected: 1,
      succeeded: 1,
      error: '',
      failures: [],
      results: [observation()],
      ready: true,
      scanning: false,
      stale: false,
      version: 'local-trend-1',
    })
    mocks.history.mockResolvedValue({
      history: [observation()],
      retention_days: 30,
    })
    mocks.defaults.mockResolvedValue({
      strategy_type: 'ai_trading',
      language: 'en',
      ai_config: {
        coin_source: { source_type: 'binance_dynamic' },
        indicators: { klines: { primary_timeframe: '15m' } },
        risk_control: { max_leverage: 3 },
      },
    })
    mocks.create.mockResolvedValue({ id: 'local-strategy' })
  })
  it('shows meaningful details, missing data, search and per-venue history', async () => {
    view()
    fireEvent.click(await screen.findByRole('button', { name: /BTCUSDT/ }))
    await waitFor(() =>
      expect(mocks.history).toHaveBeenCalledWith('binance', 'BTCUSDT', 0)
    )
    expect(screen.getAllByText('暂无有效数据')).toHaveLength(2)
    expect(screen.getByText('分数是规则一致程度，不是胜率')).toBeVisible()
    fireEvent.change(screen.getByLabelText('搜索交易对'), {
      target: { value: 'ETH' },
    })
    expect(
      screen.queryByRole('button', { name: /BTCUSDT/ })
    ).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('行情来源'), {
      target: { value: 'okx' },
    })
    await waitFor(() => expect(mocks.snapshots).toHaveBeenCalledWith('okx'))
  })
  it('creates a separate configuration without starting or modifying a trader', async () => {
    view()
    fireEvent.change(screen.getByLabelText('最低分数（35—95）'), {
      target: { value: '75' },
    })
    fireEvent.click(screen.getByRole('button', { name: '创建策略并设置' }))
    expect(await screen.findByText('策略设置页面')).toBeVisible()
    expect(mocks.create).toHaveBeenCalledWith(
      expect.objectContaining({
        config: expect.objectContaining({
          ai_config: expect.objectContaining({
            coin_source: expect.objectContaining({
              source_type: 'local_signals',
              local_signal_min_score: 75,
              local_signal_limit: 5,
            }),
          }),
        }),
      })
    )
  })
  it('labels historical data as expired instead of presenting an actionable signal', async () => {
    const row = observation()
    row.observed_at -= 700000
    mocks.snapshots.mockResolvedValue({
      exchange: 'binance',
      observed_at: row.observed_at,
      expected: 1,
      succeeded: 1,
      error: '',
      failures: [],
      results: [row],
      ready: false,
      scanning: false,
      stale: true,
    })
    view()
    expect(await screen.findByText('已过期')).toBeVisible()
    expect(screen.getByText('数据未就绪或已过期，暂不用于开仓')).toBeVisible()
  })
  it('does not mistake freshly downloaded old candles for fresh analysis', () => {
    const now = Date.parse('2026-09-27T12:05:00Z')
    const row = observation(now)
    expect(signalIsCurrent(row, now)).toBe(true)
    row.frames[2].closed_at -= 14400000
    expect(signalIsCurrent(row, now)).toBe(false)
  })
  it('expires prior candles exactly after the two-minute collection buffer', () => {
    const boundary = Date.parse('2026-09-27T12:00:00Z')
    const row = observation(boundary - 30000)
    expect(signalIsCurrent(row, boundary - 1)).toBe(true)
    expect(signalIsCurrent(row, boundary + 119999)).toBe(true)
    expect(signalIsCurrent(row, boundary + 120000)).toBe(false)
    const refreshed = observation(boundary + 120000)
    expect(signalIsCurrent(refreshed, boundary + 120000)).toBe(true)
  })
})
