import { useMemo } from 'react'
import type { Position } from '../../types'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

/**
 * RiskRadar renders derived risk telemetry for the live trading book — long /
 * short exposure split, leverage usage vs config cap, margin utilization,
 * single-name concentration, max drawdown and position count — as a dense stack
 * of self-explanatory gauge rows. Each row reads as: bilingual label · value +
 * unit · a thin gauge bar · and a one-glance verdict tag. Cream-themed
 * Bloomberg/terminal cockpit.
 *
 * Every value is DERIVED from real props. No synthetic or random data; missing
 * inputs collapse to 0 and divides are guarded.
 */

const C_AMBER = '#c8860b' // escalation tint between green and red

function fmtUsd(n: number): string {
  const a = Math.abs(n)
  const sign = n < 0 ? '-' : ''
  if (a >= 1e6) return `${sign}$${(a / 1e6).toFixed(2)}M`
  if (a >= 1e3) return `${sign}$${(a / 1e3).toFixed(1)}K`
  if (a >= 100) return `${sign}$${a.toFixed(0)}`
  // small PnLs matter here: -$0.22 must not render as -$0
  return `${sign}$${a.toFixed(2)}`
}

function pct(n: number): string {
  return `${n.toFixed(1)}%`
}

function isLong(side: string): boolean {
  return (side || '').toLowerCase() === 'long'
}

// margin utilization escalates green → amber → red as the book fills up
function utilColor(p: number): string {
  if (p > 80) return 'var(--tm-dn)'
  if (p >= 50) return C_AMBER
  return 'var(--tm-up)'
}

interface RiskRadarProps {
  positions?: Position[]
  account?: { total_equity?: number; unrealized_profit?: number; margin_used_pct?: number } | null
  config?: { btc_eth_leverage?: number; altcoin_leverage?: number; max_positions?: number } | null
  /** max_drawdown_pct is a percent (18.5 = -18.5%), not a fraction. */
  fullStats?: { max_drawdown_pct?: number; profit_factor?: number; sharpe_ratio?: number; win_rate?: number } | null
}

