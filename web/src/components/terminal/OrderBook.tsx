import { useEffect, useMemo, useRef, useState } from 'react'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

/**
 * OrderBook renders a live L2 depth ladder for a single instrument, streamed
 * directly from Binance USDⓈ-M Futures' public partial-depth stream.
 */

const BINANCE_WS = 'wss://fstream.binance.com/ws'
const DEPTH = 11 // levels per side

interface Level {
  px: number
  sz: number
}
interface BookState {
  coin: string
  bids: Level[]
  asks: Level[]
}

function binanceSymbol(raw: string): string {
  const normalized = raw.toUpperCase().trim()
  return /^[A-Z0-9]+USDT$/.test(normalized) ? normalized : 'BTCUSDT'
}

function fmtPx(px: number): string {
  if (px >= 1000) return px.toLocaleString('en-US', { maximumFractionDigits: 1 })
  if (px >= 1) return px.toLocaleString('en-US', { maximumFractionDigits: 3 })
  return px.toLocaleString('en-US', { maximumFractionDigits: 5 })
}
function fmtSz(sz: number): string {
  if (sz >= 1000) return `${(sz / 1000).toFixed(1)}k`
  if (sz >= 1) return sz.toFixed(2)
  return sz.toFixed(3)
}

interface OrderBookProps {
  /** raw business symbol (e.g. position symbol or candidate coin) */
  symbol: string
  /** optional entry price to mark the user's position level on the ladder */
  markPrice?: number
}

