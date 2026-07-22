import type { Language } from '../../i18n/translations'

interface ExecutionModeSelectorProps {
  value: 'paper' | 'live'
  onChange: (value: 'paper' | 'live') => void
	language: Language
}

export function ExecutionModeSelector({ value, onChange, language }: ExecutionModeSelectorProps) {
  return (
    <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4" data-testid="execution-mode-selector">
      <div className="mb-3 text-sm font-bold text-nofx-text">
        {language === 'zh' ? '执行模式' : 'Execution mode'}
      </div>
      <div className="grid grid-cols-2 gap-3">
        <button type="button" aria-pressed={value === 'paper'} onClick={() => onChange('paper')}
          className={`rounded-lg border p-3 text-left ${value === 'paper' ? 'border-amber-500 bg-amber-500/20' : 'border-nofx-gold/20'}`}>
          <div className="font-bold">Paper Trading</div>
          <div className="mt-1 text-xs text-nofx-text-muted">{language === 'zh' ? '实时模拟交易，不会真实下单' : 'Realtime simulation; never sends real orders'}</div>
        </button>
        <button type="button" aria-pressed={value === 'live'} onClick={() => onChange('live')}
          className={`rounded-lg border p-3 text-left ${value === 'live' ? 'border-red-500 bg-red-500/15' : 'border-nofx-gold/20'}`}>
          <div className="font-bold">Live Trading</div>
          <div className="mt-1 text-xs text-nofx-text-muted">{language === 'zh' ? '真实资金，需要启动确认' : 'Real funds; explicit confirmation required'}</div>
        </button>
      </div>
    </div>
  )
}
