import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { LanguageProvider } from '../../contexts/LanguageContext'
import type { PublicTraderConfigData } from '../../types'
import { TraderConfigViewModal } from './TraderConfigViewModal'

describe('TraderConfigViewModal', () => {
  it('renders the public trader config returned by the competition API', () => {
    const publicTraderConfig = {
      trader_id: 'trader-1',
      trader_name: 'Demo Trader',
      ai_model: 'deepseek',
      exchange: 'binance',
      is_running: true,
      invert_signals: false,
    } satisfies PublicTraderConfigData

    render(
      <LanguageProvider>
        <TraderConfigViewModal
          isOpen
          onClose={vi.fn()}
          traderData={publicTraderConfig}
        />
      </LanguageProvider>
    )

    expect(screen.getByText('DEEPSEEK')).toBeInTheDocument()
    expect(screen.getByText('BINANCE')).toBeInTheDocument()
  })
})
