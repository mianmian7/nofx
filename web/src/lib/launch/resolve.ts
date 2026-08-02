import { api } from '../api'
import type { AIModel, Exchange } from '../../types'

export function modelHasCredential(model: AIModel) {
  return Boolean(model.has_api_key || model.apiKey)
}

export function exchangeHasKey(exchange: Exchange) {
  return Boolean(
    exchange.has_api_key ||
      exchange.apiKey ||
      (exchange.exchange_type === 'hyperliquid' &&
        exchange.hyperliquidWalletAddr)
  )
}

export function pickTradingModel(models: AIModel[]) {
  return models.find((model) => model.enabled && modelHasCredential(model)) || null
}

export function pickTradingExchange(exchanges: Exchange[]) {
  return (
    exchanges.find(
      (exchange) =>
        exchange.exchange_type === 'binance' &&
        exchange.enabled &&
        exchangeHasKey(exchange)
    ) ||
    exchanges.find((exchange) => exchange.enabled && exchangeHasKey(exchange)) ||
    null
  )
}

/** Resolves an explicitly configured launch-capable AI model. */
export async function resolveLaunchModel(): Promise<AIModel | null> {
  return pickTradingModel(await api.getModelConfigs())
}

/**
 * Resolves a launch-capable exchange. Returns the exchange or a message
 * explaining the most specific missing prerequisite.
 */
export async function resolveLaunchExchange(): Promise<
  { exchange: Exchange } | { exchange: null; reason: string }
> {
  const exchanges = await api.getExchangeConfigs()
  const ready = pickTradingExchange(exchanges)
  if (ready) return { exchange: ready }

  return {
    exchange: null,
    reason: 'No enabled exchange with usable credentials is available. Configure a supported exchange first.',
  }
}
