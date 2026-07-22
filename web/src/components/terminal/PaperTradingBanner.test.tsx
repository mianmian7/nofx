import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { PaperTradingBanner } from './TerminalDashboard'

describe('PaperTradingBanner', () => {
  it('shows wallet, available balance, and used margin separately', () => {
    render(
      <PaperTradingBanner
        paper={{
          balance: 9998.25,
          equity: 10005.15,
          available_balance: 8831.58,
          used_margin: 1166.67,
          realized_pnl: -1.75,
          unrealized_pnl: 6.9,
          fees: 1.75,
          open_positions: 1,
          closed_trades: 0,
          wins: 0,
          win_rate: 0,
          max_drawdown: 0,
        }}
      />
    )

    expect(screen.getByText(/钱包 \$9,998\.25/)).toBeInTheDocument()
    expect(screen.getByText(/可用 \$8,831\.58/)).toBeInTheDocument()
    expect(screen.getByText(/占用保证金 \$1,166\.67/)).toBeInTheDocument()
  })
})
