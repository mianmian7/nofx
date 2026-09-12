import axios, {
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { HttpClient } from './httpClient'

vi.mock('sonner', () => ({ toast: { error: vi.fn() } }))

afterEach(() => vi.restoreAllMocks())

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
