import { useEffect, useMemo, useRef, useState } from 'react'
import useSWR from 'swr'
import { api } from '../../lib/api'
import type { Kline } from '../../lib/api/data'
import { Candles } from './Candles'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

/** Public perpetual candles: REST polling for every venue, with Binance's
 * existing WebSocket stream retained as an optional live enhancement. */
const BINANCE_WS = 'wss://fstream.binance.com/ws'
const INTERVAL = '1m'
const MAX_BARS = 90

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
    case 'binance':
      return 'Binance'
    default:
      return exchange ? exchange.toUpperCase() : 'Binance'
  }
}

interface KlineChartProps {
  symbol: string
  exchange?: string
  height?: number
  fill?: boolean
}

export function KlineChart({
  symbol,
  exchange = 'binance',
  height = 360,
  fill = false,
}: KlineChartProps) {
  const { language } = useLanguage()
  const tt = (key: string) => t(`terminalDashboard.${key}`, language)
  const venue = exchange.toLowerCase().trim() || 'binance'
  const coin = canonicalSymbol(symbol)
  const { data: seed, isLoading } = useSWR(
    ['kline', coin, INTERVAL, venue],
    () => api.getKlines(coin, INTERVAL, venue, MAX_BARS, true),
    {
      refreshInterval: 60000,
      revalidateOnFocus: false,
      shouldRetryOnError: false,
      keepPreviousData: true,
    }
  )

  const [liveBar, setLiveBar] = useState<Kline | null>(null)
  const [wsLive, setWsLive] = useState(false)
  const pending = useRef<Kline | null>(null)

  useEffect(() => {
    setLiveBar(null)
    setWsLive(false)
    if (venue !== 'binance') return
    let ws: WebSocket | null = null
    let raf: number | null = null
    let retry: ReturnType<typeof setTimeout> | null = null
    let closed = false

    const connect = () => {
      ws = new WebSocket(
        `${BINANCE_WS}/${coin.toLowerCase()}@kline_${INTERVAL}`
      )
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data) as {
            k?: {
              t: number
              T: number
              o: string
              h: string
              l: string
              c: string
              v: string
            }
          }
          if (!msg.k) return
          const d = msg.k
          pending.current = {
            openTime: d.t,
            closeTime: d.T,
            open: Number(d.o),
            high: Number(d.h),
            low: Number(d.l),
            close: Number(d.c),
            volume: Number(d.v),
          }
          setWsLive(true)
        } catch {
          // Ignore malformed public-market frames.
        }
      }
      ws.onclose = () => {
        if (closed) return
        setWsLive(false)
        retry = setTimeout(connect, 1500)
      }
      ws.onerror = () => ws?.close()
    }

    connect()
    const loop = () => {
      if (pending.current) {
        setLiveBar(pending.current)
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
  }, [coin, venue])

  const candles = useMemo(() => {
    const history = seed ?? []
    if (!liveBar) return history.slice(-MAX_BARS)
    const merged = [...history]
    const last = merged[merged.length - 1]
    if (last && liveBar.openTime === last.openTime)
      merged[merged.length - 1] = liveBar
    else if (!last || liveBar.openTime > last.openTime) merged.push(liveBar)
    return merged.slice(-MAX_BARS)
  }, [seed, liveBar])

  const last = candles.length ? candles[candles.length - 1].close : 0
  const first = candles.length ? candles[0].open : 0
  const change = first ? ((last - first) / first) * 100 : 0
  const live = wsLive && candles.length > 0

  return (
    <div
      style={{
        fontFamily: 'var(--tm-mono)',
        ...(fill
          ? {
              display: 'flex',
              flexDirection: 'column',
              height: '100%',
              minHeight: 0,
            }
          : {}),
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 8,
          marginBottom: 6,
        }}
      >
        <span className="tm-px" style={{ fontSize: 11 }}>
          {coin}
        </span>
        <span className="tm-sc">
          {exchangeLabel(venue)} USDⓈ-M · {INTERVAL}
        </span>
        <span
          className="tm-sc"
          style={{
            marginLeft: 'auto',
            color: live ? 'var(--tm-up)' : 'var(--tm-muted)',
          }}
        >
          {live
            ? `● ${tt('live')}`
            : isLoading || candles.length
              ? `○ ${tt('syncing')}`
              : '○ —'}
        </span>
      </div>
      {last > 0 && (
        <div
          className="tm-mono"
          style={{
            display: 'flex',
            alignItems: 'baseline',
            gap: 8,
            marginBottom: 4,
            fontSize: 12,
          }}
        >
          <span style={{ fontWeight: 600 }}>
            ${last.toLocaleString('en-US', { maximumFractionDigits: 4 })}
          </span>
          <span
            className={change >= 0 ? 'tm-up' : 'tm-dn'}
            style={{ fontSize: 11 }}
          >
            {change >= 0 ? '+' : ''}
            {change.toFixed(2)}%
          </span>
          <span className="tm-sc" style={{ marginLeft: 'auto' }}>
            {candles.length} {tt('bars')} · {INTERVAL}
          </span>
        </div>
      )}
      {candles.length > 0 ? (
        fill ? (
          <div style={{ flex: 1, minHeight: 0 }}>
            <Candles data={candles} width={380} height={height} fill />
          </div>
        ) : (
          <Candles data={candles} width={380} height={height} />
        )
      ) : (
        <div className="tm-sc" style={{ padding: '20px 0' }}>
          {tt('loadingMarket')}
        </div>
      )}
    </div>
  )
}

export default KlineChart
