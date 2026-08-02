export type LaunchCheckStatus = 'ok' | 'failed' | 'warning' | 'skipped'

export type LaunchCheckId =
  | 'ai_model'
  | 'strategy'
  | 'exchange_config'
  | 'exchange_account'
  | 'exchange_funds'

export interface LaunchCheck {
  id: LaunchCheckId | string
  status: LaunchCheckStatus
  code?: string
  message?: string
  required?: number
  actual?: number
  asset?: string
}

export interface LaunchPreflightResult {
  ready: boolean
  checks: LaunchCheck[]
  min_trading_usdc: number
  checked_at: string
}

export type LaunchOutcome =
  | { ok: true; traderId: string; warning?: string }
  | {
      ok: false
      kind: 'preflight'
      message: string
      preflight: LaunchPreflightResult
    }
  | { ok: false; kind: 'setup'; message: string }
  | { ok: false; kind: 'error'; message: string }
