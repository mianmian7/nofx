import type { PropsWithChildren } from 'react'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { api } from '../lib/api'
import type { TraderInfo } from '../types'
import { useMyTraders } from './useMyTraders'

const auth = vi.hoisted(() => ({
  user: { id: 'alice' } as { id: string } | null,
  token: 'test-token' as string | null,
}))
vi.mock('../contexts/AuthContext', () => ({ useAuth: () => auth }))
vi.mock('../lib/api', () => ({ api: { getTraders: vi.fn() } }))

function Wrapper({ children }: PropsWithChildren) {
  return (
    <SWRConfig
      value={{
        provider: () => new Map(),
        revalidateOnFocus: false,
        revalidateOnReconnect: false,
      }}
    >
      {children}
    </SWRConfig>
  )
}

function useRouteAndPage() {
  const route = useMyTraders({ refreshInterval: 0 })
  const page = useMyTraders()
  return {
    route: { data: route.data, mutate: route.mutate },
    page: { data: page.data, mutate: page.mutate },
  }
}

function tradersFor(id: string): TraderInfo[] {
  return [{ trader_id: id, trader_name: id } as TraderInfo]
}

describe('useMyTraders', () => {
  beforeEach(() => {
    auth.user = { id: 'alice' }
    auth.token = 'test-token'
    vi.mocked(api.getTraders)
      .mockReset()
      .mockResolvedValue(tradersFor('alice-1'))
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('deduplicates route/page requests and shares bound mutation results', async () => {
    const { result } = renderHook(useRouteAndPage, { wrapper: Wrapper })
    await waitFor(() =>
      expect(result.current.page.data?.[0].trader_id).toBe('alice-1')
    )
    expect(api.getTraders).toHaveBeenCalledTimes(1)
    expect(result.current.route.data).toBe(result.current.page.data)

    vi.mocked(api.getTraders).mockResolvedValue(tradersFor('new-trader'))
    await act(async () => {
      await result.current.page.mutate()
    })
    expect(api.getTraders).toHaveBeenCalledTimes(2)
    expect(result.current.route.data?.[0].trader_id).toBe('new-trader')
  })

  it('uses one polling owner for the route and page', async () => {
    vi.useFakeTimers()
    const { result } = renderHook(useRouteAndPage, { wrapper: Wrapper })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1)
    })
    expect(result.current.page.data?.[0].trader_id).toBe('alice-1')
    expect(api.getTraders).toHaveBeenCalledTimes(1)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.getTraders).toHaveBeenCalledTimes(2)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.getTraders).toHaveBeenCalledTimes(3)
  })

  it('hides the previous account list while the next account loads', async () => {
    const { result, rerender } = renderHook(useRouteAndPage, {
      wrapper: Wrapper,
    })
    await waitFor(() =>
      expect(result.current.page.data?.[0].trader_id).toBe('alice-1')
    )
    let resolveNext!: (traders: TraderInfo[]) => void
    vi.mocked(api.getTraders).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveNext = resolve
        })
    )
    auth.user = { id: 'bob' }
    rerender()
    expect(result.current.route.data).toBeUndefined()
    expect(result.current.page.data).toBeUndefined()
    await act(async () => {
      resolveNext(tradersFor('bob-1'))
    })
    expect(result.current.page.data?.[0].trader_id).toBe('bob-1')
    expect(result.current.route.data).toBe(result.current.page.data)

    auth.user = null
    auth.token = null
    rerender()
    expect(result.current.page.data).toBeUndefined()
    expect(api.getTraders).toHaveBeenCalledTimes(2)
  })

  it('does not fetch private lists without a session', () => {
    auth.user = null
    auth.token = null
    renderHook(useRouteAndPage, { wrapper: Wrapper })
    expect(api.getTraders).not.toHaveBeenCalled()
  })
})
