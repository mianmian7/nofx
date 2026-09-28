import { API_BASE, httpClient } from './helpers'

export interface SignalFrame {
  timeframe: string
  closed_at: number
  price: number
  ema20: number
  ema50: number
  rsi: number
  atr_percent: number
  volume_ratio: number
  trend: number
  momentum: number
  breakout: number
}
export interface LocalSignal {
  exchange: string
  symbol: string
  version: string
  direction: 'long' | 'short' | 'wait'
  score: number
  confirmed: boolean
  observed_at: number
  closed_at: number
  frames: SignalFrame[]
  funding_rate: number | null
  funding_time?: number
  open_interest: number | null
  open_interest_unit?: string
  open_interest_observed_at?: number
  warnings: string[]
}
export interface SignalSnapshot {
  exchange: string
  observed_at: number
  expected: number
  succeeded: number
  error: string
  failures: string[]
  results: LocalSignal[]
  ready: boolean
  scanning: boolean
  stale: boolean
  next_scan_at: number
  version: string
}
export interface SignalHistory {
  history: LocalSignal[]
  retention_days: number
}

export async function getLocalSignals(
  exchange: string
): Promise<SignalSnapshot> {
  const result = await httpClient.request<SignalSnapshot>(
    `${API_BASE}/local-signals?exchange=${encodeURIComponent(exchange)}`,
    { silent: true }
  )
  if (!result.success || !result.data)
    throw new Error(result.message || 'Market analysis unavailable')
  return result.data
}

export async function getLocalSignalHistory(
  exchange: string,
  symbol: string,
  before = 0
): Promise<SignalHistory> {
  const result = await httpClient.request<SignalHistory>(
    `${API_BASE}/local-signals/history?exchange=${encodeURIComponent(exchange)}&symbol=${encodeURIComponent(symbol)}&before=${before}`,
    { silent: true }
  )
  if (!result.success || !result.data)
    throw new Error(result.message || 'Analysis history unavailable')
  return result.data
}

export function signalIsCurrent(
  signal: LocalSignal,
  now = Date.now()
): boolean {
  const periods: Record<string, number> = {
    '15m': 900000,
    '1h': 3600000,
    '4h': 14400000,
  }
  const age = now - signal.observed_at
  if (age < 0 || age > 600000 || signal.frames.length !== 3) return false
  return ['15m', '1h', '4h'].every((tf, i) => {
    const frame = signal.frames[i]
    return (
      frame.timeframe === tf &&
      frame.closed_at < now &&
      frame.closed_at >=
        Math.floor((now - 120000) / periods[tf]) * periods[tf] - 1
    )
  })
}
