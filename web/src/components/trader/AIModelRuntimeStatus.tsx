import type { SystemStatus } from '../../types'
import type { Language } from '../../i18n/translations'

const reasonLabels: Record<string, Partial<Record<Language, string>>> = {
  auth_unavailable: {
    zh: '上游认证服务不可用',
    en: 'Upstream authentication unavailable',
  },
  rate_limited: { zh: '请求频率受限', en: 'Rate limited' },
  provider_unavailable: { zh: '模型服务不可用', en: 'Provider unavailable' },
  network_unavailable: { zh: '网络连接不可用', en: 'Network unavailable' },
  model_not_found: {
    zh: '模型不可用或不存在',
    en: 'Model unavailable or not found',
  },
}

function formatSwitchTime(value: string, language: Language): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleString(language === 'zh' ? 'zh-CN' : 'en-US')
}

export function AIModelRuntimeStatus({
  status,
  language,
}: {
  status: SystemStatus
  language: Language
}) {
  const isFallback = Boolean(status.is_fallback)
  const reason =
    reasonLabels[status.fallback_reason || '']?.[language] ||
    reasonLabels[status.fallback_reason || '']?.en ||
    status.fallback_reason ||
    (language === 'zh' ? '未知原因' : 'Unknown reason')

  return (
    <div
      className={`mb-4 rounded-lg border px-4 py-3 font-mono text-xs ${
        isFallback
          ? 'border-amber-500/40 bg-amber-500/10'
          : 'border-nofx-success/30 bg-nofx-success/5'
      }`}
      data-testid="ai-runtime-status"
    >
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
        <span>
          <span className="text-nofx-text-muted">
            {language === 'zh' ? '当前模型' : 'Current model'}:
          </span>{' '}
          <strong className="text-nofx-text">
            {status.ai_provider}/{status.ai_model}
          </strong>
        </span>
        <span className={isFallback ? 'text-amber-600' : 'text-nofx-success'}>
          {isFallback
            ? language === 'zh'
              ? '备用链运行中'
              : 'Running on fallback'
            : language === 'zh'
              ? '主模型运行中'
              : 'Running on primary'}
        </span>
        {isFallback && (
          <>
            <span>
              <span className="text-nofx-text-muted">
                {language === 'zh' ? '失败原因' : 'Failure reason'}:
              </span>{' '}
              <span className="text-nofx-text">{reason}</span>
            </span>
            {status.fallback_since && (
              <span>
                <span className="text-nofx-text-muted">
                  {language === 'zh' ? '切换时间' : 'Switched at'}:
                </span>{' '}
                <time dateTime={status.fallback_since}>
                  {formatSwitchTime(status.fallback_since, language)}
                </time>
              </span>
            )}
          </>
        )}
      </div>
    </div>
  )
}
