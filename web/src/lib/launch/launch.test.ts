import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  describeLaunchFailures,
  failedLaunchChecks,
  primarySetupTarget,
  setupTargetForCheck,
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
    min_ai_fee_usdc: 1,
    min_trading_usdc: 12,
    checked_at: new Date().toISOString(),
  }
}

describe('setupTargetForCheck', () => {
  it('routes generic model problems separately from claw402 wallet problems', () => {
    expect(setupTargetForCheck({ id: 'ai_model', status: 'failed' })).toBe(
      'model'
    )
    expect(setupTargetForCheck({ id: 'ai_wallet', status: 'failed' })).toBe(
      'claw402'
    )
    expect(
      setupTargetForCheck({ id: 'ai_wallet_funds', status: 'failed' })
    ).toBe('claw402')
  })

  it('routes exchange config/account problems to generic exchange setup', () => {
    expect(
      setupTargetForCheck({ id: 'exchange_config', status: 'failed' })
    ).toBe('exchange')
    expect(
      setupTargetForCheck({ id: 'exchange_account', status: 'failed' })
    ).toBe('exchange')
  })

  it('routes funding shortfalls to generic exchange setup', () => {
    expect(
      setupTargetForCheck({ id: 'exchange_funds', status: 'failed' })
    ).toBe('exchange')
  })

  it('has no anchor for strategy problems', () => {
    expect(setupTargetForCheck({ id: 'strategy', status: 'failed' })).toBeNull()
  })
})

describe('primarySetupTarget', () => {
  it('returns the anchor for the first failing check', () => {
    const result = preflightResult([
      { id: 'ai_model', status: 'ok' },
      { id: 'ai_wallet_funds', status: 'failed', message: 'AI wallet empty.' },
      { id: 'exchange_funds', status: 'failed', message: 'Low margin.' },
    ])
    expect(primarySetupTarget(result)).toBe('claw402')
  })

  it('returns null when everything passes', () => {
    const result = preflightResult([
      { id: 'ai_model', status: 'ok' },
      { id: 'exchange_funds', status: 'warning' },
    ])
    expect(primarySetupTarget(result)).toBeNull()
  })
})

describe('describeLaunchFailures / failedLaunchChecks', () => {
  it('collects only failed checks and joins their messages', () => {
    const result = preflightResult([
      { id: 'ai_wallet_funds', status: 'failed', message: 'AI wallet empty.' },
      { id: 'exchange_funds', status: 'warning', message: 'Testnet funds.' },
      { id: 'exchange_account', status: 'failed', message: 'Bad key.' },
      { id: 'strategy', status: 'skipped' },
    ])
    expect(failedLaunchChecks(result)).toHaveLength(2)
    expect(describeLaunchFailures(result)).toBe('AI wallet empty. Bad key.')
  })
})

describe('pickTradingModel', () => {
  const base: Partial<AIModel> = { enabled: true }

  it('prefers a user-owned direct model over claw402', () => {
    const models = [
      { ...base, id: 'openai', provider: 'openai', has_api_key: true },
      { ...base, id: 'c402', provider: 'claw402', has_api_key: true },
    ] as AIModel[]
    expect(pickTradingModel(models)?.id).toBe('openai')
  })

  it('accepts a claw402 model with only a wallet address', () => {
    const models = [
      { ...base, id: 'c402', provider: 'claw402', walletAddress: '0xabc' },
    ] as AIModel[]
    expect(pickTradingModel(models)?.id).toBe('c402')
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

  it('does not select Hyperliquid after migration to Binance execution', () => {
    const ready = {
      id: 'hl',
      exchange_type: 'hyperliquid',
      enabled: true,
      has_api_key: true,
      hyperliquidBuilderApproved: true,
      hyperliquidWalletAddr: '0x1',
    } as unknown as Exchange
    expect(pickTradingExchange([ready])).toBeNull()
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
