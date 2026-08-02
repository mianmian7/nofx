import { useEffect, useMemo, useRef, useState } from 'react'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { api } from '../../lib/api'

/**
 * OrderBook renders an L2 depth ladder for a single instrument. Binance keeps
 * its public partial-depth stream; OKX and Bitget use REST polling in the
 * first unified-market-data phase.
 */

const BINANCE_WS = 'wss://fstream.binance.com/ws'
const DEPTH = 11 // levels per side
const SMOOTH_COMMIT_MS = 250

type DisplayMode = 'smooth' | 'realtime'

interface Level {
  px: number
  sz: number
  priceDecimals: number
}
interface BookState {
  coin: string
  bids: Level[]
  asks: Level[]
}

function canonicalSymbol(raw: string): string {
  const normalized = raw
    .toUpperCase()
    .trim()
    .replace(/[-_]/g, '')
    .replace(/(SWAP|PERP)$/, '')
  if (normalized.endsWith('USDT')) return normalized
  return normalized ? `${normalized}USDT` : 'BTCUSDT'
}

function exchangeLabel(exchange: string): string {
  switch (exchange) {
    case 'okx':
      return 'OKX'
    case 'bitget':
      return 'Bitget'
    default:
      return 'Binance'
  }
}

function fmtPx(px: number, decimals?: number): string {
  if (decimals != null) {
    return px.toLocaleString('en-US', {
      minimumFractionDigits: decimals,
      maximumFractionDigits: decimals,
    })
  }
  if (px >= 1000)
    return px.toLocaleString('en-US', { maximumFractionDigits: 1 })
  if (px >= 1) return px.toLocaleString('en-US', { maximumFractionDigits: 3 })
  return px.toLocaleString('en-US', { maximumFractionDigits: 5 })
}

function rawPriceDecimals(raw: string): number {
  return Math.min(8, raw.trim().match(/\.(\d+)/)?.[1]?.length ?? 0)
}

function toLevels(levels: [string, string][]): Level[] {
  return levels
    .map(([rawPx, rawSz]) => ({
      px: Number(rawPx),
      sz: Number(rawSz),
      priceDecimals: rawPriceDecimals(rawPx),
    }))
    .filter(
      ({ px, sz }) =>
        Number.isFinite(px) && Number.isFinite(sz) && px > 0 && sz >= 0
    )
    .slice(0, DEPTH)
}
function fmtSz(sz: number): string {
  if (sz >= 1000) return `${(sz / 1000).toFixed(1)}k`
  if (sz >= 1) return sz.toFixed(2)
  return sz.toFixed(3)
}

interface OrderBookProps {
  /** raw business symbol (e.g. position symbol or candidate coin) */
  symbol: string
  exchange?: string
  /** optional entry price to mark the user's position level on the ladder */
  markPrice?: number
}

