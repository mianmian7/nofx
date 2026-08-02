import { useState, useEffect } from 'react'
import type {
  AIModel,
  Exchange,
  CreateTraderRequest,
  Strategy,
  TraderConfigData,
} from '../../types'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import {
  Pencil,
  Plus,
  X as IconX,
  Sparkles,
  ExternalLink,
  UserPlus,
} from 'lucide-react'
import { httpClient } from '../../lib/httpClient'
import { NofxSelect } from '../ui/select'
import { ExecutionModeSelector } from './ExecutionModeSelector'
import { FallbackModelSelector } from './FallbackModelSelector'
import { SameAPIModelSelector } from './SameAPIModelSelector'

// Extract the name part after the underscore
function getShortName(fullName: string): string {
  const parts = fullName.split('_')
  return parts.length > 1 ? parts[parts.length - 1] : fullName
}

export function getAIModelOptionLabel(model: AIModel): string {
  const configName = getShortName(model.name || model.id).toUpperCase()
  return model.customModelName
    ? `${configName} · ${model.customModelName}`
    : configName
}

function getStrategyAIConfig(strategy: Strategy) {
  return (
    strategy.config.ai_config ||
    (strategy.config.coin_source && strategy.config.risk_control
      ? {
          coin_source: strategy.config.coin_source,
          risk_control: strategy.config.risk_control,
        }
      : null)
  )
}

// Exchange registration link configuration
const EXCHANGE_REGISTRATION_LINKS: Record<
  string,
  { url: string; hasReferral?: boolean }
> = {
  binance: {
    url: 'https://www.binance.com/join?ref=NOFXENG',
    hasReferral: true,
  },
  okx: { url: 'https://www.okx.com/join/1865360', hasReferral: true },
  bybit: { url: 'https://partner.bybit.com/b/83856', hasReferral: true },
  hyperliquid: {
    url: 'https://app.hyperliquid.xyz/join/AITRADING',
    hasReferral: true,
  },
  aster: {
    url: 'https://www.asterdex.com/en/referral/fdfc0e',
    hasReferral: true,
  },
  lighter: {
    url: 'https://app.lighter.xyz/?referral=68151432',
    hasReferral: true,
  },
}
// Internal form state type
interface FormState {
  trader_id?: string
  trader_name: string
  ai_model: string
  exchange_id: string
  strategy_id: string
  is_cross_margin: boolean
  show_in_competition: boolean
  invert_signals: boolean
  scan_interval_minutes: number
  startup_delay_minutes: number
  fallback_model_names: string[]
  fallback_ai_model_ids: string[]
  execution_mode: 'paper' | 'live'
  initial_balance: number
}

interface TraderConfigModalProps {
  isOpen: boolean
  onClose: () => void
  traderData?: TraderConfigData | null
  isEditMode?: boolean
  availableModels?: AIModel[]
  availableExchanges?: Exchange[]
  onSave?: (data: CreateTraderRequest) => Promise<void>
}

