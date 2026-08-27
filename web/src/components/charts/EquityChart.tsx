import { useState } from 'react'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
} from 'recharts'
import useSWR from 'swr'
import { api } from '../../lib/api'
import { useLanguage } from '../../contexts/LanguageContext'
import { useTheme } from '../../contexts/ThemeContext'
import { useAuth } from '../../contexts/AuthContext'
import { t } from '../../i18n/translations'
import {
  AlertTriangle,
  BarChart3,
  DollarSign,
  Percent,
  TrendingUp as ArrowUp,
  TrendingDown as ArrowDown,
} from 'lucide-react'

interface EquityPoint {
  timestamp: string
  total_equity: number
  pnl: number
  pnl_pct: number
  cycle_number: number
}

interface EquityChartProps {
  traderId?: string
  embedded?: boolean // Embedded mode (does not show the outer card)
}

export function EquityChart({ traderId, embedded = false }: EquityChartProps) {
  const { language } = useLanguage()
  const { isDark } = useTheme()
  const { user, token } = useAuth()
  const [displayMode, setDisplayMode] = useState<'dollar' | 'percent'>('dollar')

  const {
    data: history,
    error,
    isLoading,
  } = useSWR<EquityPoint[]>(
    user && token && traderId ? `equity-history-${traderId}` : null,
    () => api.getEquityHistory(traderId, true),
    {
      refreshInterval: 30000, // Refresh every 30s (historical data updates less frequently)
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  )

  const { data: account } = useSWR(
    user && token && traderId ? `account-${traderId}` : null,
    () => api.getAccount(traderId, true),
    {
      refreshInterval: 15000, // Refresh every 15s (matches backend cache)
      revalidateOnFocus: false,
      dedupingInterval: 10000,
    }
  )

  // Loading state - show skeleton
  if (isLoading) {
    return (
      <div className={embedded ? 'p-6' : 'binance-card p-6'}>
        {!embedded && (
          <h3 className="text-lg font-semibold mb-6 text-nofx-text">
            {t('accountEquityCurve', language)}
          </h3>
        )}
        <div className="animate-pulse">
          <div className="skeleton h-64 w-full rounded"></div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className={embedded ? 'p-6' : 'binance-card p-6'}>
        <div className="flex items-center gap-3 p-4 rounded bg-nofx-danger/10 border border-nofx-danger/20">
          <AlertTriangle className="w-6 h-6 text-nofx-danger" />
          <div>
            <div className="font-semibold text-nofx-danger">
              {t('loadingError', language)}
            </div>
            <div className="text-sm text-nofx-text-muted">{error.message}</div>
          </div>
        </div>
      </div>
    )
  }

  // Get initial balance from account info (fixed configuration value)
  // Fallback: calculate from current equity - current pnl
  const initialBalance =
    account?.initial_balance && account.initial_balance > 0
      ? account.initial_balance
      : account?.total_equity && account?.total_pnl !== undefined
        ? account.total_equity - account.total_pnl
        : 1000 // Default fallback

  // If no history data or only 1 point, create initial state
  const validHistory = history && history.length > 0 ? history : []

  // If no history, show empty state with current balance
  if (validHistory.length === 0) {
    return (
      <div className={embedded ? 'p-6' : 'binance-card p-6'}>
        {!embedded && (
          <h3 className="text-lg font-semibold mb-6 text-nofx-text">
            {t('accountEquityCurve', language)}
          </h3>
        )}
        <div className="text-center py-16 text-nofx-text-muted">
          <BarChart3 className="w-12 h-12 mx-auto mb-3 opacity-40 text-nofx-gold" />
          <p className="font-mono text-sm">{t('noHistoricalData', language)}</p>
          <p className="text-xs text-nofx-text-muted/60 mt-1">
            {t('dataWillDisplayAfterTrading', language)}
          </p>
          {account && (
            <div className="mt-4 inline-block px-4 py-2 rounded-lg bg-nofx-bg-lighter border border-nofx-border">
              <span className="text-xs text-nofx-text-muted">
                {t('currentEquity', language)}:
              </span>
              <span className="text-sm font-bold mono ml-2 text-nofx-text">
                {account.total_equity.toFixed(2)} USDT
              </span>
            </div>
          )}
        </div>
      </div>
    )
  }

  // Format data for Recharts
  const chartData = validHistory.map((point) => {
    // Format timestamp: parse string to Date
    const date = new Date(point.timestamp)
    const timeStr = `${date.getMonth() + 1}/${date.getDate()} ${date.getHours().toString().padStart(2, '0')}:${date.getMinutes().toString().padStart(2, '0')}`

    return {
      time: timeStr,
      timestamp: date.getTime(),
      value:
        displayMode === 'dollar'
          ? point.total_equity
          : Number(point.pnl_pct.toFixed(2)),
      raw_equity: point.total_equity,
      raw_pnl: point.pnl,
      raw_pnl_pct: point.pnl_pct,
      cycle: point.cycle_number,
    }
  })

  // Calculate current value and profit status
  const currentValue = chartData[chartData.length - 1]
  const isProfit = currentValue.raw_pnl >= 0

  // Calculate Y-axis domain
  const calculateYDomain = () => {
    if (displayMode === 'percent') {
      const values = chartData.map((d) => d.value)
      const minVal = Math.min(...values, 0)
      const maxVal = Math.max(...values, 0)
      const range = maxVal - minVal
      const padding = range === 0 ? 5 : range * 0.15
      return [Math.floor(minVal - padding), Math.ceil(maxVal + padding)]
    } else {
      const values = chartData.map((d) => d.value)
      const minVal = Math.min(...values, initialBalance)
      const maxVal = Math.max(...values, initialBalance)
      const range = maxVal - minVal
      const padding = Math.max(range * 0.15, initialBalance * 0.01)
      return [Math.floor(minVal - padding), Math.ceil(maxVal + padding)]
    }
  }

  // Custom Tooltip
  const CustomTooltip = ({ active, payload }: any) => {
    if (active && payload && payload.length) {
      const data = payload[0].payload
      return (
        <div className="rounded p-3 shadow-xl bg-nofx-bg-lighter border border-nofx-border">
          <div className="text-xs mb-1 text-nofx-text-muted">
            Cycle #{data.cycle != null ? data.cycle : '—'}
          </div>
          <div className="font-bold mono text-nofx-text">
            {data.raw_equity.toFixed(2)} USDT
          </div>
          <div
            className={`text-sm mono font-bold ${
              data.raw_pnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'
            }`}
          >
            {data.raw_pnl >= 0 ? '+' : ''}
            {data.raw_pnl.toFixed(2)} USDT ({data.raw_pnl_pct >= 0 ? '+' : ''}
            {data.raw_pnl_pct}%)
          </div>
        </div>
      )
    }
    return null
  }

  const gridColor = isDark
    ? 'rgba(255, 255, 255, 0.05)'
    : 'rgba(26, 24, 19, 0.10)'
  const axisTextColor = isDark ? '#9DA8B6' : '#6B6557'
  const axisLineColor = isDark
    ? 'rgba(255, 255, 255, 0.08)'
    : 'rgba(26, 24, 19, 0.14)'
  const refLineColor = isDark
    ? 'rgba(255, 255, 255, 0.15)'
    : 'rgba(26, 24, 19, 0.20)'

  return (
    <div
      className={
        embedded ? 'p-3 sm:p-5' : 'binance-card p-3 sm:p-5 animate-fade-in'
      }
    >
      {/* Header */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between mb-4">
        <div className="flex-1">
          {!embedded && (
            <h3 className="text-base sm:text-lg font-bold mb-2 text-nofx-text">
              {t('accountEquityCurve', language)}
            </h3>
          )}
          <div className="flex flex-col sm:flex-row sm:items-baseline gap-2 sm:gap-4">
            <span className="text-2xl sm:text-3xl font-bold mono text-nofx-text">
              {account?.total_equity.toFixed(2) || '0.00'}
              <span className="text-base sm:text-lg ml-1 text-nofx-text-muted">
                USDT
              </span>
            </span>
            <div className="flex items-center gap-2 flex-wrap">
              <span
                className={`text-sm sm:text-lg font-bold mono px-2 sm:px-3 py-1 rounded flex items-center gap-1 border ${
                  isProfit
                    ? 'text-nofx-success bg-nofx-success/10 border-nofx-success/30'
                    : 'text-nofx-danger bg-nofx-danger/10 border-nofx-danger/30'
                }`}
              >
                {isProfit ? (
                  <ArrowUp className="w-4 h-4" />
                ) : (
                  <ArrowDown className="w-4 h-4" />
                )}
                {isProfit ? '+' : ''}
                {currentValue.raw_pnl_pct}%
              </span>
              <span className="text-xs sm:text-sm mono text-nofx-text-muted">
                ({isProfit ? '+' : ''}
                {currentValue.raw_pnl.toFixed(2)} USDT)
              </span>
            </div>
          </div>
        </div>

        {/* Display Mode Toggle */}
        <div className="flex gap-0.5 sm:gap-1 rounded p-0.5 sm:p-1 self-start sm:self-auto bg-nofx-bg-deeper border border-nofx-border">
          <button
            onClick={() => setDisplayMode('dollar')}
            className={`px-3 sm:px-4 py-1.5 sm:py-2 rounded text-xs sm:text-sm font-bold transition-all flex items-center gap-1 ${
              displayMode === 'dollar'
                ? 'bg-nofx-gold text-nofx-bg'
                : 'text-nofx-text-muted hover:text-nofx-text bg-transparent'
            }`}
          >
            <DollarSign className="w-4 h-4" /> USDT
          </button>
          <button
            onClick={() => setDisplayMode('percent')}
            className={`px-3 sm:px-4 py-1.5 sm:py-2 rounded text-xs sm:text-sm font-bold transition-all flex items-center gap-1 ${
              displayMode === 'percent'
                ? 'bg-nofx-gold text-nofx-bg'
                : 'text-nofx-text-muted hover:text-nofx-text bg-transparent'
            }`}
          >
            <Percent className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Chart */}
      <div
        className="my-2"
        style={{
          borderRadius: '8px',
          overflow: 'hidden',
          position: 'relative',
        }}
      >
        {/* NOFX Watermark */}
        <div
          style={{
            position: 'absolute',
            top: '15px',
            right: '15px',
            fontSize: '20px',
            fontWeight: 'bold',
            color: 'var(--nofx-gold-dim)',
            zIndex: 10,
            pointerEvents: 'none',
            fontFamily: 'monospace',
          }}
        >
          NOFX
        </div>
        <ResponsiveContainer width="100%" height={280}>
          <LineChart
            data={chartData}
            margin={{ top: 10, right: 20, left: 5, bottom: 30 }}
          >
            <defs>
              <linearGradient id="colorGradient" x1="0" y1="0" x2="0" y2="1">
                <stop
                  offset="5%"
                  stopColor="var(--nofx-gold)"
                  stopOpacity={0.8}
                />
                <stop
                  offset="95%"
                  stopColor="var(--nofx-gold)"
                  stopOpacity={0.2}
                />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke={gridColor} />
            <XAxis
              dataKey="time"
              stroke={axisTextColor}
              tick={{ fill: axisTextColor, fontSize: 11 }}
              tickLine={{ stroke: axisLineColor }}
              interval={Math.floor(chartData.length / 10)}
              angle={-15}
              textAnchor="end"
              height={60}
            />
            <YAxis
              stroke={axisTextColor}
              tick={{ fill: axisTextColor, fontSize: 12 }}
              tickLine={{ stroke: axisLineColor }}
              domain={calculateYDomain()}
              tickFormatter={(value) =>
                displayMode === 'dollar' ? `$${value.toFixed(0)}` : `${value}%`
              }
            />
            <Tooltip content={<CustomTooltip />} />
            <ReferenceLine
              y={displayMode === 'dollar' ? initialBalance : 0}
              stroke={refLineColor}
              strokeDasharray="3 3"
              label={{
                value:
                  displayMode === 'dollar'
                    ? t('initialBalance', language).split(' ')[0]
                    : '0%',
                fill: axisTextColor,
                fontSize: 12,
              }}
            />
            <Line
              type="natural"
              dataKey="value"
              stroke="url(#colorGradient)"
              strokeWidth={3}
              dot={
                chartData.length > 50
                  ? false
                  : { fill: 'var(--nofx-gold)', r: 3 }
              }
              activeDot={{
                r: 6,
                fill: 'var(--nofx-gold)',
                stroke: 'var(--nofx-bg)',
                strokeWidth: 2,
              }}
              connectNulls={true}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>

      {/* Footer Stats */}
      <div className="mt-3 grid grid-cols-2 sm:grid-cols-4 gap-2 sm:gap-3 pt-3 border-t border-nofx-border">
        <div className="p-2 rounded transition-all bg-nofx-gold/5 border border-nofx-gold/10">
          <div className="text-xs mb-1 uppercase tracking-wider text-nofx-text-muted">
            {t('initialBalance', language)}
          </div>
          <div className="text-xs sm:text-sm font-bold mono text-nofx-text">
            {initialBalance.toFixed(2)} USDT
          </div>
        </div>
        <div className="p-2 rounded transition-all bg-nofx-gold/5 border border-nofx-gold/10">
          <div className="text-xs mb-1 uppercase tracking-wider text-nofx-text-muted">
            {t('totalTradingCycles', language)}
          </div>
          <div className="text-xs sm:text-sm font-bold mono text-nofx-text">
            {validHistory.length}
          </div>
        </div>
        <div className="p-2 rounded transition-all bg-nofx-gold/5 border border-nofx-gold/10">
          <div className="text-xs mb-1 uppercase tracking-wider text-nofx-text-muted">
            {t('maxEquity', language)}
          </div>
          <div className="text-xs sm:text-sm font-bold mono text-nofx-success">
            {Math.max(...validHistory.map((h) => h.total_equity)).toFixed(2)}{' '}
            USDT
          </div>
        </div>
        <div className="p-2 rounded transition-all bg-nofx-gold/5 border border-nofx-gold/10">
          <div className="text-xs mb-1 uppercase tracking-wider text-nofx-text-muted">
            {t('minEquity', language)}
          </div>
          <div className="text-xs sm:text-sm font-bold mono text-nofx-danger">
            {Math.min(...validHistory.map((h) => h.total_equity)).toFixed(2)}{' '}
            USDT
          </div>
        </div>
      </div>
    </div>
  )
}
