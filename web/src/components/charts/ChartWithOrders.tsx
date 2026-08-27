import { useEffect, useRef, useState } from 'react'
import {
  createChart,
  IChartApi,
  ISeriesApi,
  Time,
  UTCTimestamp,
  CandlestickSeries,
  createSeriesMarkers,
} from 'lightweight-charts'
import { useLanguage } from '../../contexts/LanguageContext'
import { useTheme } from '../../contexts/ThemeContext'
import { httpClient } from '../../lib/httpClient'
import { t } from '../../i18n/translations'

// Order marker interface
interface OrderMarker {
  time: number // Unix timestamp (seconds)
  price: number
  side: string // BUY, SELL
  orderAction: string // OPEN_LONG, CLOSE_LONG, STOP_LOSS, TAKE_PROFIT, etc.
  status: string // NEW, FILLED, CANCELED, etc.
  symbol: string
}

// Kline data interface
interface KlineData {
  time: UTCTimestamp
  open: number
  high: number
  low: number
  close: number
  volume?: number
}

interface ChartWithOrdersProps {
  symbol: string
  interval?: string // 1m, 5m, 15m, 1h, 4h, 1d
  traderID?: string // Used to fetch orders for this trader
  height?: number
  exchange?: string // Exchange type: binance, bybit, okx, bitget, hyperliquid, aster, lighter
}