export function RiskRadar({ positions, account, config, fullStats }: RiskRadarProps) {
  const { language } = useLanguage()
  const tt = (key: string, params?: Record<string, string | number>) =>
    t(`terminalDashboard.${key}`, language, params)
  const pos = positions ?? []

  const m = useMemo(() => {
    const equity = account?.total_equity ?? 0

    let longNotional = 0
    let shortNotional = 0
    let levSum = 0
    let levCount = 0
    let maxLev = 0
    let marginSum = 0
    let topNotional = 0

    for (const p of pos) {
      const px = p.mark_price || p.entry_price || 0
      const notional = Math.abs(p.quantity || 0) * px
      if (isLong(p.side)) longNotional += notional
      else shortNotional += notional

      const lev = p.leverage || 0
      if (lev > 0) {
        levSum += lev
        levCount += 1
        if (lev > maxLev) maxLev = lev
      }
      marginSum += p.margin_used || 0
      if (notional > topNotional) topNotional = notional
    }

    const totalNotional = longNotional + shortNotional
    const netNotional = longNotional - shortNotional
    const longShare = totalNotional > 0 ? (longNotional / totalNotional) * 100 : 0
    const shortShare = totalNotional > 0 ? (shortNotional / totalNotional) * 100 : 0

    const avgLev = levCount > 0 ? levSum / levCount : 0
    const configMax = Math.max(config?.btc_eth_leverage ?? 0, config?.altcoin_leverage ?? 0)
    const levUse = configMax > 0 ? Math.min(100, (avgLev / configMax) * 100) : 0

    const marginPct =
      account?.margin_used_pct != null
        ? account.margin_used_pct
        : equity > 0
          ? (marginSum / equity) * 100
          : 0

    const concentration = totalNotional > 0 ? (topNotional / totalNotional) * 100 : 0

    const drawdown = fullStats?.max_drawdown_pct ?? 0

    const count = pos.length
    const maxPositions = config?.max_positions ?? 0
    const countUse = maxPositions > 0 ? Math.min(100, (count / maxPositions) * 100) : 0

    const upnl = account?.unrealized_profit ?? 0

    return {
      longNotional,
      shortNotional,
      netNotional,
      longShare,
      shortShare,
      totalNotional,
      avgLev,
      maxLev,
      configMax,
      levUse,
      marginPct,
      concentration,
      drawdown,
      count,
      maxPositions,
      countUse,
      upnl,
    }
  }, [pos, account, config, fullStats])

  const hasData = pos.length > 0 || account != null
  if (!hasData) {
    return <div className="tm-sc" style={{ padding: '16px 0' }}>{tt('noRiskData')}</div>
  }

  // ── one-glance verdicts ──────────────────────────────────────────────
  // Net exposure bias: Long-lean / Short-lean / Balanced by the long-share spread around 50%.
  const biasSkew = m.longShare - m.shortShare
  const exposureTag: Verdict =
    m.totalNotional === 0
      ? { text: tt('flat'), tone: 'muted' }
      : biasSkew > 15
        ? { text: tt('longLean'), tone: 'up' }
        : biasSkew < -15
          ? { text: tt('shortLean'), tone: 'dn' }
          : { text: tt('balanced'), tone: 'ink' }

  // Leverage: Safe / High / Risky by avg vs cap.
  const levTag: Verdict =
    m.configMax === 0 || m.avgLev === 0
      ? { text: '—', tone: 'muted' }
      : m.levUse > 80
        ? { text: tt('risky'), tone: 'dn' }
        : m.levUse >= 50
          ? { text: tt('high'), tone: 'amber' }
          : { text: tt('safe'), tone: 'up' }

  // Margin used: Ample / Tight / Risky.
  const marginTag: Verdict =
    m.marginPct > 80
      ? { text: tt('risky'), tone: 'dn' }
      : m.marginPct >= 50
        ? { text: tt('tight'), tone: 'amber' }
        : { text: tt('ample'), tone: 'up' }

  // Concentration: Spread / Concentrated.
  const concTag: Verdict =
    m.totalNotional === 0
      ? { text: '—', tone: 'muted' }
      : m.concentration >= 35
        ? { text: tt('concentrated'), tone: 'amber' }
        : { text: tt('spreadOut'), tone: 'up' }

  // Drawdown: Calm / Caution / Deep by depth.
  const ddTag: Verdict =
    m.drawdown <= 0
      ? { text: tt('calm'), tone: 'up' }
      : m.drawdown >= 20
        ? { text: tt('deep'), tone: 'dn' }
        : { text: tt('caution'), tone: 'amber' }

  // Positions: Room / Full.
  const countTag: Verdict =
    m.maxPositions === 0
      ? { text: `${m.count}`, tone: 'muted' }
      : m.count >= m.maxPositions
        ? { text: tt('full'), tone: 'amber' }
        : { text: tt('room'), tone: 'up' }

  return (
    <div style={{ fontFamily: 'var(--tm-mono)' }}>
      {/* header */}
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 1 }}>
        <span className="tm-px" style={{ fontSize: 11 }}>{tt('riskRadar')}</span>
        <span
          className="tm-sc"
          style={{ marginLeft: 'auto', color: m.totalNotional > 0 ? 'var(--tm-up)' : 'var(--tm-muted)' }}
        >
          {m.totalNotional > 0 ? `● ${tt('live')}` : `○ ${tt('flat')}`}
        </span>
      </div>
      <div className="tm-sc" style={{ fontSize: 9, marginBottom: 8 }}>
        {tt('riskRadarDesc')}
      </div>

      {/* Net exposure — diverging long/short split, the visual centerpiece */}
      <div style={{ marginBottom: 9, paddingBottom: 9, borderBottom: '1px solid var(--tm-hair)' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', marginBottom: 4 }}>
          <Label text={tt('netExposure')} />
          <Tag verdict={exposureTag} />
          <span className="tm-mono" style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--tm-ink)' }}>
            {tt('long')} {pct(m.longShare)}
            <span style={{ color: 'var(--tm-muted)' }}> / </span>
            {tt('short')} {pct(m.shortShare)}
          </span>
        </div>
        <div style={{ display: 'flex', height: 7, background: 'var(--tm-hair)', overflow: 'hidden' }}>
          <div style={{ width: `${m.longShare}%`, background: 'var(--tm-up)' }} />
          <div style={{ width: `${m.shortShare}%`, background: 'var(--tm-dn)' }} />
        </div>
        <div className="tm-mono" style={{ display: 'flex', justifyContent: 'space-between', fontSize: 9, marginTop: 3 }}>
          <span style={{ color: 'var(--tm-up)' }}>{tt('long')} {fmtUsd(m.longNotional)}</span>
          <span style={{ color: 'var(--tm-ink-2)' }}>
            {tt('net')} <b style={{ color: m.netNotional >= 0 ? 'var(--tm-up)' : 'var(--tm-dn)' }}>{fmtUsd(m.netNotional)}</b>
          </span>
          <span style={{ color: 'var(--tm-dn)' }}>{tt('short')} {fmtUsd(m.shortNotional)}</span>
        </div>
      </div>

      {/* gauge rows */}
      <GaugeRow
        label={tt('leverage')}
        value={`${m.avgLev.toFixed(1)}× ${tt('average')}`}
        sub={tt('peakCap', { peak: m.maxLev > 0 ? `${m.maxLev.toFixed(0)}×` : '—', cap: m.configMax > 0 ? `${m.configMax}×` : '—' })}
        fill={m.levUse}
        color={levTag.tone === 'dn' ? 'var(--tm-dn)' : levTag.tone === 'amber' ? C_AMBER : 'var(--tm-up)'}
        verdict={levTag}
      />
      <GaugeRow
        label={tt('marginUsed')}
        value={pct(m.marginPct)}
        sub={tt('ofEquity')}
        fill={Math.min(100, Math.max(0, m.marginPct))}
        color={utilColor(m.marginPct)}
        verdict={marginTag}
      />
      <GaugeRow
        label={tt('concentration')}
        value={pct(m.concentration)}
        sub={tt('topPositionShare')}
        fill={m.concentration}
        color={concTag.tone === 'amber' ? C_AMBER : 'var(--tm-up)'}
        verdict={concTag}
      />
      <GaugeRow
        label={tt('drawdown')}
        value={`-${pct(m.drawdown)}`}
        sub={tt('peakDrawdown')}
        fill={Math.min(100, m.drawdown)}
        color="var(--tm-red)"
        verdict={ddTag}
        valueColor="var(--tm-dn)"
      />
      <GaugeRow
        label={tt('positions')}
        value={m.maxPositions > 0 ? `${m.count} / ${m.maxPositions}` : `${m.count}`}
        sub={tt('heldCap')}
        fill={m.maxPositions > 0 ? m.countUse : 0}
        color={countTag.tone === 'amber' ? C_AMBER : 'var(--tm-up)'}
        verdict={countTag}
      />

      {/* unrealized PnL footer */}
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          marginTop: 8,
          paddingTop: 7,
          borderTop: '1px solid var(--tm-hair)',
        }}
      >
        <Label text={tt('unrealizedPnl')} />
        <span
          className="tm-mono"
          style={{ marginLeft: 'auto', fontSize: 13, fontWeight: 700, color: m.upnl >= 0 ? 'var(--tm-up)' : 'var(--tm-dn)' }}
        >
          {m.upnl >= 0 ? '+' : ''}{fmtUsd(m.upnl)}
        </span>
      </div>
    </div>
  )
}

