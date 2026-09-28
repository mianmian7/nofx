import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { PaperMakerPanel, PaperTradingBanner } from './TerminalDashboard'

describe('PaperMakerPanel', () => {
  it('keeps cancellation history visible after a different order fills', () => {
    render(
      <PaperMakerPanel
        makerFees={0.01}
        takerFees={0}
        events={[
          {
            order_id: 1,
            symbol: 'WLDUSDT',
            action: 'open_long',
            status: 'NEW',
            limit_price: 0.5568,
            quantity: 100,
            is_maker: true,
            time: '2026-09-27T15:32:50Z',
          },
          {
            order_id: 1,
            symbol: 'WLDUSDT',
            action: 'open_long',
            status: 'CANCELED',
            reason: 'entry_risk_rejected',
            limit_price: 0.5568,
            quantity: 100,
            is_maker: true,
            time: '2026-09-27T15:33:09Z',
          },
          {
            order_id: 2,
            symbol: 'QNTUSDT',
            action: 'open_long',
            status: 'FILLED',
            limit_price: 176.62,
            quantity: 0.153,
            filled_quantity: 0.153,
            is_maker: true,
            time: '2026-09-27T15:48:09Z',
          },
        ]}
      />
    )
    const history = screen.getByTestId('paper-order-history')
    fireEvent.click(within(history).getByText('模拟订单记录（3）'))
    const rows = within(history).getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('QNTUSDT')
    expect(rows[0]).toHaveTextContent('已成交量 0.153')
    expect(rows[1]).toHaveTextContent('WLDUSDT')
    expect(rows[1]).toHaveTextContent('已取消')
    expect(rows[1]).toHaveTextContent('开仓条件或风险检查未通过')
    expect(rows[1]).not.toHaveTextContent('已成交量')
    expect(rows[2]).toHaveTextContent('挂单中')
    expect(history).toHaveTextContent('请求成功不代表已经成交')
  })

  it('does not describe an unknown event status as an active order', () => {
    render(
      <PaperMakerPanel
        makerFees={0}
        takerFees={0}
        events={[
          {
            order_id: 9,
            symbol: 'BTCUSDT',
            action: 'open_long',
            status: 'UNRECOGNIZED',
            limit_price: 100,
            quantity: 1,
            is_maker: false,
            time: 'invalid',
          },
        ]}
      />
    )
    expect(screen.getByTestId('paper-maker-panel')).not.toHaveTextContent(
      '挂单中'
    )
    expect(screen.getByTestId('paper-order-history')).toHaveTextContent(
      'UNRECOGNIZED'
    )
    expect(screen.getByTestId('paper-order-history')).toHaveTextContent(
      '时间未知'
    )
  })
})

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