export function ChartWithOrders({
  symbol = 'BTCUSDT',
  interval = '5m',
  traderID,
  height = 500,
  exchange = 'binance', // Default to binance
}: ChartWithOrdersProps) {
  const { language } = useLanguage()
  const { isDark } = useTheme()
  const chartContainerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candlestickSeriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const seriesMarkersRef = useRef<any>(null) // Markers primitive for v5
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [tooltipData, setTooltipData] = useState<any>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)

  // Parse time: supports Unix timestamp (number) or string format
  const parseCustomTime = (time: any): number => {
    if (!time) {
      console.warn('[ChartWithOrders] Empty time value')
      return 0
    }

    // If already a number (Unix timestamp)
    if (typeof time === 'number') {
      // Determine ms vs seconds: if > 10^12, treat as milliseconds
      if (time > 1000000000000) {
        const seconds = Math.floor(time / 1000)
        console.log(
          '[ChartWithOrders] ✅ Unix timestamp (ms→s):',
          time,
          '→',
          seconds,
          '(',
          new Date(time).toISOString(),
          ')'
        )
        return seconds
      }
      console.log(
        '[ChartWithOrders] ✅ Unix timestamp (s):',
        time,
        '(',
        new Date(time * 1000).toISOString(),
        ')'
      )
      return time
    }

    const timeStr = String(time)
    console.log('[ChartWithOrders] Parsing time string:', timeStr)

    // Try standard ISO format
    const isoTime = new Date(timeStr).getTime()
    if (!isNaN(isoTime) && isoTime > 0) {
      const timestamp = Math.floor(isoTime / 1000)
      console.log(
        '[ChartWithOrders] ✅ Parsed as ISO:',
        timeStr,
        '→',
        timestamp,
        '(',
        new Date(timestamp * 1000).toISOString(),
        ')'
      )
      return timestamp
    }

    // Parse custom format "MM-DD HH:mm UTC" (for legacy data)
    const match = timeStr.match(/(\d{2})-(\d{2})\s+(\d{2}):(\d{2})\s+UTC/)
    if (match) {
      const currentYear = new Date().getFullYear()
      const [_, month, day, hour, minute] = match
      const date = new Date(
        Date.UTC(
          currentYear,
          parseInt(month) - 1,
          parseInt(day),
          parseInt(hour),
          parseInt(minute)
        )
      )
      const timestamp = Math.floor(date.getTime() / 1000)
      console.log(
        '[ChartWithOrders] ✅ Parsed as custom format:',
        timeStr,
        '→',
        timestamp,
        '(',
        new Date(timestamp * 1000).toISOString(),
        ')'
      )
      return timestamp
    }

    console.error('[ChartWithOrders] ❌ Failed to parse time:', timeStr)
    return 0
  }

  // Fetch kline data from our service
  const fetchKlineData = async (
    symbol: string,
    interval: string
  ): Promise<KlineData[]> => {
    try {
      const limit = 2000 // Fetch recent 2000 candles (more historical data)
      const klineUrl = `/api/klines?symbol=${symbol}&interval=${interval}&limit=${limit}&exchange=${exchange}`

      const result = await httpClient.request(klineUrl, { silent: true })

      if (!result.success || !result.data) {
        throw new Error('Failed to fetch kline data from our service')
      }

      const data = result.data

      // Convert backend data format to lightweight-charts format
      // Backend returns market.Kline format: {OpenTime, Open, High, Low, Close, Volume, ...}
      return data.map((candle: any) => ({
        time: Math.floor(candle.openTime / 1000) as UTCTimestamp, // ms to seconds
        open: candle.open,
        high: candle.high,
        low: candle.low,
        close: candle.close,
        volume: candle.volume,
      }))
    } catch (err) {
      console.error('Error fetching kline data:', err)
      throw err
    }
  }

  // Fetch order data
  const fetchOrders = async (
    traderID: string,
    symbol: string
  ): Promise<OrderMarker[]> => {
    try {
      // Fetch filled orders for this trader from backend API
      const result = await httpClient.request(
        `/api/orders?trader_id=${traderID}&symbol=${symbol}&status=FILLED&limit=50`,
        { silent: true }
      )

      if (!result.success || !result.data) {
        console.warn('Failed to fetch orders:', result.message)
        return []
      }

      const orders = result.data
      const markers: OrderMarker[] = []

      // Convert order data to marker format
      orders.forEach((order: any) => {
        const createdAt = order.created_at || order.CreatedAt
        const filledAt = order.filled_at || order.FilledAt
        const avgPrice = order.avg_fill_price || order.AvgFillPrice
        const price = order.price || order.Price
        const orderAction = order.order_action || order.OrderAction
        const side = order.side || order.Side
        const status = order.status || order.Status
        const symbol = order.symbol || order.Symbol

        // Use fill time (if available) or creation time
        const orderTime = filledAt || createdAt
        if (!orderTime) return

        const timeSeconds = parseCustomTime(orderTime)
        if (timeSeconds === 0) return

        // Use average fill price (if available) or order price
        const orderPrice = avgPrice || price
        if (!orderPrice || orderPrice === 0) return

        markers.push({
          time: timeSeconds,
          price: orderPrice,
          side: side || 'BUY',
          orderAction: orderAction || 'UNKNOWN',
          status: status || 'FILLED',
          symbol: symbol || '',
        })
      })

      console.log(
        `[ChartWithOrders] Loaded ${markers.length} order markers for ${symbol}`
      )
      return markers
    } catch (err) {
      console.error('Error fetching orders:', err)
      return []
    }
  }

  // Initialize chart
  useEffect(() => {
    if (!chartContainerRef.current) {
      console.error('[ChartWithOrders] Container ref is null')
      return
    }

    console.log('[ChartWithOrders] Initializing chart for', symbol, interval)

    try {
      // Create chart
      const chart = createChart(chartContainerRef.current, {
        width: chartContainerRef.current.clientWidth,
        height: height,
        layout: {
          background: { color: isDark ? '#13171F' : '#F1ECE2' },
          textColor: isDark ? '#E6EBF2' : '#1A1813',
        },
        grid: {
          vertLines: {
            color: isDark
              ? 'rgba(255, 255, 255, 0.06)'
              : 'rgba(26, 24, 19, 0.08)',
          },
          horzLines: {
            color: isDark
              ? 'rgba(255, 255, 255, 0.06)'
              : 'rgba(26, 24, 19, 0.08)',
          },
        },
        crosshair: {
          mode: 1, // Normal crosshair
        },
        rightPriceScale: {
          borderColor: isDark
            ? 'rgba(255, 255, 255, 0.12)'
            : 'rgba(26, 24, 19, 0.14)',
        },
        timeScale: {
          borderColor: isDark
            ? 'rgba(255, 255, 255, 0.12)'
            : 'rgba(26, 24, 19, 0.14)',
          timeVisible: true,
          secondsVisible: false,
        },
        localization: {
          timeFormatter: (time: number) => {
            const date = new Date(time * 1000)
            return date.toLocaleString('zh-CN', {
              month: '2-digit',
              day: '2-digit',
              hour: '2-digit',
              minute: '2-digit',
              hour12: false,
            })
          },
        },
      })

      chartRef.current = chart

      // Create candlestick series (using v5 API)
      const candlestickSeries = chart.addSeries(CandlestickSeries, {
        upColor: '#2E8B57',
        downColor: '#D6433A',
        borderUpColor: '#2E8B57',
        borderDownColor: '#D6433A',
        wickUpColor: '#2E8B57',
        wickDownColor: '#D6433A',
      })

      candlestickSeriesRef.current = candlestickSeries as any

      // Responsive resize
      const handleResize = () => {
        if (chartContainerRef.current && chartRef.current) {
          chartRef.current.applyOptions({
            width: chartContainerRef.current.clientWidth,
          })
        }
      }

      window.addEventListener('resize', handleResize)

      // Listen for crosshair movement to show OHLC info
      chart.subscribeCrosshairMove((param) => {
        if (!param.time || !param.point || !candlestickSeriesRef.current) {
          setTooltipData(null)
          return
        }

        const data = param.seriesData.get(candlestickSeriesRef.current as any)
        if (!data) {
          setTooltipData(null)
          return
        }

        const candleData = data as any
        setTooltipData({
          time: param.time,
          open: candleData.open,
          high: candleData.high,
          low: candleData.low,
          close: candleData.close,
          x: param.point.x,
          y: param.point.y,
        })
      })

      return () => {
        window.removeEventListener('resize', handleResize)
        chart.remove()
      }
    } catch (err) {
      console.error('[ChartWithOrders] Failed to initialize chart:', err)
      setError('Failed to initialize chart')
    }
  }, [height])

  // Load data
  useEffect(() => {
    const loadData = async () => {
      if (!candlestickSeriesRef.current) {
        console.log('[ChartWithOrders] Candlestick series not ready yet')
        return
      }

      console.log(
        '[ChartWithOrders] Loading data for',
        symbol,
        interval,
        'trader:',
        traderID
      )
      setLoading(true)
      setError(null)

      try {
        // 1. Fetch kline data
        console.log('[ChartWithOrders] Fetching kline data...')
        const klineData = await fetchKlineData(symbol, interval)
        console.log(
          '[ChartWithOrders] Kline data received:',
          klineData.length,
          'candles'
        )
        candlestickSeriesRef.current.setData(klineData)

        // Build kline time set for quick lookup
        const klineTimeSet = new Set(klineData.map((k) => k.time as number))
        const klineMinTime = klineData.length > 0 ? klineData[0].time : 0
        const klineMaxTime =
          klineData.length > 0 ? klineData[klineData.length - 1].time : 0
        console.log(
          '[ChartWithOrders] Kline time range:',
          klineMinTime,
          '-',
          klineMaxTime,
          'candles:',
          klineData.length
        )

        // Calculate interval in seconds
        const getIntervalSeconds = (interval: string): number => {
          const match = interval.match(/(\d+)([smhd])/)
          if (!match) return 60 // Default 1 minute
          const [, num, unit] = match
          const n = parseInt(num)
          switch (unit) {
            case 's':
              return n
            case 'm':
              return n * 60
            case 'h':
              return n * 3600
            case 'd':
              return n * 86400
            default:
              return 60
          }
        }
        const intervalSeconds = getIntervalSeconds(interval)
        console.log(
          '[ChartWithOrders] Interval:',
          interval,
          '=',
          intervalSeconds,
          'seconds'
        )

        // 2. Fetch order data and add markers
        if (traderID) {
          console.log(
            '[ChartWithOrders] Fetching orders for trader:',
            traderID,
            'symbol:',
            symbol
          )
          const orders = await fetchOrders(traderID, symbol)
          console.log(
            '[ChartWithOrders] Received orders:',
            orders.length,
            'orders'
          )

          if (orders.length === 0) {
            console.log('[ChartWithOrders] No orders to display')
          }

          // Convert orders to chart markers, aligned to kline time
          const markers: Array<{
            time: Time
            position: 'belowBar'
            color: string
            shape: 'circle'
            text: string
            price: number
            size: number
          }> = []

          orders.forEach((order) => {
            // Align order time to kline interval (floor)
            const alignedTime =
              Math.floor(order.time / intervalSeconds) * intervalSeconds

            // Check if aligned time exists in kline data
            if (!klineTimeSet.has(alignedTime)) {
              console.warn(
                '[ChartWithOrders] ⚠️ Skipping order - no matching kline:',
                order.time,
                '→',
                alignedTime,
                '(',
                new Date(order.time * 1000).toISOString(),
                ')'
              )
              return
            }

            const isBuy = order.side === 'BUY'
            markers.push({
              time: alignedTime as Time,
              position: 'belowBar' as const,
              color: isBuy ? '#2E8B57' : '#D6433A',
              shape: 'circle' as const,
              text: isBuy ? 'B' : 'S',
              price: order.price,
              size: 1,
            })
          })

          console.log(
            '[ChartWithOrders] Valid markers (with matching klines):',
            markers.length,
            'out of',
            orders.length
          )

          console.log(
            '[ChartWithOrders] Setting',
            markers.length,
            'markers on chart'
          )

          try {
            // Using v5 API: createSeriesMarkers
            if (seriesMarkersRef.current) {
              // If already exists, update markers
              seriesMarkersRef.current.setMarkers(markers)
            } else {
              // First time creating markers
              seriesMarkersRef.current = createSeriesMarkers(
                candlestickSeriesRef.current,
                markers
              )
            }
            console.log('[ChartWithOrders] ✅ Markers set successfully!')
          } catch (err) {
            console.error('[ChartWithOrders] ❌ Failed to set markers:', err)
          }
        }

        // Auto-fit view
        chartRef.current?.timeScale().fitContent()

        setLoading(false)
      } catch (err) {
        console.error('Error loading chart data:', err)
        setError(t('chartWithOrders.failedToLoad', language))
        setLoading(false)
      }
    }

    loadData()

    // Auto-refresh - update kline data every 30 seconds
    const refreshInterval = setInterval(() => {
      loadData()
    }, 30000) // 30 seconds

    return () => {
      clearInterval(refreshInterval)
    }
  }, [symbol, interval, traderID, language])

  // Dynamically update chart theme options when isDark changes
  useEffect(() => {
    if (!chartRef.current) return
    chartRef.current.applyOptions({
      layout: {
        background: { color: isDark ? '#13171F' : '#F1ECE2' },
        textColor: isDark ? '#E6EBF2' : '#1A1813',
      },
      grid: {
        vertLines: {
          color: isDark
            ? 'rgba(255, 255, 255, 0.06)'
            : 'rgba(26, 24, 19, 0.08)',
        },
        horzLines: {
          color: isDark
            ? 'rgba(255, 255, 255, 0.06)'
            : 'rgba(26, 24, 19, 0.08)',
        },
      },
      rightPriceScale: {
        borderColor: isDark
          ? 'rgba(255, 255, 255, 0.12)'
          : 'rgba(26, 24, 19, 0.14)',
      },
      timeScale: {
        borderColor: isDark
          ? 'rgba(255, 255, 255, 0.12)'
          : 'rgba(26, 24, 19, 0.14)',
      },
    })
  }, [isDark])

  return (
    <div className="relative rounded-lg overflow-hidden bg-nofx-bg border border-nofx-border">
      {/* Title bar */}
      <div className="flex items-center justify-between p-4 border-b border-nofx-border bg-nofx-bg-lighter">
        <div className="flex items-center gap-3">
          <span className="text-xl">📈</span>
          <h3 className="text-lg font-bold text-nofx-text">
            {symbol} {interval}
          </h3>
        </div>
        {loading && (
          <div className="text-sm text-nofx-text-muted">
            {t('chartWithOrders.loading', language)}
          </div>
        )}
      </div>

      {/* Chart container */}
      <div style={{ position: 'relative' }}>
        <div ref={chartContainerRef} />

        {/* OHLC Tooltip */}
        {tooltipData && (
          <div
            ref={tooltipRef}
            className="absolute left-2.5 top-2.5 p-3 rounded-md shadow-xl backdrop-blur-md bg-nofx-bg-lighter/95 border border-nofx-border text-nofx-text font-mono text-xs z-10 pointer-events-none"
          >
            <div className="mb-1.5 font-bold text-[11px] text-nofx-gold">
              {new Date((tooltipData.time as number) * 1000).toLocaleString(
                language === 'zh' ? 'zh-CN' : 'en-US',
                {
                  month: 'short',
                  day: 'numeric',
                  hour: '2-digit',
                  minute: '2-digit',
                }
              )}
            </div>
            <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[11px]">
              <span className="text-nofx-text-muted">O:</span>
              <span className="text-nofx-text font-medium">
                {tooltipData.open?.toFixed(2)}
              </span>

              <span className="text-nofx-text-muted">H:</span>
              <span className="text-nofx-success font-medium">
                {tooltipData.high?.toFixed(2)}
              </span>

              <span className="text-nofx-text-muted">L:</span>
              <span className="text-nofx-danger font-medium">
                {tooltipData.low?.toFixed(2)}
              </span>

              <span className="text-nofx-text-muted">C:</span>
              <span
                className={`font-bold ${
                  tooltipData.close >= tooltipData.open
                    ? 'text-nofx-success'
                    : 'text-nofx-danger'
                }`}
              >
                {tooltipData.close?.toFixed(2)}
              </span>
            </div>
          </div>
        )}
      </div>

      {/* Error display */}
      {error && (
        <div className="absolute inset-0 flex items-center justify-center bg-nofx-bg/90">
          <div className="text-center">
            <div className="text-2xl mb-2">⚠️</div>
            <div className="text-nofx-danger font-mono text-sm">{error}</div>
          </div>
        </div>
      )}

      {/* Legend */}
      <div className="flex items-center gap-4 p-4 text-xs border-t border-nofx-border text-nofx-text-muted bg-nofx-bg-lighter">
        <div className="flex items-center gap-2">
          <span className="font-bold text-nofx-success">B</span>
          <span>{t('chartWithOrders.buy', language)}</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="font-bold text-nofx-danger">S</span>
          <span>{t('chartWithOrders.sell', language)}</span>
        </div>
      </div>
    </div>
  )
}
