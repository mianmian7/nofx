import { useState, useEffect, useMemo } from 'react'
import { api } from '../../lib/api'
import { useLanguage } from '../../contexts/LanguageContext'
import { t, type Language } from '../../i18n/translations'
import { MetricTooltip } from '../common/MetricTooltip'
import { formatPrice, formatQuantity } from '../../utils/format'
import { NofxSelect } from '../ui/select'
import type {
  HistoricalPosition,
  TraderStats,
  SymbolStats,
  DirectionStats,
} from '../../types'

interface PositionHistoryProps {
  traderId: string
}

// Format number with proper decimals (for large numbers)
function formatNumber(value: number, decimals: number = 2): string {
  if (Math.abs(value) >= 1000000) {
    return (value / 1000000).toFixed(2) + 'M'
  }
  if (Math.abs(value) >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  }
  return value.toFixed(decimals)
}

// Format duration from minutes
function formatDuration(minutes: number): string {
  if (!minutes || minutes <= 0) return '-'
  if (minutes < 60) return `${minutes.toFixed(0)}m`
  if (minutes < 1440) return `${(minutes / 60).toFixed(1)}h`
  return `${(minutes / 1440).toFixed(1)}d`
}

// Format date
function formatDate(value: number | string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (isNaN(date.getTime())) return '-'
  return date.toLocaleDateString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// Stats Card Component with formula tooltip
function StatCard({
  title,
  value,
  suffix,
  color,
  icon,
  subtitle,
  metricKey,
  language = 'en',
}: {
  title: string
  value: string | number
  suffix?: string
  color?: string
  icon: string
  subtitle?: string
  metricKey?: string
  language?: string
}) {
  return (
    <div className="rounded-lg p-4 transition-all duration-200 hover:scale-[1.02] bg-nofx-bg-lighter border border-nofx-border">
      <div className="flex items-center gap-2 mb-2">
        <span className="text-lg">{icon}</span>
        <span className="text-xs text-nofx-text-muted">{title}</span>
        {metricKey && (
          <MetricTooltip metricKey={metricKey} language={language} size={12} />
        )}
      </div>
      <div className="flex items-baseline gap-1">
        <span
          className="text-xl font-bold font-mono text-nofx-text"
          style={{ color: color || undefined }}
        >
          {value}
        </span>
        {suffix && (
          <span className="text-sm text-nofx-text-muted">{suffix}</span>
        )}
      </div>
      {subtitle && (
        <div className="text-xs mt-1 text-nofx-text-muted">{subtitle}</div>
      )}
    </div>
  )
}

// Symbol Stats Row
function SymbolStatsRow({ stat }: { stat: SymbolStats }) {
  const totalPnl = stat.total_pnl || 0
  const winRate = stat.win_rate || 0
  const isProfit = totalPnl >= 0

  return (
    <div className="flex items-center justify-between p-3 rounded-lg transition-all duration-200 hover:bg-nofx-gold/10 border-b border-nofx-border">
      <div className="flex items-center gap-3">
        <span className="font-mono font-semibold text-nofx-text">
          {(stat.symbol || '').replace('USDT', '')}
        </span>
        <span className="text-xs text-nofx-text-muted">
          {stat.total_trades || 0} trades
        </span>
      </div>
      <div className="flex items-center gap-6">
        <div className="text-right">
          <div className="text-xs text-nofx-text-muted">Win Rate</div>
          <div
            className={`font-mono font-semibold ${
              winRate >= 60
                ? 'text-nofx-success'
                : winRate >= 40
                  ? 'text-nofx-gold'
                  : 'text-nofx-danger'
            }`}
          >
            {winRate.toFixed(1)}%
          </div>
        </div>
        <div className="text-right min-w-[80px]">
          <div className="text-xs text-nofx-text-muted">P&L</div>
          <div
            className={`font-mono font-semibold ${isProfit ? 'text-nofx-success' : 'text-nofx-danger'}`}
          >
            {totalPnl >= 0 ? '+' : ''}
            {formatNumber(totalPnl)}
          </div>
        </div>
      </div>
    </div>
  )
}

// Direction Stats Card
function DirectionStatsCard({
  stat,
  language,
}: {
  stat: DirectionStats
  language: Language
}) {
  const isLong = (stat.side || '').toLowerCase() === 'long'
  const isProfit = (stat.total_pnl || 0) >= 0
  const winRate = stat.win_rate || 0
  const tradeCount = stat.trade_count || 0
  const avgPnl = stat.avg_pnl || 0

  return (
    <div
      className={`rounded-lg p-4 bg-nofx-bg-lighter border ${
        isLong ? 'border-nofx-success/30' : 'border-nofx-danger/30'
      }`}
    >
      <div className="flex items-center gap-2 mb-3">
        <span className="text-xl">{isLong ? '📈' : '📉'}</span>
        <span
          className={`font-bold uppercase ${isLong ? 'text-nofx-success' : 'text-nofx-danger'}`}
        >
          {stat.side || 'Unknown'}
        </span>
      </div>
      <div className="grid grid-cols-4 gap-4">
        <div>
          <div className="text-xs mb-1 text-nofx-text-muted">
            {t('positionHistory.trades', language)}
          </div>
          <div className="font-mono font-semibold text-nofx-text">
            {tradeCount}
          </div>
        </div>
        <div>
          <div className="text-xs mb-1 text-nofx-text-muted">
            {t('positionHistory.winRate', language)}
          </div>
          <div
            className={`font-mono font-semibold ${
              winRate >= 60
                ? 'text-nofx-success'
                : winRate >= 40
                  ? 'text-nofx-gold'
                  : 'text-nofx-danger'
            }`}
          >
            {winRate.toFixed(1)}%
          </div>
        </div>
        <div>
          <div className="text-xs mb-1 text-nofx-text-muted">
            {t('positionHistory.totalPnL', language)}
          </div>
          <div
            className={`font-mono font-semibold ${isProfit ? 'text-nofx-success' : 'text-nofx-danger'}`}
          >
            {stat.total_pnl && stat.total_pnl >= 0 ? '+' : ''}
            {formatNumber(stat.total_pnl || 0)}
          </div>
        </div>
        <div>
          <div className="text-xs mb-1 text-nofx-text-muted">
            {t('positionHistory.avgPnL', language)}
          </div>
          <div
            className={`font-mono font-semibold ${avgPnl >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
          >
            {avgPnl >= 0 ? '+' : ''}
            {formatNumber(avgPnl)}
          </div>
        </div>
      </div>
    </div>
  )
}

// Position Row Component
function PositionRow({ position }: { position: HistoricalPosition }) {
  const side = position.side || ''
  const isLong = side.toUpperCase() === 'LONG'
  const realizedPnl = position.realized_pnl || 0
  const isProfitable = realizedPnl >= 0

  const entryTime = position.entry_time
    ? new Date(position.entry_time).getTime()
    : 0
  const exitTime = position.exit_time
    ? new Date(position.exit_time).getTime()
    : 0
  const holdingMinutes =
    entryTime && exitTime && exitTime > entryTime
      ? (exitTime - entryTime) / 60000
      : 0

  const entryPrice = position.entry_price || 0
  const exitPrice = position.exit_price || 0
  let pnlPct = 0
  if (entryPrice > 0) {
    if (isLong) {
      pnlPct = ((exitPrice - entryPrice) / entryPrice) * 100
    } else {
      pnlPct = ((entryPrice - exitPrice) / entryPrice) * 100
    }
  }

  const displayQty = position.entry_quantity || position.quantity || 0

  return (
    <tr className="transition-all duration-200 hover:bg-nofx-gold/10 border-b border-nofx-border">
      {/* Symbol */}
      <td className="py-3 px-4">
        <div className="flex items-center gap-2">
          <span className="font-mono font-semibold text-nofx-text">
            {(position.symbol || '').replace('USDT', '')}
          </span>
          <span
            className={`px-2 py-0.5 rounded text-xs font-semibold uppercase border ${
              isLong
                ? 'bg-nofx-success/15 text-nofx-success border-nofx-success/30'
                : 'bg-nofx-danger/15 text-nofx-danger border-nofx-danger/30'
            }`}
          >
            {side}
          </span>
        </div>
      </td>

      {/* Entry Price */}
      <td className="py-3 px-4 text-right font-mono text-nofx-text">
        {formatPrice(entryPrice)}
      </td>

      {/* Exit Price */}
      <td className="py-3 px-4 text-right font-mono text-nofx-text">
        {formatPrice(exitPrice)}
      </td>

      {/* Quantity */}
      <td className="py-3 px-4 text-right font-mono text-nofx-text-muted">
        {formatQuantity(displayQty)}
      </td>

      {/* Position Value */}
      <td className="py-3 px-4 text-right font-mono text-nofx-text">
        {formatNumber(entryPrice * displayQty)}
      </td>

      {/* P&L */}
      <td className="py-3 px-4 text-right">
        <div
          className={`font-mono font-semibold ${isProfitable ? 'text-nofx-success' : 'text-nofx-danger'}`}
        >
          {isProfitable ? '+' : ''}
          {formatNumber(realizedPnl)}
        </div>
        <div
          className={`text-xs ${pnlPct >= 0 ? 'text-nofx-success' : 'text-nofx-danger'}`}
        >
          {pnlPct >= 0 ? '+' : ''}
          {pnlPct.toFixed(2)}%
        </div>
      </td>

      {/* Fee */}
      <td className="py-3 px-4 text-right font-mono text-xs text-nofx-text-muted">
        -
        {(position.fee || 0) < 0.01 && (position.fee || 0) > 0
          ? (position.fee || 0).toFixed(4)
          : (position.fee || 0).toFixed(2)}
      </td>

      {/* Duration */}
      <td className="py-3 px-4 text-center text-sm text-nofx-text-muted">
        {formatDuration(holdingMinutes)}
      </td>

      {/* Exit Time */}
      <td className="py-3 px-4 text-right text-xs text-nofx-text-muted">
        {formatDate(position.exit_time)}
      </td>
    </tr>
  )
}

export function PositionHistory({ traderId }: PositionHistoryProps) {
  const { language } = useLanguage()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [positions, setPositions] = useState<HistoricalPosition[]>([])
  const [stats, setStats] = useState<TraderStats | null>(null)
  const [symbolStats, setSymbolStats] = useState<SymbolStats[]>([])
  const [directionStats, setDirectionStats] = useState<DirectionStats[]>([])

  // Pagination state
  const [pageSize, setPageSize] = useState<number>(20)
  const [currentPage, setCurrentPage] = useState<number>(1)

  // Filter state
  const [filterSymbol, setFilterSymbol] = useState<string>('all')
  const [filterSide, setFilterSide] = useState<string>('all')
  const [sortBy, setSortBy] = useState<'time' | 'pnl' | 'pnl_pct'>('time')
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('desc')

  // Fetch position history
  useEffect(() => {
    async function fetchData() {
      setLoading(true)
      try {
        const historyData = await api.getPositionHistory(traderId)
        setPositions(historyData.positions || [])
        setStats(historyData.stats || null)
        setSymbolStats(historyData.symbol_stats || [])
        setDirectionStats(historyData.direction_stats || [])
        setError(null)
      } catch (err: any) {
        setError(err.message || 'Failed to load position history')
      } finally {
        setLoading(false)
      }
    }

    if (traderId) {
      fetchData()
    }
  }, [traderId])

  // Get unique symbols for filter
  const uniqueSymbols = useMemo(() => {
    const symbols = new Set<string>()
    positions.forEach((p) => {
      if (p.symbol) symbols.add(p.symbol)
    })
    return Array.from(symbols).sort()
  }, [positions])

  // Filter and sort positions
  const filteredAndSortedPositions = useMemo(() => {
    let result = [...positions]

    if (filterSymbol !== 'all') {
      result = result.filter((p) => p.symbol === filterSymbol)
    }

    if (filterSide !== 'all') {
      result = result.filter((p) => (p.side || '').toUpperCase() === filterSide)
    }

    result.sort((a, b) => {
      let comparison = 0

      switch (sortBy) {
        case 'time': {
          const timeA = new Date(a.exit_time || a.entry_time || 0).getTime()
          const timeB = new Date(b.exit_time || b.entry_time || 0).getTime()
          comparison = timeA - timeB
          break
        }
        case 'pnl':
          comparison = (a.realized_pnl || 0) - (b.realized_pnl || 0)
          break
        case 'pnl_pct': {
          const pnlPctA =
            a.entry_price && a.entry_price > 0
              ? (((a.exit_price || 0) - a.entry_price) / a.entry_price) *
                100 *
                (a.side?.toUpperCase() === 'SHORT' ? -1 : 1)
              : 0
          const pnlPctB =
            b.entry_price && b.entry_price > 0
              ? (((b.exit_price || 0) - b.entry_price) / b.entry_price) *
                100 *
                (b.side?.toUpperCase() === 'SHORT' ? -1 : 1)
              : 0
          comparison = pnlPctA - pnlPctB
          break
        }
      }

      return sortOrder === 'desc' ? -comparison : comparison
    })

    return result
  }, [positions, filterSymbol, filterSide, sortBy, sortOrder])

  // Total count for current filter
  const totalFilteredCount = filteredAndSortedPositions.length
  const totalPages = Math.ceil(totalFilteredCount / pageSize) || 1

  // Paginated positions
  const filteredPositions = useMemo(() => {
    const start = (currentPage - 1) * pageSize
    return filteredAndSortedPositions.slice(start, start + pageSize)
  }, [filteredAndSortedPositions, currentPage, pageSize])

  // Reset to first page when filters change
  useEffect(() => {
    setCurrentPage(1)
  }, [filterSymbol, filterSide, pageSize])

  // Calculate profit/loss ratio (avg win / avg loss)
  const profitLossRatio = useMemo(() => {
    if (!stats) return 0
    const avgWin = stats.avg_win || 0
    const avgLoss = stats.avg_loss || 0
    if (avgLoss === 0) return avgWin > 0 ? Infinity : 0
    return avgWin / avgLoss
  }, [stats])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-center">
          <div className="w-8 h-8 border-2 border-nofx-gold border-t-transparent rounded-full animate-spin mx-auto mb-2" />
          <p className="text-sm text-nofx-text-muted">
            {t('loadingPositionHistory', language)}
          </p>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="rounded-lg p-6 bg-nofx-danger/10 border border-nofx-danger/20 text-nofx-danger">
        <div className="font-semibold mb-1">
          {t('failedToLoadHistory', language)}
        </div>
        <div className="text-sm text-nofx-text-muted">{error}</div>
      </div>
    )
  }

  if (positions.length === 0) {
    return (
      <div className="rounded-lg p-12 text-center bg-nofx-bg-lighter border border-nofx-border">
        <div className="text-4xl mb-4">📊</div>
        <div className="text-lg font-semibold mb-2 text-nofx-text">
          {t('positionHistory.noHistory', language)}
        </div>
        <div className="text-nofx-text-muted">
          {t('positionHistory.noHistoryDesc', language)}
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Overall Stats - Row 1: Core Metrics */}
      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
          <StatCard
            icon="📊"
            title={t('positionHistory.totalTrades', language)}
            value={stats.total_trades || 0}
            subtitle={t('positionHistory.winLoss', language, {
              win: stats.win_trades || 0,
              loss: stats.loss_trades || 0,
            })}
            language={language}
          />
          <StatCard
            icon="🎯"
            title={t('positionHistory.winRate', language)}
            value={(stats.win_rate || 0).toFixed(1)}
            suffix="%"
            color={
              (stats.win_rate || 0) >= 60
                ? 'var(--binance-green)'
                : (stats.win_rate || 0) >= 40
                  ? 'var(--nofx-gold)'
                  : 'var(--binance-red)'
            }
            metricKey="win_rate"
            language={language}
          />
          <StatCard
            icon="💰"
            title={t('positionHistory.totalPnL', language)}
            value={
              ((stats.total_pnl || 0) >= 0 ? '+' : '') +
              formatNumber(stats.total_pnl || 0)
            }
            color={
              (stats.total_pnl || 0) >= 0
                ? 'var(--binance-green)'
                : 'var(--binance-red)'
            }
            subtitle={`${t('positionHistory.fee', language)}: -${formatNumber(stats.total_fee || 0)}`}
            metricKey="total_return"
            language={language}
          />
          <StatCard
            icon="📈"
            title={t('positionHistory.profitFactor', language)}
            value={(stats.profit_factor || 0).toFixed(2)}
            color={
              (stats.profit_factor || 0) >= 1.5
                ? 'var(--binance-green)'
                : (stats.profit_factor || 0) >= 1
                  ? 'var(--nofx-gold)'
                  : 'var(--binance-red)'
            }
            subtitle={t('positionHistory.profitFactorDesc', language)}
            metricKey="profit_factor"
            language={language}
          />
          <StatCard
            icon="⚖️"
            title={t('positionHistory.plRatio', language)}
            value={
              profitLossRatio === Infinity ? '∞' : profitLossRatio.toFixed(2)
            }
            color={
              profitLossRatio >= 1.5
                ? 'var(--binance-green)'
                : profitLossRatio >= 1
                  ? 'var(--nofx-gold)'
                  : 'var(--binance-red)'
            }
            subtitle={t('positionHistory.plRatioDesc', language)}
            metricKey="expectancy"
            language={language}
          />
        </div>
      )}

      {/* Overall Stats - Row 2: Advanced Metrics */}
      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
          <StatCard
            icon="📉"
            title={t('positionHistory.sharpeRatio', language)}
            value={(stats.sharpe_ratio || 0).toFixed(2)}
            color={
              (stats.sharpe_ratio || 0) >= 1
                ? 'var(--binance-green)'
                : (stats.sharpe_ratio || 0) >= 0
                  ? 'var(--nofx-gold)'
                  : 'var(--binance-red)'
            }
            subtitle={t('positionHistory.sharpeRatioDesc', language)}
            metricKey="sharpe_ratio"
            language={language}
          />
          <StatCard
            icon="🔻"
            title={t('positionHistory.maxDrawdown', language)}
            value={(stats.max_drawdown_pct || 0).toFixed(1)}
            suffix="%"
            color={
              (stats.max_drawdown_pct || 0) <= 10
                ? 'var(--binance-green)'
                : (stats.max_drawdown_pct || 0) <= 20
                  ? 'var(--nofx-gold)'
                  : 'var(--binance-red)'
            }
            metricKey="max_drawdown"
            language={language}
          />
          <StatCard
            icon="🏆"
            title={t('positionHistory.avgWin', language)}
            value={'+' + formatNumber(stats.avg_win || 0)}
            color="var(--binance-green)"
            metricKey="avg_trade_pnl"
            language={language}
          />
          <StatCard
            icon="💸"
            title={t('positionHistory.avgLoss', language)}
            value={'-' + formatNumber(stats.avg_loss || 0)}
            color="var(--binance-red)"
            language={language}
          />
          <StatCard
            icon="💵"
            title={t('positionHistory.netPnL', language)}
            value={
              ((stats.total_pnl || 0) - (stats.total_fee || 0) >= 0
                ? '+'
                : '') +
              formatNumber((stats.total_pnl || 0) - (stats.total_fee || 0))
            }
            color={
              (stats.total_pnl || 0) - (stats.total_fee || 0) >= 0
                ? 'var(--binance-green)'
                : 'var(--binance-red)'
            }
            subtitle={t('positionHistory.netPnLDesc', language)}
            language={language}
          />
        </div>
      )}

      {/* Direction Stats */}
      {directionStats.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {directionStats.map((stat) => (
            <DirectionStatsCard
              key={stat.side}
              stat={stat}
              language={language}
            />
          ))}
        </div>
      )}

      {/* Symbol Performance */}
      {symbolStats.length > 0 && (
        <div className="rounded-lg p-4 bg-nofx-bg-lighter border border-nofx-border">
          <div className="flex items-center gap-2 mb-4">
            <span className="text-lg">🏅</span>
            <span className="font-semibold text-nofx-text">
              {t('positionHistory.symbolPerformance', language)}
            </span>
          </div>
          <div className="space-y-1">
            {symbolStats.slice(0, 10).map((stat) => (
              <SymbolStatsRow key={stat.symbol} stat={stat} />
            ))}
          </div>
        </div>
      )}

      {/* Position List */}
      <div className="rounded-lg overflow-hidden bg-nofx-bg-lighter border border-nofx-border">
        {/* Filters */}
        <div className="flex flex-wrap items-center gap-4 p-4 border-b border-nofx-border">
          <div className="flex items-center gap-2">
            <span className="text-sm text-nofx-text-muted">
              {t('positionHistory.symbol', language)}:
            </span>
            <NofxSelect
              value={filterSymbol}
              onChange={(val) => setFilterSymbol(val)}
              options={[
                {
                  value: 'all',
                  label: t('positionHistory.allSymbols', language),
                },
                ...uniqueSymbols.map((s) => ({
                  value: s,
                  label: (s || '').replace('USDT', ''),
                })),
              ]}
              className="rounded px-3 py-1.5 text-sm bg-nofx-bg-deeper border border-nofx-border text-nofx-text"
            />
          </div>

          <div className="flex items-center gap-2">
            <span className="text-sm text-nofx-text-muted">
              {t('positionHistory.side', language)}:
            </span>
            <div className="flex rounded overflow-hidden border border-nofx-border">
              {['all', 'LONG', 'SHORT'].map((side) => (
                <button
                  key={side}
                  onClick={() => setFilterSide(side)}
                  className={`px-3 py-1.5 text-sm capitalize transition-colors ${
                    filterSide === side
                      ? 'bg-nofx-gold/15 text-nofx-gold font-bold'
                      : 'bg-transparent text-nofx-text-muted hover:text-nofx-text'
                  }`}
                >
                  {side === 'all' ? t('positionHistory.all', language) : side}
                </button>
              ))}
            </div>
          </div>

          <div className="flex items-center gap-2 ml-auto">
            <span className="text-sm text-nofx-text-muted">
              {t('positionHistory.sort', language)}:
            </span>
            <NofxSelect
              value={`${sortBy}-${sortOrder}`}
              onChange={(val) => {
                const [by, order] = val.split('-') as [
                  'time' | 'pnl' | 'pnl_pct',
                  'asc' | 'desc',
                ]
                setSortBy(by)
                setSortOrder(order)
              }}
              options={[
                {
                  value: 'time-desc',
                  label: t('positionHistory.latestFirst', language),
                },
                {
                  value: 'time-asc',
                  label: t('positionHistory.oldestFirst', language),
                },
                {
                  value: 'pnl-desc',
                  label: t('positionHistory.highestPnL', language),
                },
                {
                  value: 'pnl-asc',
                  label: t('positionHistory.lowestPnL', language),
                },
              ]}
              className="rounded px-3 py-1.5 text-sm bg-nofx-bg-deeper border border-nofx-border text-nofx-text"
            />
          </div>
        </div>

        {/* Table */}
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="bg-nofx-bg-deeper border-b border-nofx-border">
                <th className="py-3 px-4 text-left text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.symbol', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.entry', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.exit', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.qty', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.value', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.pnl', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.fee', language)}
                </th>
                <th className="py-3 px-4 text-center text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.duration', language)}
                </th>
                <th className="py-3 px-4 text-right text-xs font-semibold uppercase tracking-wider text-nofx-text-muted">
                  {t('positionHistory.closedAt', language)}
                </th>
              </tr>
            </thead>
            <tbody>
              {filteredPositions.map((position) => (
                <PositionRow key={position.id} position={position} />
              ))}
            </tbody>
          </table>
        </div>

        {/* Footer with Pagination */}
        <div className="flex flex-wrap items-center justify-between gap-4 p-4 text-sm border-t border-nofx-border text-nofx-text-muted">
          {/* Left: Count info */}
          <div className="flex items-center gap-4">
            <span>
              {t('positionHistory.showingPositions', language, {
                count: totalFilteredCount,
                total: positions.length,
              })}
            </span>
            {totalFilteredCount > 0 && (
              <span>
                {t('positionHistory.totalPnL', language)}:{' '}
                <span
                  className={`font-mono font-semibold ${
                    filteredAndSortedPositions.reduce(
                      (sum, p) => sum + (p.realized_pnl || 0),
                      0
                    ) >= 0
                      ? 'text-nofx-success'
                      : 'text-nofx-danger'
                  }`}
                >
                  {filteredAndSortedPositions.reduce(
                    (sum, p) => sum + (p.realized_pnl || 0),
                    0
                  ) >= 0
                    ? '+'
                    : ''}
                  {formatNumber(
                    filteredAndSortedPositions.reduce(
                      (sum, p) => sum + (p.realized_pnl || 0),
                      0
                    )
                  )}
                </span>
              </span>
            )}
          </div>

          {/* Right: Pagination controls */}
          <div className="flex items-center gap-3">
            {/* Page size selector */}
            <div className="flex items-center gap-2">
              <span className="text-xs">
                {t('positionHistory.perPage', language)}:
              </span>
              <NofxSelect
                value={pageSize}
                onChange={(val) => setPageSize(Number(val))}
                options={[
                  { value: 10, label: '10' },
                  { value: 20, label: '20' },
                  { value: 50, label: '50' },
                  { value: 100, label: '100' },
                ]}
                className="bg-nofx-bg-deeper border border-nofx-border rounded px-2 py-1 text-xs text-nofx-text transition-colors"
              />
            </div>

            {/* Page navigation */}
            {totalPages > 1 && (
              <div className="flex items-center gap-1">
                <button
                  onClick={() => setCurrentPage(1)}
                  disabled={currentPage === 1}
                  className="px-2 py-1 rounded text-xs transition-colors bg-nofx-bg-deeper text-nofx-text disabled:opacity-40 disabled:cursor-not-allowed hover:bg-nofx-gold/20"
                >
                  «
                </button>
                <button
                  onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                  disabled={currentPage === 1}
                  className="px-2 py-1 rounded text-xs transition-colors bg-nofx-bg-deeper text-nofx-text disabled:opacity-40 disabled:cursor-not-allowed hover:bg-nofx-gold/20"
                >
                  ‹
                </button>
                <span className="px-2 text-xs text-nofx-text font-mono">
                  {currentPage} / {totalPages}
                </span>
                <button
                  onClick={() =>
                    setCurrentPage((p) => Math.min(totalPages, p + 1))
                  }
                  disabled={currentPage === totalPages}
                  className="px-2 py-1 rounded text-xs transition-colors bg-nofx-bg-deeper text-nofx-text disabled:opacity-40 disabled:cursor-not-allowed hover:bg-nofx-gold/20"
                >
                  ›
                </button>
                <button
                  onClick={() => setCurrentPage(totalPages)}
                  disabled={currentPage === totalPages}
                  className="px-2 py-1 rounded text-xs transition-colors bg-nofx-bg-deeper text-nofx-text disabled:opacity-40 disabled:cursor-not-allowed hover:bg-nofx-gold/20"
                >
                  »
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
