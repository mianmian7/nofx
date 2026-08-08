import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Bot,
  Check,
  Loader2,
  Plus,
  RefreshCw,
  Save,
  Shield,
  Sparkles,
  Target,
  Trash2,
} from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { DeepVoidBackground } from '../components/common/DeepVoidBackground'
import { api } from '../lib/api'
import { confirmToast, notify } from '../lib/notify'
import type {
  AIStrategyConfig,
  AIModel,
  CoinSourceConfig,
  IndicatorConfig,
  RiskControlConfig,
  Strategy,
  StrategyConfig,
  TradeThrottleConfig,
} from '../types'
import type { MarketSymbol } from '../lib/api/data'
import type { BacktestJob } from '../lib/api/strategies'
import { launchAutopilot } from '../lib/launch/launchAutopilot'
import { buildDashboardPath } from '../router/paths'

const API_BASE = import.meta.env.VITE_API_BASE || ''

type Scope =
  | 'all'
  | 'crypto'
  | 'stock'
  | 'commodity'
  | 'index'
  | 'forex'
  | 'pre_ipo'

type CandidateSource = 'binance_dynamic' | 'static' | 'watchlist'

const scopeOptions: Array<{ value: Scope; zh: string; en: string }> = [
  { value: 'all', zh: '全部', en: 'All' },
  { value: 'stock', zh: '美股', en: 'US Stocks' },
  { value: 'crypto', zh: '加密货币', en: 'Crypto' },
  { value: 'commodity', zh: '商品', en: 'Commodities' },
  { value: 'index', zh: '指数', en: 'Indices' },
  { value: 'forex', zh: '外汇', en: 'FX' },
  { value: 'pre_ipo', zh: '上市前', en: 'Pre-IPO' },
]

const timeframeOptions = ['5m', '15m', '30m', '1h', '4h', '1d']
const barCountOptions = [20, 30, 50]
const leverageOptions = [1, 2, 3, 5, 10, 20, 50, 75, 100, 125]
const maxWatchlistAssets = 50
const maxWatchlistCandidateAssets = 20

const text = (language: string, zh: string, en: string) =>
  language === 'zh' ? zh : en

type Profile = 'careful' | 'balanced' | 'active' | 'custom'

const profileOptions: Array<{
  value: Exclude<Profile, 'custom'>
  zh: string
  en: string
  zhNote: string
  enNote: string
  maxPositions: number
  leverage: number
  confidence: number
  timeframe: string
  bars: number
  perPositionMargin: number
  promptZh: string
  promptEn: string
}> = [
  {
    value: 'careful',
    zh: '谨慎',
    en: 'Careful',
    zhNote: '减少交易，只处理多项条件一致的机会',
    enNote: 'Fewer trades, only aligned signals',
    maxPositions: 1,
    leverage: 2,
    confidence: 82,
    timeframe: '1h',
    bars: 30,
    perPositionMargin: 0.1,
    promptZh:
      '谨慎模式：只有趋势、动量、成交量和原始 K 线相互支持时才开仓；出现冲突时等待。',
    promptEn:
      'Careful mode: open only when trend, momentum, volume and raw candles agree; wait on conflicts.',
  },
  {
    value: 'balanced',
    zh: '平衡',
    en: 'Balanced',
    zhNote: '在机会数量和风险之间保持平衡',
    enNote: 'Recommended balance of opportunity and risk',
    maxPositions: 2,
    leverage: 3,
    confidence: 75,
    timeframe: '15m',
    bars: 30,
    perPositionMargin: 0.15,
    promptZh:
      '平衡模式：优先分析流动性较高的候选交易对，结合多周期趋势和原始 K 线设置止损与目标。',
    promptEn:
      'Balanced mode: prioritize liquid candidates and combine multi-timeframe trend with raw candles for stops and targets.',
  },
  {
    value: 'active',
    zh: '积极',
    en: 'Active',
    zhNote: '更快捕捉趋势，但仍限制仓位与风险',
    enNote: 'Faster trend capture with more positions',
    maxPositions: 3,
    leverage: 5,
    confidence: 68,
    timeframe: '5m',
    bars: 50,
    perPositionMargin: 0.2,
    promptZh:
      '积极模式：更快响应强趋势和放量行情，但必须设置明确止损，并避免在数据冲突时追单。',
    promptEn:
      'Active mode: react faster to strong trend and volume, but always set an explicit stop and avoid chasing conflicting data.',
  },
]

const bigMoveTradeThrottle: Required<TradeThrottleConfig> = {
  reentry_cooldown_minutes: 180,
  max_opens_per_hour: 3,
  max_opens_per_cycle: 2,
}

function getAIConfig(config: StrategyConfig): AIStrategyConfig | null {
  if (config.ai_config) return config.ai_config
  if (config.coin_source && config.indicators && config.risk_control) {
    return {
      coin_source: config.coin_source,
      indicators: config.indicators,
      risk_control: config.risk_control,
      prompt_sections: config.prompt_sections,
      custom_prompt: config.custom_prompt,
    }
  }
  return null
}

function normalizeSourceType(
  sourceType: CoinSourceConfig['source_type'] | string | undefined
): CoinSourceConfig['source_type'] {
  if (
    sourceType === 'static' ||
    sourceType === 'hyper_all' ||
    sourceType === 'hyper_main' ||
    sourceType === 'hyper_rank'
  ) {
    return sourceType
  }
  return 'binance_dynamic'
}

function defaultCoinSource(
  source?: Partial<CoinSourceConfig>
): CoinSourceConfig {
  return {
    source_type: normalizeSourceType(source?.source_type),
    binance_dynamic_limit: Math.min(
      Math.max(source?.binance_dynamic_limit || 10, 1),
      10
    ),
    static_coins: source?.static_coins || [],
    watchlist: source?.watchlist || [],
    use_watchlist: source?.use_watchlist || false,
    excluded_coins: source?.excluded_coins || [],
    use_hyper_all: source?.use_hyper_all || false,
    use_hyper_main: source?.use_hyper_main || false,
    hyper_main_limit: source?.hyper_main_limit || 0,
    hyper_rank_category: source?.hyper_rank_category || 'all',
    hyper_rank_direction: source?.hyper_rank_direction || 'gainers',
    hyper_rank_limit: source?.hyper_rank_limit || 0,
  }
}

function defaultIndicators(
  indicators?: Partial<IndicatorConfig>
): IndicatorConfig {
  const klines = indicators?.klines || {
    primary_timeframe: '15m',
    primary_count: 30,
    enable_multi_timeframe: false,
  }

  return {
    klines: {
      primary_timeframe: klines.primary_timeframe || '15m',
      primary_count: klines.primary_count || 30,
      longer_timeframe: klines.longer_timeframe || '',
      longer_count: klines.longer_count || 0,
      enable_multi_timeframe: klines.enable_multi_timeframe || false,
      selected_timeframes: klines.selected_timeframes || [
        klines.primary_timeframe || '15m',
      ],
    },
    enable_raw_klines: indicators?.enable_raw_klines !== false,
    enable_ema: indicators?.enable_ema || false,
    enable_macd: indicators?.enable_macd || false,
    enable_rsi: indicators?.enable_rsi || false,
    enable_atr: indicators?.enable_atr || false,
    enable_boll: indicators?.enable_boll || false,
    enable_volume: indicators?.enable_volume || false,
    enable_oi: indicators?.enable_oi || false,
    enable_funding_rate: indicators?.enable_funding_rate || false,
    ema_periods: indicators?.ema_periods,
    rsi_periods: indicators?.rsi_periods,
    atr_periods: indicators?.atr_periods,
    boll_periods: indicators?.boll_periods,
    external_data_sources: indicators?.external_data_sources,
  }
}

