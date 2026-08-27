import { useMemo, useState } from 'react'
import {
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceLine,
  Legend,
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
import { BarChart3, TrendingUp, TrendingDown, Zap } from 'lucide-react'

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

export function ComparisonChart({ traders }: ComparisonChartProps) {
  const { language } = useLanguage()
  const { isDark } = useTheme()
  const [selectedPeriod, setSelectedPeriod] = useState('7d') // Default to 7 days

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

  const MAX_DISPLAY_POINTS = 500
  const displayData =
    combinedData.length > MAX_DISPLAY_POINTS
      ? combinedData.slice(-MAX_DISPLAY_POINTS)
      : combinedData

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

      return (
        <div className="rounded-xl p-4 shadow-2xl backdrop-blur-md bg-nofx-bg-lighter/95 border border-nofx-border min-w-[200px]">
          <div className="flex items-center gap-2 mb-3 pb-2 border-b border-nofx-border">
            <Zap className="w-3.5 h-3.5 text-nofx-gold" />
            <span className="text-xs font-medium text-nofx-gold">
              {dateStr} {data.time}
            </span>
          </div>
          <div className="space-y-2.5">
            {traders.map((trader) => {
              const pnlPct = data[`${trader.trader_id}_pnl_pct`]
              const equity = data[`${trader.trader_id}_equity`]
              if (pnlPct === undefined) return null
              const isPositive = pnlPct >= 0

              return (
                <div
                  key={trader.trader_id}
                  className="flex items-center justify-between gap-4"
                >
                  <div className="flex items-center gap-2">
                    <div
                      className="w-2.5 h-2.5 rounded-full"
                      style={{ background: traderColor(trader.trader_id) }}
                    />
                    <span className="text-xs font-medium truncate max-w-[100px] text-nofx-text">
                      {trader.trader_name}
                    </span>
                  </div>
                  <div className="text-right">
                    <div
                      className={`text-sm font-bold mono flex items-center gap-1 ${
                        isPositive ? 'text-nofx-success' : 'text-nofx-danger'
                      }`}
                    >
                      {isPositive ? (
                        <TrendingUp className="w-3 h-3" />
                      ) : (
                        <TrendingDown className="w-3 h-3" />
                      )}
                      {isPositive ? '+' : ''}
                      {pnlPct.toFixed(2)}%
                    </div>
                    <div className="text-[10px] mono text-nofx-text-muted">
                      ${equity?.toFixed(2)}
                    </div>
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
  const gap =
    traderStats.length > 1
      ? Math.abs(traderStats[0].currentPnl - traderStats[1].currentPnl).toFixed(
          2
        )
      : '0.00'

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
              onClick={() => setSelectedPeriod(period.key)}
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
          {traderStats.slice(0, 3).map((trader, idx) => (
            <div
              key={trader.trader_id}
              className={`flex items-center gap-2 px-3 py-1.5 rounded-full transition-all hover:scale-105 border ${
                idx === 0
                  ? 'bg-nofx-gold/15 border-nofx-gold/30'
                  : 'bg-nofx-bg-lighter border-nofx-border'
              }`}
            >
              <div
                className="w-2 h-2 rounded-full"
                style={{ background: traderColor(trader.trader_id) }}
              />
              <span className="text-xs font-medium truncate max-w-[80px] text-nofx-text">
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
            </div>
          ))}
        </div>
      </div>

      {/* Chart */}
      <div className="relative rounded-xl overflow-hidden bg-nofx-bg border border-nofx-border">
        {/* Watermark */}
        <div
          style={{
            position: 'absolute',
            top: '50%',
            left: '50%',
            transform: 'translate(-50%, -50%)',
            fontSize: '80px',
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

        <ResponsiveContainer width="100%" height={420}>
          <ComposedChart
            data={displayData}
            margin={{ top: 20, right: 20, left: 10, bottom: 20 }}
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
              dataKey="time"
              stroke={axisTextColor}
              tick={{ fill: axisTextColor, fontSize: 10 }}
              tickLine={false}
              axisLine={{ stroke: axisLineColor }}
              interval={Math.max(Math.floor(displayData.length / 8), 1)}
            />

            <YAxis
              stroke={axisTextColor}
              tick={{ fill: axisTextColor, fontSize: 10 }}
              tickLine={false}
              axisLine={false}
              domain={calculateYDomain()}
              tickFormatter={(value) => `${value.toFixed(1)}%`}
              width={50}
            />

            <Tooltip content={<CustomTooltip />} />

            {/* Zero reference line */}
            <ReferenceLine
              y={0}
              stroke={refLineColor}
              strokeDasharray="8 4"
              strokeWidth={1}
            />

            {/* Area fills for top 2 traders */}
            {traders.slice(0, 2).map((trader) => (
              <Area
                key={`area-${trader.trader_id}`}
                type="monotone"
                dataKey={`${trader.trader_id}_pnl_pct`}
                fill={`url(#area-gradient-${trader.trader_id})`}
                stroke="none"
                connectNulls
              />
            ))}

            {/* Lines for all traders */}
            {traders.map((trader, idx) => (
              <Line
                key={trader.trader_id}
                type="monotone"
                dataKey={`${trader.trader_id}_pnl_pct`}
                stroke={traderColor(trader.trader_id)}
                strokeWidth={idx === 0 ? 3 : 2}
                dot={false}
                activeDot={{
                  r: 6,
                  fill: traderColor(trader.trader_id),
                  stroke: 'var(--nofx-bg)',
                  strokeWidth: 2,
                }}
                name={trader.trader_name}
                connectNulls
              />
            ))}

            <Legend
              wrapperStyle={{ paddingTop: '16px' }}
              content={({ payload }) => {
                const filteredPayload =
                  payload?.filter(
                    (entry: any) =>
                      entry.value && !entry.value.includes('_pnl_pct')
                  ) || []

                return (
                  <div
                    style={{
                      display: 'flex',
                      justifyContent: 'center',
                      gap: '20px',
                      flexWrap: 'wrap',
                    }}
                  >
                    {filteredPayload.map((entry: any, index: number) => {
                      const trader = traders.find(
                        (t) => t.trader_name === entry.value
                      )
                      const traderStat = traderStats.find(
                        (t) => t.trader_id === trader?.trader_id
                      )
                      const pnl = traderStat?.currentPnl || 0
                      return (
                        <div
                          key={`legend-${index}`}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '6px',
                          }}
                        >
                          <div
                            style={{
                              width: '8px',
                              height: '8px',
                              borderRadius: '50%',
                              backgroundColor: entry.color,
                            }}
                          />
                          <span className="text-xs font-medium text-nofx-text">
                            {entry.value}
                            <span
                              className={`ml-1.5 font-mono ${pnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
                            >
                              ({pnl >= 0 ? '+' : ''}
                              {pnl.toFixed(2)}%)
                            </span>
                          </span>
                        </div>
                      )
                    })}
                  </div>
                )
              }}
            />
          </ComposedChart>
        </ResponsiveContainer>
      </div>

      {/* Bottom Stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 sm:gap-3">
        <div className="p-3 rounded-lg text-center bg-nofx-gold/10 border border-nofx-gold/20">
          <div className="text-[10px] uppercase tracking-wider mb-1 text-nofx-text-muted">
            {t('leader', language)}
          </div>
          <div className="text-sm font-bold truncate text-nofx-gold">
            {leader?.trader_name || '-'}
          </div>
        </div>
        <div className="p-3 rounded-lg text-center bg-nofx-success/10 border border-nofx-success/20">
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
        <div className="p-3 rounded-lg text-center bg-nofx-bg-lighter border border-nofx-border">
          <div className="text-[10px] uppercase tracking-wider mb-1 text-nofx-text-muted">
            {t('currentGap', language)}
          </div>
          <div className="text-sm font-bold mono text-nofx-text">{gap}%</div>
        </div>
        <div className="p-3 rounded-lg text-center bg-nofx-bg-lighter border border-nofx-border">
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
