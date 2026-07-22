import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  PaperFundingPanel,
  PaperMakerPanel,
  PaperTradingBanner,
} from './TerminalDashboard'

describe('Paper funding display', () => {
  it('shows funding net in the Paper banner and current/next funding details', () => {
    render(
      <>
        <PaperTradingBanner
          paper={{
            balance: 9998.75,
            equity: 9998.75,
            available_balance: 9498.75,
            used_margin: 500,
            realized_pnl: -1.25,
            unrealized_pnl: 0,
            fees: 0,
            open_positions: 1,
            closed_trades: 0,
            wins: 0,
            win_rate: 0,
            max_drawdown: 0.0125,
            funding_net: -1.25,
            funding_paid: 1.25,
            funding_received: 0,
          }}
        />
        <PaperFundingPanel
          statuses={[
            {
              symbol: 'XAUUSDT',
              funding_rate: 0.0001,
              mark_price: 4119.065,
              index_price: 4119.07,
              next_funding_time: Date.parse('2026-07-22T16:00:00Z'),
              updated_at: '2026-07-22T14:00:00Z',
            },
          ]}
          payments={[
            {
              id: 'XAUUSDT:long:1',
              symbol: 'XAUUSDT',
              side: 'long',
              quantity: 1,
              mark_price: 12500,
              funding_rate: 0.0001,
              funding_time: Date.parse('2026-07-22T12:00:00Z'),
              payment: 1.25,
              wallet_delta: -1.25,
              applied_at: '2026-07-22T12:00:01Z',
            },
          ]}
        />
      </>
    )

    expect(screen.getByText(/Funding 净额 -\$1\.25/)).toBeInTheDocument()
    expect(screen.getByText('XAUUSDT')).toBeInTheDocument()
    expect(screen.getByText(/0\.0100%/)).toBeInTheDocument()
    expect(screen.getByText(/下次结算/)).toBeInTheDocument()
    expect(screen.getByText(/最近 Funding -\$1\.25/)).toBeInTheDocument()
  })

  it('shows maker/taker fee split and auditable pending-order state', () => {
    render(
      <PaperMakerPanel
        pendingOrders={[
          {
            order_id: 42,
            symbol: 'MUUSDT',
            action: 'open_long',
            side: 'long',
            limit_price: 99.9,
            quantity: 3,
            filled_quantity: 1,
            remaining_quantity: 2,
            position_size_usd: 299.7,
            leverage: 3,
            reduce_only: false,
            status: 'PARTIALLY_FILLED',
            reprice_count: 1,
            created_at: '2026-07-22T12:00:00Z',
            updated_at: '2026-07-22T12:00:05Z',
            expires_at: '2026-07-22T12:00:20Z',
          },
        ]}
        events={[
          {
            order_id: 42,
            symbol: 'MUUSDT',
            action: 'open_long',
            status: 'PARTIALLY_FILLED',
            limit_price: 99.9,
            quantity: 3,
            filled_quantity: 1,
            is_maker: true,
            time: '2026-07-22T12:00:05Z',
          },
        ]}
        makerFees={0.02}
        takerFees={0.5}
      />
    )

    expect(screen.getByText(/MAKER FIRST/)).toBeInTheDocument()
    expect(screen.getByText(/Maker \$0\.02/)).toBeInTheDocument()
    expect(screen.getByText(/Taker \$0\.5/)).toBeInTheDocument()
    expect(screen.getAllByText(/MUUSDT/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/部分成交/).length).toBeGreaterThan(0)
  })
})
