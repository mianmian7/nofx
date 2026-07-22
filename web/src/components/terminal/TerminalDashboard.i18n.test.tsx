import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LanguageProvider } from '../../contexts/LanguageContext'
import { EdgeProfile } from './EdgeProfile'
import { ExecutionLog } from './ExecutionLog'
import { FlowMarkets } from './FlowMarkets'
import { RiskRadar } from './RiskRadar'
import { SignalMatrix } from './SignalMatrix'

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
})
