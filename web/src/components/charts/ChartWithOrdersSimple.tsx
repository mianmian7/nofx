import { useEffect, useState } from 'react'
import { httpClient } from '../../lib/httpClient'

interface ChartWithOrdersSimpleProps {
  symbol: string
  interval?: string
  traderID?: string
  height?: number
}

export function ChartWithOrdersSimple({
  symbol = 'BTCUSDT',
  interval = '5m',
  traderID,
  height = 500,
}: ChartWithOrdersSimpleProps) {
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [klineCount, setKlineCount] = useState(0)
  const [orderCount, setOrderCount] = useState(0)

  useEffect(() => {
    const loadData = async () => {
      setLoading(true)
      setError(null)

      try {
        const limit = 100
        const klineUrl = `/api/klines?symbol=${symbol}&interval=${interval}&limit=${limit}`
        const klineResult = await httpClient.request(klineUrl, { silent: true })

        if (!klineResult.success || !klineResult.data) {
          throw new Error('Failed to fetch klines from our service')
        }

        setKlineCount(klineResult.data.length)

        if (traderID) {
          const tradesUrl = `/api/trades?trader_id=${traderID}&symbol=${symbol}&limit=100`
          const tradesResult = await httpClient.request(tradesUrl, {
            silent: true,
          })

          if (tradesResult.success && tradesResult.data) {
            setOrderCount(tradesResult.data.length)
          }
        }

        setLoading(false)
      } catch (err: any) {
        setError(err.message || 'Failed to load data')
        setLoading(false)
      }
    }

    loadData()
  }, [symbol, interval, traderID])

  return (
    <div
      className="relative rounded-lg overflow-hidden bg-nofx-bg border border-nofx-border"
      style={{ minHeight: height }}
    >
      {/* Title bar */}
      <div className="flex items-center justify-between p-4 border-b border-nofx-border">
        <div className="flex items-center gap-3">
          <span className="text-xl">📈</span>
          <h3 className="text-lg font-bold text-nofx-text">
            {symbol} {interval} (Test Mode)
          </h3>
        </div>
        {loading && (
          <div className="text-sm text-nofx-text-muted">Loading...</div>
        )}
      </div>

      {/* Test info */}
      <div className="p-8 space-y-4">
        {error ? (
          <div className="text-center">
            <div className="text-2xl mb-2">⚠️</div>
            <div className="text-nofx-danger">{error}</div>
          </div>
        ) : (
          <>
            <div className="p-4 rounded bg-nofx-bg-lighter border border-nofx-border">
              <div className="text-sm mb-2 text-nofx-text-muted">
                Binance Kline Data
              </div>
              <div className="text-2xl font-bold text-nofx-success">
                {klineCount} klines
              </div>
            </div>

            {traderID && (
              <div className="p-4 rounded bg-nofx-bg-lighter border border-nofx-border">
                <div className="text-sm mb-2 text-nofx-text-muted">
                  Historical Order Data
                </div>
                <div className="text-2xl font-bold text-nofx-gold">
                  {orderCount} orders
                </div>
              </div>
            )}

            <div className="p-4 rounded bg-nofx-bg-lighter border border-nofx-border">
              <div className="text-sm mb-2 text-nofx-text-muted">Status</div>
              <div className="text-lg text-nofx-text">
                ✅ Data fetched successfully, chart component in development
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
