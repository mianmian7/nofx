interface SameAPIModelSelectorProps {
  models: string[]
  primaryModelName?: string
  selectedModelNames: string[]
  onChange: (modelNames: string[]) => void
  language: 'zh' | 'en' | 'id'
  loading?: boolean
  error?: string
}

export function SameAPIModelSelector({
  models,
  primaryModelName,
  selectedModelNames,
  onChange,
  language,
  loading,
  error,
}: SameAPIModelSelectorProps) {
  const options = Array.from(
    new Set([...models, ...selectedModelNames])
  ).filter((modelName) => modelName && modelName !== primaryModelName)

  if (loading) {
    return (
      <p className="text-xs text-nofx-text-muted">
        {language === 'zh'
          ? '正在读取该 API 提供的模型…'
          : 'Loading models exposed by this API…'}
      </p>
    )
  }
  if (options.length === 0) {
    return (
      <p className="text-xs text-nofx-text-muted">
        {error ||
          (language === 'zh'
            ? '该 API 暂未返回可选备用模型。'
            : 'This API did not return any fallback models.')}
      </p>
    )
  }

  return (
    <div
      className="space-y-2"
      role="group"
      aria-label="Same API fallback models"
    >
      {options.map((modelName) => {
        const checked = selectedModelNames.includes(modelName)
        return (
          <label
            key={modelName}
            className="flex items-center justify-between gap-3 rounded border border-nofx-gold/20 bg-nofx-bg-lighter px-3 py-2 cursor-pointer"
          >
            <span className="flex min-w-0 items-center gap-3">
              <input
                type="checkbox"
                checked={checked}
                onChange={() => {
                  onChange(
                    checked
                      ? selectedModelNames.filter((name) => name !== modelName)
                      : [...selectedModelNames, modelName]
                  )
                }}
              />
              <span className="truncate text-sm text-nofx-text">
                {modelName}
              </span>
            </span>
            {checked && (
              <span className="shrink-0 text-xs text-nofx-gold">
                {language === 'zh'
                  ? `优先级 ${selectedModelNames.indexOf(modelName) + 1}`
                  : `Priority ${selectedModelNames.indexOf(modelName) + 1}`}
              </span>
            )}
          </label>
        )
      })}
      {error && <p className="text-xs text-amber-600">{error}</p>}
    </div>
  )
}
