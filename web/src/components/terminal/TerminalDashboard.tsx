import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties } from 'react'
import useSWR, { mutate } from 'swr'
import { api } from '../../lib/api'
import { confirmToast, notify } from '../../lib/notify'
import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  TraderInfo,
  TraderFullStats,
  PositionHistoryResponse,
  PaperPerformance,
  PaperClosedTrade,
  PaperFundingPayment,
  PaperFundingStatus,
  PaperPendingOrder,
  PaperOrderEvent,
} from '../../types'
import { OrchestrationTopology } from './OrchestrationTopology'
import { OrderBook } from './OrderBook'
import { KlineChart } from './KlineChart'
import { ExecutionLog } from './ExecutionLog'
import { RiskRadar } from './RiskRadar'
import { EdgeProfile } from './EdgeProfile'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

import './terminal.css'

interface TerminalDashboardProps {
  selectedTrader?: TraderInfo
  traders?: TraderInfo[]
  selectedTraderId?: string
  onTraderSelect: (traderId: string) => void
  status?: SystemStatus
  account?: AccountInfo
  positions?: Position[]
  decisions?: DecisionRecord[]
}

interface TraderSelectorProps {
  traderId?: string
  traders?: TraderInfo[]
  onTraderSelect: (traderId: string) => void
  ariaLabel: string
}

function TraderSelector({
  traderId,
  traders,
  onTraderSelect,
  ariaLabel,
}: TraderSelectorProps) {
  if (!traders || traders.length === 0) {
    return null
  }

  return (
    <select
      value={traderId ?? ''}
      onChange={(event) => onTraderSelect(event.target.value)}
      aria-label={ariaLabel}
      className="tm-mono terminal-trader-select"
    >
      {traders.map((trader) => (
        <option
          key={trader.trader_id}
          value={trader.trader_id}
          style={{ color: '#111' }}
        >
          {trader.trader_name}
        </option>
      ))}
    </select>
  )
}

function fmtUsd(n: number | undefined, signed = false): string {
  if (n == null || Number.isNaN(n)) return '—'
  const sign = signed && n > 0 ? '+' : n < 0 ? '-' : ''
  return `${sign}$${Math.abs(n).toLocaleString('en-US', { maximumFractionDigits: 2 })}`
}

export function PaperTradingBanner({
  paper,
  performance,
  exchange = 'binance',
}: {
  paper: NonNullable<SystemStatus['paper']>
  performance?: PaperPerformance
  exchange?: string
}) {
  const closedNet = performance?.total_pnl ?? paper.realized_pnl
  const allFees = performance?.total_fees ?? paper.fees
  const makerFees = performance?.maker_fees ?? paper.maker_fees ?? 0
  const takerFees = performance?.taker_fees ?? paper.taker_fees ?? allFees
  return (
    <div
      data-testid="paper-trading-banner"
      className="tm-mono"
      style={{
        margin: '8px 14px 0',
        padding: '10px 12px',
        border: '2px solid #d28b18',
        background: 'rgba(210,139,24,0.12)',
        color: 'var(--tm-ink)',
        display: 'flex',
        gap: 14,
        flexWrap: 'wrap',
        alignItems: 'center',
      }}
    >
      <strong style={{ color: '#b56f00' }}>PAPER TRADING · 模拟交易</strong>
      <span>
        使用实时 {exchange.toUpperCase()} 行情与真实 AI
        决策，但不会向交易所发送真实订单。
      </span>
      <span>
        钱包 {fmtUsd(paper.balance)} · 可用 {fmtUsd(paper.available_balance)} ·
        占用保证金 {fmtUsd(paper.used_margin)} · 权益 {fmtUsd(paper.equity)} ·
        平仓净收益 {fmtUsd(closedNet, true)} · 未实现{' '}
        {fmtUsd(paper.unrealized_pnl, true)} · Paper 总费用（含当前持仓入场费）{' '}
        {fmtUsd(allFees)}（Maker {fmtUsd(makerFees)} / Taker {fmtUsd(takerFees)}
        ）· 挂单 {paper.pending_orders ?? 0} · 已平仓{' '}
        {performance?.total_trades ?? paper.closed_trades} · 胜率{' '}
        {(performance?.win_rate ?? paper.win_rate).toFixed(1)}% · 最大回撤{' '}
        {(performance?.max_drawdown_pct ?? paper.max_drawdown).toFixed(2)}%
        {' · '}Funding 净额 {fmtUsd(paper.funding_net ?? 0, true)}
      </span>
    </div>
  )
}

export function PaperMakerPanel({
  pendingOrders,
  events,
  makerFees,
  takerFees,
}: {
  pendingOrders?: PaperPendingOrder[]
  events?: PaperOrderEvent[]
  makerFees: number
  takerFees: number
}) {
  const recent =
    events && events.length > 0 ? events[events.length - 1] : undefined
  const statusLabel = (status: string) => {
    if (status === 'PARTIALLY_FILLED') return '部分成交'
    if (status === 'FILLED') return '已成交'
    if (status === 'CANCELED') return '已撤单'
    return '挂单中'
  }
  return (
    <div
      data-testid="paper-maker-panel"
      className="tm-mono"
      style={{
        margin: '6px 14px 0',
        padding: '7px 10px',
        border: '1px solid var(--tm-hair)',
        display: 'flex',
        gap: 14,
        flexWrap: 'wrap',
        fontSize: 10,
        color: 'var(--tm-ink-2)',
      }}
    >
      <strong style={{ color: 'var(--tm-ink)' }}>PAPER MAKER FIRST</strong>
      <span>
        手续费 Maker {fmtUsd(makerFees)} · Taker {fmtUsd(takerFees)}
      </span>
      <span>当前挂单 {pendingOrders?.length ?? 0}</span>
      {(pendingOrders ?? []).slice(0, 3).map((order) => (
        <span key={order.order_id}>
          <b>{order.symbol}</b> {order.action} · {statusLabel(order.status)}{' '}
          {order.filled_quantity.toLocaleString()}/
          {order.quantity.toLocaleString()}
          {' @ '}
          {order.limit_price.toLocaleString()}
        </span>
      ))}
      {recent && (
        <span>
          最近事件 #{recent.order_id} {recent.symbol} ·{' '}
          {statusLabel(recent.status)}
          {recent.reason ? ` (${recent.reason})` : ''}
        </span>
      )}
    </div>
  )
}

