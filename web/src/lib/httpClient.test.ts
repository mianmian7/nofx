import axios, {
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { ApiError, HttpClient } from './httpClient'

vi.mock('sonner', () => ({ toast: { error: vi.fn() } }))

afterEach(() => vi.restoreAllMocks())
beforeEach(() => vi.clearAllMocks())

function errorClient(status: number, data: Record<string, unknown>) {
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    throw new axios.AxiosError(
      'Request failed',
      'ERR_BAD_RESPONSE',
      config,
      null,
      {
        data,
        status,
        statusText: 'Error',
        headers: {},
        config,
      }
    )
  })
  const instance = axios.create({ adapter })
  vi.spyOn(axios, 'create').mockReturnValue(instance)
  return new HttpClient()
}

describe('HTTP error details', () => {
  it.each([
    [403, 'Trader access denied'],
    [404, 'Trader not found'],
  ])(
    'preserves the %i resource error during a silent request',
    async (status, message) => {
      const data = { error: message, error_key: 'trader.unavailable' }
      const client = errorClient(status, data)
      const failure = await client
        .request('/api/status', { silent: true })
        .catch((error) => error)

      expect(failure).toBeInstanceOf(ApiError)
      expect(failure).toMatchObject({
        message,
        statusCode: status,
        errorKey: data.error_key,
        errorData: data,
      })
      expect(toast.error).not.toHaveBeenCalled()
    }
  )

  it('uses the resource error in the 404 notification', async () => {
    const client = errorClient(404, { error: 'Trader not found' })
    await expect(client.request('/api/status')).rejects.toThrow(
      'Trader not found'
    )
    expect(toast.error).toHaveBeenCalledWith(
      'Resource Not Found',
      expect.objectContaining({ description: 'Trader not found' })
    )
  })

  it('preserves a server status even when its body has no message', async () => {
    const client = errorClient(502, {})
    await expect(
      client.request('/api/status', { silent: true })
    ).rejects.toMatchObject({ statusCode: 502 })
  })

  it('keeps structured business and service-unavailable responses', async () => {
    for (const status of [400, 503]) {
      vi.restoreAllMocks()
      const data = {
        error: 'Trader runtime is not loaded',
        error_key: 'trader.runtime_unavailable',
      }
      const client = errorClient(status, data)
      await expect(
        client.request('/api/status', { silent: true })
      ).resolves.toMatchObject({
        success: false,
        message: data.error,
        statusCode: status,
        errorData: data,
      })
    }
  })
})

describe('HTTP request cancellation', () => {
  it('passes the signal to Axios and preserves cancellation without a toast', async () => {
    // A real Axios instance/interceptor chain, with only the transport replaced.
    const adapter = vi.fn(
      (config: InternalAxiosRequestConfig) =>
        new Promise<AxiosResponse>((_resolve, reject) => {
          config.signal?.addEventListener?.('abort', () => {
            reject(new axios.CanceledError('request canceled', config))
          })
        })
    )
    const instance = axios.create({ adapter })
    vi.spyOn(axios, 'create').mockReturnValue(instance)
    const client = new HttpClient()
    const controller = new AbortController()
    const request = client.request('/api/depth', { signal: controller.signal })
    const rejection = expect(request).rejects.toSatisfy(axios.isCancel)
    await vi.waitFor(() => expect(adapter).toHaveBeenCalledOnce())
    expect(adapter.mock.calls[0][0].signal).toBe(controller.signal)
    controller.abort()
    await rejection
    expect(toast.error).not.toHaveBeenCalled()
  })
})
