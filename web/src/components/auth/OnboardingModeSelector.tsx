import type { UserMode } from '../../lib/onboarding'

interface OnboardingModeSelectorProps {
  language: string
  mode: UserMode
  onChange: (mode: UserMode) => void
}

export function OnboardingModeSelector({
  language,
  mode,
  onChange,
}: OnboardingModeSelectorProps) {
  const isZh = language === 'zh'

  const options: Array<{
    id: UserMode
    title: string
    badge?: string
    description: string
  }> = [
    {
      id: 'beginner',
      title: isZh ? 'Claw402 托管模型' : 'Managed Claw402 model',
      description: isZh
        ? '使用托管模型调用；交易候选和执行仍走 Binance Futures。'
        : 'Use managed model calls while market candidates and execution stay on Binance Futures.',
    },
    {
      id: 'advanced',
      title: isZh ? 'Binance + 自有模型' : 'Binance + your own model',
      badge: isZh ? '推荐' : 'Recommended',
      description: isZh
        ? '配置你自己的 AI API 和 Binance Futures；默认使用免费的 Binance 本地动态候选。'
        : 'Configure your own AI API and Binance Futures; free Binance local dynamic candidates are the default.',
    },
  ]

  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-nofx-text-muted">
        {isZh ? 'Experience' : 'Experience'}
      </div>
      <div className="grid grid-cols-1 gap-2">
        {options.map((option) => {
          const selected = option.id === mode
          return (
            <button
              key={option.id}
              type="button"
              onClick={() => onChange(option.id)}
              className={`w-full rounded-xl border px-4 py-3 text-left transition-all ${
                selected
                  ? 'border-nofx-gold/60 bg-nofx-gold/10'
                  : 'border-[rgba(26,24,19,0.14)] bg-nofx-bg-lighter hover:border-nofx-gold/40'
              }`}
            >
              <div className="flex items-center gap-2 text-sm font-semibold text-nofx-text">
                <span>{option.title}</span>
                {option.badge ? (
                  <span className="rounded-full bg-nofx-gold px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-nofx-bg">
                    {option.badge}
                  </span>
                ) : null}
              </div>
              <p className="mt-1 text-xs leading-5 text-nofx-text-muted">
                {option.description}
              </p>
            </button>
          )
        })}
      </div>
    </div>
  )
}
