import { api } from '../api'
import { ApiError } from '../httpClient'
import { describeLaunchFailures } from './preflight'
import { resolveLaunchExchange, resolveLaunchModel } from './resolve'
import type { LaunchOutcome } from './types'

export const AUTOPILOT_TRADER_NAME = 'NOFX Autopilot'

export interface LaunchAutopilotOptions {
  /**
   * Provides the strategy id to trade. Called only AFTER preflight passes so
   * a failed launch never mutates or activates a strategy as a side effect.
   */
  ensureStrategy: () => Promise<string>
  scanIntervalMinutes?: number
}

/**
 * The single Autopilot launch path shared by Strategy Studio and the guided
 * launch panel. Order matters: resolve → preflight (server-side, fresh
 * balances) → strategy → create/update trader → start. No side effects happen
 * before preflight passes.
 */
export async function launchAutopilot(
  options: LaunchAutopilotOptions
): Promise<LaunchOutcome> {
  const { ensureStrategy, scanIntervalMinutes = 5 } = options

  try {
    const model = await resolveLaunchModel()
    if (!model) {
      return {
        ok: false,
        kind: 'setup',
        message: 'No enabled AI model with valid credentials is ready.',
      }
    }

    const exchangeResult = await resolveLaunchExchange()
    if (!exchangeResult.exchange) {
      return {
        ok: false,
        kind: 'setup',
        message: exchangeResult.reason,
      }
    }
    const exchange = exchangeResult.exchange

    // Autopilot defaults to Paper Trading. It uses public Binance market data
    // and must not require exchange trading permission or account balance.

    const strategyId = await ensureStrategy()

    const traderRequest = {
      name: AUTOPILOT_TRADER_NAME,
      ai_model_id: model.id,
      exchange_id: exchange.id,
      strategy_id: strategyId,
      scan_interval_minutes: scanIntervalMinutes,
      is_cross_margin: true,
      show_in_competition: true,
      execution_mode: 'paper' as const,
    }

    // Re-fetch the live trader list before deciding create vs update. Stale
    // props/snapshots would create a duplicate and orphan dashboards onto a
    // deleted id.
    const existingTraders = await api.getTraders(true)
    const existing =
      existingTraders.find(
        (trader) => trader.trader_name === AUTOPILOT_TRADER_NAME
      ) || null

    const autopilot = existing
      ? await api.updateTrader(existing.trader_id, traderRequest)
      : await api.createTrader(traderRequest)

    if (!autopilot.is_running) {
      try {
        await api.startTrader(autopilot.trader_id)
      } catch (err) {
        // Launch is idempotent: the update path restarts a running trader
        // asynchronously, so a racing "already running" rejection is success.
        const alreadyRunning =
          err instanceof ApiError &&
          err.errorKey === 'trader.start.already_running'
        if (!alreadyRunning) throw err
      }
    }

    return {
      ok: true,
      traderId: autopilot.trader_id,
      warning: autopilot.startup_warning,
    }
  } catch (err) {
    // The server re-runs preflight on start; surface its structured result if
    // readiness changed between our check and the start call.
    if (
      err instanceof ApiError &&
      err.errorKey === 'trader.start.preflight_failed'
    ) {
      const preflight = err.errorData?.preflight
      if (preflight) {
        return {
          ok: false,
          kind: 'preflight',
          message: describeLaunchFailures(preflight) || err.message,
          preflight,
        }
      }
    }
    return {
      ok: false,
      kind: 'error',
      message:
        err instanceof Error ? err.message : 'Failed to launch NOFX Autopilot',
    }
  }
}

/**
 * Default strategy provisioning for the guided panel: reuse an active local
 * or static strategy, otherwise create the local Binance dynamic default.
 */
export async function ensureDefaultStrategy(): Promise<string> {
  const strategies = await api.getStrategies()
  const existing =
    strategies.find(
      (strategy) =>
        strategy.is_active &&
        ['binance_dynamic', 'static'].includes(
          strategy.config?.ai_config?.coin_source?.source_type || ''
        )
    ) ||
    strategies.find((strategy) =>
      strategy.name.toLowerCase().includes('local dynamic')
    )

  if (existing) {
    if (!existing.is_active) {
      await api.activateStrategy(existing.id)
    }
    return existing.id
  }

  const config = await api.getDefaultStrategyConfig()
  const created = await api.createStrategy({
    name: 'NOFX Local Dynamic Strategy',
    description:
      'Public Binance market data builds the candidate pool; the configured AI model makes the final decision.',
    config,
  })
  if (created?.id) {
    await api.activateStrategy(created.id)
    return created.id
  }

  const refreshed = await api.getStrategies()
  const fallback = refreshed.find((strategy) =>
    strategy.name.toLowerCase().includes('local dynamic')
  )
  if (!fallback) throw new Error('Failed to create local dynamic strategy')
  await api.activateStrategy(fallback.id)
  return fallback.id
}
