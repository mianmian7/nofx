import { useState, useEffect } from 'react'
import {
  Shield,
  TrendingUp,
  AlertTriangle,
  Activity,
  Box,
  ChevronDown,
  ChevronUp,
} from 'lucide-react'
import { useLanguage } from '../../contexts/LanguageContext'
import { ts, gridRisk } from '../../i18n/strategy-translations'

interface GridRiskInfo {
  regime_level: string
  current_leverage: number
  recommended_leverage: number
  effective_leverage: number
  position_percent: number
  current_position: number
  max_position: number
  liquidation_price: number
  liquidation_distance: number
  current_price: number
  breakout_level: string
  breakout_direction: string
  short_box_lower: number
  short_box_upper: number
  mid_box_lower: number
  mid_box_upper: number
  long_box_lower: number
  long_box_upper: number
}

interface GridRiskPanelProps {
  traderId: string
}

export function GridRiskPanel({ traderId }: GridRiskPanelProps) {
  const { language } = useLanguage()
  const [riskInfo, setRiskInfo] = useState<GridRiskInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(true)

  useEffect(() => {
    const fetchRiskInfo = async () => {
      try {
        const res = await fetch(`/api/traders/${traderId}/grid-risk`)
        if (!res.ok) {
          if (res.status === 404) {
            setRiskInfo(null)
            setLoading(false)
            return
          }
          throw new Error(`HTTP ${res.status}`)
        }
        const data = await res.json()
        setRiskInfo(data)
        setError(null)
      } catch (err: any) {
        setError(err.message || 'Failed to fetch grid risk info')
      } finally {
        setLoading(false)
      }
    }

    fetchRiskInfo()
    const interval = setInterval(fetchRiskInfo, 10000)
    return () => clearInterval(interval)
  }, [traderId])

  const getRegimeColor = (level: string) => {
    switch (level) {
      case 'strong_trending':
      case 'trending':
        return '#D6433A'
      case 'calm':
      case 'standard':
        return '#2E8B57'
      case 'volatile':
        return '#E0483B'
      default:
        return 'var(--text-tertiary)'
    }
  }

  const getBreakoutColor = (level: string) => {
    switch (level) {
      case 'danger':
        return '#D6433A'
      case 'warning':
      case 'caution':
        return '#E0483B'
      case 'safe':
      case 'none':
        return '#2E8B57'
      default:
        return 'var(--text-tertiary)'
    }
  }

  const getPositionColor = (percent: number) => {
    if (percent >= 80) return '#D6433A'
    if (percent >= 60) return '#E0483B'
    return '#2E8B57'
  }

  const formatPrice = (price: number) => {
    if (!price) return '-'
    if (price >= 1000)
      return price.toLocaleString('en-US', {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      })
    if (price >= 1) return price.toFixed(4)
    return price.toFixed(6)
  }

  const formatUSD = (value: number) => {
    return `$${value.toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`
  }

  if (loading) {
    return (
      <div className="p-3 text-center text-xs text-nofx-text-muted">
        {ts(gridRisk.loading, language)}
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 text-center text-xs text-nofx-danger">
        {ts(gridRisk.error, language)}: {error}
      </div>
    )
  }

  if (!riskInfo) {
    return (
      <div className="p-3 text-center text-xs text-nofx-text-muted">
        {ts(gridRisk.noData, language)}
      </div>
    )
  }

  return (
    <div className="rounded-lg bg-nofx-bg-lighter border border-nofx-border">
      {/* Collapsible Header */}
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-nofx-bg-deeper transition-colors"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="flex items-center gap-2">
          <Shield className="w-4 h-4 text-nofx-gold" />
          <span className="font-medium text-sm text-nofx-text">
            {ts(gridRisk.gridRisk, language)}
          </span>
        </div>
        <div className="flex items-center gap-3">
          {/* Summary badges when collapsed */}
          <div className="flex items-center gap-2 text-xs">
            <span
              className="px-2 py-0.5 rounded"
              style={{
                background: getRegimeColor(riskInfo.regime_level) + '20',
                color: getRegimeColor(riskInfo.regime_level),
              }}
            >
              {ts(
                gridRisk[
                  (riskInfo.regime_level || 'standard') as keyof typeof gridRisk
                ],
                language
              )}
            </span>
            <span className="font-mono text-nofx-text">
              {riskInfo.effective_leverage.toFixed(1)}x
            </span>
            <span
              className="font-mono"
              style={{ color: getPositionColor(riskInfo.position_percent) }}
            >
              {riskInfo.position_percent.toFixed(0)}%
            </span>
          </div>
          {expanded ? (
            <ChevronUp className="w-4 h-4 text-nofx-text-muted" />
          ) : (
            <ChevronDown className="w-4 h-4 text-nofx-text-muted" />
          )}
        </div>
      </div>

      {/* Expanded Content */}
      {expanded && (
        <div className="px-3 pb-3 space-y-3">
          {/* Row 1: Leverage & Position */}
          <div className="grid grid-cols-2 gap-3">
            {/* Leverage */}
            <div className="p-2 rounded bg-nofx-bg-deeper border border-nofx-border/50">
              <div className="flex items-center gap-1 mb-2">
                <TrendingUp className="w-3 h-3 text-nofx-gold" />
                <span className="text-xs font-medium text-nofx-text-muted">
                  {ts(gridRisk.leverageInfo, language)}
                </span>
              </div>
              <div className="grid grid-cols-3 gap-1 text-xs">
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.currentLeverage, language)}
                  </div>
                  <div className="font-mono text-nofx-text">
                    {riskInfo.current_leverage}x
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.effectiveLeverage, language)}
                  </div>
                  <div className="font-mono text-nofx-gold">
                    {riskInfo.effective_leverage.toFixed(2)}x
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.recommendedLeverage, language)}
                  </div>
                  <div
                    className={`font-mono ${
                      riskInfo.current_leverage > riskInfo.recommended_leverage
                        ? 'text-nofx-danger'
                        : 'text-nofx-success'
                    }`}
                  >
                    {riskInfo.recommended_leverage}x
                  </div>
                </div>
              </div>
            </div>

            {/* Position */}
            <div className="p-2 rounded bg-nofx-bg-deeper border border-nofx-border/50">
              <div className="flex items-center gap-1 mb-2">
                <Activity className="w-3 h-3 text-nofx-gold" />
                <span className="text-xs font-medium text-nofx-text-muted">
                  {ts(gridRisk.positionInfo, language)}
                </span>
              </div>
              <div className="grid grid-cols-3 gap-1 text-xs">
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.currentPosition, language)}
                  </div>
                  <div className="font-mono text-nofx-text">
                    {formatUSD(riskInfo.current_position)}
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.maxPosition, language)}
                  </div>
                  <div className="font-mono text-nofx-text">
                    {formatUSD(riskInfo.max_position)}
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.positionPercent, language)}
                  </div>
                  <div
                    className="font-mono"
                    style={{
                      color: getPositionColor(riskInfo.position_percent),
                    }}
                  >
                    {riskInfo.position_percent.toFixed(1)}%
                  </div>
                </div>
              </div>
              {/* Mini progress bar */}
              <div className="h-1 mt-2 rounded-full overflow-hidden bg-nofx-bg">
                <div
                  className="h-full rounded-full"
                  style={{
                    width: `${Math.min(riskInfo.position_percent, 100)}%`,
                    background: getPositionColor(riskInfo.position_percent),
                  }}
                />
              </div>
            </div>
          </div>

          {/* Row 2: Market State & Liquidation */}
          <div className="grid grid-cols-2 gap-3">
            {/* Market State */}
            <div className="p-2 rounded bg-nofx-bg-deeper border border-nofx-border/50">
              <div className="flex items-center gap-1 mb-2">
                <Shield className="w-3 h-3 text-nofx-gold" />
                <span className="text-xs font-medium text-nofx-text-muted">
                  {ts(gridRisk.marketState, language)}
                </span>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.regimeLevel, language)}
                  </div>
                  <div
                    className="font-medium"
                    style={{
                      color: getRegimeColor(riskInfo.regime_level),
                    }}
                  >
                    {ts(
                      gridRisk[
                        (riskInfo.regime_level ||
                          'standard') as keyof typeof gridRisk
                      ],
                      language
                    )}
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.currentPrice, language)}
                  </div>
                  <div className="font-mono text-nofx-text">
                    {formatPrice(riskInfo.current_price)}
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.breakoutLevel, language)}
                  </div>
                  <div
                    className="font-medium"
                    style={{
                      color: getBreakoutColor(riskInfo.breakout_level),
                    }}
                  >
                    {ts(
                      gridRisk[
                        (riskInfo.breakout_level ||
                          'none') as keyof typeof gridRisk
                      ],
                      language
                    )}
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.breakoutDirection, language)}
                  </div>
                  <div
                    className={`font-medium ${
                      riskInfo.breakout_direction === 'up'
                        ? 'text-nofx-success'
                        : riskInfo.breakout_direction === 'down'
                          ? 'text-nofx-danger'
                          : 'text-nofx-text-muted'
                    }`}
                  >
                    {riskInfo.breakout_direction
                      ? ts(
                          gridRisk[
                            riskInfo.breakout_direction as keyof typeof gridRisk
                          ],
                          language
                        )
                      : '-'}
                  </div>
                </div>
              </div>
            </div>

            {/* Liquidation */}
            <div className="p-2 rounded bg-nofx-bg-deeper border border-nofx-border/50">
              <div className="flex items-center gap-1 mb-2">
                <AlertTriangle className="w-3 h-3 text-nofx-danger" />
                <span className="text-xs font-medium text-nofx-text-muted">
                  {ts(gridRisk.liquidationInfo, language)}
                </span>
              </div>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.liquidationPrice, language)}
                  </div>
                  <div className="font-mono text-nofx-danger">
                    {riskInfo.liquidation_price > 0
                      ? formatPrice(riskInfo.liquidation_price)
                      : '-'}
                  </div>
                </div>
                <div>
                  <div className="text-nofx-text-muted">
                    {ts(gridRisk.liquidationDistance, language)}
                  </div>
                  <div className="font-mono text-nofx-danger">
                    {riskInfo.liquidation_distance > 0
                      ? `${riskInfo.liquidation_distance.toFixed(1)}%`
                      : '-'}
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* Row 3: Box State */}
          <div className="p-2 rounded bg-nofx-bg-deeper border border-nofx-border/50">
            <div className="flex items-center gap-1 mb-2">
              <Box className="w-3 h-3 text-nofx-gold" />
              <span className="text-xs font-medium text-nofx-text-muted">
                {ts(gridRisk.boxState, language)}
              </span>
            </div>
            <div className="grid grid-cols-3 gap-2 text-xs">
              <div className="flex justify-between">
                <span className="text-nofx-text-muted">
                  {ts(gridRisk.shortBox, language)}
                </span>
                <span className="font-mono text-nofx-text">
                  {formatPrice(riskInfo.short_box_lower)} -{' '}
                  {formatPrice(riskInfo.short_box_upper)}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-nofx-text-muted">
                  {ts(gridRisk.midBox, language)}
                </span>
                <span className="font-mono text-nofx-text">
                  {formatPrice(riskInfo.mid_box_lower)} -{' '}
                  {formatPrice(riskInfo.mid_box_upper)}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-nofx-text-muted">
                  {ts(gridRisk.longBox, language)}
                </span>
                <span className="font-mono text-nofx-text">
                  {formatPrice(riskInfo.long_box_lower)} -{' '}
                  {formatPrice(riskInfo.long_box_upper)}
                </span>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
