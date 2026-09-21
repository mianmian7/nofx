import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DecisionRecord, SystemStatus } from '../../types'
import { TerminalDashboard } from './TerminalDashboard'

vi.mock('swr', () => ({ default: () => ({}), mutate: vi.fn() }))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('./OrderBook', () => ({ OrderBook: () => null }))
vi.mock('./KlineChart', () => ({ KlineChart: () => null }))

const startedAt = new Date('2026-09-22T10:00:00Z').getTime()
const status = {
  trader_id: 'clock-trader',
  is_running: false,
  scan_interval: '1m0s',
  call_count: 2,
} as SystemStatus
const decision: DecisionRecord = {
  timestamp: new Date(startedAt).toISOString(),
  cycle_number: 2,
  system_prompt: '',
  input_prompt: '',
  cot_trace: '',
  decision_json: '',
  raw_response: '',
  account_state: {
    total_balance: 1000,
    available_balance: 1000,
    total_unrealized_profit: 0,
    position_count: 0,
    margin_used_pct: 0,
  },
  positions: [],
  candidate_coins: [],
  decisions: [],
  execution_log: [],
  success: true,
}

function dashboard(nextStatus = status) {
  return (
    <TerminalDashboard
      onTraderSelect={vi.fn()}
      status={nextStatus}
      decisions={[decision]}
    />
  )
}

function displayedClock(time: number) {
  return new Date(time).toLocaleTimeString('en-GB', { hour12: false })
}

describe('dashboard clock and countdown', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(startedAt)
  })

  afterEach(() => {
    cleanup()
    vi.clearAllTimers()
    vi.useRealTimers()
  })

  it('updates the wall clock and countdown each second and reaches due now', () => {
    render(dashboard())
    expect(
      screen.getByText(displayedClock(startedAt), {
        selector: '.terminal-context-clock',
      })
    ).toBeInTheDocument()
    expect(screen.getByText(/1m 0s/)).toBeInTheDocument()

    act(() => vi.advanceTimersByTime(1000))
    expect(
      screen.getByText(displayedClock(startedAt + 1000), {
        selector: '.terminal-context-clock',
      })
    ).toBeInTheDocument()
    expect(screen.getByText(/0m 59s/)).toBeInTheDocument()

    act(() => vi.advanceTimersByTime(59_000))
    expect(screen.getByText('due now')).toBeInTheDocument()
  })

  it('keeps one time snapshot across unrelated renders until the next tick', () => {
    const { rerender } = render(dashboard())
    // Changing wall time without firing a timer must not change render output.
    vi.setSystemTime(startedAt + 5000)
    rerender(dashboard({ ...status, call_count: 3 }))
    expect(
      screen.getByText(displayedClock(startedAt), {
        selector: '.terminal-context-clock',
      })
    ).toBeInTheDocument()
    expect(screen.getByText(/1m 0s/)).toBeInTheDocument()

    act(() => vi.advanceTimersByTime(1000))
    expect(
      screen.getByText(displayedClock(startedAt + 6000), {
        selector: '.terminal-context-clock',
      })
    ).toBeInTheDocument()
    expect(screen.getByText(/0m 54s/)).toBeInTheDocument()
  })

  it('clears its timer on unmount', () => {
    const before = vi.getTimerCount()
    const { unmount } = render(dashboard())
    expect(vi.getTimerCount()).toBeGreaterThan(before)
    unmount()
    expect(vi.getTimerCount()).toBe(before)
  })
})
