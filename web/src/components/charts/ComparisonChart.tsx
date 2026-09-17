import { useMemo, useState } from 'react'
import {
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
  Area,
  ComposedChart,
} from 'recharts'
import useSWR from 'swr'
import { api } from '../../lib/api'
import type { CompetitionTraderData } from '../../types'
import { getTraderColor } from '../../utils/traderColors'
import { useLanguage } from '../../contexts/LanguageContext'
import { useTheme } from '../../contexts/ThemeContext'
import { t } from '../../i18n/translations'
import { BarChart3, TrendingUp, Zap } from 'lucide-react'

// Time period options: 1D, 3D, 7D, 30D, All
const TIME_PERIODS = [
  { key: '1d', hours: 24 },
  { key: '3d', hours: 72 },
  { key: '7d', hours: 168 },
  { key: '30d', hours: 720 },
  { key: 'all', hours: 0 },
] as const

interface ComparisonChartProps {
  traders: CompetitionTraderData[]
}

export type TraderEquityHistories = Record<string, any[]>

export function mapEquityHistoriesByTraderId(
  traders: CompetitionTraderData[],
  histories: TraderEquityHistories
): TraderEquityHistories {
  return Object.fromEntries(
    traders.map((trader) => [
      trader.trader_id,
      histories[trader.trader_id] || [],
    ])
  )
}

type ComparisonDataPoint = Record<string, any>

export function rebaseVisibleComparisonData(
  visibleData: ComparisonDataPoint[],
  traders: CompetitionTraderData[],
  selectedHours: number
): ComparisonDataPoint[] {
  if (selectedHours === 0) return visibleData

  const baselines = new Map<string, number>()

  return visibleData.map((point) => {
    let rebasedPoint: ComparisonDataPoint | undefined

    traders.forEach((trader) => {
      const key = `${trader.trader_id}_pnl_pct`
      const value = point[key]
      if (typeof value !== 'number' || Number.isNaN(value)) return

      if (!baselines.has(trader.trader_id)) {
        baselines.set(trader.trader_id, value)
      }

      rebasedPoint ??= { ...point }
      rebasedPoint[key] = value - baselines.get(trader.trader_id)!
    })

    return rebasedPoint ?? point
  })
}

export function buildComparisonDisplayData(
  combinedData: ComparisonDataPoint[],
  traders: CompetitionTraderData[],
  selectedHours: number,
  maxDisplayPoints = 500
): ComparisonDataPoint[] {
  const visibleData =
    combinedData.length > maxDisplayPoints
      ? combinedData.slice(-maxDisplayPoints)
      : combinedData

  return rebaseVisibleComparisonData(visibleData, traders, selectedHours)
}