export function OrderBook({
  symbol,
  exchange = 'binance',
  markPrice,
}: OrderBookProps) {
  const { language } = useLanguage()
  const tt = (key: string) => t(`terminalDashboard.${key}`, language)
  const venue = exchange.toLowerCase().trim() || 'binance'
  const coin = useMemo(() => canonicalSymbol(symbol || ''), [symbol])
  const [book, setBook] = useState<BookState | null>(null)
  const [status, setStatus] = useState<'connecting' | 'live' | 'down'>(
    'connecting'
  )
  const [displayMode, setDisplayMode] = useState<DisplayMode>(() =>
    localStorage.getItem('orderBookDisplayMode') === 'realtime'
      ? 'realtime'
      : 'smooth'
  )
  const displayModeRef = useRef(displayMode)

  const selectDisplayMode = (mode: DisplayMode) => {
    displayModeRef.current = mode
    setDisplayMode(mode)
    localStorage.setItem('orderBookDisplayMode', mode)
  }

  useEffect(() => {
    let active = true
    setBook(null)
    setStatus('connecting')
    const refresh = () => {
      const depthRequest =
        venue === 'binance'
          ? api.getDepth(coin, 20, true)
          : api.getDepth(coin, 20, venue, true)
      depthRequest
        .then((snapshot) => {
          if (!active) return
          setBook({
            coin,
            bids: toLevels(snapshot.bids),
            asks: toLevels(snapshot.asks),
          })
          if (venue !== 'binance') setStatus('live')
        })
        .catch(() => {
          // Keep the last successful snapshot visible during transient outages.
          if (active && !book) setStatus('down')
        })
    }
    refresh()
    if (venue !== 'binance') {
      const intervalId = setInterval(refresh, 5000)
      return () => {
        active = false
        clearInterval(intervalId)
      }
    }
    return () => {
      active = false
    }
  }, [coin, venue])

  // live L2 stream
  const pending = useRef<BookState | null>(null)
  useEffect(() => {
    if (!coin || venue !== 'binance') return
    pending.current = null
    let ws: WebSocket | null = null
    let raf: number | null = null
    let retry: ReturnType<typeof setTimeout> | null = null
    let closed = false
    let lastCommitAt = 0

    const connect = () => {
      setStatus('connecting')
      ws = new WebSocket(`${BINANCE_WS}/${coin.toLowerCase()}@depth20@100ms`)
      ws.onmessage = (ev) => {
        if (closed) return
        try {
          const msg = JSON.parse(ev.data) as {
            b?: [string, string][]
            a?: [string, string][]
            bids?: [string, string][]
            asks?: [string, string][]
          }
          const bids = msg.b ?? msg.bids
          const asks = msg.a ?? msg.asks
          if (!Array.isArray(bids) || !Array.isArray(asks)) return
          pending.current = { coin, bids: toLevels(bids), asks: toLevels(asks) }
          setStatus('live')
        } catch {
          /* ignore malformed frame */
        }
      }
      ws.onclose = () => {
        if (closed) return
        setStatus('down')
        retry = setTimeout(connect, 1500)
      }
      ws.onerror = () => ws?.close()
    }

    connect()
    // Always receive Binance's 100ms stream at full cadence. Smooth mode only
    // throttles visual commits and keeps the newest pending snapshot.
    const loop = (now: number) => {
      const canCommit =
        displayModeRef.current === 'realtime' ||
        now - lastCommitAt >= SMOOTH_COMMIT_MS
      if (pending.current && canCommit) {
        setBook(pending.current)
        pending.current = null
        lastCommitAt = now
      }
      raf = requestAnimationFrame(loop)
    }
    raf = requestAnimationFrame(loop)

    return () => {
      closed = true
      pending.current = null
      if (raf) cancelAnimationFrame(raf)
      if (retry) clearTimeout(retry)
      ws?.close()
    }
  }, [coin, venue])

  const view = useMemo(() => {
    if (!book) return null
    const asks = book.asks.slice(0, DEPTH)
    const bids = book.bids.slice(0, DEPTH)
    // cumulative depth for background bars
    let ca = 0
    const askRows = asks.map((l) => ({ ...l, cum: (ca += l.sz) }))
    let cb = 0
    const bidRows = bids.map((l) => ({ ...l, cum: (cb += l.sz) }))
    const maxCum = Math.max(ca, cb, 1)
    const bestAsk = asks[0]?.px ?? 0
    const bestBid = bids[0]?.px ?? 0
    const mid = bestAsk && bestBid ? (bestAsk + bestBid) / 2 : 0
    const spread = bestAsk && bestBid ? bestAsk - bestBid : 0
    const spreadBps = mid ? (spread / mid) * 10000 : 0
    const priceDecimals = Math.max(
      0,
      ...asks.map((level) => level.priceDecimals),
      ...bids.map((level) => level.priceDecimals)
    )
    // buy/sell pressure across the visible book (by notional)
    const bidVol = bidRows.reduce((s, l) => s + l.sz * l.px, 0)
    const askVol = askRows.reduce((s, l) => s + l.sz * l.px, 0)
    const bidPct = bidVol + askVol > 0 ? (bidVol / (bidVol + askVol)) * 100 : 50
    // the single visible level nearest the user's entry — marked with ▸
    let markLevel: number | undefined
    if (markPrice) {
      let bd = Infinity
      for (const l of [...asks, ...bids]) {
        const d = Math.abs(l.px - markPrice)
        if (d < bd) {
          bd = d
          markLevel = l.px
        }
      }
    }
    return {
      askRows: askRows.reverse(),
      bidRows,
      maxCum,
      mid,
      spread,
      spreadBps,
      priceDecimals,
      bidPct,
      markLevel,
    }
  }, [book, markPrice])

  const rowH = 16

  return (
    <div style={{ fontFamily: 'var(--tm-mono)' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 8,
          marginBottom: 6,
        }}
      >
        <span className="tm-px" style={{ fontSize: 11 }}>
          {tt('orderBook')}
        </span>
        <span className="tm-sc">
          {exchangeLabel(venue)} USDⓈ-M · {coin}
        </span>
        <span
          role="group"
          aria-label={tt('updateMode')}
          style={{ display: 'inline-flex', gap: 2 }}
        >
          {(['smooth', 'realtime'] as const).map((mode) => (
            <button
              key={mode}
              type="button"
              aria-pressed={displayMode === mode}
              aria-label={
                mode === 'smooth' ? tt('smooth') : tt('realtimeUpdates')
              }
              onClick={() => selectDisplayMode(mode)}
              className="tm-sc"
              style={{
                border: '1px solid var(--tm-hair)',
                background:
                  displayMode === mode ? 'var(--tm-paper-2)' : 'transparent',
                color:
                  displayMode === mode ? 'var(--tm-ink)' : 'var(--tm-muted)',
                cursor: 'pointer',
                padding: '1px 4px',
                fontSize: 9,
              }}
            >
              {mode === 'smooth' ? tt('smooth') : tt('realtimeShort')}
            </button>
          ))}
        </span>
        <span
          className="tm-sc"
          style={{
            marginLeft: 'auto',
            color: status === 'live' ? 'var(--tm-up)' : 'var(--tm-muted)',
          }}
        >
          {status === 'live'
            ? `● ${tt('live')}`
            : status === 'connecting'
              ? `○ ${tt('syncing')}`
              : `○ ${tt('down')}`}
        </span>
      </div>

      {!view ? (
        <div className="tm-sc" style={{ padding: '16px 0' }}>
          {language === 'zh'
            ? `正在连接 ${exchangeLabel(venue)} 行情…`
            : `Connecting to ${exchangeLabel(venue)} market data…`}
        </div>
      ) : (
        <div style={{ fontSize: 11 }}>
          {/* column header */}
          <div
            className="tm-sc"
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr 1fr',
              gap: 4,
              marginBottom: 2,
            }}
          >
            <span>{tt('price')}</span>
            <span style={{ textAlign: 'right' }}>{tt('size')}</span>
            <span style={{ textAlign: 'right' }}>{tt('cumulative')}</span>
          </div>

          {/* asks (red), best ask nearest the mid — keyed by PRICE so each level
              keeps its identity and flashes independently when its size changes */}
          {view.askRows.map((l) => (
            <Row
              key={`a-${l.px}`}
              px={l.px}
              sz={l.sz}
              priceDecimals={l.priceDecimals}
              cum={l.cum}
              maxCum={view.maxCum}
              side="ask"
              h={rowH}
              mark={view.markLevel}
              motion={displayMode === 'realtime'}
            />
          ))}

          {/* mid / spread */}
          <div
            className="tm-mono"
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '3px 0',
              margin: '2px 0',
              borderTop: '1px solid var(--tm-hair)',
              borderBottom: '1px solid var(--tm-hair)',
            }}
          >
            <span
              className="tm-px"
              style={{ fontSize: 12, color: 'var(--tm-red)' }}
            >
              {fmtPx(view.mid, Math.min(8, view.priceDecimals + 1))}
            </span>
            <span className="tm-sc" style={{ marginLeft: 'auto' }}>
              {tt('spread')} {fmtPx(view.spread, view.priceDecimals)} ·{' '}
              {view.spreadBps.toFixed(1)}bps
            </span>
          </div>

          {/* bids (green) — keyed by price, same independent-flash behavior */}
          {view.bidRows.map((l) => (
            <Row
              key={`b-${l.px}`}
              px={l.px}
              sz={l.sz}
              priceDecimals={l.priceDecimals}
              cum={l.cum}
              maxCum={view.maxCum}
              side="bid"
              h={rowH}
              mark={view.markLevel}
              motion={displayMode === 'realtime'}
            />
          ))}

          {/* buy/sell pressure across the visible book */}
          <div style={{ marginTop: 7 }}>
            <div style={{ display: 'flex', height: 6 }}>
              <div
                style={{
                  width: `${view.bidPct}%`,
                  background: 'var(--tm-up)',
                  transition:
                    displayMode === 'realtime'
                      ? 'width 0.2s ease-out'
                      : 'width 0.08s linear',
                }}
              />
              <div style={{ flex: 1, background: 'var(--tm-dn)' }} />
            </div>
            <div
              className="tm-sc"
              style={{ display: 'flex', fontSize: 9, marginTop: 2 }}
            >
              <span className="tm-up">B {view.bidPct.toFixed(1)}%</span>
              <span style={{ marginLeft: 'auto' }} className="tm-dn">
                {(100 - view.bidPct).toFixed(1)}% S
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