function defaultTradeThrottle(
  throttle?: Partial<TradeThrottleConfig>
): Required<TradeThrottleConfig> {
  return {
    reentry_cooldown_minutes: throttle?.reentry_cooldown_minutes ?? 30,
    max_opens_per_hour: throttle?.max_opens_per_hour ?? 30,
    max_opens_per_cycle: throttle?.max_opens_per_cycle ?? 6,
  }
}

function defaultRisk(risk?: Partial<RiskControlConfig>): RiskControlConfig {
  return {
    max_positions: risk?.max_positions ?? 2,
    position_sizing_mode: risk?.position_sizing_mode ?? 'notional_based',
    max_leverage: risk?.max_leverage ?? 3,
    btc_eth_max_position_value_ratio:
      risk?.btc_eth_max_position_value_ratio ?? 1.5,
    altcoin_max_position_value_ratio:
      risk?.altcoin_max_position_value_ratio ?? 1.5,
    btc_eth_max_margin_ratio: risk?.btc_eth_max_margin_ratio ?? 0.15,
    altcoin_max_margin_ratio: risk?.altcoin_max_margin_ratio ?? 0.15,
    max_margin_usage: risk?.max_margin_usage ?? 0.5,
    min_position_size: risk?.min_position_size || 12,
    min_risk_reward_ratio: risk?.min_risk_reward_ratio || 2,
    min_confidence: risk?.min_confidence || 78,
    trade_throttle: defaultTradeThrottle(risk?.trade_throttle),
  }
}

function simplifyConfig(
  config: StrategyConfig | null | undefined
): StrategyConfig {
  const ai = config ? getAIConfig(config) : null
  return {
    strategy_type: 'ai_trading',
    language: config?.language || 'zh',
    ai_config: {
      coin_source: defaultCoinSource(ai?.coin_source),
      indicators: defaultIndicators(ai?.indicators),
      risk_control: defaultRisk(ai?.risk_control),
      custom_prompt: ai?.custom_prompt || '',
      prompt_sections: ai?.prompt_sections,
    },
    grid_config: null,
    publish_config: config?.publish_config,
  }
}

function normalizeSymbol(symbol: string): string {
  return symbol
    .trim()
    .toUpperCase()
    .replace(/^XYZ:/, '')
    .replace(/-USDC$/, '')
}

function normalizeWatchlistToken(symbol: string): string {
  return symbol
    .trim()
    .toUpperCase()
    .replace(/^XYZ:/, '')
    .replace(/-USDC$/, '')
    .replace(/USDT$/, '')
}

function watchlistCandidateSymbols(symbols: string[]): string[] {
  return symbols
    .map(normalizeWatchlistToken)
    .filter(Boolean)
    .slice(0, maxWatchlistCandidateAssets)
    .map((symbol) => `${symbol}USDT`)
}

function resolveWatchlistAsset(symbol: string, pool: MarketSymbol[]) {
  const requested = normalizeWatchlistToken(symbol)
  const canonical = `${requested}USDT`
  const asset = pool.find(
    (item) => item.symbol.toUpperCase() === canonical.toUpperCase()
  )
  return { requested, canonical, asset }
}

function formatWatchlistPrice(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '—'
  return `$${value.toFixed(value >= 1 ? 2 : 4)}`
}

function categoryLabel(category: string | undefined, language: string): string {
  const option = scopeOptions.find((item) => item.value === category)
  if (!option) return category || 'TradeFi'
  return text(language, option.zh, option.en)
}

