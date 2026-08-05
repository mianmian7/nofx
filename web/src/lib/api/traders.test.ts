import { beforeEach, describe, expect, it, vi } from 'vitest'

const request = vi.hoisted(() => vi.fn())

vi.mock('./helpers', () => ({
  API_BASE: '/api',
  httpClient: { request },
}))

import { traderApi } from './traders'

describe('traderApi.startTrader', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    request.mockResolvedValue({ success: true })
  })

  it('starts paper traders without a live confirmation query', async () => {
    await traderApi.startTrader('paper-1')

    expect(request).toHaveBeenCalledWith('/api/traders/paper-1/start', {
      method: 'POST',
      params: undefined,
      timeout: 120_000,
    })
  })

  it('sends live confirmation only when explicitly requested', async () => {
    await traderApi.startTrader('live-1', { liveConfirm: true })

    expect(request).toHaveBeenCalledWith('/api/traders/live-1/start', {
      method: 'POST',
      params: { live_confirm: 'true' },
      timeout: 120_000,
    })
  })
})