export function PaperFundingPanel({
  statuses,
  payments,
}: {
  statuses?: PaperFundingStatus[]
  payments?: PaperFundingPayment[]
}) {
  const recent =
    payments && payments.length > 0 ? payments[payments.length - 1] : undefined
  if ((!statuses || statuses.length === 0) && !recent) return null
  return (
    <div
      data-testid="paper-funding-panel"
      className="tm-mono"
      style={{
        margin: '6px 14px 0',
        padding: '7px 10px',
        border: '1px solid var(--tm-hair)',
        display: 'flex',
        gap: 14,
        flexWrap: 'wrap',
        fontSize: 10,
        color: 'var(--tm-ink-2)',
      }}
    >
      <strong style={{ color: 'var(--tm-ink)' }}>PAPER FUNDING</strong>
      {(statuses ?? []).map((status) => (
        <span key={status.symbol}>
          <b>{status.symbol}</b> 当前 {(status.funding_rate * 100).toFixed(4)}%
          · 下次结算{' '}
          {new Date(status.next_funding_time).toLocaleString('zh-CN', {
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            hour12: false,
          })}
        </span>
      ))}
      {recent && (
        <span>
          最近 Funding {fmtUsd(recent.wallet_delta, true)} · {recent.symbol}{' '}
          {recent.side}
        </span>
      )}
    </div>
  )
}

function paperTradeToHistory(trade: PaperClosedTrade, traderId: string) {
  return {
    id: trade.exit_order_id,
    trader_id: traderId,
    exchange_id: 'paper',
    exchange_type: 'paper',
    symbol: trade.symbol,
    side: trade.side,
    quantity: trade.quantity,
    entry_quantity: trade.quantity,
    entry_price: trade.entry_price,
    entry_order_id: String(trade.entry_order_id),
    entry_time: new Date(trade.entry_time).getTime(),
    exit_price: trade.exit_price,
    exit_order_id: String(trade.exit_order_id),
    exit_time: new Date(trade.exit_time).getTime(),
    realized_pnl: trade.realized_pnl,
    fee: trade.fee,
    leverage: trade.leverage,
    status: 'CLOSED',
    close_reason: trade.close_reason,
    created_at: trade.entry_time,
    updated_at: trade.exit_time,
  }
}

function aggregatePaperHistory(
  performance: PaperPerformance,
  traderId: string
): PositionHistoryResponse {
  const positions = performance.closed_trades.map((trade) =>
    paperTradeToHistory(trade, traderId)
  )
  const stats: TraderFullStats = {
    total_trades: performance.total_trades,
    win_trades: performance.win_trades,
    loss_trades: performance.loss_trades,
    win_rate: performance.win_rate,
    profit_factor: performance.profit_factor,
    sharpe_ratio: performance.sharpe_ratio,
    total_pnl: performance.total_pnl,
    total_fee: performance.total_fees,
    avg_win: performance.avg_win,
    avg_loss: performance.avg_loss,
    max_drawdown_pct: performance.max_drawdown_pct,
  }
  const bySymbol = new Map<string, PaperClosedTrade[]>()
  const bySide = new Map<string, PaperClosedTrade[]>()
  for (const trade of performance.closed_trades) {
    bySymbol.set(trade.symbol, [...(bySymbol.get(trade.symbol) ?? []), trade])
    bySide.set(trade.side, [...(bySide.get(trade.side) ?? []), trade])
  }
  return {
    positions,
    stats,
    symbol_stats: [...bySymbol.entries()].map(([symbol, trades]) => {
      const wins = trades.filter((trade) => trade.realized_pnl > 0).length
      const pnl = trades.reduce((sum, trade) => sum + trade.realized_pnl, 0)
      const holdMins = trades.reduce(
        (sum, trade) =>
          sum +
          Math.max(
            0,
            (new Date(trade.exit_time).getTime() -
              new Date(trade.entry_time).getTime()) /
              60000
          ),
        0
      )
      return {
        symbol,
        total_trades: trades.length,
        win_trades: wins,
        win_rate: trades.length ? (wins / trades.length) * 100 : 0,
        total_pnl: pnl,
        avg_pnl: trades.length ? pnl / trades.length : 0,
        avg_hold_mins: trades.length ? holdMins / trades.length : 0,
      }
    }),
    direction_stats: [...bySide.entries()].map(([side, trades]) => {
      const wins = trades.filter((trade) => trade.realized_pnl > 0).length
      const pnl = trades.reduce((sum, trade) => sum + trade.realized_pnl, 0)
      return {
        side,
        trade_count: trades.length,
        win_rate: trades.length ? (wins / trades.length) * 100 : 0,
        total_pnl: pnl,
        avg_pnl: trades.length ? pnl / trades.length : 0,
      }
    }),
  }
}

