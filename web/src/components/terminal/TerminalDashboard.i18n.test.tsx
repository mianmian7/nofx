import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LanguageProvider } from '../../contexts/LanguageContext'
import { EdgeProfile } from './EdgeProfile'
import { ExecutionLog } from './ExecutionLog'
import { FlowMarkets } from './FlowMarkets'
import { RiskRadar } from './RiskRadar'
import { SignalMatrix } from './SignalMatrix'
import type { DecisionRecord } from '../../types'

describe('terminal dashboard localization', () => {
  beforeEach(() => {
    localStorage.setItem('language', 'zh')
  })

  it('renders empty dashboard panels in Simplified Chinese', () => {
    render(
      <LanguageProvider>
        <>
          <ExecutionLog />
          <SignalMatrix />
          <FlowMarkets />
          <RiskRadar />
          <EdgeProfile />
        </>
      </LanguageProvider>
    )

    expect(screen.getByText('执行日志')).toBeInTheDocument()
    expect(screen.getByText('暂无执行事件。')).toBeInTheDocument()
    expect(screen.getByText('信号矩阵')).toBeInTheDocument()
    expect(screen.getByText('暂无信号数据（Claw402）。')).toBeInTheDocument()
    expect(
      screen.getByText('暂无净流入数据（需要 Claw402 付费数据）。')
    ).toBeInTheDocument()
    expect(screen.getByText('暂无实时风险数据。')).toBeInTheDocument()
    expect(screen.getByText('暂无已平仓交易。')).toBeInTheDocument()
  })

  it('shows the original AI entry beside the inverted execution', () => {
    const decision: DecisionRecord = {
      timestamp: '2026-07-31T01:20:00Z',
      cycle_number: 97,
      system_prompt: '',
      input_prompt: '',
      cot_trace: '',
      decision_json: '',
      raw_response:
        '<decision>[{"symbol":"SKHY","action":"open_long"}]</decision>',
      account_state: {
        total_balance: 1000,
        available_balance: 900,
        total_unrealized_profit: 0,
        position_count: 0,
        margin_used_pct: 0,
      },
      positions: [],
      candidate_coins: ['SKHY'],
      decisions: [
        {
          action: 'open_short',
          symbol: 'SKHY',
          quantity: 1,
          leverage: 4,
          price: 149.2,
          confidence: 64,
          order_id: 123,
          timestamp: '2026-07-31T01:20:01Z',
          success: true,
        },
      ],
      execution_log: ['✓ SKHY open_short succeeded · confidence 64%'],
      success: true,
    }

    render(
      <LanguageProvider>
        <ExecutionLog decisions={[decision]} />
      </LanguageProvider>
    )

    expect(screen.getByText('AI 原始')).toBeInTheDocument()
    expect(screen.getByText('open_long')).toBeInTheDocument()
    expect(screen.getByText('反向执行')).toBeInTheDocument()
    expect(screen.getByText('open_short')).toBeInTheDocument()
    expect(screen.getByText('反向')).toBeInTheDocument()
    expect(screen.getByText('succeeded · confidence 64%')).toBeInTheDocument()
  })
})
