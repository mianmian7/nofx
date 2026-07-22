import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ExecutionModeSelector } from './ExecutionModeSelector'

describe('ExecutionModeSelector', () => {
  it('defaults visibly to Paper and warns that it never sends real orders', () => {
    const onChange = vi.fn()
    render(<ExecutionModeSelector value="paper" onChange={onChange} language="zh" />)
    expect(screen.getByText('实时模拟交易，不会真实下单')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Paper Trading/ })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByRole('button', { name: /Live Trading/ }))
    expect(onChange).toHaveBeenCalledWith('live')
  })
})