export function formatComparisonTick(
  timestamp: string,
  timeSpanHours: number,
  includeHours = false
): string {
  const date = new Date(timestamp)
  if (isNaN(date.getTime())) return ''

  if (timeSpanHours <= 26) {
    return date.toLocaleTimeString('zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
  }

  if (timeSpanHours <= 96 || includeHours) {
    const m = date.getMonth() + 1
    const d = date.getDate()
    const hh = date.getHours().toString().padStart(2, '0')
    const mm = date.getMinutes().toString().padStart(2, '0')
    return `${m}/${d} ${hh}:${mm}`
  }

  return `${date.getMonth() + 1}/${date.getDate()}`
}

export function calculateComparisonTicks(
  data: Array<{ timestamp?: string }>,
  targetCount = 5
): string[] {
  if (!data || data.length === 0) return []
  const validData = data.filter((d) => Boolean(d.timestamp))
  if (validData.length <= targetCount) {
    return validData.map((d) => d.timestamp!)
  }

  const ticks: string[] = []
  const step = (validData.length - 1) / (targetCount - 1)
  for (let i = 0; i < targetCount; i++) {
    const idx = Math.min(Math.round(i * step), validData.length - 1)
    const ts = validData[idx]?.timestamp
    if (ts && !ticks.includes(ts)) {
      ticks.push(ts)
    }
  }
  return ticks
}

export function ComparisonChart({ traders }: ComparisonChartProps) {
  const { language } = useLanguage()
  const { isDark } = useTheme()
  const [selectedPeriod, setSelectedPeriod] = useState('7d') // Default to 7 days
  const [activePoint, setActivePoint] = useState<any | null>(null)
  const [highlightedTraderId, setHighlightedTraderId] = useState<string | null>(
    null
  )

  const handleToggleHighlight = (traderId: string) => {
    setHighlightedTraderId((prev) => (prev === traderId ? null : traderId))
  }

  // Get hours for selected period
  const selectedHours =
    TIME_PERIODS.find((p) => p.key === selectedPeriod)?.hours || 0

  // Generate unique key for SWR (include period and hours)
  const tradersKey = traders
    .map((t) => t.trader_id)
    .sort()
    .join(',')

  const { data: allTraderHistories, isLoading } = useSWR(
    traders.length > 0
      ? `equity-histories-${tradersKey}-${selectedHours}`
      : null,
    async () => {
      const traderIds = traders.map((trader) => trader.trader_id)
      const batchData = await api.getEquityHistoryBatch(
        traderIds,
        selectedHours
      )
      return traders.map((trader) => {
        const history = batchData.histories?.[trader.trader_id] || []

        // If backend doesn't return total_pnl_pct, calculate it from equity
        if (history.length > 0 && history[0].total_pnl_pct === undefined) {
          const initialEquity = history[0].total_equity
          history.forEach((point: any) => {
            point.total_pnl_pct =
              initialEquity > 0
                ? ((point.total_equity - initialEquity) / initialEquity) * 100
                : 0
          })
        }

        return history
      })
    },
    {
      refreshInterval: 30000,
      revalidateOnFocus: false,
      dedupingInterval: 0,
      keepPreviousData: false,
    }
  )

  const traderHistories = useMemo(() => {
    if (!allTraderHistories) {
      return traders.map(() => ({ data: undefined }))
    }
    return allTraderHistories.map((data) => ({ data }))
  }, [allTraderHistories, traders.length])

  const combinedData = useMemo(() => {
    const allLoaded = traderHistories.every((h) => h.data)
    if (!allLoaded) return []

    const timestampMap = new Map<
      string,
      {
        timestamp: string
        time: string
        traders: Map<
          string,
          { pnl_pct: number; equity: number; originalTs?: string }
        >
      }
    >()

    // Helper function to normalize timestamp to nearest minute
    const normalizeTimestamp = (ts: string): string => {
      const date = new Date(ts)
      date.setSeconds(0, 0)
      return date.toISOString()
    }

    traderHistories.forEach((history, index) => {
      const trader = traders[index]
      if (!history.data) return

      history.data.forEach((point: any) => {
        const normalizedTs = normalizeTimestamp(point.timestamp)

        if (!timestampMap.has(normalizedTs)) {
          const date = new Date(normalizedTs)
          let time: string
          if (selectedHours <= 24) {
            time = date.toLocaleTimeString('zh-CN', {
              hour: '2-digit',
              minute: '2-digit',
            })
          } else if (selectedHours <= 72) {
            time = `${date.getMonth() + 1}/${date.getDate()} ${date.getHours().toString().padStart(2, '0')}:${date.getMinutes().toString().padStart(2, '0')}`
          } else {
            time = `${date.getMonth() + 1}/${date.getDate()}`
          }
          timestampMap.set(normalizedTs, {
            timestamp: normalizedTs,
            time,
            traders: new Map(),
          })
        }

        const existing = timestampMap
          .get(normalizedTs)!
          .traders.get(trader.trader_id)
        if (
          !existing ||
          new Date(point.timestamp) > new Date(existing.originalTs || '')
        ) {
          timestampMap.get(normalizedTs)!.traders.set(trader.trader_id, {
            pnl_pct: point.total_pnl_pct || 0,
            equity: point.total_equity,
            originalTs: point.timestamp,
          })
        }
      })
    })

    const sortedEntries = Array.from(timestampMap.entries()).sort(
      ([tsA], [tsB]) => new Date(tsA).getTime() - new Date(tsB).getTime()
    )

    const lastKnown: Map<string, { pnl_pct: number; equity: number }> =
      new Map()

    const combined = sortedEntries.map(([ts, data], index) => {
      const entry: any = {
        index: index + 1,
        time: data.time,
        timestamp: ts,
      }

      traders.forEach((trader) => {
        const traderData = data.traders.get(trader.trader_id)
        if (traderData) {
          lastKnown.set(trader.trader_id, {
            pnl_pct: traderData.pnl_pct,
            equity: traderData.equity,
          })
          entry[`${trader.trader_id}_pnl_pct`] = traderData.pnl_pct
          entry[`${trader.trader_id}_equity`] = traderData.equity
        } else {
          const last = lastKnown.get(trader.trader_id)
          if (last) {
            entry[`${trader.trader_id}_pnl_pct`] = last.pnl_pct
            entry[`${trader.trader_id}_equity`] = last.equity
          }
        }
      })

      return entry
    })

    return combined
  }, [allTraderHistories, traders, selectedHours, traderHistories])

  const MAX_DISPLAY_POINTS = 500
  const displayData = useMemo(() => {
    return buildComparisonDisplayData(
      combinedData,
      traders,
      selectedHours,
      MAX_DISPLAY_POINTS
    )
  }, [combinedData, traders, selectedHours])

  const timeSpanHours = useMemo(() => {
    if (displayData.length < 2) return 0
    const start = new Date(displayData[0].timestamp).getTime()
    const end = new Date(displayData[displayData.length - 1].timestamp).getTime()
    return Math.max(0, (end - start) / (1000 * 3600))
  }, [displayData])

  const ticks = useMemo(() => {
    return calculateComparisonTicks(displayData, 5)
  }, [displayData])

  const hasDuplicateDates = useMemo(() => {
    const formattedDates = ticks.map((ts) => {
      const d = new Date(ts)
      return `${d.getMonth() + 1}/${d.getDate()}`
    })
    return new Set(formattedDates).size < formattedDates.length
  }, [ticks])

  const traderColor = (traderId: string) => getTraderColor(traders, traderId)

  if (isLoading) {
    return (
      <div className="flex flex-col items-center justify-center py-20 bg-nofx-bg-lighter rounded-xl border border-nofx-border">
        <div className="relative">
          <div className="w-16 h-16 border-4 border-nofx-gold border-t-transparent rounded-full animate-spin" />
          <TrendingUp className="w-6 h-6 absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 text-nofx-gold" />
        </div>
        <div className="text-sm mt-4 font-medium text-nofx-text-muted">
          {t('loadingChartData', language) || 'Loading chart data...'}
        </div>
      </div>
    )
  }

  if (combinedData.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-20 bg-nofx-bg-lighter rounded-xl border border-nofx-border">
        <div className="w-20 h-20 rounded-2xl flex items-center justify-center mb-4 bg-nofx-gold/10">
          <BarChart3 className="w-10 h-10 text-nofx-gold opacity-60" />
        </div>
        <div className="text-lg font-bold mb-2 text-nofx-text">
          {t('noHistoricalData', language)}
        </div>
        <div className="text-sm text-center max-w-xs text-nofx-text-muted">
          {t('dataWillAppear', language)}
        </div>
      </div>
    )
  }



  const calculateYDomain = () => {
    const allValues: number[] = []
    displayData.forEach((point) => {
      traders.forEach((trader) => {
        const value = point[`${trader.trader_id}_pnl_pct`]
        if (value !== undefined && !isNaN(value)) {
          allValues.push(value)
        }
      })
    })

    if (allValues.length === 0) return [-2, 2]

    const minVal = Math.min(...allValues)
    const maxVal = Math.max(...allValues)
    const range = maxVal - minVal
    const padding = Math.max(range * 0.2, 2)

    return [
      Math.floor((minVal - padding) * 10) / 10,
      Math.ceil((maxVal + padding) * 10) / 10,
    ]
  }

  const CustomTooltip = ({ active, payload }: any) => {
    if (active && payload && payload.length) {
      const data = payload[0].payload
      const date = new Date(data.timestamp)
      const dateStr = date.toLocaleDateString('zh-CN', {
        month: 'short',
        day: 'numeric',
      })
      const timeStr = date.toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      })

      return (
        <div className="hidden md:block rounded-xl p-3.5 shadow-2xl bg-nofx-bg-lighter border border-nofx-border min-w-[390px] max-w-[480px] z-50">
          <div className="flex items-center justify-between gap-2 mb-2.5 pb-2 border-b border-nofx-border">
            <div className="flex items-center gap-1.5">
              <Zap className="w-3.5 h-3.5 text-nofx-gold" />
              <span className="text-xs font-medium text-nofx-gold">
                {dateStr} {timeStr}
              </span>
            </div>
            <span className="text-[10px] text-nofx-text-muted">
              {traders.length} {t('traders', language) || 'traders'}
            </span>
          </div>
          <div className="grid grid-cols-2 gap-x-4 gap-y-2">
            {traders.map((trader) => {
              const pnlPct = data[`${trader.trader_id}_pnl_pct`]
              if (pnlPct === undefined) return null
              const isPositive = pnlPct >= 0

              return (
                <div
                  key={trader.trader_id}
                  className="flex items-center justify-between gap-2.5 text-xs py-0.5"
                >
                  <div className="flex items-center gap-1.5 min-w-0">
                    <div
                      className="w-1.5 h-1.5 rounded-full shrink-0"
                      style={{ background: traderColor(trader.trader_id) }}
                    />
                    <span
                      className="font-medium text-nofx-text whitespace-nowrap"
                      title={trader.trader_name}
                    >
                      {trader.trader_name}
                    </span>
                  </div>
                  <div className="text-right shrink-0">
                    <span
                      className={`font-bold mono text-xs ${
                        isPositive ? 'text-nofx-success' : 'text-nofx-danger'
                      }`}
                    >
                      {isPositive ? '+' : ''}
                      {pnlPct.toFixed(2)}%
                    </span>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )
    }
    return null
  }

  const traderStats = traders
    .map((trader) => {
      let currentPnl = 0
      let currentEquity = 0
      for (let i = displayData.length - 1; i >= 0; i--) {
        const pnl = displayData[i]?.[`${trader.trader_id}_pnl_pct`]
        if (pnl !== undefined) {
          currentPnl = pnl
          currentEquity = displayData[i]?.[`${trader.trader_id}_equity`] || 0
          break
        }
      }
      return { ...trader, currentPnl, currentEquity }
    })
    .sort((a, b) => b.currentPnl - a.currentPnl)

  const leader = traderStats[0]
  const leaderName = leader?.trader_name || '-'
  const leaderParts = leaderName.split('-')
  const hasLeaderPrefix = leaderParts.length > 1
  const leaderPrefix = hasLeaderPrefix
    ? leaderParts.slice(0, -1).join('-')
    : ''
  const leaderCoreName = hasLeaderPrefix
    ? leaderParts[leaderParts.length - 1]
    : leaderName

  const gap =
    traderStats.length > 1
      ? Math.abs(traderStats[0].currentPnl - traderStats[1].currentPnl).toFixed(
          2
        )
      : '0.00'

  const activeDate = activePoint ? new Date(activePoint.timestamp) : null
  const activeDateStr = activeDate
    ? activeDate.toLocaleDateString('zh-CN', {
        month: 'short',
        day: 'numeric',
      })
    : ''
  const activeTimeStr = activeDate
    ? activeDate.toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      })
    : ''
  const currentSnapshotPoint =
    activePoint || displayData[displayData.length - 1]

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
    <div className="space-y-4">
      {/* Time Period Selector + Mini Stats Bar */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        {/* Time Period Buttons */}
        <div className="flex items-center gap-1">
          {TIME_PERIODS.map((period) => (
            <button
              key={period.key}
              onClick={() => {
                setSelectedPeriod(period.key)
                setActivePoint(null)
                setHighlightedTraderId(null)
              }}
              className={`px-3 py-1.5 text-xs font-medium rounded-lg transition-all border ${
                selectedPeriod === period.key
                  ? 'bg-nofx-gold/15 text-nofx-gold border-nofx-gold/40 font-bold'
                  : 'bg-nofx-bg-lighter text-nofx-text-muted border-nofx-border hover:text-nofx-text'
              }`}
            >
              {t(`comparisonChart.${period.key}`, language)}
            </button>
          ))}
        </div>

        {/* Mini Stats Bar */}
        <div className="flex items-center gap-2 flex-wrap">
          {traderStats.slice(0, 3).map((trader, idx) => {
            const isHighlighted = highlightedTraderId === trader.trader_id
            const isDimmed =
              highlightedTraderId !== null && !isHighlighted

            return (
              <button
                type="button"
                key={trader.trader_id}
                onClick={() => handleToggleHighlight(trader.trader_id)}
                className={`flex items-center gap-2 px-3 py-1.5 rounded-full transition-all cursor-pointer border ${
                  isHighlighted
                    ? 'ring-2 ring-nofx-gold bg-nofx-gold/25 border-nofx-gold shadow-md scale-105'
                    : isDimmed
                      ? 'opacity-40 hover:opacity-80 bg-nofx-bg-lighter border-nofx-border'
                      : idx === 0
                        ? 'bg-nofx-gold/15 border-nofx-gold/30 hover:scale-105'
                        : 'bg-nofx-bg-lighter border-nofx-border hover:scale-105'
                }`}
                title={
                  isHighlighted
                    ? language === 'zh'
                      ? '点击取消高亮'
                      : 'Click to reset highlight'
                    : language === 'zh'
                      ? `点击高亮 ${trader.trader_name}`
                      : `Highlight ${trader.trader_name}`
                }
              >
                <div
                  className={`w-2 h-2 rounded-full ${isHighlighted ? 'scale-125' : ''}`}
                  style={{ background: traderColor(trader.trader_id) }}
                />
                <span className="text-xs font-medium text-nofx-text whitespace-nowrap">
                  {trader.trader_name}
                </span>
                <span
                  className={`text-xs font-bold mono ${
                    trader.currentPnl >= 0
                      ? 'text-nofx-success'
                      : 'text-nofx-danger'
                  }`}
                >
                  {trader.currentPnl >= 0 ? '+' : ''}
                  {trader.currentPnl.toFixed(2)}%
                </span>
              </button>
            )
          })}
        </div>
      </div>

      {/* Chart */}
      <div className="relative rounded-xl overflow-hidden bg-nofx-bg border border-nofx-border p-3 sm:p-4">
        {/* Watermark */}
        <div
          style={{
            position: 'absolute',
            top: '38%',
            left: '50%',
            transform: 'translate(-50%, -50%)',
            fontSize: '72px',
            fontWeight: 'bold',
            color: 'var(--nofx-gold-dim)',
            zIndex: 1,
            pointerEvents: 'none',
            fontFamily: 'monospace',
            letterSpacing: '0.1em',
          }}
        >
          NOFX
        </div>

        <div className="h-[280px] sm:h-[360px] w-full relative z-10">
          {/* Top HUD badge: Snapshot time or Highlight info */}
          {(activePoint || highlightedTraderId) && (
            <div className="absolute top-1 left-1/2 -translate-x-1/2 z-30 px-3 py-1 rounded-full bg-nofx-bg-lighter/95 border border-nofx-gold/40 text-nofx-gold text-xs font-mono font-medium shadow-xl flex items-center gap-2 animate-fade-in pointer-events-auto">
              <span className="w-2 h-2 rounded-full bg-nofx-gold animate-pulse" />
              {activePoint && (
                <span>
                  {activeDateStr} {activeTimeStr}
                </span>
              )}
              {highlightedTraderId && (
                <span className="flex items-center gap-1">
                  {activePoint ? '•' : ''}{' '}
                  {language === 'zh' ? '聚焦' : 'Focus'}:{' '}
                  <span className="font-bold text-nofx-text">
                    {
                      traders.find(
                        (t) => t.trader_id === highlightedTraderId
                      )?.trader_name
                    }
                  </span>
                </span>
              )}
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation()
                  setActivePoint(null)
                  setHighlightedTraderId(null)
                }}
                className="ml-1 px-1.5 py-0.5 rounded bg-nofx-gold/15 hover:bg-nofx-gold/30 text-[10px] text-nofx-gold transition-colors"
                title={language === 'zh' ? '恢复默认' : 'Reset'}
              >
                {language === 'zh' ? '恢复默认' : 'Reset'}
              </button>
            </div>
          )}

          <ResponsiveContainer width="100%" height="100%">
            <ComposedChart
              data={displayData}
              margin={{ top: 12, right: 12, left: -6, bottom: 8 }}
              onClick={() => {
                if (highlightedTraderId) setHighlightedTraderId(null)
              }}
              onMouseMove={(e: any) => {
                if (e?.activePayload?.[0]?.payload) {
                  setActivePoint(e.activePayload[0].payload)
                }
              }}
              onMouseLeave={() => {
                setActivePoint(null)
              }}
            >
              <defs>
                {traders.map((trader) => (
                  <linearGradient
                    key={`area-gradient-${trader.trader_id}`}
                    id={`area-gradient-${trader.trader_id}`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="0%"
                      stopColor={traderColor(trader.trader_id)}
                      stopOpacity={0.3}
                    />
                    <stop
                      offset="100%"
                      stopColor={traderColor(trader.trader_id)}
                      stopOpacity={0}
                    />
                  </linearGradient>
                ))}
                {/* Glow filter */}
                <filter id="glow" x="-50%" y="-50%" width="200%" height="200%">
                  <feGaussianBlur stdDeviation="2" result="coloredBlur" />
                  <feMerge>
                    <feMergeNode in="coloredBlur" />
                    <feMergeNode in="SourceGraphic" />
                  </feMerge>
                </filter>
              </defs>

              <CartesianGrid
                strokeDasharray="3 3"
                stroke={gridColor}
                vertical={false}
              />

              <XAxis
                dataKey="timestamp"
                ticks={ticks}
                tickFormatter={(ts) =>
                  formatComparisonTick(ts, timeSpanHours, hasDuplicateDates)
                }
                stroke={axisTextColor}
                tick={{ fill: axisTextColor, fontSize: 10 }}
                tickLine={false}
                axisLine={{ stroke: axisLineColor }}
                interval={0}
              />

              <YAxis
                stroke={axisTextColor}
                tick={{ fill: axisTextColor, fontSize: 10 }}
                tickLine={false}
                axisLine={false}
                domain={calculateYDomain()}
                tickFormatter={(value) => `${value.toFixed(1)}%`}
                width={44}
              />

              <Tooltip content={<CustomTooltip />} />

              {/* Zero reference line */}
              <ReferenceLine
                y={0}
                stroke={refLineColor}
                strokeDasharray="8 4"
                strokeWidth={1}
              />

              {/* Area fills for highlighted trader or top 2 traders */}
              {traders
                .filter((t) =>
                  highlightedTraderId
                    ? t.trader_id === highlightedTraderId
                    : true
                )
                .slice(0, highlightedTraderId ? 1 : 2)
                .map((trader) => (
                  <Area
                    key={`area-${trader.trader_id}`}
                    type="monotone"
                    dataKey={`${trader.trader_id}_pnl_pct`}
                    fill={`url(#area-gradient-${trader.trader_id})`}
                    stroke="none"
                    connectNulls
                  />
                ))}

              {/* Lines for all traders with highlight support */}
              {traders.map((trader, idx) => {
                const isHighlighted =
                  highlightedTraderId === trader.trader_id
                const isDimmed =
                  highlightedTraderId !== null && !isHighlighted
                const color = traderColor(trader.trader_id)

                return (
                  <Line
                    key={trader.trader_id}
                    type="monotone"
                    dataKey={`${trader.trader_id}_pnl_pct`}
                    stroke={color}
                    strokeWidth={
                      isHighlighted ? 4 : isDimmed ? 1.5 : idx === 0 ? 3 : 2
                    }
                    strokeOpacity={isDimmed ? 0.15 : 1}
                    dot={isHighlighted ? { r: 3, fill: color } : false}
                    activeDot={
                      isDimmed
                        ? false
                        : {
                            r: isHighlighted ? 7 : 5,
                            fill: color,
                            stroke: 'var(--nofx-bg)',
                            strokeWidth: 2,
                          }
                    }
                    name={trader.trader_name}
                    connectNulls
                  />
                )
              })}
            </ComposedChart>
          </ResponsiveContainer>
        </div>

        {/* Legend: Separated from chart plot area to prevent squishing */}
        <div className="mt-3 pt-3 border-t border-nofx-border/60 relative z-10">
          <div className="flex items-center justify-between mb-2 px-0.5">
            <div className="text-[11px] font-medium text-nofx-text-muted flex items-center gap-1.5 flex-wrap">
              <span>{t('traders', language) || 'Traders'}</span>
              {activePoint ? (
                <span className="text-nofx-gold font-mono flex items-center gap-1">
                  • {activeDateStr} {activeTimeStr} (
                  {language === 'zh' ? '历史快照' : 'Snapshot'})
                </span>
              ) : highlightedTraderId ? (
                <span className="text-nofx-gold font-mono flex items-center gap-1">
                  • {language === 'zh' ? '已聚焦高亮' : 'Focused'}
                </span>
              ) : (
                <span className="text-[10px] text-nofx-text-muted/70">
                  (
                  {language === 'zh'
                    ? '点击卡片高亮单条曲线'
                    : 'Click to highlight'}
                  )
                </span>
              )}
            </div>
            {(activePoint || highlightedTraderId) && (
              <button
                type="button"
                onClick={() => {
                  setActivePoint(null)
                  setHighlightedTraderId(null)
                }}
                className="text-[10px] text-nofx-gold hover:underline cursor-pointer"
              >
                {language === 'zh' ? '恢复全部' : 'Reset all'}
              </button>
            )}
          </div>

          <div className="grid grid-cols-2 xl:grid-cols-3 gap-2.5">
            {traders.map((trader) => {
              const isHighlighted =
                highlightedTraderId === trader.trader_id
              const isDimmed =
                highlightedTraderId !== null && !isHighlighted
              const color = traderColor(trader.trader_id)
              const traderStat = traderStats.find(
                (t) => t.trader_id === trader.trader_id
              )
              const pnl =
                currentSnapshotPoint?.[`${trader.trader_id}_pnl_pct`] ??
                traderStat?.currentPnl ??
                0

              return (
                <button
                  type="button"
                  key={trader.trader_id}
                  onClick={() => handleToggleHighlight(trader.trader_id)}
                  className={`flex items-center justify-between gap-2 px-3 py-2 rounded-lg border text-left transition-all cursor-pointer ${
                    isHighlighted
                      ? 'ring-2 ring-nofx-gold bg-nofx-gold/20 border-nofx-gold shadow-md scale-[1.02]'
                      : isDimmed
                        ? 'opacity-40 hover:opacity-80 bg-nofx-bg-lighter/40 border-nofx-border/40'
                        : activePoint
                          ? 'bg-nofx-gold/5 border-nofx-gold/35 shadow-sm hover:border-nofx-gold/60'
                          : 'bg-nofx-bg-lighter/70 border-nofx-border/60 hover:border-nofx-gold/40'
                  }`}
                  title={
                    isHighlighted
                      ? language === 'zh'
                        ? '点击取消高亮'
                        : 'Click to reset highlight'
                      : language === 'zh'
                        ? `点击高亮 ${trader.trader_name}`
                        : `Highlight ${trader.trader_name}`
                  }
                >
                  <div className="flex items-center gap-2 min-w-0 flex-1">
                    <span
                      className={`w-2.5 h-2.5 rounded-full shrink-0 transition-transform ${isHighlighted ? 'scale-125' : ''}`}
                      style={{ backgroundColor: color }}
                    />
                    <span
                      className={`text-xs font-semibold truncate leading-tight ${
                        isHighlighted ? 'text-nofx-gold' : 'text-nofx-text'
                      }`}
                      title={trader.trader_name}
                    >
                      {trader.trader_name}
                    </span>
                  </div>
                  <span
                    className={`text-xs font-mono shrink-0 ml-1.5 ${
                      pnl >= 0
                        ? 'text-nofx-success font-semibold'
                        : 'text-nofx-danger font-semibold'
                    }`}
                  >
                    {pnl >= 0 ? '+' : ''}
                    {pnl.toFixed(2)}%
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      </div>

      {/* Bottom Stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 sm:gap-3">
        <div
          className="p-2.5 sm:p-3 rounded-lg text-center bg-nofx-gold/10 border border-nofx-gold/20 flex flex-col justify-center min-h-[64px]"
          title={leaderName}
        >
          <div className="text-[10px] uppercase tracking-wider mb-0.5 text-nofx-text-muted">
            {t('leader', language)}
          </div>
          {hasLeaderPrefix ? (
            <div className="flex flex-col items-center min-w-0">
              <span className="text-[10px] text-nofx-gold/70 leading-tight truncate max-w-full">
                {leaderPrefix}
              </span>
              <span className="text-xs sm:text-sm font-bold text-nofx-gold leading-tight truncate max-w-full">
                {leaderCoreName}
              </span>
            </div>
          ) : (
            <div className="text-xs sm:text-sm font-bold truncate text-nofx-gold">
              {leaderName}
            </div>
          )}
        </div>
        <div className="p-2.5 sm:p-3 rounded-lg text-center bg-nofx-success/10 border border-nofx-success/20 flex flex-col justify-center min-h-[64px]">
          <div className="text-[10px] uppercase tracking-wider mb-1 text-nofx-text-muted">
            {t('leadPnL', language) || 'Lead PnL'}
          </div>
          <div
            className={`text-sm font-bold mono ${
              (leader?.currentPnl || 0) >= 0
                ? 'text-nofx-success'
                : 'text-nofx-danger'
            }`}
          >
            {(leader?.currentPnl || 0) >= 0 ? '+' : ''}
            {(leader?.currentPnl || 0).toFixed(2)}%
          </div>
        </div>
        <div className="p-2.5 sm:p-3 rounded-lg text-center bg-nofx-bg-lighter border border-nofx-border flex flex-col justify-center min-h-[64px]">
          <div className="text-[10px] uppercase tracking-wider mb-1 text-nofx-text-muted">
            {t('currentGap', language)}
          </div>
          <div className="text-sm font-bold mono text-nofx-text">{gap}%</div>
        </div>
        <div className="p-2.5 sm:p-3 rounded-lg text-center bg-nofx-bg-lighter border border-nofx-border flex flex-col justify-center min-h-[64px]">
          <div className="text-[10px] uppercase tracking-wider mb-1 text-nofx-text-muted">
            {t('dataPoints', language)}
          </div>
          <div className="text-sm font-bold mono text-nofx-text">
            {displayData.length}
          </div>
        </div>
      </div>
    </div>
  )
}
