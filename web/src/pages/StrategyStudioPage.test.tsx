import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { StrategyStudioPage } from './StrategyStudioPage'

const apiMocks = vi.hoisted(() => ({
  getStrategies: vi.fn(),
  getSymbols: vi.fn(),
  getVergexSignalRanking: vi.fn(),
  getModelConfigs: vi.fn(),
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
    vi.clearAllMocks()
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
    apiMocks.getVergexSignalRanking.mockRejectedValue(
      new Error('paid upstream should not load on mount')
    )
  })

  it('loads the local Binance pool without calling the paid signal board', async () => {
    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    await waitFor(() => expect(apiMocks.getStrategies).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(apiMocks.getSymbols).toHaveBeenCalledWith('binance')
    )
    expect(apiMocks.getVergexSignalRanking).not.toHaveBeenCalled()
    expect(apiMocks.getModelConfigs).toHaveBeenCalledOnce()
    expect(
      await screen.findByRole('button', { name: 'Binance 动态候选' })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: '保存' })).toBeVisible()
    expect(screen.getByText('历史 AI 回放')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Claw402/Vergex（可选付费）' })
    ).not.toBeInTheDocument()
  })

  it('renders every advanced setting label in Chinese', async () => {
    render(
      <MemoryRouter>
        <StrategyStudioPage />
      </MemoryRouter>
    )

    fireEvent.click(await screen.findByText('高级设置'))

    expect(screen.getByText('原始 K 线')).toBeVisible()
    expect(screen.getByText('时间周期')).toBeVisible()
    expect(screen.getByText('K 线数量')).toBeVisible()
    expect(screen.getByText('交易参数')).toBeVisible()
    expect(screen.getByText('最大持仓数')).toBeVisible()
    expect(screen.getByText('杠杆')).toBeVisible()
    expect(screen.getByText('入场置信度')).toBeVisible()
    expect(screen.getByText('策略备注')).toBeVisible()
    expect(
      screen.getByPlaceholderText(
        '例如：只交易清晰趋势；当候选信号与 K 线冲突时跳过入场。'
      )
    ).toBeVisible()
    expect(screen.queryByText('Raw candles')).not.toBeInTheDocument()
    expect(screen.queryByText('Trading parameters')).not.toBeInTheDocument()
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
    const fetchMock = vi.fn().mockResolvedValue({ ok: true })
    vi.stubGlobal('fetch', fetchMock)

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

    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce())
    const init = fetchMock.mock.calls[0][1] as RequestInit
    const body = JSON.parse(String(init.body))
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