export function TraderConfigModal({
  isOpen,
  onClose,
  traderData,
  isEditMode = false,
  availableModels = [],
  availableExchanges = [],
  onSave,
}: TraderConfigModalProps) {
  const { language } = useLanguage()
  const [formData, setFormData] = useState<FormState>({
    trader_name: '',
    ai_model: '',
    exchange_id: '',
    strategy_id: '',
    is_cross_margin: true,
    show_in_competition: true,
    invert_signals: false,
    scan_interval_minutes: 15,
    startup_delay_minutes: 0,
    fallback_model_names: [],
    fallback_ai_model_ids: [],
    execution_mode: 'paper',
    initial_balance: 10000,
  })
  const [isSaving, setIsSaving] = useState(false)
  const [strategies, setStrategies] = useState<Strategy[]>([])

  // Fetch the user's strategy list
  useEffect(() => {
    const fetchStrategies = async () => {
      try {
        const result = await httpClient.get<{ strategies: Strategy[] }>(
          '/api/strategies'
        )
        if (result.success && result.data?.strategies) {
          const strategyList = result.data.strategies
          setStrategies(strategyList)
          // If no strategy is selected, default to the active strategy
          if (!formData.strategy_id && !isEditMode) {
            const activeStrategy = strategyList.find((s) => s.is_active)
            if (activeStrategy) {
              setFormData((prev) => ({
                ...prev,
                strategy_id: activeStrategy.id,
              }))
            } else if (strategyList.length > 0) {
              setFormData((prev) => ({
                ...prev,
                strategy_id: strategyList[0].id,
              }))
            }
          }
        }
      } catch (error) {
        console.error('Failed to fetch strategies:', error)
      }
    }
    if (isOpen) {
      fetchStrategies()
    }
  }, [isOpen])

  useEffect(() => {
    if (traderData) {
      setFormData({
        ...traderData,
        strategy_id: traderData.strategy_id || '',
        execution_mode: traderData.execution_mode || 'paper',
        initial_balance: traderData.initial_balance || 10000,
        invert_signals: traderData.invert_signals ?? false,
        startup_delay_minutes: traderData.startup_delay_minutes ?? 0,
        fallback_model_names: traderData.fallback_model_names || [],
        fallback_ai_model_ids: traderData.fallback_ai_model_ids || [],
      })
    } else if (!isEditMode) {
      setFormData({
        trader_name: '',
        ai_model: availableModels[0]?.id || '',
        exchange_id: availableExchanges[0]?.id || '',
        strategy_id: '',
        is_cross_margin: true,
        show_in_competition: true,
        invert_signals: false,
        scan_interval_minutes: 15,
        startup_delay_minutes: 0,
        fallback_model_names: [],
        fallback_ai_model_ids: [],
        execution_mode: 'paper',
        initial_balance: 10000,
      })
    }
  }, [traderData, isEditMode, availableModels, availableExchanges])

  if (!isOpen) return null

  const handleInputChange = (field: keyof FormState, value: any) => {
    setFormData((prev) => ({ ...prev, [field]: value }))
  }

  const handleExchangeChange = (exchangeId: string) => {
    setFormData((prev) => ({ ...prev, exchange_id: exchangeId }))
  }

  const handleSave = async () => {
    if (!onSave) return

    const originalBalance = traderData?.initial_balance || 10000
    const resetsPaperAccount =
      Boolean(isEditMode && formData.execution_mode === 'paper') &&
      formData.initial_balance !== originalBalance
    if (
      resetsPaperAccount &&
      !window.confirm(t('confirmResetPaperBalance', language))
    ) {
      return
    }

    setIsSaving(true)
    try {
      const saveData: CreateTraderRequest = {
        name: formData.trader_name,
        ai_model_id: formData.ai_model,
        exchange_id: formData.exchange_id,
        strategy_id: formData.strategy_id,
        is_cross_margin: formData.is_cross_margin,
        show_in_competition: formData.show_in_competition,
        invert_signals: formData.invert_signals,
        scan_interval_minutes: formData.scan_interval_minutes,
        startup_delay_minutes: Math.min(
          Math.max(0, formData.startup_delay_minutes),
          Math.max(0, formData.scan_interval_minutes - 1)
        ),
        fallback_model_names: formData.fallback_model_names,
        fallback_ai_model_ids: formData.fallback_ai_model_ids,
        execution_mode: formData.execution_mode,
        initial_balance:
          formData.execution_mode === 'paper'
            ? formData.initial_balance
            : undefined,
        reset_paper_account: resetsPaperAccount,
      }

      await onSave(saveData)
    } catch (error) {
      console.error(t('saveFailed', language) + ':', error)
    } finally {
      setIsSaving(false)
    }
  }

  const selectedStrategy = strategies.find((s) => s.id === formData.strategy_id)
  const selectedAIModel = availableModels.find(
    (model) => model.id === formData.ai_model
  )

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50 backdrop-blur-sm p-4 overflow-y-auto">
      <div
        className="bg-nofx-bg-lighter border border-nofx-gold/20 rounded-xl shadow-2xl max-w-2xl w-full my-8"
        style={{ maxHeight: 'calc(100vh - 4rem)' }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between p-6 border-b border-nofx-gold/20 bg-nofx-bg-lighter sticky top-0 z-10 rounded-t-xl">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-nofx-gold flex items-center justify-center text-white">
              {isEditMode ? (
                <Pencil className="w-5 h-5" />
              ) : (
                <Plus className="w-5 h-5" />
              )}
            </div>
            <div>
              <h2 className="text-xl font-bold text-nofx-text">
                {isEditMode
                  ? t('editTrader', language)
                  : t('createTrader', language)}
              </h2>
              <p className="text-sm text-nofx-text-muted mt-1">
                {isEditMode
                  ? t('editTraderConfig', language)
                  : t('selectStrategyAndConfigParams', language)}
              </p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="w-8 h-8 rounded-lg text-nofx-text-muted hover:text-nofx-text hover:bg-nofx-bg-deeper transition-colors flex items-center justify-center"
          >
            <IconX className="w-4 h-4" />
          </button>
        </div>

        {/* Content */}
        <div
          className="p-6 space-y-6 overflow-y-auto"
          style={{ maxHeight: 'calc(100vh - 16rem)' }}
        >
          <ExecutionModeSelector
            value={formData.execution_mode}
            onChange={(value) => handleInputChange('execution_mode', value)}
            language={language}
          />
          {formData.execution_mode === 'paper' && (
            <div className="bg-nofx-bg border border-nofx-gold/20 rounded-lg p-5">
              <label className="text-sm text-nofx-text block mb-2">
                {t('paperInitialBalanceLabel', language)}
              </label>
              <input
                type="number"
                value={formData.initial_balance}
                onChange={(e) =>
                  handleInputChange(
                    'initial_balance',
                    Math.max(1, Number(e.target.value) || 1)
                  )
                }
                min="1"
                step="100"
                className="w-full px-3 py-2 bg-nofx-bg-lighter border border-nofx-gold/20 rounded text-nofx-text focus:border-nofx-gold focus:outline-none"
              />
              <p className="text-xs text-nofx-text-muted mt-1">
                {t(
                  isEditMode
                    ? 'paperInitialBalanceEditHint'
                    : 'paperInitialBalanceCreateHint',
                  language
                )}
              </p>
            </div>
          )}
          {/* Basic Info */}
          <div className="bg-nofx-bg border border-nofx-gold/20 rounded-lg p-5">
            <h3 className="text-lg font-semibold text-nofx-text mb-5 flex items-center gap-2">
              <span className="text-nofx-gold">1</span>{' '}
              {t('basicConfig', language)}
            </h3>
            <div className="space-y-4">
              <div>
                <label className="text-sm text-nofx-text block mb-2">
                  {t('traderNameRequired', language)}
                </label>
                <input
                  type="text"
                  value={formData.trader_name}
                  onChange={(e) =>
                    handleInputChange('trader_name', e.target.value)
                  }
                  className="w-full px-3 py-2 bg-nofx-bg-lighter border border-nofx-gold/20 rounded text-nofx-text focus:border-nofx-gold focus:outline-none"
                  placeholder={t('enterTraderNamePlaceholder', language)}
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-sm text-nofx-text block mb-2">
                    {language === 'zh'
                      ? 'AI API 配置 *'
                      : 'AI API configuration *'}
                  </label>
                  <NofxSelect
                    value={formData.ai_model}
                    onChange={(val) =>
                      setFormData((previous) => ({
                        ...previous,
                        ai_model: val,
                        fallback_model_names: [],
                        fallback_ai_model_ids:
                          previous.fallback_ai_model_ids.filter(
                            (modelId) => modelId !== val
                          ),
                      }))
                    }
                    className="w-full px-3 py-2 bg-nofx-bg-lighter border border-nofx-gold/20 rounded text-nofx-text"
                    options={availableModels.map((model) => ({
                      value: model.id,
                      label: getAIModelOptionLabel(model),
                    }))}
                  />
                  {selectedAIModel?.customModelName && (
                    <p className="mt-1 text-xs text-nofx-text-muted">
                      {language === 'zh' ? '当前主模型' : 'Current primary'}:{' '}
                      <span className="font-medium text-nofx-text">
                        {selectedAIModel.customModelName}
                      </span>
                      {selectedAIModel.modelNames?.length
                        ? language === 'zh'
                          ? ` · 已保存 ${selectedAIModel.modelNames.length} 个 API 模型`
                          : ` · ${selectedAIModel.modelNames.length} saved API models`
                        : ''}
                    </p>
                  )}
                </div>
                <div>
                  <label className="text-sm text-nofx-text block mb-2">
                    {t('exchangeRequired', language)}
                  </label>
                  <NofxSelect
                    value={formData.exchange_id}
                    onChange={handleExchangeChange}
                    className="w-full px-3 py-2 bg-nofx-bg-lighter border border-nofx-gold/20 rounded text-nofx-text"
                    options={availableExchanges.map((exchange) => ({
                      value: exchange.id,
                      label:
                        getShortName(
                          exchange.name || exchange.exchange_type || exchange.id
                        ).toUpperCase() +
                        (exchange.account_name
                          ? ` - ${exchange.account_name}`
                          : ''),
                    }))}
                  />
                  {/* Exchange Registration Link */}
                  {formData.exchange_id &&
                    (() => {
                      // Find the selected exchange to get its type
                      const selectedExchange = availableExchanges.find(
                        (e) => e.id === formData.exchange_id
                      )
                      const exchangeType =
                        selectedExchange?.exchange_type?.toLowerCase() || ''
                      const regLink = EXCHANGE_REGISTRATION_LINKS[exchangeType]
                      if (!regLink) return null
                      return (
                        <a
                          href={regLink.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="mt-2 inline-flex items-center gap-1.5 text-xs text-nofx-text-muted hover:text-nofx-gold transition-colors"
                        >
                          <UserPlus className="w-3.5 h-3.5" />
                          <span>{t('noExchangeAccount', language)}</span>
                          {regLink.hasReferral && (
                            <span className="px-1.5 py-0.5 bg-nofx-gold/10 text-nofx-gold rounded text-[10px]">
                              {t('discount', language)}
                            </span>
                          )}
                          <ExternalLink className="w-3 h-3" />
                        </a>
                      )
                    })()}
                </div>
              </div>
              <div>
                <label className="text-sm text-nofx-text block mb-2">
                  {language === 'zh'
                    ? '同一 API 备用模型（按选择顺序）'
                    : 'Same API fallback models (selection order)'}
                </label>
                <SameAPIModelSelector
                  models={selectedAIModel?.modelNames || []}
                  primaryModelName={selectedAIModel?.customModelName}
                  selectedModelNames={formData.fallback_model_names}
                  onChange={(modelNames) =>
                    handleInputChange('fallback_model_names', modelNames)
                  }
                  language={language}
                />
                <p className="text-xs text-nofx-text-muted mt-1">
                  {language === 'zh'
                    ? '来自模型配置中已读取并保存的模型列表，共用同一 API Key 和 Base URL。'
                    : 'Uses the verified model list saved on this API configuration; all entries share its key and base URL.'}
                </p>
              </div>
              <div>
                <label className="text-sm text-nofx-text block mb-2">
                  {language === 'zh'
                    ? '独立配置最终备用模型'
                    : 'Independent configured fallback models'}
                </label>
                <FallbackModelSelector
                  models={availableModels}
                  primaryModelId={formData.ai_model}
                  selectedModelIds={formData.fallback_ai_model_ids}
                  onChange={(modelIds) =>
                    handleInputChange('fallback_ai_model_ids', modelIds)
                  }
                  language={language}
                />
                <p className="text-xs text-nofx-text-muted mt-1">
                  {language === 'zh'
                    ? '仅可选择已配置并启用的模型；运行时将按这里的顺序依次切换。'
                    : 'Only configured and enabled models can be selected. Runtime failover follows this order.'}
                </p>
              </div>
            </div>
          </div>

          {/* Strategy Selection */}
          <div className="bg-nofx-bg border border-nofx-gold/20 rounded-lg p-5">
            <h3 className="text-lg font-semibold text-nofx-text mb-5 flex items-center gap-2">
              <span className="text-nofx-gold">2</span>{' '}
              {t('selectTradingStrategy', language)}
              <Sparkles className="w-4 h-4 text-nofx-gold" />
            </h3>
            <div className="space-y-4">
              <div>
                <label className="text-sm text-nofx-text block mb-2">
                  {t('useStrategy', language)}
                </label>
                <NofxSelect
                  value={formData.strategy_id}
                  onChange={(val) => handleInputChange('strategy_id', val)}
                  className="w-full px-3 py-2 bg-nofx-bg-lighter border border-nofx-gold/20 rounded text-nofx-text"
                  options={[
                    { value: '', label: t('noStrategyManual', language) },
                    ...strategies.map((strategy) => ({
                      value: strategy.id,
                      label:
                        strategy.name +
                        (strategy.is_active
                          ? t('strategyActive', language)
                          : '') +
                        (strategy.is_default
                          ? t('strategyDefault', language)
                          : ''),
                    })),
                  ]}
                />
                {strategies.length === 0 && (
                  <p className="text-xs text-nofx-text-muted mt-2">
                    {t('noStrategyHint', language)}
                  </p>
                )}
              </div>

              {/* Strategy Preview */}
              {selectedStrategy && (
                <div className="mt-3 p-4 bg-nofx-bg-lighter border border-nofx-gold/20 rounded-lg">
                  <div className="flex items-center gap-2 mb-2">
                    <span className="text-nofx-gold text-sm font-medium">
                      {t('strategyDetails', language)}
                    </span>
                    {selectedStrategy.is_active && (
                      <span className="px-2 py-0.5 bg-nofx-success/20 text-nofx-success text-xs rounded">
                        {t('activating', language)}
                      </span>
                    )}
                  </div>
                  <p className="text-sm text-nofx-text-muted mb-2">
                    {selectedStrategy.description ||
                      (language === 'zh' ? 'No description' : 'No description')}
                  </p>
                  {selectedStrategy.config.strategy_type === 'grid_trading' &&
                  selectedStrategy.config.grid_config ? (
                    <div className="grid grid-cols-2 gap-2 text-xs text-nofx-text-muted">
                      <div>
                        {language === 'zh' ? 'Symbol' : 'Symbol'}:{' '}
                        {selectedStrategy.config.grid_config.symbol || '-'}
                      </div>
                      <div>
                        {language === 'zh' ? 'Grids' : 'Grids'}:{' '}
                        {selectedStrategy.config.grid_config.grid_count}
                      </div>
                    </div>
                  ) : (
                    (() => {
                      const aiConfig = getStrategyAIConfig(selectedStrategy)
                      if (!aiConfig) return null
                      return (
                        <div className="grid grid-cols-2 gap-2 text-xs text-nofx-text-muted">
                          <div>
                            {t('coinSource', language)}:{' '}
                            {aiConfig.coin_source.source_type ===
                            'binance_dynamic'
                              ? language === 'zh'
                                ? 'Binance 本地动态候选'
                                : 'Binance local dynamic'
                              : aiConfig.coin_source.source_type === 'static'
                                ? language === 'zh'
                                  ? '固定交易对'
                                  : 'Fixed symbols'
                                : aiConfig.coin_source.source_type ===
                                    'hyper_rank'
                                  ? language === 'zh'
                                    ? 'Hyperliquid ranking'
                                    : 'Hyperliquid ranking'
                                  : aiConfig.coin_source.source_type ===
                                      'hyper_all'
                                    ? language === 'zh'
                                      ? 'Hyperliquid all markets'
                                      : 'Hyperliquid all markets'
                                    : aiConfig.coin_source.source_type ===
                                        'hyper_main'
                                      ? language === 'zh'
                                        ? 'Hyperliquid main markets'
                                        : 'Hyperliquid main markets'
                                      : '-'}
                          </div>
                          <div>
                            {t('marginLimit', language)}:{' '}
                            {(
                              (aiConfig.risk_control?.max_margin_usage || 0.9) *
                              100
                            ).toFixed(0)}
                            %
                          </div>
                        </div>
                      )
                    })()
                  )}
                </div>
              )}
            </div>
          </div>

          {/* Trading Parameters */}
          <div className="bg-nofx-bg border border-nofx-gold/20 rounded-lg p-5">
            <h3 className="text-lg font-semibold text-nofx-text mb-5 flex items-center gap-2">
              <span className="text-nofx-gold">3</span>{' '}
              {t('tradingParams', language)}
            </h3>
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-sm text-nofx-text block mb-2">
                    {t('marginMode', language)}
                  </label>
                  <div className="flex gap-2">
                    <button
                      type="button"
                      onClick={() => handleInputChange('is_cross_margin', true)}
                      className={`flex-1 px-3 py-2 rounded text-sm ${
                        formData.is_cross_margin
                          ? 'bg-nofx-gold text-white'
                          : 'bg-nofx-bg-lighter text-nofx-text-muted border border-nofx-gold/20'
                      }`}
                    >
                      {t('crossMargin', language)}
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        handleInputChange('is_cross_margin', false)
                      }
                      className={`flex-1 px-3 py-2 rounded text-sm ${
                        !formData.is_cross_margin
                          ? 'bg-nofx-gold text-white'
                          : 'bg-nofx-bg-lighter text-nofx-text-muted border border-nofx-gold/20'
                      }`}
                    >
                      {t('isolatedMargin', language)}
                    </button>
                  </div>
                </div>
                <div>
                  <label className="text-sm text-nofx-text block mb-2">
                    {t('aiScanInterval', language)}
                  </label>
                  <input
                    type="number"
                    value={formData.scan_interval_minutes}
                    onChange={(e) => {
                      const parsedValue = Number(e.target.value)
                      const safeValue = Number.isFinite(parsedValue)
                        ? Math.max(3, parsedValue)
                        : 3
                      handleInputChange('scan_interval_minutes', safeValue)
                    }}
                    className="w-full px-3 py-2 bg-nofx-bg-lighter border border-nofx-gold/20 rounded text-nofx-text focus:border-nofx-gold focus:outline-none"
                    min="3"
                    max="60"
                    step="1"
                  />
                  <p className="text-xs text-nofx-text-muted mt-1">
                    {t('scanIntervalRecommend', language)}
                  </p>
                </div>
              </div>

              {/* Competition visibility */}
              <div>
                <label className="text-sm text-nofx-text block mb-2">
                  {t('competitionDisplay', language)}
                </label>
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() =>
                      handleInputChange('show_in_competition', true)
                    }
                    className={`flex-1 px-3 py-2 rounded text-sm ${
                      formData.show_in_competition
                        ? 'bg-nofx-gold text-white'
                        : 'bg-nofx-bg-lighter text-nofx-text-muted border border-nofx-gold/20'
                    }`}
                  >
                    {t('show', language)}
                  </button>
                  <button
                    type="button"
                    onClick={() =>
                      handleInputChange('show_in_competition', false)
                    }
                    className={`flex-1 px-3 py-2 rounded text-sm ${
                      !formData.show_in_competition
                        ? 'bg-nofx-gold text-white'
                        : 'bg-nofx-bg-lighter text-nofx-text-muted border border-nofx-gold/20'
                    }`}
                  >
                    {t('hide', language)}
                  </button>
                </div>
                <p className="text-xs text-nofx-text-muted mt-1">
                  {t('hiddenInCompetition', language)}
                </p>
              </div>

              {/* Signal Inversion */}
              <div>
                <label className="text-sm text-nofx-text block mb-2">
                  {language === 'zh'
                    ? '反向交易 / 信号取反'
                    : 'Invert AI Signals'}
                </label>
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => handleInputChange('invert_signals', true)}
                    className={`flex-1 px-3 py-2 rounded text-sm ${
                      formData.invert_signals
                        ? 'bg-amber-600 text-white font-medium'
                        : 'bg-nofx-bg-lighter text-nofx-text-muted border border-nofx-gold/20'
                    }`}
                  >
                    {language === 'zh' ? '开启取反' : 'Enabled'}
                  </button>
                  <button
                    type="button"
                    onClick={() => handleInputChange('invert_signals', false)}
                    className={`flex-1 px-3 py-2 rounded text-sm ${
                      !formData.invert_signals
                        ? 'bg-nofx-gold text-white'
                        : 'bg-nofx-bg-lighter text-nofx-text-muted border border-nofx-gold/20'
                    }`}
                  >
                    {language === 'zh' ? '正常方向' : 'Normal'}
                  </button>
                </div>
                <p className="text-xs text-nofx-text-muted mt-1">
                  {language === 'zh'
                    ? '开启后，AI 决策结果将自动翻转：open_long 转换为 open_short，open_short 转换为 open_long'
                    : 'When enabled, open_long automatically converts to open_short and open_short to open_long'}
                </p>
              </div>

              <div className="p-3 bg-nofx-bg-lighter border border-nofx-gold/20 rounded flex items-center gap-2">
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  className="w-4 h-4 text-nofx-gold"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <circle cx="12" cy="12" r="10" />
                  <line x1="12" x2="12" y1="8" y2="12" />
                  <line x1="12" x2="12.01" y1="16" y2="16" />
                </svg>
                <span className="text-sm text-nofx-text-muted">
                  {t('autoFetchBalanceInfo', language)}
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-3 p-6 border-t border-nofx-gold/20 bg-nofx-bg-lighter sticky bottom-0 z-10 rounded-b-xl">
          <button
            onClick={onClose}
            className="px-6 py-3 bg-nofx-bg-deeper text-nofx-text rounded-lg hover:bg-nofx-bg transition-all duration-200 border border-nofx-gold/20"
          >
            {t('cancel', language)}
          </button>
          {onSave && (
            <button
              onClick={handleSave}
              disabled={
                isSaving ||
                !formData.trader_name ||
                !formData.ai_model ||
                !formData.exchange_id
              }
              className="px-8 py-3 bg-nofx-gold text-white rounded-lg hover:bg-nofx-gold/90 transition-all duration-200 disabled:bg-nofx-bg-deeper disabled:text-nofx-text-muted disabled:cursor-not-allowed font-medium shadow-lg"
            >
              {isSaving
                ? t('saving', language)
                : isEditMode
                  ? t('editTrader', language)
                  : t('createTraderButton', language)}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
