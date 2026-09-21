import type { PropsWithChildren } from 'react'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { SWRConfig } from 'swr'
import { api } from '../lib/api'
import { ApiError } from '../lib/httpClient'
import type { SystemStatus, TraderInfo } from '../types'
import { AppRoutes } from './AppRoutes'

vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({
    user: { id: 'alice' },
    token: 'test-token',
    isLoading: false,
  }),
}))
vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'zh', setLanguage: vi.fn() }),
}))
vi.mock('../hooks/useSystemConfig', () => ({
  useSystemConfig: () => ({ config: { initialized: true }, loading: false }),
}))
vi.mock('../components/common/HeaderBar', () => ({
  default: () => <div id="dash-header-slot" />,
}))
vi.mock('../components/common/SiteFooter', () => ({ SiteFooter: () => null }))
vi.mock('../components/auth/LoginRequiredOverlay', () => ({
  LoginRequiredOverlay: () => null,
}))
vi.mock('framer-motion', () => ({
  AnimatePresence: ({ children }: PropsWithChildren) => children,
  motion: { div: ({ children }: PropsWithChildren) => <div>{children}</div> },
}))
vi.mock('../components/terminal/OrderBook', () => ({ OrderBook: () => null }))
vi.mock('../components/terminal/KlineChart', () => ({ KlineChart: () => null }))
vi.mock('../lib/api', () => ({
  api: {
    getStatus: vi.fn(),
    getTraders: vi.fn(),
    getAccount: vi.fn(),
    getPositions: vi.fn(),
    getLatestDecisions: vi.fn(),
    getFullStats: vi.fn(),
    getPositionHistory: vi.fn(),
    getTraderConfig: vi.fn(),
  },
}))

const traders = [
  { trader_id: 'alpha-1', trader_name: 'Alpha', exchange_type: 'binance' },
  { trader_id: 'beta-2', trader_name: 'Beta', exchange_type: 'binance' },
] as TraderInfo[]

function statusFor(id: string, running: boolean): SystemStatus {
  return { trader_id: id, is_running: running, call_count: 8 } as SystemStatus
}

async function advance(ms = 1) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

async function openDashboard() {
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppRoutes />
      </MemoryRouter>
    </SWRConfig>
  )
  await advance()
}

