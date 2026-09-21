import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../httpClient'
import { dataApi } from './data'

const request = vi.hoisted(() => vi.fn())
vi.mock('./helpers', () => ({ API_BASE: '/api', httpClient: { request } }))

describe('dataApi.getStatus', () => {
  beforeEach(() => vi.clearAllMocks())

  it.each([400, 503])(
    'retains the backend message and status for HTTP %i',
    async (statusCode) => {
      const errorData = { error: 'Trader runtime is not loaded' }
      request.mockResolvedValue({
        success: false,
        message: errorData.error,
        statusCode,
        errorKey: 'trader.runtime_unavailable',
        errorData,
      })
      const failure = await dataApi
        .getStatus('trader-1', true)
        .catch((error) => error)
      expect(failure).toBeInstanceOf(ApiError)
      expect(failure).toMatchObject({
        message: errorData.error,
        statusCode,
        errorKey: 'trader.runtime_unavailable',
        errorData,
      })
    }
  )

  it('returns the successful status and safely encodes the selected ID', async () => {
    const status = { trader_id: 'trader&other=1', is_running: false }
    request.mockResolvedValue({ success: true, data: status })
    await expect(dataApi.getStatus(status.trader_id, true)).resolves.toBe(
      status
    )
    expect(request).toHaveBeenCalledWith(
      '/api/status?trader_id=trader%26other%3D1',
      { silent: true }
    )
  })

  it('preserves a resource error thrown by the HTTP client', async () => {
    const failure = new ApiError('Trader not found', undefined, undefined, 404)
    request.mockRejectedValue(failure)
    await expect(dataApi.getStatus('trader-1', true)).rejects.toBe(failure)
  })

  it('passes the caller cancellation signal to the transport', async () => {
    request.mockResolvedValue({ success: true, data: { is_running: false } })
    const controller = new AbortController()
    await dataApi.getStatus('trader-1', true, controller.signal)
    expect(request).toHaveBeenCalledWith('/api/status?trader_id=trader-1', {
      silent: true,
      signal: controller.signal,
    })
  })
})
