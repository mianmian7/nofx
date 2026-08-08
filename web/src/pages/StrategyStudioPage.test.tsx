import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { StrategyStudioPage } from './StrategyStudioPage'

const apiMocks = vi.hoisted(() => ({
  getStrategies: vi.fn(),
  getSymbols: vi.fn(),
  getModelConfigs: vi.fn(),
  createStrategy: vi.fn(),
  updateStrategy: vi.fn(),
  startStrategyBacktest: vi.fn(),
  getStrategyBacktest: vi.fn(),
}))

vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ token: 'test-token' }),
}))

vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'zh', setLanguage: vi.fn() }),
}))

vi.mock('../components/common/DeepVoidBackground', () => ({
  DeepVoidBackground: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
}))

vi.mock('../lib/api', () => ({ api: apiMocks }))

vi.mock('../lib/notify', () => ({
  confirmToast: vi.fn(),
  notify: { error: vi.fn(), success: vi.fn() },
}))

describe('StrategyStudioPage initial data policy', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  beforeEach(() => {
    vi.resetAllMocks()
    apiMocks.getStrategies.mockResolvedValue([
      {
        id: 'strategy-1',
        name: 'Local dynamic strategy',
        description: '',
        is_active: true,
        is_default: true,
        is_public: false,
        config_visible: true,
        created_at: '',
        updated_at: '',
        config: {
          strategy_type: 'ai_trading',
          ai_config: {
            coin_source: {
              source_type: 'binance_dynamic',
              binance_dynamic_limit: 10,
            },
          },
        },
      },
    ])
    apiMocks.getSymbols.mockResolvedValue({
      exchange: 'binance',
      symbols: [],
      count: 0,
    })
    apiMocks.getModelConfigs.mockResolvedValue([])
    apiMocks.createStrategy.mockResolvedValue({
      id: 'strategy-new',
      name: 'NOFX 本地动态策略',
      description: '',
      is_active: false,
      is_default: false,
      is_public: false,
      config_visible: false,
      created_at: '',
      updated_at: '',
      config: {},
    })
    apiMocks.updateStrategy.mockResolvedValue({})
  })

  it('loads the local Binance pool without calling a removed remote source', async () => {
    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    await waitFor(() => expect(apiMocks.getStrategies).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(apiMocks.getSymbols).toHaveBeenCalledWith('binance')
    )
    expect(apiMocks.getModelConfigs).toHaveBeenCalledOnce()
    expect(
      await screen.findByRole('button', { name: 'Binance 动态候选' })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: '保存' })).toBeVisible()
    expect(screen.getByText('历史 AI 回放')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /remote signal source/i })
    ).not.toBeInTheDocument()
  })

  it('exposes a new strategy button and keeps the local dynamic defaults', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        strategy_type: 'ai_trading',
        ai_config: {
          coin_source: { source_type: 'binance_dynamic' },
          indicators: {
            klines: { primary_timeframe: '15m', primary_count: 30 },
          },
          risk_control: {},
        },
      }),
    })
    vi.stubGlobal('fetch', fetchMock)
    apiMocks.getStrategies.mockResolvedValueOnce([]).mockResolvedValueOnce([])

    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    const newStrategyButtons = await screen.findAllByRole('button', {
      name: '新建策略',
    })
    fireEvent.click(newStrategyButtons[0])

    await waitFor(() => expect(apiMocks.createStrategy).toHaveBeenCalledOnce())
    const payload = apiMocks.createStrategy.mock.calls[0][0]
    expect(payload.name).toBe('NOFX 本地动态策略')
    expect(payload.config.ai_config.coin_source.source_type).toBe(
      'binance_dynamic'
    )
    expect(payload.config.ai_config.indicators.klines.primary_timeframe).toBe(
      '15m'
    )
    expect(payload.config.ai_config.risk_control.max_positions).toBe(3)
    expect(payload.config.ai_config.risk_control.trade_throttle).toMatchObject({
      reentry_cooldown_minutes: 180,
      max_opens_per_hour: 3,
      max_opens_per_cycle: 2,
    })
  })

  it('filters candidate symbols without changing the selected source', async () => {
    apiMocks.getSymbols.mockImplementation(async (exchange: string) =>
      exchange === 'binance'
        ? {
            exchange,
            count: 2,
            symbols: [
              {
                symbol: 'BTCUSDT',
                display: 'BTCUSDT',
                name: 'BTC',
                category: 'crypto',
                exchange,
              },
              {
                symbol: 'ETHUSDT',
                display: 'ETHUSDT',
                name: 'ETH',
                category: 'crypto',
                exchange,
              },
            ],
          }
        : { exchange, symbols: [], count: 0 }
    )

    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    expect(await screen.findByRole('button', { name: /BTCUSDT/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /ETHUSDT/ })).toBeVisible()

    fireEvent.change(screen.getByLabelText('搜索候选交易对'), {
      target: { value: 'BTC' },
    })

    expect(screen.getByRole('button', { name: /BTCUSDT/ })).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /ETHUSDT/ })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Binance 动态候选' })
    ).toBeVisible()
  })

  it('shows modified preset parameters as custom and labels the leverage cap', async () => {
    apiMocks.getStrategies.mockResolvedValue([
      {
        id: 'strategy-1',
        name: 'Balanced with one extra slot',
        description: '',
        is_active: true,
        is_default: false,
        is_public: false,
        config_visible: true,
        created_at: '',
        updated_at: '',
        config: {
          strategy_type: 'ai_trading',
          ai_config: {
            coin_source: { source_type: 'binance_dynamic' },
            indicators: {
              klines: { primary_timeframe: '15m', primary_count: 30 },
            },
            risk_control: {
              max_positions: 3,
              max_leverage: 3,
              max_margin_usage: 0.5,
              min_confidence: 75,
            },
          },
        },
      },
    ])

    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    fireEvent.click(await screen.findByText('高级设置'))

    expect(screen.getByText('自定义')).toBeVisible()
    expect(screen.getByText('最大杠杆')).toBeVisible()
    expect(screen.getByRole('option', { name: '50x' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '125x' })).toBeInTheDocument()
    expect(screen.getByText('原始 K 线')).toBeVisible()
    expect(screen.getByText('时间周期')).toBeVisible()
    expect(screen.getByText('K 线数量')).toBeVisible()
    expect(screen.getByText('交易参数')).toBeVisible()
    expect(screen.getByText('最大持仓数')).toBeVisible()
    expect(screen.getByText('入场置信度')).toBeVisible()
    expect(screen.getByText('策略备注')).toBeVisible()
    expect(
      screen.getByPlaceholderText(
        '例如：只交易清晰趋势；当候选信号与 K 线冲突时跳过入场。'
      )
    ).toBeVisible()
    expect(screen.queryByText('Raw candles')).not.toBeInTheDocument()
    expect(screen.queryByText('Trading parameters')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '平衡' }))
    expect(screen.queryByText('自定义')).not.toBeInTheDocument()
  })

  it('labels deterministic replay work as processed candles instead of AI calls', async () => {
    apiMocks.startStrategyBacktest.mockResolvedValue({
      id: 'job-1',
      strategy_id: 'strategy-1',
      mode: 'trend_v1',
      status: 'completed',
      stage: 'completed',
      decision_progress: 4180,
      decision_total: 4180,
      result: {
        symbol: 'BTCUSDT',
        initial_balance: 1000,
        final_equity: 1058.8,
        total_return_pct: 5.88,
        max_drawdown_pct: 8.79,
        trade_count: 44,
        win_rate_pct: 25,
        profit_factor: 1.2,
        decision_calls: 4180,
        execution_policy: 'close_decision_next_open_fill',
        trades: [],
      },
    })

    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    fireEvent.click(await screen.findByRole('button', { name: '运行趋势基准' }))

    expect(await screen.findByText('处理 K 线')).toBeVisible()
    expect(screen.queryByText('模型调用')).not.toBeInTheDocument()
  })

  it('renders a persisted observation-only watchlist from Binance TradFi quotes', async () => {
    apiMocks.getStrategies.mockResolvedValue([
      {
        id: 'strategy-1',
        name: 'Local dynamic strategy',
        description: '',
        is_active: true,
        is_default: true,
        is_public: false,
        config_visible: true,
        created_at: '',
        updated_at: '',
        config: {
          strategy_type: 'ai_trading',
          ai_config: {
            coin_source: {
              source_type: 'binance_dynamic',
              binance_dynamic_limit: 10,
              watchlist: ['SKHYNIX', 'SAMSUNG', 'QQQ'],
            },
          },
        },
      },
    ])
    apiMocks.getSymbols.mockImplementation(async (exchange: string) =>
      exchange === 'binance-tradifi'
        ? {
            exchange,
            count: 3,
            symbols: [
              {
                symbol: 'SKHYNIXUSDT',
                display: 'SKHYNIXUSDT',
                name: 'SKHYNIX',
                category: 'stock',
                exchange: 'binance-tradifi',
                mark_price: 1303.8,
              },
              {
                symbol: 'SAMSUNGUSDT',
                display: 'SAMSUNGUSDT',
                name: 'SAMSUNG',
                category: 'stock',
                exchange: 'binance-tradifi',
                mark_price: 182.45,
              },
              {
                symbol: 'QQQUSDT',
                display: 'QQQUSDT',
                name: 'QQQ',
                category: 'stock',
                exchange: 'binance-tradifi',
                mark_price: 708.81,
              },
            ],
          }
        : { exchange, symbols: [], count: 0 }
    )

    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    expect(await screen.findByText('我的自选')).toBeVisible()
    expect(screen.getByText('SKHYNIX')).toBeVisible()
    expect(screen.getByText('SAMSUNG')).toBeVisible()
    expect(screen.getByText('QQQ')).toBeVisible()
    await waitFor(() =>
      expect(apiMocks.getSymbols).toHaveBeenCalledWith('binance-tradifi')
    )
    expect(screen.getByText('$1303.80')).toBeVisible()
    expect(screen.getByText('$708.81')).toBeVisible()
    expect(screen.queryByText('当前数据源未上线')).not.toBeInTheDocument()
  })

  it('persists the watchlist as the selected Binance AI candidate pool', async () => {
    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    fireEvent.change(
      await screen.findByPlaceholderText('输入代码，可用斜杠、逗号或空格分隔'),
      { target: { value: 'SKHYNIX/QQQ/SPY' } }
    )
    fireEvent.click(screen.getByRole('button', { name: '添加到自选' }))
    fireEvent.click(screen.getByRole('button', { name: '启用为 AI 候选池' }))
    fireEvent.click(screen.getByRole('button', { name: '保存自选' }))

    await waitFor(() => expect(apiMocks.updateStrategy).toHaveBeenCalledOnce())
    const body = apiMocks.updateStrategy.mock.calls[0][1]
    expect(body.config.ai_config.coin_source.watchlist).toEqual([
      'SKHYNIX',
      'QQQ',
      'SPY',
    ])
    expect(body.config.ai_config.coin_source.source_type).toBe('static')
    expect(body.config.ai_config.coin_source.use_watchlist).toBe(true)
    expect(body.config.ai_config.coin_source.static_coins).toEqual([
      'SKHYNIXUSDT',
      'QQQUSDT',
      'SPYUSDT',
    ])
  })
})