// ── verdict tag ────────────────────────────────────────────────────────
type Tone = 'up' | 'dn' | 'amber' | 'ink' | 'muted'

interface Verdict {
  text: string
  tone: Tone
}

function toneColor(tone: Tone): string {
  switch (tone) {
    case 'up':
      return 'var(--tm-up)'
    case 'dn':
      return 'var(--tm-dn)'
    case 'amber':
      return C_AMBER
    case 'ink':
      return 'var(--tm-ink)'
    default:
      return 'var(--tm-muted)'
  }
}

function Tag({ verdict }: { verdict: Verdict }) {
  const c = toneColor(verdict.tone)
  return (
    <span
      style={{
        marginLeft: 6,
        padding: '0 4px',
        fontSize: 9,
        lineHeight: '13px',
        letterSpacing: '0.08em',
        color: c,
        border: `1px solid ${c}`,
        borderRadius: 2,
      }}
    >
      {verdict.text}
    </span>
  )
}

// ── bilingual label block ──────────────────────────────────────────────
function Label({ text }: { text: string }) {
  return (
    <span style={{ display: 'inline-flex', flexDirection: 'column', lineHeight: 1.2 }}>
      <span style={{ fontSize: 11, color: 'var(--tm-ink)', fontWeight: 600 }}>{text}</span>
    </span>
  )
}

interface GaugeRowProps {
  label: string
  value: string
  sub?: string
  fill: number
  color: string
  verdict: Verdict
  valueColor?: string
}

function GaugeRow({ label, value, sub, fill, color, verdict, valueColor }: GaugeRowProps) {
  const w = Math.min(100, Math.max(0, fill))
  return (
    <div style={{ marginBottom: 9 }}>
      <div style={{ display: 'flex', alignItems: 'center', marginBottom: 4 }}>
        <Label text={label} />
        <Tag verdict={verdict} />
        <span
          className="tm-mono"
          style={{ marginLeft: 'auto', fontSize: 12, fontWeight: 600, color: valueColor ?? 'var(--tm-ink)' }}
        >
          {value}
        </span>
      </div>
      <div style={{ height: 5, background: 'var(--tm-hair)', overflow: 'hidden' }}>
        <div style={{ width: `${w}%`, height: '100%', background: color, transition: 'width 0.2s ease-out' }} />
      </div>
      {sub && (
        <div className="tm-sc" style={{ fontSize: 8, marginTop: 2 }}>{sub}</div>
      )}
    </div>
  )
}

export default RiskRadar