interface RowProps {
  px: number
  sz: number
  priceDecimals: number
  cum: number
  maxCum: number
  side: 'ask' | 'bid'
  h: number
  mark?: number
  motion: boolean
}
function fmtNotional(n: number): string {
  if (n >= 1e9) return `$${(n / 1e9).toFixed(2)}B`
  if (n >= 1e6) return `$${(n / 1e6).toFixed(2)}M`
  if (n >= 1e3) return `$${(n / 1e3).toFixed(1)}K`
  return `$${n.toFixed(0)}`
}
function Row({
  px,
  sz,
  priceDecimals,
  cum,
  maxCum,
  side,
  h,
  mark,
  motion,
}: RowProps) {
  const pct = Math.min(100, (cum / maxCum) * 100)
  const color = side === 'ask' ? 'var(--tm-dn)' : 'var(--tm-up)'
  // bold cumulative-depth bar, saturated toward the edge, that animates its
  // width as the book updates (the live "growing ladder" effect)
  const bar =
    side === 'ask'
      ? 'linear-gradient(to left, rgba(214,67,58,0.36), rgba(214,67,58,0.05))'
      : 'linear-gradient(to left, rgba(46,139,87,0.36), rgba(46,139,87,0.05))'
  const isMark = mark != null && px === mark

  // this Row instance is keyed by price, so these refs persist across updates —
  // we flash green/red only when THIS level's size actually changes, and keep
  // the direction class fixed until the next change so the animation isn't cut
  // short by the 60fps re-renders.
  const prevSz = useRef(sz)
  const dirRef = useRef('')
  if (sz !== prevSz.current) {
    dirRef.current = sz > prevSz.current ? 'ob-up' : 'ob-dn'
    prevSz.current = sz
  }

  return (
    <div
      style={{
        position: 'relative',
        height: h,
        display: 'flex',
        alignItems: 'center',
      }}
    >
      {/* per-row flash overlay — keyed by size so it remounts (replays the
          animation) exactly when this level's size changes */}
      <div
        key={sz}
        className={motion ? dirRef.current : ''}
        style={{ position: 'absolute', inset: 0, pointerEvents: 'none' }}
      />
      <div
        style={{
          position: 'absolute',
          right: 0,
          top: 0,
          bottom: 0,
          width: `${pct}%`,
          background: bar,
          transition: motion ? 'width 0.16s ease-out' : 'width 0.08s linear',
        }}
      />
      <div
        style={{
          position: 'relative',
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr',
          gap: 4,
          width: '100%',
          alignItems: 'center',
        }}
      >
        <span style={{ color, fontWeight: isMark ? 700 : 500 }}>
          {isMark ? '▸ ' : ''}
          {fmtPx(px, priceDecimals)}
        </span>
        <span style={{ textAlign: 'right', color: 'var(--tm-ink)' }}>
          {fmtSz(sz)}
        </span>
        <span style={{ textAlign: 'right', color: 'var(--tm-muted)' }}>
          {fmtNotional(cum * px)}
        </span>
      </div>
    </div>
  )
}

export default OrderBook