describe('dashboard status request and display', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.resetAllMocks()
    vi.mocked(api.getTraders).mockResolvedValue(traders)
    vi.mocked(api.getStatus).mockResolvedValue(statusFor('alpha-1', true))
    vi.mocked(api.getPositions).mockResolvedValue([])
    vi.mocked(api.getLatestDecisions).mockResolvedValue([])
  })

  afterEach(() => {
    cleanup()
    vi.clearAllTimers()
    vi.useRealTimers()
  })

  it('keeps successful status in the existing headers without an extra status bar', async () => {
    await openDashboard()
    expect(screen.getAllByText(/^● 运行中$/)).toHaveLength(2)
    expect(
      screen.queryByRole('button', { name: '刷新状态' })
    ).not.toBeInTheDocument()
  })

  it('shows loading rather than stopped before the initial status arrives', async () => {
    let resolve!: (status: SystemStatus) => void
    vi.mocked(api.getStatus).mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    await openDashboard()
    expect(screen.getAllByText(/状态加载中/).length).toBeGreaterThan(0)
    expect(screen.queryByText(/已停止/)).not.toBeInTheDocument()
    await act(async () => {
      resolve(statusFor('alpha-1', false))
    })
    expect(screen.getAllByText(/已停止/).length).toBeGreaterThan(0)
    expect(screen.queryByText(/状态加载中/)).not.toBeInTheDocument()
  })

  it('shows unavailable, not stopped, when there are no traders', async () => {
    vi.mocked(api.getTraders).mockResolvedValue([])
    await openDashboard()
    expect(screen.getAllByText(/状态不可用/).length).toBeGreaterThan(0)
    expect(screen.queryByText(/已停止/)).not.toBeInTheDocument()
    expect(api.getStatus).not.toHaveBeenCalled()
  })

  it.each([400, 404])(
    'surfaces HTTP %i without automatic retries, and allows explicit refresh',
    async (code) => {
      vi.mocked(api.getStatus).mockRejectedValue(
        new ApiError('Trader not found', undefined, undefined, code)
      )
      await openDashboard()
      expect(screen.getByRole('alert')).toHaveTextContent('Trader not found')
      expect(screen.queryByText(/已停止/)).not.toBeInTheDocument()
      await advance(120_000)
      expect(api.getStatus).toHaveBeenCalledTimes(1)
      vi.mocked(api.getStatus).mockResolvedValue(statusFor('alpha-1', true))
      fireEvent.click(screen.getByRole('button', { name: '刷新状态' }))
      await advance()
      expect(api.getStatus).toHaveBeenCalledTimes(2)
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(screen.getAllByText(/● 运行中/).length).toBeGreaterThan(0)
    }
  )

  it.each([503, undefined])(
    'bounds retries for temporary error %s and recovers on refresh',
    async (code) => {
      vi.mocked(api.getStatus).mockRejectedValue(
        new ApiError('Temporarily unavailable', undefined, undefined, code)
      )
      await openDashboard()
      await advance(120_000)
      expect(api.getStatus).toHaveBeenCalledTimes(3)
      expect(screen.getByRole('alert')).toHaveTextContent(
        'Temporarily unavailable'
      )
      vi.mocked(api.getStatus).mockResolvedValue(statusFor('alpha-1', false))
      fireEvent.click(screen.getByRole('button', { name: '刷新状态' }))
      await advance()
      expect(api.getStatus).toHaveBeenCalledTimes(4)
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(screen.getAllByText(/已停止/).length).toBeGreaterThan(0)
    }
  )

  it('marks retained status stale after polling fails, then removes the warning on recovery', async () => {
    await openDashboard()
    expect(screen.getAllByText(/● 运行中/).length).toBeGreaterThan(0)
    vi.mocked(api.getStatus).mockRejectedValue(
      new ApiError('Trader unavailable', undefined, undefined, 503)
    )
    await advance(15_000)
    expect(screen.getByRole('alert')).toHaveTextContent('Trader unavailable')
    expect(screen.getAllByText(/状态已过期/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/上次：运行中/).length).toBeGreaterThan(0)
    expect(screen.queryByText(/● 运行中/)).not.toBeInTheDocument()
    vi.mocked(api.getStatus).mockResolvedValue(statusFor('alpha-1', false))
    fireEvent.click(screen.getByRole('button', { name: '刷新状态' }))
    await advance()
    expect(screen.queryByText(/状态已过期/)).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getAllByText(/已停止/).length).toBeGreaterThan(0)
  })

  it('does not show the previous trader status while a newly selected trader is loading', async () => {
    await openDashboard()
    let resolve!: (status: SystemStatus) => void
    vi.mocked(api.getStatus).mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    fireEvent.change(screen.getAllByRole('combobox')[0], {
      target: { value: 'beta-2' },
    })
    await advance()
    expect(screen.getAllByText(/状态加载中/).length).toBeGreaterThan(0)
    expect(screen.queryByText(/● 运行中|已停止/)).not.toBeInTheDocument()
    await act(async () => {
      resolve(statusFor('beta-2', false))
    })
    expect(screen.getAllByText(/已停止/).length).toBeGreaterThan(0)
  })

  it('clears selection and stops polling the removed trader when the list becomes empty', async () => {
    await openDashboard()
    vi.mocked(api.getTraders).mockResolvedValue([])
    await advance(10_000)
    expect(screen.getAllByText(/状态不可用/).length).toBeGreaterThan(0)
    expect(screen.queryByText(/● 运行中|已停止/)).not.toBeInTheDocument()
    await advance(60_000)
    expect(api.getStatus).toHaveBeenCalledTimes(1)
  })
})