function formatChange(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return ''
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(2)}%`
}

function profileFromConfig(
  config: AIStrategyConfig | null | undefined
): Profile {
  if (!config) return 'custom'
  const risk = config.risk_control
  const klines = config.indicators?.klines
  if (!risk || !klines) return 'custom'

  const matched = profileOptions.find((profile) => {
    const selectedTimeframes = klines.selected_timeframes || [
      klines.primary_timeframe,
    ]
    return (
      risk.max_positions === profile.maxPositions &&
      risk.max_leverage === profile.leverage &&
      risk.min_confidence === profile.confidence &&
      risk.position_sizing_mode === 'margin_based' &&
      risk.btc_eth_max_margin_ratio === profile.perPositionMargin &&
      risk.altcoin_max_margin_ratio === profile.perPositionMargin &&
      klines.primary_timeframe === profile.timeframe &&
      klines.primary_count === profile.bars &&
      klines.enable_multi_timeframe !== true &&
      selectedTimeframes.length === 1 &&
      selectedTimeframes[0] === profile.timeframe &&
      config.indicators.enable_raw_klines !== false &&
      (config.custom_prompt === profile.promptZh ||
        config.custom_prompt === profile.promptEn)
    )
  })
  return matched?.value || 'custom'
}

function clampNumber(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value))
}

export function StrategyStudioPage() {
  const { token } = useAuth()
  const { language } = useLanguage()
  const navigate = useNavigate()
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [selectedStrategy, setSelectedStrategy] = useState<Strategy | null>(
    null
  )
  const [editingConfig, setEditingConfig] = useState<StrategyConfig | null>(
    null
  )
  const [symbols, setSymbols] = useState<MarketSymbol[]>([])
  const [watchlistSymbols, setWatchlistSymbols] = useState<MarketSymbol[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [symbolsLoading, setSymbolsLoading] = useState(false)
  const [symbolsError, setSymbolsError] = useState('')
  const [watchlistLoading, setWatchlistLoading] = useState(false)
  const [watchlistError, setWatchlistError] = useState('')
  const [hasChanges, setHasChanges] = useState(false)
  const [models, setModels] = useState<AIModel[]>([])
  const [backtestModelId, setBacktestModelId] = useState('')
  const [backtestMode, setBacktestMode] = useState<'trend_v1' | 'ai_replay'>(
    'trend_v1'
  )
  const [backtestSymbol, setBacktestSymbol] = useState('BTCUSDT')
  const [backtestStart, setBacktestStart] = useState(() =>
    new Date(Date.now() - 2 * 365 * 24 * 60 * 60 * 1000)
      .toISOString()
      .slice(0, 10)
  )
  const [backtestEnd, setBacktestEnd] = useState(() =>
    new Date().toISOString().slice(0, 10)
  )
  const [backtestMaxCalls, setBacktestMaxCalls] = useState(12)
  const [backtestJob, setBacktestJob] = useState<BacktestJob | null>(null)
  const [backtestStarting, setBacktestStarting] = useState(false)
  const [creatingStrategy, setCreatingStrategy] = useState(false)
  const [scope, setScope] = useState<Scope>('all')
  const [candidateQuery, setCandidateQuery] = useState('')
  const [watchlistInput, setWatchlistInput] = useState('')

  const aiConfig = editingConfig?.ai_config || null
  const coinSource = aiConfig?.coin_source
  const indicators = aiConfig?.indicators
  const risk = aiConfig?.risk_control
  const throttle = defaultTradeThrottle(risk?.trade_throttle)
  const selectedSymbols = coinSource?.static_coins || []
  const watchlist = coinSource?.watchlist || []
  const watchlistCandidateMode = coinSource?.use_watchlist === true
  const activeProfile = profileFromConfig(aiConfig)

  const visibleSymbols = useMemo(() => {
    const candidatePool = watchlistCandidateMode ? watchlistSymbols : symbols
    const scopedSymbols =
      scope === 'all'
        ? candidatePool
        : candidatePool.filter((item) => item.category === scope)
    const query = candidateQuery.trim().toUpperCase()
    const filteredSymbols = query
      ? scopedSymbols.filter((item) =>
          [item.symbol, item.display, item.name]
            .filter(Boolean)
            .some((value) => value!.toUpperCase().includes(query))
        )
      : scopedSymbols
    return [...filteredSymbols].sort(
      (left, right) => (right.volume_24h || 0) - (left.volume_24h || 0)
    )
  }, [candidateQuery, scope, symbols, watchlistCandidateMode, watchlistSymbols])

  const selectedSet = useMemo(
    () => new Set(selectedSymbols.map(normalizeSymbol)),
    [selectedSymbols]
  )

  const loadStrategies = useCallback(
    async (preferredStrategyId?: string) => {
      if (!token) return
      setLoading(true)
      try {
        const result = await api.getStrategies()
        setStrategies(result)
        const nextStrategy =
          (preferredStrategyId
            ? result.find((strategy) => strategy.id === preferredStrategyId)
            : null) ||
          result.find((strategy) => strategy.is_active) ||
          result[0] ||
          null
        setSelectedStrategy(nextStrategy)
        setEditingConfig(
          nextStrategy ? simplifyConfig(nextStrategy.config) : null
        )
        setCandidateQuery('')
        setScope('all')
        setHasChanges(false)
      } catch (error) {
        notify.error(
          error instanceof Error ? error.message : 'Failed to load strategies'
        )
      } finally {
        setLoading(false)
      }
    },
    [token]
  )

  const loadSymbols = useCallback(async () => {
    setSymbolsLoading(true)
    setSymbolsError('')
    try {
      const result = await api.getSymbols('binance')
      setSymbols(result.symbols || [])
    } catch (error) {
      setSymbolsError(
        error instanceof Error ? error.message : 'Symbol list unavailable'
      )
    } finally {
      setSymbolsLoading(false)
    }
  }, [])

  const loadWatchlistSymbols = useCallback(async () => {
    setWatchlistLoading(true)
    setWatchlistError('')
    try {
      const result = await api.getSymbols('binance-tradifi')
      setWatchlistSymbols(result.symbols || [])
    } catch (error) {
      setWatchlistError(
        error instanceof Error ? error.message : 'Watchlist quotes unavailable'
      )
    } finally {
      setWatchlistLoading(false)
    }
  }, [])

  const loadModels = useCallback(async () => {
    if (!token) return
    try {
      const configuredModels = (await api.getModelConfigs()).filter(
        (model) => model.enabled && model.has_api_key !== false
      )
      setModels(configuredModels)
      setBacktestModelId(
        (currentModelId) => currentModelId || configuredModels[0]?.id || ''
      )
    } catch {
      setModels([])
    }
  }, [token])

  useEffect(() => {
    void loadStrategies()
    void loadSymbols()
    void loadWatchlistSymbols()
    void loadModels()
  }, [loadModels, loadStrategies, loadSymbols, loadWatchlistSymbols])

  useEffect(() => {
    if (
      !backtestJob ||
      backtestJob.status === 'completed' ||
      backtestJob.status === 'failed'
    ) {
      return
    }
    const timer = window.setTimeout(async () => {
      try {
        setBacktestJob(await api.getStrategyBacktest(backtestJob.id))
      } catch (error) {
        notify.error(
          error instanceof Error ? error.message : 'Failed to refresh replay'
        )
      }
    }, 2000)
    return () => window.clearTimeout(timer)
  }, [backtestJob])

  const patchAI = (patch: Partial<AIStrategyConfig>) => {
    setEditingConfig((previousConfig) => {
      const baseConfig = simplifyConfig(previousConfig)
      return {
        ...baseConfig,
        language: language as 'zh' | 'en',
        ai_config: {
          ...baseConfig.ai_config!,
          ...patch,
        },
      }
    })
    setHasChanges(true)
  }

  const patchCoinSource = (patch: Partial<CoinSourceConfig>) => {
    patchAI({
      coin_source: defaultCoinSource({
        ...coinSource,
        ...patch,
      }),
    })
  }

  const patchIndicators = (patch: Partial<IndicatorConfig>) => {
    patchAI({
      indicators: defaultIndicators({
        ...indicators,
        ...patch,
      }),
    })
  }

  const patchRisk = (patch: Partial<RiskControlConfig>) => {
    patchAI({
      risk_control: defaultRisk({
        ...risk,
        ...patch,
      }),
    })
  }

  const patchThrottle = (patch: Partial<TradeThrottleConfig>) => {
    patchRisk({
      trade_throttle: {
        ...throttle,
        ...patch,
      },
    })
  }

  const createStrategy = async () => {
    if (!token || creatingStrategy) return
    setCreatingStrategy(true)
    try {
      const response = await fetch(
        `${API_BASE}/api/strategies/default-config?lang=${language}`,
        { headers: { Authorization: `Bearer ${token}` } }
      )
      const defaultConfig = response.ok
        ? simplifyConfig(await response.json())
        : simplifyConfig(null)
      defaultConfig.language = language as 'zh' | 'en'
      defaultConfig.ai_config = {
        ...defaultConfig.ai_config!,
        coin_source: defaultCoinSource({
          ...defaultConfig.ai_config?.coin_source,
          source_type: 'binance_dynamic',
          static_coins: [],
          use_watchlist: false,
          binance_dynamic_limit: 10,
        }),
        indicators: defaultIndicators({
          ...defaultConfig.ai_config?.indicators,
          klines: {
            primary_timeframe: '15m',
            primary_count: 30,
            enable_multi_timeframe: false,
            selected_timeframes: ['15m'],
          },
        }),
        risk_control: defaultRisk({
          ...defaultConfig.ai_config?.risk_control,
          max_positions: 3,
          position_sizing_mode: 'margin_based',
          max_leverage: 3,
          btc_eth_max_position_value_ratio: 1,
          altcoin_max_position_value_ratio: 0.5,
          btc_eth_max_margin_ratio: 0.15,
          altcoin_max_margin_ratio: 0.15,
          max_margin_usage: 0.5,
          min_position_size: 12,
          min_risk_reward_ratio: 2,
          min_confidence: 75,
          trade_throttle: bigMoveTradeThrottle,
        }),
        custom_prompt:
          'Rank the local Binance perpetual candidates by liquid market activity, confirm each setup with raw OHLCV candles, and trade only when the configured risk/reward and confidence requirements are met.',
        prompt_sections: undefined,
      }
      const created = await api.createStrategy({
        name: text(language, 'NOFX 本地动态策略', 'NOFX Local Dynamic Strategy'),
        description: text(
          language,
          '使用 Binance 公共永续合约行情动态筛选候选币，再由你配置的 AI 模型结合原始 K 线做决策。',
          'Dynamically rank candidates from public Binance perpetual-market data, then let your configured AI model decide from raw candles.'
        ),
        config: defaultConfig,
      })
      await loadStrategies(created.id)
    } catch (error) {
      notify.error(
        error instanceof Error ? error.message : 'Failed to create strategy'
      )
    } finally {
      setCreatingStrategy(false)
    }
  }

  const saveStrategy = async (
    activateAfter = false,
    overrideConfig?: StrategyConfig,
    successMessage?: string
  ) => {
    if (!selectedStrategy || (!editingConfig && !overrideConfig)) return
    setSaving(true)
    try {
      const config = simplifyConfig(overrideConfig || editingConfig)
      config.language = language as 'zh' | 'en'
      await api.updateStrategy(selectedStrategy.id, {
        name: selectedStrategy.name,
        description: selectedStrategy.description,
        config,
      })
      if (activateAfter) await api.activateStrategy(selectedStrategy.id)
      setHasChanges(false)
      notify.success(
        successMessage ||
          text(
            language,
            activateAfter ? '策略已保存并启用' : '策略已保存',
            activateAfter ? 'Strategy saved and activated' : 'Strategy saved'
          )
      )
      await loadStrategies(selectedStrategy.id)
    } catch (error) {
      notify.error(
        error instanceof Error ? error.message : 'Failed to save strategy'
      )
    } finally {
      setSaving(false)
    }
  }

  const buildAutopilotConfig = (): StrategyConfig => {
    const baseConfig = simplifyConfig(editingConfig)
    baseConfig.language = language as 'zh' | 'en'
    baseConfig.ai_config = {
      ...baseConfig.ai_config!,
      coin_source: defaultCoinSource(baseConfig.ai_config?.coin_source),
      indicators: defaultIndicators(baseConfig.ai_config?.indicators),
      risk_control: defaultRisk(baseConfig.ai_config?.risk_control),
    }
    return baseConfig
  }

  const startAutopilot = async () => {
    if (!selectedStrategy) return
    setSaving(true)
    try {
      const outcome = await launchAutopilot({
        scanIntervalMinutes: 15,
        ensureStrategy: async () => {
          const config = buildAutopilotConfig()
          setEditingConfig(config)
          await api.updateStrategy(selectedStrategy.id, {
            name: selectedStrategy.name,
            description:
              selectedStrategy.description ||
              'Local Binance dynamic candidates evaluated by the configured AI model.',
            config,
          })
          await api.activateStrategy(selectedStrategy.id)
          return selectedStrategy.id
        },
      })

      if (!outcome.ok) {
        notify.error(outcome.message)
        return
      }
      if (outcome.warning) notify.warning(outcome.warning)
      notify.success(text(language, 'NOFX 自动交易已启动', 'NOFX Autopilot started'))
      setHasChanges(false)
      await loadStrategies(selectedStrategy.id)
      navigate(buildDashboardPath(outcome.traderId))
    } finally {
      setSaving(false)
    }
  }

  const activateStrategy = async () => {
    if (!selectedStrategy) return
    try {
      await api.activateStrategy(selectedStrategy.id)
      notify.success(text(language, '策略已启用', 'Strategy activated'))
      await loadStrategies(selectedStrategy.id)
    } catch (error) {
      notify.error(
        error instanceof Error ? error.message : 'Failed to activate strategy'
      )
    }
  }

  const deleteStrategy = async () => {
    if (!selectedStrategy || selectedStrategy.is_active) return
    const confirmed = await confirmToast(
      text(language, '删除这个策略？', 'Delete this strategy?'),
      {
        title: text(language, '确认删除', 'Confirm delete'),
        okText: text(language, '删除', 'Delete'),
        cancelText: text(language, '取消', 'Cancel'),
      }
    )
    if (!confirmed) return
    try {
      await api.deleteStrategy(selectedStrategy.id)
      notify.success(text(language, '策略已删除', 'Strategy deleted'))
      await loadStrategies()
    } catch (error) {
      notify.error(
        error instanceof Error ? error.message : 'Failed to delete strategy'
      )
    }
  }

  const toggleSymbol = (symbol: string) => {
    const normalized = normalizeSymbol(symbol)
    const nextSymbols = selectedSet.has(normalized)
      ? selectedSymbols.filter((item) => normalizeSymbol(item) !== normalized)
      : [...selectedSymbols, symbol].slice(0, maxWatchlistCandidateAssets)
    patchCoinSource({
      source_type: nextSymbols.length > 0 ? 'static' : 'binance_dynamic',
      static_coins: nextSymbols,
      use_watchlist: false,
      binance_dynamic_limit: 10,
    })
  }

  const addWatchlistSymbols = () => {
    const additions = watchlistInput
      .split(/[\s,，;；/]+/)
      .map(normalizeWatchlistToken)
      .filter(Boolean)
    const nextWatchlist = Array.from(
      new Set([...watchlist.map(normalizeWatchlistToken), ...additions])
    ).slice(0, maxWatchlistAssets)
    if (nextWatchlist.length === 0) return
    patchCoinSource({ watchlist: nextWatchlist })
    setWatchlistInput('')
  }

  const removeWatchlistSymbol = (symbol: string) => {
    const normalized = normalizeWatchlistToken(symbol)
    patchCoinSource({
      watchlist: watchlist.filter(
        (item) => normalizeWatchlistToken(item) !== normalized
      ),
    })
  }

  const enableWatchlistCandidates = () => {
    const candidates = watchlistCandidateSymbols(watchlist)
    if (candidates.length === 0) return
    patchCoinSource({
      source_type: 'static',
      static_coins: candidates,
      use_watchlist: true,
      binance_dynamic_limit: 10,
    })
  }

  const selectCandidateSource = (source: CandidateSource) => {
    setCandidateQuery('')
    setScope('all')
    if (source === 'binance_dynamic') {
      patchCoinSource({
        source_type: 'binance_dynamic',
        static_coins: [],
        use_watchlist: false,
        binance_dynamic_limit: 10,
      })
      void loadSymbols()
      return
    }
    if (source === 'watchlist') {
      if (watchlist.length === 0) return
      enableWatchlistCandidates()
      void loadWatchlistSymbols()
      return
    }
    if (selectedSymbols.length === 0) return
    patchCoinSource({
      source_type: 'static',
      static_coins: selectedSymbols,
      use_watchlist: false,
      binance_dynamic_limit: 10,
    })
  }

  const setTimeframe = (timeframe: string) => {
    patchIndicators({
      klines: {
        primary_timeframe: timeframe,
        primary_count: indicators?.klines.primary_count || 30,
        enable_multi_timeframe: false,
        selected_timeframes: [timeframe],
      },
    })
  }

  const setBarCount = (count: number) => {
    patchIndicators({
      klines: {
        primary_timeframe: indicators?.klines.primary_timeframe || '15m',
        primary_count: count,
        enable_multi_timeframe: false,
        selected_timeframes: [indicators?.klines.primary_timeframe || '15m'],
      },
    })
  }

  const applyProfile = (profile: (typeof profileOptions)[number]) => {
    setEditingConfig((previousConfig) => {
      const baseConfig = simplifyConfig(previousConfig)
      const currentAI = baseConfig.ai_config!
      return {
        ...baseConfig,
        language: language as 'zh' | 'en',
        ai_config: {
          ...currentAI,
          indicators: defaultIndicators({
            ...currentAI.indicators,
            klines: {
              primary_timeframe: profile.timeframe,
              primary_count: profile.bars,
              enable_multi_timeframe: false,
              selected_timeframes: [profile.timeframe],
            },
          }),
          risk_control: defaultRisk({
            ...currentAI.risk_control,
            max_positions: profile.maxPositions,
            position_sizing_mode: 'margin_based',
            max_leverage: profile.leverage,
            btc_eth_max_margin_ratio: profile.perPositionMargin,
            altcoin_max_margin_ratio: profile.perPositionMargin,
            min_confidence: profile.confidence,
          }),
          custom_prompt: text(language, profile.promptZh, profile.promptEn),
          prompt_sections: undefined,
        },
      }
    })
    setHasChanges(true)
  }

  const startBacktest = async () => {
    if (!selectedStrategy) return
    if (backtestMode === 'ai_replay' && !backtestModelId) return
    setBacktestStarting(true)
    try {
      const job = await api.startStrategyBacktest(selectedStrategy.id, {
        mode: backtestMode,
        ai_model_id: backtestMode === 'ai_replay' ? backtestModelId : undefined,
        symbol: normalizeSymbol(backtestSymbol),
        timeframe:
          backtestMode === 'trend_v1'
            ? '4h'
            : indicators?.klines.primary_timeframe || '15m',
        start_time: new Date(`${backtestStart}T00:00:00Z`).toISOString(),
        end_time: new Date(`${backtestEnd}T23:59:59Z`).toISOString(),
        initial_balance: 1000,
        fee_bps: 5,
        slippage_bps: 2,
        max_ai_calls: backtestMaxCalls,
        confirm_ai_calls: backtestMode === 'ai_replay',
      })
      setBacktestJob(job)
    } catch (error) {
      notify.error(
        error instanceof Error ? error.message : 'Failed to start replay'
      )
    } finally {
      setBacktestStarting(false)
    }
  }

  const activeCandidateSource: CandidateSource =
    coinSource?.source_type === 'static' && watchlistCandidateMode
      ? 'watchlist'
      : coinSource?.source_type === 'static'
        ? 'static'
        : 'binance_dynamic'

  if (loading) {
    return (
      <div className="flex min-h-[70vh] items-center justify-center">
        <Loader2 className="h-7 w-7 animate-spin text-nofx-gold" />
      </div>
    )
  }

  return (
    <DeepVoidBackground className="min-h-[calc(100vh-64px)] bg-nofx-bg">
      <div className="border-b border-[rgba(26,24,19,0.14)] bg-nofx-bg/75 px-5 py-4 backdrop-blur">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h1 className="text-xl font-semibold text-nofx-text">
              {text(language, 'NOFX Autopilot', 'NOFX Autopilot')}
            </h1>
            <p className="mt-1 text-sm text-nofx-text-muted">
              {text(
                language,
                '默认使用 Binance 公开行情动态筛选候选交易对，再交给你配置的 AI 模型决定交易或等待。',
                'Uses public Binance market data to build a dynamic candidate pool, then lets your configured AI model trade or wait.'
              )}
            </p>
          </div>
          <button
            type="button"
            onClick={startAutopilot}
            disabled={saving || !selectedStrategy}
            className="inline-flex items-center gap-2 rounded-lg bg-nofx-gold px-4 py-2 text-sm font-semibold text-nofx-bg hover:bg-nofx-gold-highlight disabled:opacity-50"
          >
            {saving ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Bot className="h-4 w-4" />
            )}
            {text(language, '启动自动交易', 'Launch Autopilot')}
          </button>
        </div>
      </div>

      <div className="grid min-h-[calc(100vh-137px)] grid-cols-1 lg:grid-cols-[280px_minmax(0,1fr)]">
        <aside className="border-r border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper p-3">
          <div className="mb-2 flex items-center justify-between gap-2 px-2">
            <div className="text-xs font-medium uppercase tracking-wide text-nofx-text-muted">
              {text(language, '我的策略', 'My strategies')}
            </div>
            <button
              type="button"
              onClick={createStrategy}
              disabled={creatingStrategy}
              title={text(language, '新建策略', 'New strategy')}
              aria-label={text(language, '新建策略', 'New strategy')}
              className="inline-flex items-center gap-1 rounded-md border border-nofx-gold/30 bg-nofx-gold/10 px-2 py-1 text-xs font-semibold text-nofx-gold hover:bg-nofx-gold/15 disabled:opacity-50"
            >
              {creatingStrategy ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Plus className="h-3.5 w-3.5" />
              )}
              {text(language, '新建', 'New')}
            </button>
          </div>
          <div className="space-y-2">
            {strategies.map((strategy) => (
              <button
                key={strategy.id}
                type="button"
                onClick={() => {
                  setSelectedStrategy(strategy)
                  setEditingConfig(simplifyConfig(strategy.config))
                  setCandidateQuery('')
                  setScope('all')
                  setHasChanges(false)
                }}
                className={`w-full rounded-lg border px-3 py-3 text-left transition ${
                  selectedStrategy?.id === strategy.id
                    ? 'border-nofx-gold bg-nofx-gold/10'
                    : 'border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter hover:border-[rgba(26,24,19,0.24)]'
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="line-clamp-2 text-sm font-medium text-nofx-text">
                    {strategy.name}
                  </span>
                  {strategy.is_active ? (
                    <span className="rounded bg-nofx-success/15 px-1.5 py-0.5 text-[10px] text-nofx-success">
                      {text(language, '启用', 'Active')}
                    </span>
                  ) : null}
                </div>
                {strategy.description ? (
                  <div className="mt-1 line-clamp-2 text-xs text-nofx-text-muted">
                    {strategy.description}
                  </div>
                ) : null}
              </button>
            ))}
          </div>
        </aside>

        <main className="overflow-y-auto p-5">
          {selectedStrategy && aiConfig && coinSource && indicators && risk ? (
            <div className="mx-auto max-w-7xl space-y-4">
              <section className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                <div className="flex flex-wrap items-start justify-between gap-4">
                  <div className="min-w-0 flex-1">
                    <input
                      value={selectedStrategy.name}
                      onChange={(event) => {
                        setSelectedStrategy({
                          ...selectedStrategy,
                          name: event.target.value,
                        })
                        setHasChanges(true)
                      }}
                      className="w-full bg-transparent text-lg font-semibold text-nofx-text outline-none"
                    />
                    <input
                      value={selectedStrategy.description || ''}
                      onChange={(event) => {
                        setSelectedStrategy({
                          ...selectedStrategy,
                          description: event.target.value,
                        })
                        setHasChanges(true)
                      }}
                      placeholder={text(
                        language,
                        '策略说明',
                        'One-line strategy note'
                      )}
                      className="mt-1 w-full bg-transparent text-sm text-nofx-text-muted outline-none placeholder:text-nofx-text-muted/50"
                    />
                    {hasChanges ? (
                      <div className="mt-2 text-xs text-nofx-gold">
                        {text(language, '有未保存的修改', 'Unsaved changes')}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <button
                      type="button"
                      onClick={() => void saveStrategy(true)}
                      disabled={saving}
                      className="inline-flex items-center gap-2 rounded-lg bg-nofx-success px-3 py-2 text-sm font-semibold text-nofx-bg disabled:opacity-45"
                    >
                      <Check className="h-4 w-4" />
                      {text(language, '保存并启用', 'Save and use')}
                    </button>
                    <button
                      type="button"
                      onClick={() => void saveStrategy()}
                      disabled={saving || !hasChanges}
                      className="inline-flex items-center gap-2 rounded-lg bg-nofx-gold px-3 py-2 text-sm font-semibold text-nofx-bg disabled:opacity-45"
                    >
                      <Save className="h-4 w-4" />
                      {text(language, '保存', 'Save')}
                    </button>
                    {!selectedStrategy.is_active ? (
                      <button
                        type="button"
                        onClick={activateStrategy}
                        className="rounded-lg border border-nofx-success/30 px-3 py-2 text-sm text-nofx-success"
                      >
                        {text(language, '启用', 'Activate')}
                      </button>
                    ) : null}
                    {!selectedStrategy.is_active ? (
                      <button
                        type="button"
                        onClick={() => void deleteStrategy()}
                        className="inline-flex items-center gap-2 rounded-lg border border-nofx-danger/30 px-3 py-2 text-sm text-nofx-danger"
                      >
                        <Trash2 className="h-4 w-4" />
                        {text(language, '删除', 'Delete')}
                      </button>
                    ) : null}
                  </div>
                </div>
              </section>

              <section className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <div className="flex items-center gap-2 text-sm font-semibold text-nofx-text">
                      <Sparkles className="h-4 w-4 text-nofx-gold" />
                      {text(language, '候选交易对来源', 'Candidate sources')}
                    </div>
                    <div className="mt-1 text-xs text-nofx-text-muted">
                      {text(
                        language,
                        '行情候选来自交易所公开市场数据；AI 模型只负责最终交易判断。',
                        'Candidate symbols come from public exchange market data; the configured AI model makes the final decision.'
                      )}
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <button
                      type="button"
                      onClick={() => selectCandidateSource('binance_dynamic')}
                      disabled={symbolsLoading}
                      className={`inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-xs disabled:opacity-50 ${
                        activeCandidateSource === 'binance_dynamic'
                          ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                          : 'border-[rgba(26,24,19,0.14)] text-nofx-text-muted hover:text-nofx-text'
                      }`}
                    >
                      <RefreshCw
                        className={`h-3.5 w-3.5 ${symbolsLoading ? 'animate-spin' : ''}`}
                      />
                      {text(language, 'Binance 动态候选', 'Binance dynamic')}
                    </button>
                    <button
                      type="button"
                      onClick={() => selectCandidateSource('watchlist')}
                      disabled={watchlistLoading || watchlist.length === 0}
                      className={`inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-xs disabled:opacity-50 ${
                        activeCandidateSource === 'watchlist'
                          ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                          : 'border-[rgba(26,24,19,0.14)] text-nofx-text-muted hover:text-nofx-text'
                      }`}
                    >
                      <Target className="h-3.5 w-3.5" />
                      {text(language, '我的自选候选', 'My watchlist')}
                    </button>
                    <button
                      type="button"
                      onClick={() => selectCandidateSource('static')}
                      disabled={selectedSymbols.length === 0}
                      className={`inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-xs disabled:opacity-50 ${
                        activeCandidateSource === 'static'
                          ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                          : 'border-[rgba(26,24,19,0.14)] text-nofx-text-muted hover:text-nofx-text'
                      }`}
                    >
                      <Target className="h-3.5 w-3.5" />
                      {text(language, '固定交易对', 'Fixed symbols')}
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        watchlistCandidateMode
                          ? void loadWatchlistSymbols()
                          : void loadSymbols()
                      }
                      disabled={symbolsLoading || watchlistLoading}
                      className="inline-flex items-center gap-2 rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper px-3 py-2 text-xs text-nofx-text-muted hover:text-nofx-text disabled:opacity-50"
                    >
                      <RefreshCw
                        className={`h-3.5 w-3.5 ${symbolsLoading || watchlistLoading ? 'animate-spin' : ''}`}
                      />
                      {text(language, '刷新', 'Refresh')}
                    </button>
                  </div>
                </div>

                <div className="mt-4 grid gap-2 md:grid-cols-[minmax(0,1fr)_auto]">
                  <input
                    type="search"
                    value={candidateQuery}
                    onChange={(event) => setCandidateQuery(event.target.value)}
                    placeholder={text(
                      language,
                      '搜索候选交易对，例如 BTC、ETH 或 QQQ',
                      'Search candidates, e.g. BTC, ETH, or QQQ'
                    )}
                    aria-label={text(
                      language,
                      '搜索候选交易对',
                      'Search candidate symbols'
                    )}
                    className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text outline-none focus:border-nofx-gold"
                  />
                  {candidateQuery ? (
                    <button
                      type="button"
                      onClick={() => setCandidateQuery('')}
                      className="rounded-lg border border-[rgba(26,24,19,0.14)] px-4 py-2 text-sm text-nofx-text-muted hover:text-nofx-text"
                    >
                      {text(language, '清除搜索', 'Clear search')}
                    </button>
                  ) : null}
                </div>

                <div className="mt-3 flex flex-wrap gap-2">
                  {scopeOptions.map((option) => {
                    const count =
                      option.value === 'all'
                        ? visibleSymbols.length
                        : visibleSymbols.filter(
                            (item) => item.category === option.value
                          ).length
                    return (
                      <button
                        key={option.value}
                        type="button"
                        onClick={() => setScope(option.value)}
                        className={`rounded-lg border px-3 py-2 text-xs transition ${
                          scope === option.value
                            ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                            : 'border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper text-nofx-text-muted hover:text-nofx-text'
                        }`}
                      >
                        {text(language, option.zh, option.en)}
                        {count > 0 ? (
                          <span className="ml-2 opacity-70">{count}</span>
                        ) : null}
                      </button>
                    )
                  })}
                </div>

                {symbolsError || watchlistError ? (
                  <div className="mt-4 rounded-lg border border-nofx-gold/20 bg-nofx-gold/10 px-3 py-2 text-xs text-nofx-gold">
                    {symbolsError || watchlistError}
                  </div>
                ) : null}

                <div className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                  {visibleSymbols.map((item) => {
                    const symbol = normalizeSymbol(item.symbol)
                    const selected = selectedSet.has(symbol)
                    return (
                      <button
                        key={`${item.exchange}-${symbol}`}
                        type="button"
                        onClick={() => toggleSymbol(symbol)}
                        className={`rounded-lg border p-3 text-left transition ${
                          selected
                            ? 'border-nofx-gold bg-nofx-gold/10'
                            : 'border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper hover:border-[rgba(26,24,19,0.24)]'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-mono text-sm font-semibold text-nofx-text">
                            {symbol}
                          </span>
                          <span className="text-[10px] text-nofx-text-muted">
                            {formatChange(item.change_24h_pct)}
                          </span>
                        </div>
                        <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-nofx-text-muted">
                          <span>{categoryLabel(item.category, language)}</span>
                          <span>
                            {item.mark_price
                              ? `$${item.mark_price.toFixed(2)}`
                              : text(language, '可用', 'ready')}
                          </span>
                        </div>
                      </button>
                    )
                  })}
                </div>

                {visibleSymbols.length === 0 && !symbolsLoading && !watchlistLoading ? (
                  <div className="mt-4 rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper px-3 py-3 text-sm text-nofx-text-muted">
                    {candidateQuery
                      ? text(
                          language,
                          '没有匹配的候选交易对，请更换搜索词。',
                          'No candidates match this search.'
                        )
                      : text(
                          language,
                          '候选交易对暂不可用，请稍后刷新。',
                          'Candidate symbols are unavailable; refresh later.'
                        )}
                  </div>
                ) : null}
              </section>

              <section className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <div className="flex items-center gap-2 text-sm font-semibold text-nofx-text">
                      <Target className="h-4 w-4 text-nofx-gold" />
                      {text(language, '我的自选', 'My watchlist')}
                    </div>
                    <div className="mt-1 max-w-3xl text-xs leading-5 text-nofx-text-muted">
                      {text(
                        language,
                        '使用交易所公开行情观察自选交易对。启用后，这些自选将成为 AI 每轮分析的候选池；添加自选本身不会启动自动交易。',
                        'Use public exchange quotes to observe a personal watchlist. Once enabled, these symbols become the AI candidate pool; adding symbols alone does not start trading.'
                      )}
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <button
                      type="button"
                      onClick={enableWatchlistCandidates}
                      disabled={watchlist.length === 0}
                      className={`inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-xs font-semibold disabled:opacity-40 ${
                        watchlistCandidateMode
                          ? 'border-nofx-success/30 bg-nofx-success/10 text-nofx-success'
                          : 'border-nofx-gold/30 bg-nofx-gold/10 text-nofx-gold'
                      }`}
                    >
                      <Bot className="h-3.5 w-3.5" />
                      {watchlistCandidateMode
                        ? text(language, '正在作为 AI 候选池', 'Active AI candidate pool')
                        : text(language, '启用为 AI 候选池', 'Use as AI candidate pool')}
                    </button>
                    <button
                      type="button"
                      onClick={() => void loadWatchlistSymbols()}
                      disabled={watchlistLoading}
                      className="inline-flex items-center gap-2 rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper px-3 py-2 text-xs text-nofx-text-muted hover:text-nofx-text disabled:opacity-50"
                    >
                      <RefreshCw
                        className={`h-3.5 w-3.5 ${watchlistLoading ? 'animate-spin' : ''}`}
                      />
                      {text(language, '刷新行情', 'Refresh quotes')}
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        void saveStrategy(
                          false,
                          undefined,
                          text(language, '自选已保存', 'Watchlist saved')
                        )
                      }
                      disabled={saving || !hasChanges}
                      className="inline-flex items-center gap-2 rounded-lg border border-nofx-gold/30 bg-nofx-gold/10 px-3 py-2 text-xs font-semibold text-nofx-gold disabled:opacity-40"
                    >
                      <Save className="h-3.5 w-3.5" />
                      {text(language, '保存自选', 'Save watchlist')}
                    </button>
                  </div>
                </div>

                <div className="mt-4 grid gap-2 md:grid-cols-[minmax(0,1fr)_auto]">
                  <input
                    value={watchlistInput}
                    onChange={(event) => setWatchlistInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') addWatchlistSymbols()
                    }}
                    placeholder={text(
                      language,
                      '输入代码，可用斜杠、逗号或空格分隔',
                      'Enter symbols separated by slash, comma, or space'
                    )}
                    className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text outline-none"
                  />
                  <button
                    type="button"
                    onClick={addWatchlistSymbols}
                    disabled={!watchlistInput.trim()}
                    className="inline-flex items-center justify-center gap-2 rounded-lg border border-nofx-gold/30 bg-nofx-gold/10 px-4 py-2 text-sm font-semibold text-nofx-gold disabled:opacity-40"
                  >
                    <Plus className="h-4 w-4" />
                    {text(language, '添加到自选', 'Add to watchlist')}
                  </button>
                </div>

                {watchlist.length > 0 ? (
                  <div className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                    {watchlist.map((symbol) => {
                      const resolved = resolveWatchlistAsset(
                        symbol,
                        watchlistSymbols
                      )
                      return (
                        <div
                          key={resolved.requested}
                          className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper p-3"
                        >
                          <div className="flex items-start justify-between gap-2">
                            <div>
                              <div className="font-mono text-sm font-semibold text-nofx-text">
                                {resolved.requested}
                              </div>
                              {resolved.asset ? (
                                <div className="mt-1 text-[11px] text-nofx-success">
                                  {resolved.asset.symbol}
                                </div>
                              ) : (
                                <div className="mt-1 text-[11px] text-nofx-text-muted">
                                  {text(
                                    language,
                                    '当前行情中未找到',
                                    'Unavailable in current quotes'
                                  )}
                                </div>
                              )}
                            </div>
                            <button
                              type="button"
                              aria-label={text(
                                language,
                                `移除 ${resolved.requested}`,
                                `Remove ${resolved.requested}`
                              )}
                              onClick={() => removeWatchlistSymbol(symbol)}
                              className="rounded p-1 text-nofx-text-muted hover:bg-nofx-danger/10 hover:text-nofx-danger"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </button>
                          </div>
                          {resolved.asset ? (
                            <div className="mt-3 flex items-end justify-between gap-2">
                              <div className="text-xs text-nofx-text-muted">
                                {categoryLabel(resolved.asset.category, language)}
                              </div>
                              <div className="font-mono text-base font-semibold text-nofx-text">
                                {formatWatchlistPrice(resolved.asset.mark_price)}
                              </div>
                            </div>
                          ) : null}
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <div className="mt-4 flex items-center gap-2 rounded-lg border border-dashed border-[rgba(26,24,19,0.18)] px-3 py-4 text-xs text-nofx-text-muted">
                    <Shield className="h-4 w-4" />
                    {text(
                      language,
                      '暂无自选；添加后只用于行情观察。',
                      'No watchlist items yet; added symbols are for observation only.'
                    )}
                  </div>
                )}
              </section>

              <section className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                <div className="text-sm font-semibold text-nofx-text">
                  {text(language, '历史 AI 回放', 'Historical AI replay')}
                </div>
                <div className="mt-1 max-w-3xl text-xs leading-5 text-nofx-text-muted">
                  {text(
                    language,
                    '对所填交易对做单标的回放。默认使用可复现的趋势基准；也可切换为真实 AI 回放。',
                    'Replay one symbol at a time. The reproducible trend benchmark is the default; real AI replay is also available.'
                  )}
                </div>
                <div className="mt-4 grid gap-3 md:grid-cols-2 lg:grid-cols-5">
                  <label className="text-xs text-nofx-text-muted">
                    {text(language, '回放模式', 'Replay mode')}
                    <select
                      value={backtestMode}
                      onChange={(event) =>
                        setBacktestMode(
                          event.target.value as 'trend_v1' | 'ai_replay'
                        )
                      }
                      className="mt-1 w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                    >
                      <option value="trend_v1">
                        {text(language, '趋势基准', 'Trend benchmark')}
                      </option>
                      <option value="ai_replay">
                        {text(language, '真实 AI 回放', 'Real AI replay')}
                      </option>
                    </select>
                  </label>
                  <label className="text-xs text-nofx-text-muted">
                    {text(language, '交易对', 'Symbol')}
                    <input
                      value={backtestSymbol}
                      onChange={(event) => setBacktestSymbol(event.target.value)}
                      className="mt-1 w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                    />
                  </label>
                  <label className="text-xs text-nofx-text-muted">
                    {text(language, '开始日期', 'Start date')}
                    <input
                      type="date"
                      value={backtestStart}
                      onChange={(event) => setBacktestStart(event.target.value)}
                      className="mt-1 w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                    />
                  </label>
                  <label className="text-xs text-nofx-text-muted">
                    {text(language, '结束日期', 'End date')}
                    <input
                      type="date"
                      value={backtestEnd}
                      onChange={(event) => setBacktestEnd(event.target.value)}
                      className="mt-1 w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                    />
                  </label>
                  <label className="text-xs text-nofx-text-muted">
                    {text(language, '最大 AI 调用', 'Max AI calls')}
                    <input
                      type="number"
                      min={1}
                      max={50}
                      value={backtestMaxCalls}
                      disabled={backtestMode !== 'ai_replay'}
                      onChange={(event) =>
                        setBacktestMaxCalls(
                          clampNumber(Number(event.target.value) || 1, 1, 50)
                        )
                      }
                      className="mt-1 w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text disabled:opacity-45"
                    />
                  </label>
                </div>
                {backtestMode === 'ai_replay' ? (
                  <label className="mt-3 block max-w-sm text-xs text-nofx-text-muted">
                    {text(language, 'AI 模型', 'AI model')}
                    <select
                      value={backtestModelId}
                      onChange={(event) => setBacktestModelId(event.target.value)}
                      className="mt-1 w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                    >
                      {models.map((model) => (
                        <option key={model.id} value={model.id}>
                          {model.name}
                        </option>
                      ))}
                    </select>
                  </label>
                ) : null}
                <button
                  type="button"
                  onClick={startBacktest}
                  disabled={
                    backtestStarting ||
                    (backtestMode === 'ai_replay' && !backtestModelId) ||
                    !backtestSymbol.trim() ||
                    (backtestJob !== null &&
                      backtestJob.status !== 'completed' &&
                      backtestJob.status !== 'failed')
                  }
                  className="mt-3 inline-flex items-center gap-2 rounded-lg border border-nofx-gold/30 bg-nofx-gold/10 px-4 py-2 text-sm font-semibold text-nofx-gold disabled:opacity-40"
                >
                  {backtestStarting ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <RefreshCw className="h-4 w-4" />
                  )}
                  {backtestMode === 'ai_replay'
                    ? text(language, '开始真实 AI 回放', 'Start real AI replay')
                    : text(language, '运行趋势基准', 'Run trend benchmark')}
                </button>
                {backtestJob?.status === 'failed' ? (
                  <div className="mt-3 rounded-lg border border-nofx-danger/20 bg-nofx-danger/10 px-3 py-2 text-sm text-nofx-danger">
                    {backtestJob.error || 'Historical replay failed'}
                  </div>
                ) : null}
                {backtestJob?.status === 'completed' && backtestJob.result ? (
                  <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
                    {[
                      [
                        text(language, '总收益', 'Total return'),
                        `${backtestJob.result.total_return_pct.toFixed(2)}%`,
                      ],
                      [
                        text(language, '最大回撤', 'Max drawdown'),
                        `${backtestJob.result.max_drawdown_pct.toFixed(2)}%`,
                      ],
                      [
                        text(language, '交易次数', 'Trades'),
                        String(backtestJob.result.trade_count),
                      ],
                      [
                        text(language, '胜率', 'Win rate'),
                        `${backtestJob.result.win_rate_pct.toFixed(1)}%`,
                      ],
                      [
                        backtestJob.mode === 'ai_replay'
                          ? text(language, '模型调用', 'AI calls')
                          : text(language, '处理 K 线', 'Candles processed'),
                        String(backtestJob.result.decision_calls),
                      ],
                    ].map(([label, value]) => (
                      <div
                        key={label}
                        className="rounded-lg bg-nofx-bg-deeper px-3 py-3"
                      >
                        <div className="text-xs text-nofx-text-muted">
                          {label}
                        </div>
                        <div className="mt-1 font-mono text-lg font-semibold text-nofx-text">
                          {value}
                        </div>
                      </div>
                    ))}
                  </div>
                ) : null}
              </section>

              <details className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper p-4">
                <summary className="cursor-pointer text-sm font-semibold text-nofx-text">
                  {text(language, '高级设置', 'Advanced settings')}
                </summary>
                <div className="mt-4 rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                  <div className="mb-3 text-sm font-semibold text-nofx-text">
                    {text(language, '交易风格', 'Trading style')}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {profileOptions.map((profile) => (
                      <button
                        key={profile.value}
                        type="button"
                        onClick={() => applyProfile(profile)}
                        className={`rounded-lg border px-3 py-2 text-sm transition ${
                          activeProfile === profile.value
                            ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                            : 'border-[rgba(26,24,19,0.14)] text-nofx-text-muted hover:text-nofx-text'
                        }`}
                      >
                        {text(language, profile.zh, profile.en)}
                      </button>
                    ))}
                    {activeProfile === 'custom' ? (
                      <span className="rounded-lg border border-nofx-gold/40 bg-nofx-gold/10 px-3 py-2 text-sm text-nofx-gold">
                        {text(language, '自定义', 'Custom')}
                      </span>
                    ) : null}
                  </div>
                </div>

                <div className="mt-4 grid gap-4 lg:grid-cols-2">
                  <div className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                    <div className="mb-4 flex items-center gap-2 text-sm font-semibold text-nofx-text">
                      <Sparkles className="h-4 w-4 text-nofx-gold" />
                      {text(language, '原始 K 线', 'Raw candles')}
                    </div>
                    <div className="space-y-4">
                      <div>
                        <div className="mb-2 text-xs text-nofx-text-muted">
                          {text(language, '时间周期', 'Timeframe')}
                        </div>
                        <div className="flex flex-wrap gap-2">
                          {timeframeOptions.map((timeframe) => (
                            <button
                              key={timeframe}
                              type="button"
                              onClick={() => setTimeframe(timeframe)}
                              className={`rounded-lg border px-3 py-2 text-sm ${
                                indicators.klines.primary_timeframe === timeframe
                                  ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                                  : 'border-[rgba(26,24,19,0.14)] text-nofx-text-muted hover:text-nofx-text'
                              }`}
                            >
                              {timeframe}
                            </button>
                          ))}
                        </div>
                      </div>
                      <div>
                        <div className="mb-2 text-xs text-nofx-text-muted">
                          {text(language, 'K 线数量', 'Candle count')}
                        </div>
                        <div className="flex flex-wrap gap-2">
                          {barCountOptions.map((count) => (
                            <button
                              key={count}
                              type="button"
                              onClick={() => setBarCount(count)}
                              className={`rounded-lg border px-3 py-2 text-sm ${
                                indicators.klines.primary_count === count
                                  ? 'border-nofx-gold bg-nofx-gold/10 text-nofx-gold'
                                  : 'border-[rgba(26,24,19,0.14)] text-nofx-text-muted hover:text-nofx-text'
                              }`}
                            >
                              {count}
                            </button>
                          ))}
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                    <div className="mb-4 text-sm font-semibold text-nofx-text">
                      {text(language, '交易参数', 'Trading parameters')}
                    </div>
                    <div className="grid gap-4 sm:grid-cols-2">
                      <label className="space-y-2 text-xs text-nofx-text-muted">
                        <span>{text(language, '最大持仓数', 'Max positions')}</span>
                        <input
                          type="number"
                          min={1}
                          max={20}
                          value={risk.max_positions}
                          onChange={(event) =>
                            patchRisk({
                              max_positions: clampNumber(
                                Number(event.target.value) || 1,
                                1,
                                20
                              ),
                            })
                          }
                          className="w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                        />
                      </label>
                      <label className="space-y-2 text-xs text-nofx-text-muted">
                        <span>{text(language, '最大杠杆', 'Maximum leverage')}</span>
                        <select
                          value={risk.max_leverage}
                          onChange={(event) =>
                            patchRisk({ max_leverage: Number(event.target.value) })
                          }
                          className="w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                        >
                          {leverageOptions.map((leverage) => (
                            <option key={leverage} value={leverage}>
                              {leverage}x
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="space-y-2 text-xs text-nofx-text-muted">
                        <span>{text(language, '入场置信度', 'Entry confidence')}</span>
                        <input
                          type="number"
                          min={1}
                          max={100}
                          value={risk.min_confidence}
                          onChange={(event) =>
                            patchRisk({
                              min_confidence: clampNumber(
                                Number(event.target.value) || 1,
                                1,
                                100
                              ),
                            })
                          }
                          className="w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                        />
                      </label>
                      <label className="space-y-2 text-xs text-nofx-text-muted">
                        <span>{text(language, '保证金使用上限', 'Margin usage limit')}</span>
                        <input
                          type="number"
                          min={0.05}
                          max={1}
                          step={0.05}
                          value={risk.max_margin_usage}
                          onChange={(event) =>
                            patchRisk({
                              max_margin_usage: clampNumber(
                                Number(event.target.value) || 0.05,
                                0.05,
                                1
                              ),
                            })
                          }
                          className="w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                        />
                      </label>
                    </div>
                  </div>
                </div>

                <div className="mt-4 rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                  <div className="mb-2 text-sm font-semibold text-nofx-text">
                    {text(language, '交易节流', 'Trade throttle')}
                  </div>
                  <div className="grid gap-4 sm:grid-cols-3">
                    {(
                      [
                        ['reentry_cooldown_minutes', '重入冷却（分钟）', 'Re-entry cooldown (min)'],
                        ['max_opens_per_hour', '每小时最大开仓', 'Max opens per hour'],
                        ['max_opens_per_cycle', '每周期最大开仓', 'Max opens per cycle'],
                      ] as const
                    ).map(([key, zh, en]) => (
                      <label key={key} className="space-y-2 text-xs text-nofx-text-muted">
                        <span>{text(language, zh, en)}</span>
                        <input
                          type="number"
                          min={1}
                          value={throttle[key]}
                          onChange={(event) =>
                            patchThrottle({
                              [key]: Math.max(1, Number(event.target.value) || 1),
                            })
                          }
                          className="w-full rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text"
                        />
                      </label>
                    ))}
                  </div>
                </div>

                <div className="mt-4 rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter p-4">
                  <div className="mb-2 text-sm font-semibold text-nofx-text">
                    {text(language, '策略备注', 'Strategy note')}
                  </div>
                  <textarea
                    value={aiConfig.custom_prompt || ''}
                    onChange={(event) =>
                      patchAI({ custom_prompt: event.target.value })
                    }
                    placeholder={text(
                      language,
                      '例如：只交易清晰趋势；当候选信号与 K 线冲突时跳过入场。',
                      'Example: only trade clean trends; skip entries when market data conflicts with candles.'
                    )}
                    className="h-28 w-full resize-none rounded-lg border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-2 text-sm text-nofx-text outline-none placeholder:text-nofx-text-muted/50"
                  />
                </div>
              </details>
            </div>
          ) : (
            <div className="flex h-full items-center justify-center">
              <button
                type="button"
                onClick={createStrategy}
                disabled={creatingStrategy}
                className="inline-flex items-center gap-2 rounded-lg bg-nofx-gold px-4 py-2 text-sm font-semibold text-nofx-bg hover:bg-nofx-gold-highlight"
              >
                {creatingStrategy ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Plus className="h-4 w-4" />
                )}
                {text(language, '新建策略', 'Create a strategy')}
              </button>
            </div>
          )}
        </main>
      </div>
    </DeepVoidBackground>
  )
}