export function OrderBook({ symbol, markPrice }: OrderBookProps) {
  const { language } = useLanguage()
  const tt = (key: string) => t(`terminalDashboard.${key}`, language)
  const coin = useMemo(() => binanceSymbol(symbol || ''), [symbol])
  const [book, setBook] = useState<BookState | null>(null)
  const [status, setStatus] = useState<'connecting' | 'live' | 'down'>('connecting')

  // live L2 stream
  const pending = useRef<BookState | null>(null)
  useEffect(() => {
    if (!coin) return
    let ws: WebSocket | null = null
    let raf: number | null = null
    let retry: ReturnType<typeof setTimeout> | null = null
    let closed = false

    const connect = () => {
      setStatus('connecting')
      ws = new WebSocket(`${BINANCE_WS}/${coin.toLowerCase()}@depth20@100ms`)
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data) as { bids?: [string, string][]; asks?: [string, string][] }
          if (!Array.isArray(msg.bids) || !Array.isArray(msg.asks)) return
          const toLevels = (levels: [string, string][]): Level[] =>
            levels.slice(0, DEPTH).map(([px, sz]) => ({ px: Number(px), sz: Number(sz) }))
          pending.current = { coin, bids: toLevels(msg.bids), asks: toLevels(msg.asks) }
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
    // flush on every animation frame (~16ms) so individual rows tick the instant
    // the venue pushes a change — millisecond-level, not a batched interval
    const loop = () => {
      if (pending.current) {
        setBook(pending.current)
        pending.current = null
      }
      raf = requestAnimationFrame(loop)
    }
    raf = requestAnimationFrame(loop)

    return () => {
      closed = true
      if (raf) cancelAnimationFrame(raf)
      if (retry) clearTimeout(retry)
      ws?.close()
    }
  }, [coin])

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
    return { askRows: askRows.reverse(), bidRows, maxCum, mid, spread, spreadBps, bidPct, markLevel }
  }, [book, markPrice])

  const rowH = 16

  return (
    <div style={{ fontFamily: 'var(--tm-mono)' }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 6 }}>
        <span className="tm-px" style={{ fontSize: 11 }}>{tt('orderBook')}</span>
        <span className="tm-sc">Binance USDⓈ-M · {coin}</span>
        <span
          className="tm-sc"
          style={{ marginLeft: 'auto', color: status === 'live' ? 'var(--tm-up)' : 'var(--tm-muted)' }}
        >
          {status === 'live' ? `● ${tt('live')}` : status === 'connecting' ? `○ ${tt('syncing')}` : `○ ${tt('down')}`}
        </span>
      </div>

      {!view ? (
        <div className="tm-sc" style={{ padding: '16px 0' }}>{language === 'zh' ? '正在连接 Binance 行情…' : 'Connecting to Binance market data…'}</div>
      ) : (
        <div style={{ fontSize: 11 }}>
          {/* column header */}
          <div className="tm-sc" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 4, marginBottom: 2 }}>
            <span>{tt('price')}</span>
            <span style={{ textAlign: 'right' }}>{tt('size')}</span>
            <span style={{ textAlign: 'right' }}>{tt('cumulative')}</span>
          </div>

          {/* asks (red), best ask nearest the mid — keyed by PRICE so each level
              keeps its identity and flashes independently when its size changes */}
          {view.askRows.map((l) => (
            <Row key={`a-${l.px}`} px={l.px} sz={l.sz} cum={l.cum} maxCum={view.maxCum} side="ask" h={rowH} mark={view.markLevel} />
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
            <span className="tm-px" style={{ fontSize: 12, color: 'var(--tm-red)' }}>{fmtPx(view.mid)}</span>
            <span className="tm-sc" style={{ marginLeft: 'auto' }}>{tt('spread')} {fmtPx(view.spread)} · {view.spreadBps.toFixed(1)}bps</span>
          </div>

          {/* bids (green) — keyed by price, same independent-flash behavior */}
          {view.bidRows.map((l) => (
            <Row key={`b-${l.px}`} px={l.px} sz={l.sz} cum={l.cum} maxCum={view.maxCum} side="bid" h={rowH} mark={view.markLevel} />
          ))}

          {/* buy/sell pressure across the visible book */}
          <div style={{ marginTop: 7 }}>
            <div style={{ display: 'flex', height: 6 }}>
              <div style={{ width: `${view.bidPct}%`, background: 'var(--tm-up)', transition: 'width 0.2s ease-out' }} />
              <div style={{ flex: 1, background: 'var(--tm-dn)' }} />
            </div>
            <div className="tm-sc" style={{ display: 'flex', fontSize: 9, marginTop: 2 }}>
              <span className="tm-up">B {view.bidPct.toFixed(1)}%</span>
              <span style={{ marginLeft: 'auto' }} className="tm-dn">{(100 - view.bidPct).toFixed(1)}% S</span>
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
  cum: number
  maxCum: number
  side: 'ask' | 'bid'
  h: number
  mark?: number
}
function fmtNotional(n: number): string {
  if (n >= 1e9) return `$${(n / 1e9).toFixed(2)}B`
  if (n >= 1e6) return `$${(n / 1e6).toFixed(2)}M`
  if (n >= 1e3) return `$${(n / 1e3).toFixed(1)}K`
  return `$${n.toFixed(0)}`
}
function Row({ px, sz, cum, maxCum, side, h, mark }: RowProps) {
  const pct = Math.min(100, (cum / maxCum) * 100)
  const color = side === 'ask' ? 'var(--tm-dn)' : 'var(--tm-up)'
  // bold cumulative-depth bar, saturated toward the edge, that animates its
  // width as the book updates (the live "growing ladder" effect)
  const bar = side === 'ask'
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
    <div style={{ position: 'relative', height: h, display: 'flex', alignItems: 'center' }}>
      {/* per-row flash overlay — keyed by size so it remounts (replays the
          animation) exactly when this level's size changes */}
      <div key={sz} className={dirRef.current} style={{ position: 'absolute', inset: 0, pointerEvents: 'none' }} />
      <div style={{ position: 'absolute', right: 0, top: 0, bottom: 0, width: `${pct}%`, background: bar, transition: 'width 0.16s ease-out' }} />
      <div style={{ position: 'relative', display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 4, width: '100%', alignItems: 'center' }}>
        <span style={{ color, fontWeight: isMark ? 700 : 500 }}>
          {isMark ? '▸ ' : ''}{fmtPx(px)}
        </span>
        <span style={{ textAlign: 'right', color: 'var(--tm-ink)' }}>{fmtSz(sz)}</span>
        <span style={{ textAlign: 'right', color: 'var(--tm-muted)' }}>{fmtNotional(cum * px)}</span>
      </div>
    </div>
  )
}

export default OrderBook