export function resolveDashboardPerformance(
  status: SystemStatus | undefined,
  liveStats: TraderFullStats,
  liveHistory: PositionHistoryResponse
): { fullStats: TraderFullStats; history: PositionHistoryResponse }
export function resolveDashboardPerformance(
  status: SystemStatus | undefined,
  liveStats: TraderFullStats | undefined,
  liveHistory: PositionHistoryResponse | undefined
): {
  fullStats: TraderFullStats | undefined
  history: PositionHistoryResponse | undefined
}
export function resolveDashboardPerformance(
  status: SystemStatus | undefined,
  liveStats: TraderFullStats | undefined,
  liveHistory: PositionHistoryResponse | undefined
) {
  if (status?.execution_mode !== 'paper' || !status.paper_performance) {
    return { fullStats: liveStats, history: liveHistory }
  }
  const history = aggregatePaperHistory(
    status.paper_performance,
    status.trader_id ?? ''
  )
  return { fullStats: history.stats as TraderFullStats, history }
}
function fmtPct(n: number | undefined): string {
  if (n == null || Number.isNaN(n)) return '—'
  return `${n >= 0 ? '+' : ''}${n.toFixed(2)}%`
}
/** Price with magnitude-aware precision: 64,416 · 184.2 · 2.3775 · 0.0067 */
function fmtPx(n: number | undefined): string {
  if (n == null || Number.isNaN(n) || n === 0) return '—'
  const dp = n >= 1000 ? 0 : n >= 100 ? 1 : n >= 1 ? 2 : 4
  return n.toLocaleString('en-US', {
    minimumFractionDigits: dp,
    maximumFractionDigits: dp,
  })
}
function baseLabel(raw?: string): string {
  if (!raw) return ''
  return raw
    .toUpperCase()
    .replace(/^XYZ:/, '')
    .replace(/[-_]/g, '')
    .replace(/(USDT|USDC|USD)$/, '')
}
function marketSymbol(raw?: string): string {
  const normalized = (raw || '')
    .toUpperCase()
    .trim()
    .replace(/[-_]/g, '')
    .replace(/(SWAP|PERP)$/, '')
  if (normalized.endsWith('USDT')) return normalized
  return normalized ? `${normalized}USDT` : ''
}
function parseScanMinutes(scan?: string): number {
  if (!scan) return 15
  const m = scan.match(/(\d+)\s*m/i)
  if (m) return parseInt(m[1], 10)
  const h = scan.match(/(\d+)\s*h/i)
  if (h) return parseInt(h[1], 10) * 60
  const n = parseInt(scan, 10)
  return Number.isFinite(n) && n > 0 ? n : 15
}
function fmtTime(raw?: string | number): string {
  if (raw == null || raw === '') return ''
  let n = typeof raw === 'number' ? raw : Number(raw)
  if (Number.isFinite(n)) {
    if (n < 1e12) n *= 1000
    return new Date(n).toLocaleString('en-GB', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
  }
  const d = new Date(raw as string)
  return Number.isNaN(d.getTime())
    ? ''
    : d.toLocaleString('en-GB', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      })
}

/** Hold duration from entry/exit epoch-ms as a compact 45m / 2h10 / 1d3h. */
function fmtHold(entry?: number, exit?: number): string {
  if (!entry || !exit || exit <= entry) return '—'
  const mins = Math.round((exit - entry) / 60000)
  if (mins < 60) return `${mins}m`
  const h = Math.floor(mins / 60)
  const m = mins % 60
  if (h < 24) return m ? `${h}h${m}` : `${h}h`
  const d = Math.floor(h / 24)
  return `${d}d${h % 24}h`
}

function useTick(ms = 1000) {
  const [, set] = useState(0)
  useEffect(() => {
    const id = setInterval(() => set((n) => n + 1), ms)
    return () => clearInterval(id)
  }, [ms])
}

