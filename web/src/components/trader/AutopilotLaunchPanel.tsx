import { useMemo, useState } from 'react'
import { AlertCircle, CheckCircle2, Loader2, ShieldCheck, Zap } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { buildDashboardPath } from '../../router/paths'
import {
  ensureDefaultStrategy,
  launchAutopilot,
} from '../../lib/launch/launchAutopilot'
import type {
  AIModel,
  Exchange,
  ExchangeAccountState,
  TraderInfo,
} from '../../types'

interface AutopilotLaunchPanelProps {
  models: AIModel[]
  exchanges: Exchange[]
  exchangeAccountStates: Record<string, ExchangeAccountState>
  traders?: TraderInfo[]
  isLoggedIn: boolean
  language: string
  onRefresh: () => Promise<void>
  onOpenModelConfig: () => void
  onOpenExchangeConfig: () => void
}

type SetupItemStatus = 'ready' | 'missing' | 'warning'

function getSetupItemStatus(
  hasConfiguration: boolean,
  accountState?: ExchangeAccountState
): SetupItemStatus {
  if (!hasConfiguration) return 'missing'
  if (accountState && accountState.status !== 'ok') return 'warning'
  return 'ready'
}

export function AutopilotLaunchPanel({
  models,
  exchanges,
  exchangeAccountStates,
  traders = [],
  isLoggedIn,
  language,
  onRefresh,
  onOpenModelConfig,
  onOpenExchangeConfig,
}: AutopilotLaunchPanelProps) {
  const navigate = useNavigate()
  const [launching, setLaunching] = useState(false)
  const isZh = language === 'zh'

  const configuredModel = useMemo(
    () =>
      models.find(
        (model) => model.enabled && (model.has_api_key || model.apiKey)
      ) || null,
    [models]
  )

  const configuredExchange = useMemo(
    () =>
      exchanges.find(
        (exchange) =>
          exchange.enabled &&
          (exchange.has_api_key || exchange.apiKey || exchange.id)
      ) || null,
    [exchanges]
  )

  const exchangeState = configuredExchange
    ? exchangeAccountStates[configuredExchange.id]
    : undefined
  const modelStatus = getSetupItemStatus(Boolean(configuredModel))
  const exchangeStatus = getSetupItemStatus(
    Boolean(configuredExchange),
    exchangeState
  )
  const existingTrader = useMemo(
    () =>
      traders.find((trader) => trader.trader_name === 'NOFX Autopilot') || null,
    [traders]
  )

  const handleLaunch = async () => {
    if (!isLoggedIn || launching) return
    setLaunching(true)
    try {
      const outcome = await launchAutopilot({
        ensureStrategy: ensureDefaultStrategy,
        scanIntervalMinutes: 5,
      })

      if (!outcome.ok) {
        toast.error(outcome.message)
        await onRefresh()
        return
      }

      if (outcome.warning) toast.warning(outcome.warning)
      await onRefresh()
      toast.success(isZh ? 'NOFX Autopilot 已启动' : 'NOFX Autopilot is running')
      navigate(buildDashboardPath(outcome.traderId))
    } finally {
      setLaunching(false)
    }
  }

  const renderStatusIcon = (status: SetupItemStatus) => {
    if (status === 'ready') {
      return <CheckCircle2 className="h-4 w-4 text-nofx-success" />
    }
    return <AlertCircle className="h-4 w-4 text-nofx-warning" />
  }

  return (
    <section
      id="autopilot-launch-panel"
      className="rounded-2xl border border-nofx-gold/20 bg-nofx-bg-lighter p-5 md:p-6"
    >
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <div className="inline-flex items-center gap-2 text-sm font-semibold text-nofx-text">
            <ShieldCheck className="h-4 w-4 text-nofx-gold" />
            {isZh ? '本地动态 Autopilot' : 'Local dynamic Autopilot'}
          </div>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-nofx-text-muted">
            {isZh
              ? '使用目标交易所的公共永续行情生成候选池，再交给你配置的 AI 模型判断。'
              : 'The selected exchange public market data builds the candidate pool, then your configured AI model makes the decision.'}
          </p>
        </div>
        <button
          type="button"
          onClick={() =>
            existingTrader?.is_running
              ? navigate(buildDashboardPath(existingTrader.trader_id))
              : void handleLaunch()
          }
          disabled={
            !isLoggedIn ||
            launching ||
            modelStatus === 'missing' ||
            exchangeStatus === 'missing'
          }
          className="inline-flex shrink-0 items-center justify-center gap-2 rounded-xl bg-nofx-gold px-5 py-3 text-sm font-bold text-white disabled:cursor-not-allowed disabled:opacity-40"
        >
          {launching ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <Zap className="h-4 w-4" />
          )}
          {existingTrader?.is_running
            ? isZh
              ? '查看运行状态'
              : 'View running bot'
            : isZh
              ? '启动 Autopilot'
              : 'Launch Autopilot'}
        </button>
      </div>

      <div className="mt-5 grid gap-3 md:grid-cols-2">
        <button
          type="button"
          onClick={onOpenModelConfig}
          className="flex items-center justify-between rounded-xl border border-nofx-gold/15 bg-nofx-bg-deeper px-4 py-3 text-left"
        >
          <span>
            <span className="block text-sm font-semibold text-nofx-text">
              {isZh ? '1. 配置 AI 模型' : '1. Configure an AI model'}
            </span>
            <span className="mt-1 block text-xs text-nofx-text-muted">
              {configuredModel
                ? `${configuredModel.name} · ${configuredModel.provider}`
                : isZh
                  ? '需要 API Key'
                  : 'API credentials required'}
            </span>
          </span>
          {renderStatusIcon(modelStatus)}
        </button>

        <button
          type="button"
          onClick={onOpenExchangeConfig}
          className="flex items-center justify-between rounded-xl border border-nofx-gold/15 bg-nofx-bg-deeper px-4 py-3 text-left"
        >
          <span>
            <span className="block text-sm font-semibold text-nofx-text">
              {isZh ? '2. 配置交易所' : '2. Configure an exchange'}
            </span>
            <span className="mt-1 block text-xs text-nofx-text-muted">
              {configuredExchange
                ? `${configuredExchange.name}${exchangeState?.display_balance ? ` · ${exchangeState.display_balance}` : ''}`
                : isZh
                  ? '需要交易所账户'
                  : 'Exchange account required'}
            </span>
          </span>
          {renderStatusIcon(exchangeStatus)}
        </button>
      </div>

      {exchangeStatus === 'warning' && (
        <p className="mt-4 text-xs text-nofx-warning">
          {exchangeState?.error_message ||
            (isZh
              ? '交易所账户状态暂时无法确认，启动时会再次检查。'
              : 'The exchange account could not be fully verified. It will be checked again at launch.')}
        </p>
      )}
    </section>
  )
}
