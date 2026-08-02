import { API_BASE, httpClient } from '../api/helpers'
import type { LaunchCheck, LaunchPreflightResult } from './types'

export interface LaunchPreflightRequest {
  ai_model_id: string
  exchange_id: string
  strategy_id?: string
}

/**
 * Server-side launch readiness checks. This is the single source of truth for
 * balances and minimums — never gate the UI on cached client-side values.
 */
export async function runLaunchPreflight(
  request: LaunchPreflightRequest
): Promise<LaunchPreflightResult> {
  const result = await httpClient.post<LaunchPreflightResult>(
    `${API_BASE}/launch/preflight`,
    request
  )
  if (!result.success || !result.data) {
    throw new Error(result.message || 'Failed to run launch preflight')
  }
  return result.data
}

export async function getTraderPreflight(
  traderId: string
): Promise<LaunchPreflightResult> {
  const result = await httpClient.get<LaunchPreflightResult>(
    `${API_BASE}/traders/${traderId}/preflight`
  )
  if (!result.success || !result.data) {
    throw new Error(result.message || 'Failed to run launch preflight')
  }
  return result.data
}

export function failedLaunchChecks(
  result: LaunchPreflightResult
): LaunchCheck[] {
  return result.checks.filter((check) => check.status === 'failed')
}

export function launchWarnings(result: LaunchPreflightResult): LaunchCheck[] {
  return result.checks.filter((check) => check.status === 'warning')
}

/** Human-readable one-liner combining every failing check. */
export function describeLaunchFailures(result: LaunchPreflightResult): string {
  return failedLaunchChecks(result)
    .map((check) => check.message)
    .filter(Boolean)
    .join(' ')
}