export function TerminalDashboard({
  selectedTrader,
  traders,
  selectedTraderId,
  onTraderSelect,
  status: propStatus,
  account: propAccount,
  positions: propPositions,
  decisions: propDecisions,
}: TerminalDashboardProps) {
  const { language } = useLanguage()
  const tt = (key: string, params?: Record<string, string | number>) =>
    t(`terminalDashboard.${key}`, language, params)
  const traderId = selectedTrader?.trader_id || selectedTraderId
  const [closing, setClosing] = useState<string | null>(null)
  useTick(1000)
  const clock = new Date().toLocaleTimeString('en-GB', { hour12: false })

  async function closePositionRow(symbol: string, side: 'LONG' | 'SHORT') {
    if (!traderId || closing) return
    const ok = await confirmToast(`Market-close ${symbol} ${side}?`, {
      title: 'Close position',
      okText: 'Close',
      cancelText: 'Cancel',
    })
    if (!ok) return
    setClosing(symbol)
    try {
      await api.closePosition(traderId, symbol, side)
      notify.success(`${symbol} ${side} closed`)
      mutate(`positions-${traderId}`)
      mutate(`account-${traderId}`)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : 'Close failed')
    } finally {
      setClosing(null)
    }
  }

  async function closeAllPositions(open: Position[]) {
    if (!traderId || closing || open.length === 0) return
    const ok = await confirmToast(
      `Market-close ALL ${open.length} open positions?`,
      { title: 'Flatten book', okText: 'Close all', cancelText: 'Cancel' }
    )
    if (!ok) return
    setClosing('__all__')
    let failed = 0
    // Sequential: parallel closes race on exchange nonces / rate limits.
    for (const p of open) {
      const side = /long|buy/i.test(p.side) ? 'LONG' : 'SHORT'
      try {
        await api.closePosition(traderId, p.symbol, side)
      } catch {
        failed++
      }
    }
    mutate(`positions-${traderId}`)
    mutate(`account-${traderId}`)
    if (failed === 0) notify.success('All positions closed')
    else notify.error(`${failed}/${open.length} closes failed`)
    setClosing(null)
  }

  const { data: realFullStats } = useSWR(
    traderId ? ['full-stats', traderId] : null,
    () => api.getFullStats(traderId!, true),
    { refreshInterval: 30000, shouldRetryOnError: false }
  )
  const { data: realHistory } = useSWR(
    traderId ? ['pos-history', traderId] : null,
    () => api.getPositionHistory(traderId!, 50, true),
    { refreshInterval: 60000, shouldRetryOnError: false }
  )
  const { data: realConfig } = useSWR(
    traderId ? ['trader-config', traderId] : null,
    () => api.getTraderConfig(traderId!, true),
    { refreshInterval: 120000, shouldRetryOnError: false }
  )
  const status = propStatus
  const account = propAccount
  const positions = propPositions
  const decisions = propDecisions
  const dashboardPerformance = useMemo(
    () => resolveDashboardPerformance(status, realFullStats, realHistory),
    [status, realFullStats, realHistory]
  )
  const fullStats = dashboardPerformance.fullStats
  const history = dashboardPerformance.history
  const config = realConfig
  const marketExchange = (
    selectedTrader?.exchange_type ||
    config?.exchange_type ||
    'binance'
  ).toLowerCase()

  const latest = decisions && decisions.length > 0 ? decisions[0] : undefined
  const candidateCoins = latest?.candidate_coins ?? []
  const activeSym = useMemo(() => {
    const symbols = [
      ...(positions ?? []).map((p) => p.symbol),
      ...candidateCoins,
    ]
    return symbols.map(marketSymbol).find(Boolean) || 'BTCUSDT'
  }, [positions, candidateCoins])

  const pnl = account?.total_pnl ?? 0
  const pnlPct = account?.total_pnl_pct ?? 0
  const up = pnl >= 0
  const running = status?.is_running

  // Direction comes only from the AI's recorded decision, never external boards.
  const dirFor = useMemo(() => {
    const dec = new Map<string, 'long' | 'short'>()
    ;(latest?.decisions ?? []).forEach((d) => {
      const b = baseLabel(d.symbol)
      if (d.action === 'open_long' || d.action === 'close_short')
        dec.set(b, 'long')
      else if (d.action === 'open_short' || d.action === 'close_long')
        dec.set(b, 'short')
    })
    return (sym: string): 'long' | 'short' => {
      const b = baseLabel(sym)
      return dec.get(b) ?? 'long'
    }
  }, [latest])

  const scanMin =
    config?.scan_interval_minutes || parseScanMinutes(status?.scan_interval)
  const nextCycleMs = useMemo(() => {
    if (!latest?.timestamp) return null
    return new Date(latest.timestamp).getTime() + scanMin * 60_000
  }, [latest?.timestamp, scanMin])
  let countdown = '—'
  if (nextCycleMs) {
    const ms = nextCycleMs - Date.now()
    if (ms <= 0) countdown = 'due now'
    else {
      const s = Math.floor(ms / 1000)
      countdown = `${Math.floor(s / 60)}m ${s % 60}s`
    }
  }

  const recentTrades = (history?.positions ?? []).slice(0, 8)
  const symbolStats = useMemo(
    () =>
      (history?.symbol_stats ?? [])
        .slice()
        .sort((a, b) => b.total_trades - a.total_trades)
        .slice(0, 6),
    [history]
  )
  const maxSymTrades = symbolStats.reduce(
    (m, s) => Math.max(m, s.total_trades),
    1
  )

  const sc: CSSProperties = { padding: '10px 14px' }
  const cellBorder = '1px solid var(--tm-hair)'

  // Portal the trader selector + run status into the global nav so the app has
  // a single top bar (no separate dashboard titlebar).
  const [navSlot, setNavSlot] = useState<HTMLElement | null>(null)
  useEffect(() => {
    setNavSlot(document.getElementById('dash-header-slot'))
  }, [])

  const traderSelectorLabel = t('switchTrader', language)

  return (
    <div className="nofx-terminal" style={{ minHeight: '100vh', padding: 0 }}>
      {/* centered, capped content column — no border (keeps it from feeling
          embedded) but bounded so the aspect-ratio SVGs don't balloon on wide screens */}
      {navSlot &&
        createPortal(
          <span
            className="nofx-terminal"
            style={{
              background: 'transparent',
              display: 'flex',
              alignItems: 'center',
              gap: 12,
              marginLeft: 16,
              paddingLeft: 16,
              borderLeft: '1px solid rgba(26,24,19,0.15)',
              fontSize: 11,
            }}
          >
            <span className="tm-sc" style={{ color: 'var(--tm-muted)' }}>
              {tt('orchestration')}
            </span>
            <TraderSelector
              traderId={traderId}
              traders={traders}
              onTraderSelect={onTraderSelect}
              ariaLabel={traderSelectorLabel}
            />
            <span
              style={{ color: running ? 'var(--tm-up)' : 'var(--tm-muted)' }}
            >
              {running ? `● ${tt('running')}` : `○ ${tt('stopped')}`}
            </span>
            <span className="tm-sc" style={{ color: 'var(--tm-muted)' }}>
              {tt('cycle')}
            </span>
            <span className="tm-mono" style={{ color: 'var(--tm-ink)' }}>
              {status?.last_persisted_cycle ?? status?.call_count ?? '—'}
            </span>
            <span
              className="tm-px"
              style={{ fontSize: 12, color: 'var(--tm-ink)' }}
            >
              {clock}
            </span>
          </span>,
          navSlot
        )}
      {/* The desktop selector lives in HeaderBar. Keep a real control in the
          dashboard flow on small screens because the desktop header slot is
          intentionally hidden below the lg breakpoint. */}
      <div className="terminal-mobile-context tm-mono">
        <span className="terminal-mobile-context-label tm-sc">
          {traderSelectorLabel}
        </span>
        <TraderSelector
          traderId={traderId}
          traders={traders}
          onTraderSelect={onTraderSelect}
          ariaLabel={traderSelectorLabel}
        />
        <span
          className="terminal-context-status"
          style={{ color: running ? 'var(--tm-up)' : 'var(--tm-muted)' }}
        >
          {running ? `● ${tt('running')}` : `○ ${tt('stopped')}`}
        </span>
        <span className="terminal-context-cycle tm-sc">
          {tt('cycle')}{' '}
          {status?.last_persisted_cycle ?? status?.call_count ?? '—'}
        </span>
        <span className="terminal-context-clock tm-px">{clock}</span>
      </div>
      <div
        className="tm-box terminal-dashboard-shell"
        style={{ maxWidth: 1280, margin: '0 auto', border: 'none' }}
      >
        {status?.execution_mode === 'paper' && status.paper && (
          <>
            <PaperTradingBanner
              paper={status.paper}
              performance={status.paper_performance}
              exchange={marketExchange}
            />
            <PaperMakerPanel
              pendingOrders={status.paper_pending_orders}
              events={status.paper_order_events}
              makerFees={
                status.paper_performance?.maker_fees ??
                status.paper.maker_fees ??
                0
              }
              takerFees={
                status.paper_performance?.taker_fees ??
                status.paper.taker_fees ??
                status.paper.fees
              }
            />
            <PaperFundingPanel
              statuses={status.paper_funding_status}
              payments={status.paper_recent_funding}
            />
          </>
        )}
        {/* Runtime health warnings should remain visible while the bot is paused. */}
        {status?.safe_mode && (
            <div
              className="tm-mono"
              style={{
                display: 'flex',
                gap: 10,
                alignItems: 'center',
                margin: '8px 14px 0',
                padding: '8px 12px',
                fontSize: 11,
                border: '1px solid var(--tm-down)',
                color: 'var(--tm-down)',
                background: 'rgba(200,60,40,0.06)',
                flexWrap: 'wrap',
              }}
            >
              <span style={{ fontWeight: 600 }}>
                {tt('safeMode')}
              </span>
              <span style={{ color: 'var(--tm-ink-2)' }}>
                {status.safe_mode_reason || ''}
              </span>
            </div>
          )}
        {/* first-run reassurance — a fresh autopilot looks idle for its first
            minute (the AI is reading the market); tell newcomers what to expect */}
        {status?.is_running &&
          (status.call_count ?? 0) <= 1 &&
          !status.safe_mode && (
            <div
              className="tm-mono"
              style={{
                display: 'flex',
                gap: 10,
                alignItems: 'center',
                margin: '8px 14px 0',
                padding: '8px 12px',
                fontSize: 11,
                border: '1px solid var(--tm-up)',
                color: 'var(--tm-ink)',
                background: 'rgba(40,140,80,0.06)',
                flexWrap: 'wrap',
              }}
            >
              <span style={{ fontWeight: 600, color: 'var(--tm-up)' }}>
                {tt('aiLive')}
              </span>
              <span style={{ color: 'var(--tm-ink-2)' }}>
                {tt('aiLiveDesc')}
              </span>
            </div>
          )}
        {/* config / identity strip — first row, flows directly under the global nav */}
        <div
          className="tm-mono"
          style={{
            display: 'flex',
            gap: 16,
            padding: '6px 14px',
            fontSize: 11,
            color: 'var(--tm-ink-2)',
            flexWrap: 'wrap',
          }}
        >
          <span style={{ fontWeight: 500 }}>
            {selectedTrader?.trader_name ?? 'NOFX'}
          </span>
          <span>
            <span className="tm-sc">{tt('model')} </span>
            {(() => {
              const raw = config?.ai_model || status?.ai_model || ''
              if (!raw) return '—'
              return raw.length > 16
                ? raw.slice(0, 16).toUpperCase()
                : raw.toUpperCase()
            })()}
          </span>
          <span>
            <span className="tm-sc">{tt('strategy')} </span>
            {config?.strategy_name || selectedTrader?.strategy_name || '—'}
          </span>
          <span>
            <span className="tm-sc">{tt('leverage')} </span>
            {config?.max_leverage ?? '—'}×
          </span>
          <span>
            <span className="tm-sc">{tt('scan')} </span>
            {scanMin}m
          </span>
          <span>
            <span className="tm-sc">{tt('universe')} </span>
            {candidateCoins.length}
          </span>
          <span>
            <span className="tm-sc">{tt('positions')} </span>
            {positions?.length ?? 0}
          </span>
          <span style={{ marginLeft: 'auto' }}>
            <span className="tm-sc">{tt('nextCycle')} </span>
            {countdown}
          </span>
        </div>
        <div className="tm-rule" />

        {/* metric row — "Total P/L" is equity-based (includes unrealized);
            "Realized P/L" is closed-trades only and matches PF/win-rate/sharpe,
            so the two never read as contradicting each other */}
        <div
          className="terminal-metric-grid"
          style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)' }}
        >
          {[
            {
              l: tt('equity'),
              v: fmtUsd(account?.total_equity),
              c: 'var(--tm-ink)',
            },
            {
              l: tt('totalPnl'),
              v: `${fmtUsd(pnl, true)} (${fmtPct(pnlPct)})`,
              c: up ? 'var(--tm-up)' : 'var(--tm-dn)',
            },
            {
              l: tt('realizedPnl'),
              v: fullStats != null ? fmtUsd(fullStats.total_pnl, true) : '—',
              c:
                fullStats != null && fullStats.total_pnl >= 0
                  ? 'var(--tm-up)'
                  : 'var(--tm-dn)',
            },
            {
              l: tt('profitFactor'),
              v: fullStats != null ? fullStats.profit_factor.toFixed(2) : '—',
              c: 'var(--tm-ink)',
            },
            // max_drawdown_pct is already a percent (18.5 = -18.5%)
            {
              l: tt('maxDrawdown'),
              v:
                fullStats != null
                  ? `-${fullStats.max_drawdown_pct.toFixed(1)}%`
                  : '—',
              c: 'var(--tm-dn)',
            },
          ].map((m, i) => (
            <div
              key={m.l}
              style={{
                padding: '12px 14px',
                borderRight: i < 4 ? cellBorder : 'none',
              }}
            >
              <div className="tm-sc">{m.l}</div>
              <div
                className="tm-mono"
                style={{
                  fontSize: 17,
                  fontWeight: 500,
                  color: m.c,
                  marginTop: 3,
                }}
              >
                {m.v}
              </div>
            </div>
          ))}
        </div>
        <div className="tm-rule" />

        {/* trades summary */}
        {fullStats != null && (
          <>
            <div
              className="tm-mono"
              style={{
                display: 'flex',
                gap: 18,
                padding: '6px 14px',
                fontSize: 11,
                color: 'var(--tm-ink-2)',
                flexWrap: 'wrap',
              }}
            >
              <span className="tm-sc">
                {tt('trades')}{' '}
                <b style={{ color: 'var(--tm-ink)' }}>
                  {fullStats.total_trades}
                </b>
              </span>
              <span className="tm-sc tm-up">
                {tt('win')} {fullStats.win_trades} (
                {fullStats.win_rate.toFixed(1)}%)
              </span>
              <span className="tm-sc tm-dn">
                {tt('loss')} {fullStats.loss_trades}
              </span>
              {/* fee-drag chain: gross realized − fees = net realized */}
              <span className="tm-sc">
                {tt('gross')}{' '}
                <b style={{ color: 'var(--tm-ink)' }}>
                  {fmtUsd(
                    fullStats.total_pnl +
                      (status?.execution_mode === 'paper'
                        ? (status.paper_performance?.closed_trade_fees ?? 0)
                        : fullStats.total_fee),
                    true
                  )}
                </b>
              </span>
              <span className="tm-sc">
                {status?.execution_mode === 'paper'
                  ? 'Paper 总费用（含持仓入场费）'
                  : tt('fees')}{' '}
                <b style={{ color: 'var(--tm-ink)' }}>
                  -{fmtUsd(fullStats.total_fee)}
                </b>
              </span>
              <span className="tm-sc">
                {tt('net')}{' '}
                <b
                  style={{
                    color:
                      fullStats.total_pnl >= 0
                        ? 'var(--tm-up)'
                        : 'var(--tm-dn)',
                  }}
                >
                  {fmtUsd(fullStats.total_pnl, true)}
                </b>
              </span>
              <span className="tm-sc">
                {tt('sharpePerTrade')}{' '}
                <b style={{ color: 'var(--tm-ink)' }}>
                  {fullStats.sharpe_ratio.toFixed(2)}
                </b>
              </span>
              <span className="tm-sc">
                {tt('avgWinLoss')}{' '}
                <b style={{ color: 'var(--tm-ink)' }}>
                  {fullStats.avg_win.toFixed(2)}/{fullStats.avg_loss.toFixed(2)}
                </b>
              </span>
            </div>
            <div className="tm-rule" />
          </>
        )}

        {/* Exchange-specific public market data. */}
        <div
          className="terminal-market-grid"
          style={{
            display: 'grid',
            gridTemplateColumns: 'minmax(0,0.9fr) minmax(0,1.4fr)',
          }}
        >
          <div
            className="terminal-market-panel"
            style={{
              ...sc,
              borderRight: cellBorder,
              overflow: 'hidden',
            }}
          >
            <OrderBook
              symbol={activeSym}
              exchange={marketExchange}
              markPrice={
                positions?.find((p) => marketSymbol(p.symbol) === activeSym)
                  ?.entry_price
              }
            />
          </div>
          <div
            className="terminal-market-panel"
            style={{
              ...sc,
              display: 'flex',
              flexDirection: 'column',
              minHeight: 0,
            }}
          >
            <div style={{ flex: 1, minHeight: 0 }}>
              <KlineChart symbol={activeSym} exchange={marketExchange} fill />
            </div>
          </div>
        </div>
        <div className="tm-rule" />

        {/* orchestration topology — second row, full width (the agent workflow) */}
        <div style={sc}>
          <div
            style={{
              display: 'flex',
              alignItems: 'baseline',
              gap: 10,
              marginBottom: 4,
            }}
          >
            <span className="tm-px" style={{ fontSize: 12 }}>
              {tt('orchestrationTopology')}
            </span>
            <span className="tm-sc">{tt('orchestrationTopologyDesc')}</span>
          </div>
          <OrchestrationTopology
            layers={[
              {
                key: 'universe',
                title: tt('universe').toUpperCase(),
                zh: tt('universe'),
                items: candidateCoins
                  .filter((symbol) => marketSymbol(symbol))
                  .map((symbol) => ({ symbol, dir: dirFor(symbol) })),
              },
              {
                key: 'decision',
                title: tt('decision').toUpperCase(),
                zh: tt('decision'),
                items: (latest?.decisions ?? [])
                  .filter((d) => marketSymbol(d.symbol))
                  .map((d) => ({ symbol: d.symbol, dir: dirFor(d.symbol) })),
              },
              {
                // executed & live: every open position is an executed order, so
                // EXECUTE mirrors the live book (this cycle's fills plus anything
                // still open from prior cycles) and flows straight into HOLD
                key: 'exec',
                title: tt('execute').toUpperCase(),
                zh: tt('execute'),
                items: (positions ?? []).map((p) => ({
                  symbol: p.symbol,
                  dir: (p.side || '').toLowerCase().includes('short')
                    ? ('short' as const)
                    : ('long' as const),
                })),
              },
              {
                key: 'hold',
                title: tt('hold').toUpperCase(),
                zh: tt('hold'),
                items: (positions ?? []).map((p) => ({
                  symbol: p.symbol,
                  dir: (p.side || '').toLowerCase().includes('short')
                    ? ('short' as const)
                    : ('long' as const),
                })),
              },
            ]}
          />
        </div>
        <div className="tm-rule" />

        {/* ── row 3: execution log · risk radar · recent trades ── */}
        <div
          className="terminal-data-grid"
          style={{
            display: 'grid',
            gridTemplateColumns: 'minmax(0,1.1fr) minmax(0,1fr) minmax(0,1fr)',
          }}
        >
          <div style={{ ...sc, borderRight: cellBorder }}>
            <ExecutionLog decisions={decisions} height={432} />
          </div>
          <div style={{ ...sc, borderRight: cellBorder }}>
            <RiskRadar
              positions={positions}
              account={account}
              config={config}
              fullStats={fullStats}
            />
          </div>
          <div style={sc}>
            {/* live open positions (the book right now) */}
            <div
              style={{
                display: 'flex',
                alignItems: 'baseline',
                gap: 8,
                marginBottom: 6,
              }}
            >
              <span className="tm-px" style={{ fontSize: 11 }}>
                {tt('currentPositions')}
              </span>
              <span className="tm-sc">{tt('currentPositionsDesc')}</span>
              <span className="tm-sc" style={{ marginLeft: 'auto' }}>
                {positions?.length ?? 0} {tt('open')}
              </span>
              {traderId && positions && positions.length > 0 && (
                <button
                  type="button"
                  onClick={() => void closeAllPositions(positions)}
                  disabled={closing !== null}
                  className="tm-mono"
                  style={{
                    background: 'transparent',
                    border: '1px solid var(--tm-dn)',
                    color: 'var(--tm-dn)',
                    borderRadius: 3,
                    fontSize: 9,
                    padding: '1px 6px',
                    cursor: closing ? 'not-allowed' : 'pointer',
                    opacity: closing ? 0.5 : 1,
                  }}
                >
                  {closing === '__all__' ? 'closing…' : 'close all'}
                </button>
              )}
            </div>
            {positions && positions.length > 0 ? (
              <div className="terminal-table-scroll">
                <table
                  className="tm-mono"
                  style={{
                    width: '100%',
                    borderCollapse: 'collapse',
                    fontSize: 11,
                  }}
                >
                  <thead>
                    <tr className="tm-sc" style={{ fontSize: 9 }}>
                      <td style={{ padding: '0 0 3px' }}>{tt('symbol')}</td>
                      <td style={{ padding: '0 0 3px' }}>
                        {tt('sideLeverage')}
                      </td>
                      <td style={{ padding: '0 0 3px', textAlign: 'right' }}>
                        {tt('entry')}
                      </td>
                      <td style={{ padding: '0 0 3px', textAlign: 'right' }}>
                        {tt('size')}
                      </td>
                      <td style={{ padding: '0 0 3px', textAlign: 'right' }}>
                        {tt('pnl')}
                      </td>
                      <td style={{ padding: '0 0 3px', textAlign: 'right' }}>
                        {tt('returnPct')}
                      </td>
                      <td style={{ padding: '0 0 3px' }} />
                    </tr>
                  </thead>
                  <tbody>
                    {positions.map((p, i) => {
                      const long = /long|buy/i.test(p.side)
                      const win = (p.unrealized_pnl ?? 0) >= 0
                      const notional =
                        Math.abs(p.quantity ?? 0) *
                        (p.mark_price || p.entry_price || 0)
                      return (
                        <tr
                          key={`${p.symbol}-${i}`}
                          style={{ borderTop: '1px solid var(--tm-hair)' }}
                        >
                          <td style={{ padding: '5px 0', fontWeight: 500 }}>
                            {baseLabel(p.symbol)}
                          </td>
                          <td
                            style={{ padding: '5px 0' }}
                            className={long ? 'tm-up' : 'tm-dn'}
                          >
                            {long ? tt('long') : tt('short')}{' '}
                            <span style={{ color: 'var(--tm-muted)' }}>
                              {p.leverage}×
                            </span>
                          </td>
                          <td
                            style={{
                              padding: '5px 0',
                              textAlign: 'right',
                              color: 'var(--tm-ink-2)',
                            }}
                          >
                            {fmtPx(p.entry_price)}
                          </td>
                          <td
                            style={{
                              padding: '5px 0',
                              textAlign: 'right',
                              color: 'var(--tm-ink-2)',
                            }}
                          >
                            {fmtUsd(notional)}
                          </td>
                          <td
                            style={{ padding: '5px 0', textAlign: 'right' }}
                            className={win ? 'tm-up' : 'tm-dn'}
                          >
                            {fmtUsd(p.unrealized_pnl, true)}
                          </td>
                          <td
                            style={{ padding: '5px 0', textAlign: 'right' }}
                            className={win ? 'tm-up' : 'tm-dn'}
                          >
                            {(p.unrealized_pnl_pct ?? 0).toFixed(2)}%
                          </td>
                          <td
                            style={{
                              padding: '5px 0 5px 8px',
                              textAlign: 'right',
                              width: 1,
                            }}
                          >
                            {traderId && (
                              <button
                                type="button"
                                onClick={() =>
                                  void closePositionRow(
                                    p.symbol,
                                    long ? 'LONG' : 'SHORT'
                                  )
                                }
                                disabled={closing !== null}
                                title={`Close ${p.symbol}`}
                                className="tm-mono"
                                style={{
                                  background: 'transparent',
                                  border: '1px solid var(--tm-dn)',
                                  color: 'var(--tm-dn)',
                                  borderRadius: 3,
                                  fontSize: 9,
                                  padding: '1px 5px',
                                  cursor: closing ? 'not-allowed' : 'pointer',
                                  opacity: closing ? 0.5 : 1,
                                }}
                              >
                                {closing === p.symbol ? '…' : 'close'}
                              </button>
                            )}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="tm-sc" style={{ padding: '8px 0' }}>
                {tt('noOpenPositions')}
              </div>
            )}

            <div className="tm-rule" style={{ margin: '12px 0 10px' }} />

            <div
              style={{
                display: 'flex',
                alignItems: 'baseline',
                gap: 8,
                marginBottom: 6,
              }}
            >
              <span className="tm-px" style={{ fontSize: 11 }}>
                {tt('recentTrades')}
              </span>
              <span className="tm-sc">{tt('recentTradesDesc')}</span>
            </div>
            {recentTrades.length > 0 ? (
              <div className="terminal-table-scroll">
                <table
                  className="tm-mono"
                  style={{
                    width: '100%',
                    borderCollapse: 'collapse',
                    fontSize: 11,
                  }}
                >
                  <thead>
                    <tr className="tm-sc" style={{ fontSize: 9 }}>
                      <td style={{ padding: '0 0 3px' }}>{tt('symbol')}</td>
                      <td style={{ padding: '0 0 3px' }}>{tt('side')}</td>
                      <td style={{ padding: '0 0 3px', textAlign: 'right' }}>
                        {tt('holdTime')}
                      </td>
                      <td style={{ padding: '0 0 3px' }}>{tt('closed')}</td>
                      <td style={{ padding: '0 0 3px', textAlign: 'right' }}>
                        {tt('pnl')}
                      </td>
                    </tr>
                  </thead>
                  <tbody>
                    {recentTrades.map((p) => {
                      const win = p.realized_pnl >= 0
                      return (
                        <tr
                          key={p.id}
                          style={{ borderTop: '1px solid var(--tm-hair)' }}
                        >
                          <td style={{ padding: '5px 0', fontWeight: 500 }}>
                            {baseLabel(p.symbol)}
                          </td>
                          <td
                            style={{ padding: '5px 0' }}
                            className={
                              p.side === 'long' || p.side === 'LONG'
                                ? 'tm-up'
                                : 'tm-dn'
                            }
                          >
                            {p.side === 'long' || p.side === 'LONG'
                              ? tt('long')
                              : tt('short')}
                          </td>
                          <td
                            style={{
                              padding: '5px 0',
                              textAlign: 'right',
                              color: 'var(--tm-ink-2)',
                            }}
                          >
                            {fmtHold(p.entry_time, p.exit_time)}
                          </td>
                          <td
                            style={{
                              padding: '5px 0 5px 6px',
                              color: 'var(--tm-muted)',
                            }}
                          >
                            {fmtTime(p.exit_time)}
                          </td>
                          <td
                            style={{ padding: '5px 0', textAlign: 'right' }}
                            className={win ? 'tm-up' : 'tm-dn'}
                          >
                            {fmtUsd(p.realized_pnl, true)}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="tm-sc" style={{ padding: '8px 0' }}>
                {tt('noClosedTrades')}
              </div>
            )}
          </div>
        </div>
        <div className="tm-rule" />

        {/* Closed-trade analytics only; no external signal or flow board. */}
        <div
          className="terminal-analytics-grid"
          style={{
            display: 'grid',
            gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)',
          }}
        >
          <div style={{ ...sc, borderRight: cellBorder }}>
            <div
              style={{
                display: 'flex',
                alignItems: 'baseline',
                gap: 8,
                marginBottom: 8,
              }}
            >
              <span className="tm-px" style={{ fontSize: 11 }}>
                {tt('bySymbol')}
              </span>
              <span className="tm-sc">{tt('bySymbolDesc')}</span>
            </div>
            {symbolStats.length > 0 ? (
              symbolStats.map((s) => (
                <div key={s.symbol} style={{ marginBottom: 7 }}>
                  <div
                    className="tm-mono"
                    style={{ display: 'flex', fontSize: 11, marginBottom: 2 }}
                  >
                    <span style={{ fontWeight: 500 }}>
                      {baseLabel(s.symbol)}
                    </span>
                    <span className="tm-sc" style={{ marginLeft: 8 }}>
                      {tt('tradesWin', {
                        trades: s.total_trades,
                        win: s.win_rate.toFixed(0),
                      })}
                    </span>
                    <span
                      className={s.total_pnl >= 0 ? 'tm-up' : 'tm-dn'}
                      style={{ marginLeft: 'auto' }}
                    >
                      {fmtUsd(s.total_pnl, true)}
                    </span>
                  </div>
                  <div style={{ height: 4, background: 'var(--tm-hair)' }}>
                    <div
                      style={{
                        height: 4,
                        width: `${(s.total_trades / maxSymTrades) * 100}%`,
                        background:
                          s.total_pnl >= 0 ? 'var(--tm-up)' : 'var(--tm-dn)',
                      }}
                    />
                  </div>
                </div>
              ))
            ) : (
              <div className="tm-sc">{tt('noSymbolHistory')}</div>
            )}
          </div>
          <div style={sc}>
            <div
              style={{
                display: 'flex',
                alignItems: 'baseline',
                gap: 8,
                marginBottom: 8,
              }}
            >
              <span className="tm-px" style={{ fontSize: 11 }}>
                {tt('edgeProfile')}
              </span>
              <span className="tm-sc">{tt('edgeProfileDesc')}</span>
            </div>
            <EdgeProfile positions={history?.positions} />
          </div>
        </div>
      </div>
    </div>
  )
}

export default TerminalDashboard
