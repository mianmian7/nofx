import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { SystemStatus } from '../../types'
import { AIModelRuntimeStatus } from './AIModelRuntimeStatus'

const baseStatus: SystemStatus = {
  trader_id: 'trader-1',
  trader_name: 'Trader',
  ai_model: 'gpt-primary',
  ai_provider: 'openai',
  is_running: true,
  start_time: '',
  runtime_minutes: 1,
  call_count: 1,
  initial_balance: 1000,
  scan_interval: '15m',
  stop_until: '',
  last_reset_time: '',
}

describe('AIModelRuntimeStatus', () => {
  it('shows the active fallback model, reason, and switch time', () => {
    render(
      <AIModelRuntimeStatus
        language="en"
        status={{
          ...baseStatus,
          ai_provider: 'deepseek',
          ai_model: 'deepseek-chat',
          is_fallback: true,
          fallback_reason: 'rate_limited',
          fallback_since: '2026-07-30T08:00:00Z',
        }}
      />
    )

    expect(screen.getByText('deepseek/deepseek-chat')).toBeVisible()
    expect(screen.getByText('Running on fallback')).toBeVisible()
    expect(screen.getByText('Rate limited')).toBeVisible()
    expect(screen.getByRole('time')).toHaveAttribute(
      'datetime',
      '2026-07-30T08:00:00Z'
    )
  })

  it('shows primary state without stale failover details after recovery', () => {
    render(
      <AIModelRuntimeStatus
        language="en"
        status={{ ...baseStatus, is_fallback: false }}
      />
    )

    expect(screen.getByText('openai/gpt-primary')).toBeVisible()
    expect(screen.getByText('Running on primary')).toBeVisible()
    expect(screen.queryByText('Failure reason:')).not.toBeInTheDocument()
    expect(screen.queryByRole('time')).not.toBeInTheDocument()
  })
})
