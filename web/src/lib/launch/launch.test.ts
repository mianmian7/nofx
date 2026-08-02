import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  describeLaunchFailures,
  failedLaunchChecks,
  launchWarnings,
} from './preflight'
import { pickTradingExchange, pickTradingModel } from './resolve'
import type { LaunchCheck, LaunchPreflightResult } from './types'
import type { AIModel, Exchange } from '../../types'

vi.mock('../api', () => ({ api: {} }))
vi.mock('../api/helpers', () => ({
  API_BASE: '',
  httpClient: { get: vi.fn(), post: vi.fn() },
}))

function preflightResult(checks: LaunchCheck[]): LaunchPreflightResult {
  return {
    ready: checks.every((check) => check.status !== 'failed'),
    checks,
    min_trading_usdc: 12,
    checked_at: new Date().toISOString(),
  }
}

describe('launch preflight helpers', () => {
  it('collects failed and warning checks separately', () => {
    const result = preflightResult([
      { id: 'ai_model', status: 'failed', message: 'Model is not configured.' },
      { id: 'exchange_funds', status: 'warning', message: 'Testnet funds.' },
      { id: 'exchange_account', status: 'failed', message: 'Bad key.' },
      { id: 'strategy', status: 'skipped' },
    ])

    expect(failedLaunchChecks(result)).toHaveLength(2)
    expect(launchWarnings(result)).toHaveLength(1)
    expect(describeLaunchFailures(result)).toBe(
      'Model is not configured. Bad key.'
    )
  })
})

describe('pickTradingModel', () => {
  const base: Partial<AIModel> = { enabled: true }

  it('selects the first enabled model with credentials', () => {
    const models = [
      { ...base, id: 'openai', provider: 'openai', has_api_key: true },
    ] as AIModel[]
    expect(pickTradingModel(models)?.id).toBe('openai')
  })

  it('returns null when nothing usable exists', () => {
    const models = [
      { id: 'x', provider: 'openai', enabled: false, has_api_key: true },
      { id: 'y', provider: 'deepseek', enabled: true },
    ] as AIModel[]
    expect(pickTradingModel(models)).toBeNull()
  })
})

describe('pickTradingExchange', () => {
  beforeEach(() => vi.clearAllMocks())

  it('accepts a native Hyperliquid account with wallet credentials', () => {
    const ready = {
      id: 'hl',
      exchange_type: 'hyperliquid',
      enabled: true,
      has_api_key: true,
      hyperliquidBuilderApproved: true,
      hyperliquidWalletAddr: '0x1',
    } as unknown as Exchange
    expect(pickTradingExchange([ready])?.id).toBe('hl')
  })

  it('accepts an enabled Binance account without Hyperliquid setup', () => {
    const binance = {
      id: 'binance',
      exchange_type: 'binance',
      enabled: true,
      has_api_key: true,
    } as unknown as Exchange
    const hyperliquid = {
      id: 'hl',
      exchange_type: 'hyperliquid',
      enabled: true,
      has_api_key: true,
      hyperliquidBuilderApproved: true,
      hyperliquidWalletAddr: '0x1',
    } as unknown as Exchange

    expect(pickTradingExchange([hyperliquid, binance])?.id).toBe('binance')
  })

  it('preserves other enabled non-Hyperliquid venues', () => {
    const bybit = {
      id: 'bybit',
      exchange_type: 'bybit',
      enabled: true,
      has_api_key: true,
    } as unknown as Exchange
    expect(pickTradingExchange([bybit])?.id).toBe('bybit')
  })
})
