import type { AIModel } from '../../types'
import type { Language } from '../../i18n/translations'

interface FallbackModelSelectorProps {
  models: AIModel[]
  primaryModelId: string
  selectedModelIds: string[]
  onChange: (modelIds: string[]) => void
  language: Language
}

function modelLabel(model: AIModel): string {
  const configuredModel = model.customModelName
    ? ` · ${model.customModelName}`
    : ''
  return `${model.name || model.id} · ${model.provider}${configuredModel}`
}

export function FallbackModelSelector({
  models,
  primaryModelId,
  selectedModelIds,
  onChange,
  language,
}: FallbackModelSelectorProps) {
  const options = models.filter(
    (model) => model.enabled && model.id !== primaryModelId
  )

  if (options.length === 0) {
    return (
      <p className="text-xs text-nofx-text-muted">
        {language === 'zh'
          ? '暂无其他已启用模型。请先在模型配置中添加并启用备用模型。'
          : 'No other enabled models are available. Configure and enable a fallback model first.'}
      </p>
    )
  }

  return (
    <div className="space-y-2" role="group" aria-label="Fallback AI models">
      {options.map((model, index) => {
        const checked = selectedModelIds.includes(model.id)
        return (
          <label
            key={model.id}
            className="flex items-center justify-between gap-3 rounded border border-nofx-gold/20 bg-nofx-bg-lighter px-3 py-2 cursor-pointer"
          >
            <span className="flex min-w-0 items-center gap-3">
              <input
                type="checkbox"
                checked={checked}
                onChange={() => {
                  if (checked) {
                    onChange(
                      selectedModelIds.filter((modelId) => modelId !== model.id)
                    )
                    return
                  }
                  onChange([...selectedModelIds, model.id])
                }}
              />
              <span className="truncate text-sm text-nofx-text">
                {modelLabel(model)}
              </span>
            </span>
            {checked && (
              <span className="shrink-0 text-xs text-nofx-gold">
                {language === 'zh'
                  ? `优先级 ${selectedModelIds.indexOf(model.id) + 1}`
                  : `Priority ${selectedModelIds.indexOf(model.id) + 1}`}
              </span>
            )}
            {!checked && index === 0 && selectedModelIds.length === 0 && (
              <span className="sr-only">
                {language === 'zh'
                  ? '可选备用模型'
                  : 'Available fallback model'}
              </span>
            )}
          </label>
        )
      })}
    </div>
  )
}
