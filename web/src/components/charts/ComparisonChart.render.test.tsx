import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { LanguageProvider } from '../../contexts/LanguageContext'
import { ThemeProvider } from '../../contexts/ThemeContext'
import type { CompetitionTraderData } from '../../types'
import { ComparisonChart } from './ComparisonChart'

const getEquityHistoryBatchMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/api', () => ({
  api: {
    getEquityHistoryBatch: getEquityHistoryBatchMock,
  },
}))

function renderChart(traders: CompetitionTraderData[]) {
  return render(
    <ThemeProvider>
      <LanguageProvider>
        <ComparisonChart traders={traders} />
      </LanguageProvider>
    </ThemeProvider>
  )
}

const mockTraders: CompetitionTraderData[] = [
  {
    trader_id: 'trader-1',
    trader_name: '挑战-多周期-Luna',
    ai_model: 'luna',
    exchange: 'binance',
    total_equity: 103,
    total_pnl: 3,
    total_pnl_pct: 3.0,
    position_count: 1,
    margin_used_pct: 10,
    is_running: true,
  },
  {
    trader_id: 'trader-2',
    trader_name: '挑战-多周期-Gemini',
    ai_model: 'gemini',
    exchange: 'binance',
    total_equity: 101.54,
    total_pnl: 1.54,
    total_pnl_pct: 1.54,
    position_count: 1,
    margin_used_pct: 10,
    is_running: true,
  },
]

class MockResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

describe('ComparisonChart Component', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    localStorage.setItem('language', 'zh')
  })

  it('renders separated responsive legend outside chart SVG', async () => {
    getEquityHistoryBatchMock.mockResolvedValue({
      histories: {
        'trader-1': [
          {
            timestamp: '2026-09-13T00:00:00Z',
            total_equity: 100,
            total_pnl_pct: 0,
          },
          {
            timestamp: '2026-09-14T00:00:00Z',
            total_equity: 103,
            total_pnl_pct: 3,
          },
        ],
        'trader-2': [
          {
            timestamp: '2026-09-13T00:00:00Z',
            total_equity: 100,
            total_pnl_pct: 0,
          },
          {
            timestamp: '2026-09-14T00:00:00Z',
            total_equity: 101.54,
            total_pnl_pct: 1.54,
          },
        ],
      },
    })

    const { container } = renderChart(mockTraders)

    await waitFor(() => {
      expect(screen.getAllByText('挑战-多周期-Luna').length).toBeGreaterThan(0)
      expect(screen.getAllByText('挑战-多周期-Gemini').length).toBeGreaterThan(0)
    })

    // Recharts internal legend wrapper should not exist because legend is separated
    const rechartsLegend = container.querySelector('.recharts-legend-wrapper')
    expect(rechartsLegend).toBeNull()

    // Standalone legend cards should exist in a responsive grid
    const legendGrid = container.querySelector('.grid-cols-2')
    expect(legendGrid).not.toBeNull()
    expect(legendGrid?.textContent).toContain('挑战-多周期-Luna')
    expect(legendGrid?.textContent).toContain('+3.00%')

    // Legend header should show guide text
    expect(screen.getByText('(点击卡片高亮单条曲线)')).toBeDefined()
  })

  it('highlights trader and shows top HUD when clicked', async () => {
    getEquityHistoryBatchMock.mockResolvedValue({
      histories: {
        'trader-1': [
          {
            timestamp: '2026-09-13T00:00:00Z',
            total_equity: 100,
            total_pnl_pct: 0,
          },
          {
            timestamp: '2026-09-14T00:00:00Z',
            total_equity: 103,
            total_pnl_pct: 3,
          },
        ],
      },
    })

    const { container } = renderChart([mockTraders[0]])

    await waitFor(() => {
      expect(screen.getAllByText('挑战-多周期-Luna').length).toBeGreaterThan(0)
    })

    // Click the legend card
    const cardButton = container.querySelector('button[title*="点击高亮 挑战-多周期-Luna"]') as HTMLButtonElement
    expect(cardButton).not.toBeNull()
    cardButton.click()

    // Top HUD should show the highlighted trader full name and reset button
    await waitFor(() => {
      expect(screen.getAllByText('挑战-多周期-Luna').length).toBeGreaterThan(1)
      expect(screen.getByText('恢复全部')).toBeDefined()
      expect(screen.getByText('恢复默认')).toBeDefined()
    })
  })
})
