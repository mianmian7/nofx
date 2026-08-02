import React, { useEffect, useState } from 'react'
import { ExternalLink, Trash2 } from 'lucide-react'
import type { AIModel } from '../../types'
import type { Language } from '../../i18n/translations'
import { t } from '../../i18n/translations'
import { api } from '../../lib/api'
import { getModelIcon } from '../common/ModelIcons'
import { ModelCard } from './ModelCard'
import { ModelStepIndicator } from './ModelStepIndicator'
import { AI_PROVIDER_CONFIG, getShortName } from './model-constants'

interface ModelConfigModalProps {
  allModels: AIModel[]
  configuredModels: AIModel[]
  editingModelId: string | null
  initialModelId?: string | null
  onSave: (
    modelId: string,
    apiKey: string,
    baseUrl?: string,
    modelName?: string,
    modelNames?: string[]
  ) => void
  onDelete: (modelId: string) => void
  onClose: () => void
  language: Language
}

export function ModelConfigModal({
  allModels,
  configuredModels,
  editingModelId,
  initialModelId,
  onSave,
  onDelete,
  onClose,
  language,
}: ModelConfigModalProps) {
  const [currentStep, setCurrentStep] = useState(
    editingModelId || initialModelId ? 1 : 0
  )
  const [selectedModelId, setSelectedModelId] = useState(
    editingModelId || initialModelId || ''
  )
  const [apiKey, setApiKey] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [modelName, setModelName] = useState('')
  const [modelNames, setModelNames] = useState<string[]>([])
  const [availableModelNames, setAvailableModelNames] = useState<string[]>([])
  const [isDiscoveringModels, setIsDiscoveringModels] = useState(false)
  const [modelDiscoveryError, setModelDiscoveryError] = useState('')

  const configuredModel = configuredModels?.find(
    (model) => model.id === selectedModelId
  )
  const supportedModel = allModels?.find(
    (model) => model.id === selectedModelId
  )
  const selectedModel = editingModelId
    ? configuredModel || supportedModel
    : supportedModel || configuredModel

  useEffect(() => {
    if (!editingModelId || !selectedModel) return
    setApiKey(selectedModel.apiKey || '')
    setBaseUrl(selectedModel.customApiUrl || '')
    setModelName(selectedModel.customModelName || '')
    setModelNames(selectedModel.modelNames || [])
    setAvailableModelNames(selectedModel.modelNames || [])
  }, [editingModelId, selectedModel])

  const handleSelectModel = (modelId: string) => {
    setSelectedModelId(modelId)
    setCurrentStep(1)
  }

  const handleBack = () => {
    if (editingModelId) {
      onClose()
      return
    }
    setCurrentStep(0)
    setSelectedModelId('')
  }

  const handleSubmit = (event: React.FormEvent) => {
    event.preventDefault()
    const hasSavedKey = Boolean(editingModelId && selectedModel?.has_api_key)
    if (!selectedModelId || (!apiKey.trim() && !hasSavedKey)) return
    onSave(
      selectedModelId,
      apiKey.trim(),
      baseUrl.trim() || undefined,
      modelName.trim() || undefined,
      modelNames
    )
  }

  const handleDiscoverModels = async () => {
    if (!selectedModel) return
    setIsDiscoveringModels(true)
    setModelDiscoveryError('')
    try {
      const discoveredModelNames = await api.discoverAIModels({
        model_id: selectedModelId,
        provider: selectedModel.provider,
        api_key: apiKey.trim(),
        custom_api_url: baseUrl.trim(),
      })
      setAvailableModelNames(discoveredModelNames)
      const retainedModelNames = modelNames.filter((name) =>
        discoveredModelNames.includes(name)
      )
      const initialPrimaryModel = discoveredModelNames.includes(modelName)
        ? modelName
        : discoveredModelNames[0]
      const nextSelectedModelNames =
        retainedModelNames.length > 0
          ? retainedModelNames
          : initialPrimaryModel
            ? [initialPrimaryModel]
            : []
      setModelNames(nextSelectedModelNames)
      if (!nextSelectedModelNames.includes(modelName)) {
        setModelName(nextSelectedModelNames[0] || '')
      }
    } catch {
      setModelDiscoveryError(
        language === 'zh'
          ? '读取模型列表失败，请检查 API Key 和 Base URL。'
          : 'Failed to read models. Check the API key and base URL.'
      )
    } finally {
      setIsDiscoveringModels(false)
    }
  }

  const availableModels = allModels || []
  const configuredIds = new Set(configuredModels?.map((model) => model.id) || [])
  const stepLabels = [
    t('modelConfig.selectModel', language),
    t('modelConfig.configureApi', language),
  ]

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center overflow-y-auto bg-black/60 p-4 backdrop-blur-sm">
      <div
        className="relative my-8 w-full max-w-[52rem] rounded-2xl bg-nofx-bg-lighter shadow-2xl"
        style={{ maxHeight: 'calc(100vh - 4rem)' }}
      >
        <div className="flex items-center justify-between p-6 pb-2">
          <div className="flex items-center gap-3">
            {currentStep > 0 && !editingModelId && (
              <button
                type="button"
                onClick={handleBack}
                className="rounded-lg p-2 transition-colors hover:bg-nofx-bg-deeper"
                aria-label="Back"
              >
                <span aria-hidden="true">←</span>
              </button>
            )}
            <h3 className="text-xl font-bold" style={{ color: '#1A1813' }}>
              {editingModelId
                ? t('editAIModel', language)
                : t('addAIModel', language)}
            </h3>
          </div>
          <div className="flex items-center gap-2">
            {editingModelId && (
              <button
                type="button"
                onClick={() => onDelete(editingModelId)}
                className="rounded-lg p-2 transition-colors hover:bg-nofx-danger/20"
                style={{ color: '#D6433A' }}
                aria-label="Delete model"
              >
                <Trash2 className="h-4 w-4" />
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg p-2 transition-colors hover:bg-nofx-bg-deeper"
              style={{ color: '#8A8478' }}
              aria-label="Close"
            >
              ×
            </button>
          </div>
        </div>

        {!editingModelId && (
          <div className="px-6">
            <ModelStepIndicator currentStep={currentStep} labels={stepLabels} />
          </div>
        )}

        <div
          className="overflow-y-auto px-6 pb-6"
          style={{ maxHeight: 'calc(100vh - 16rem)' }}
        >
          {currentStep === 0 && !editingModelId && (
            <ModelSelectionStep
              availableModels={availableModels}
              configuredIds={configuredIds}
              selectedModelId={selectedModelId}
              onSelectModel={handleSelectModel}
              language={language}
            />
          )}

          {(currentStep === 1 || editingModelId) && selectedModel && (
            <StandardProviderConfigForm
              selectedModel={selectedModel}
              apiKey={apiKey}
              baseUrl={baseUrl}
              modelName={modelName}
              modelNames={modelNames}
              availableModelNames={availableModelNames}
              isDiscoveringModels={isDiscoveringModels}
              modelDiscoveryError={modelDiscoveryError}
              editingModelId={editingModelId}
              onApiKeyChange={setApiKey}
              onBaseUrlChange={setBaseUrl}
              onModelNameChange={setModelName}
              onModelNamesChange={setModelNames}
              onDiscoverModels={handleDiscoverModels}
              onBack={handleBack}
              onSubmit={handleSubmit}
              language={language}
            />
          )}
        </div>
      </div>
    </div>
  )
}

function ModelSelectionStep({
  availableModels,
  configuredIds,
  selectedModelId,
  onSelectModel,
  language,
}: {
  availableModels: AIModel[]
  configuredIds: Set<string>
  selectedModelId: string
  onSelectModel: (modelId: string) => void
  language: Language
}) {
  return (
    <div className="space-y-4">
      <div className="text-sm font-semibold" style={{ color: '#1A1813' }}>
        {t('modelConfig.chooseProvider', language)}
      </div>
      <div className="grid grid-cols-3 gap-3 sm:grid-cols-4">
        {availableModels.map((model) => (
          <ModelCard
            key={model.id}
            model={model}
            selected={selectedModelId === model.id}
            onClick={() => onSelectModel(model.id)}
            configured={configuredIds.has(model.id)}
          />
        ))}
      </div>
      <div className="pt-2 text-center text-xs" style={{ color: '#8A8478' }}>
        {t('modelConfig.modelsConfigured', language)}
      </div>
    </div>
  )
}

function StandardProviderConfigForm({
  selectedModel,
  apiKey,
  baseUrl,
  modelName,
  modelNames,
  availableModelNames,
  isDiscoveringModels,
  modelDiscoveryError,
  editingModelId,
  onApiKeyChange,
  onBaseUrlChange,
  onModelNameChange,
  onModelNamesChange,
  onDiscoverModels,
  onBack,
  onSubmit,
  language,
}: {
  selectedModel: AIModel
  apiKey: string
  baseUrl: string
  modelName: string
  modelNames: string[]
  availableModelNames: string[]
  isDiscoveringModels: boolean
  modelDiscoveryError: string
  editingModelId: string | null
  onApiKeyChange: (value: string) => void
  onBaseUrlChange: (value: string) => void
  onModelNameChange: (value: string) => void
  onModelNamesChange: (value: string[]) => void
  onDiscoverModels: () => void
  onBack: () => void
  onSubmit: (event: React.FormEvent) => void
  language: Language
}) {
  const hasSavedKey = Boolean(editingModelId && selectedModel.has_api_key)
  const providerConfig = AI_PROVIDER_CONFIG[selectedModel.provider]
  const displayedPrimaryModelName =
    modelName || selectedModel.customModelName || ''

  return (
    <form onSubmit={onSubmit} className="space-y-5">
      <div className="flex items-center gap-4 rounded-xl border border-black/10 bg-[#F1ECE2] p-4">
        <div className="flex h-12 w-12 items-center justify-center rounded-xl border border-nofx-gold/20 bg-nofx-bg-deeper">
          {getModelIcon(selectedModel.provider || selectedModel.id, {
            width: 32,
            height: 32,
          }) || (
            <span className="text-lg font-bold" style={{ color: '#E0483B' }}>
              {selectedModel.name[0]}
            </span>
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-lg font-semibold" style={{ color: '#1A1813' }}>
            {getShortName(selectedModel.name)}
          </div>
          <div className="text-xs" style={{ color: '#8A8478' }}>
            {selectedModel.provider} · {providerConfig?.defaultModel || selectedModel.id}
          </div>
        </div>
        {providerConfig?.apiUrl && (
          <a
            href={providerConfig.apiUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 rounded-lg border border-nofx-gold/30 bg-nofx-gold/10 px-3 py-2 text-sm font-medium text-nofx-gold"
          >
            <ExternalLink className="h-4 w-4" />
            {t('modelConfig.getApiKey', language)}
          </a>
        )}
      </div>

      {editingModelId && (
        <div className="rounded-xl border border-nofx-success/20 bg-nofx-success/10 p-3 text-xs text-nofx-success">
          Current model key status:{' '}
          {hasSavedKey ? 'API Key configured' : 'API Key not configured'}
        </div>
      )}

      <div className="space-y-2">
        <label className="text-sm font-semibold" style={{ color: '#1A1813' }}>
          API Key
        </label>
        <input
          type="password"
          value={apiKey}
          onChange={(event) => onApiKeyChange(event.target.value)}
          placeholder={hasSavedKey ? 'Saved. Re-enter to replace.' : t('enterAPIKey', language)}
          className="w-full rounded-xl px-4 py-3"
          style={{
            background: '#F1ECE2',
            border: '1px solid rgba(26,24,19,0.14)',
            color: '#1A1813',
          }}
          required={!editingModelId || !hasSavedKey}
        />
      </div>

      <div className="space-y-2">
        <label className="text-sm font-semibold" style={{ color: '#1A1813' }}>
          {t('customBaseURL', language)}
        </label>
        <input
          type="url"
          value={baseUrl}
          onChange={(event) => onBaseUrlChange(event.target.value)}
          placeholder={t('customBaseURLPlaceholder', language)}
          className="w-full rounded-xl px-4 py-3"
          style={{
            background: '#F1ECE2',
            border: '1px solid rgba(26,24,19,0.14)',
            color: '#1A1813',
          }}
        />
        <div className="text-xs" style={{ color: '#8A8478' }}>
          {t('leaveBlankForDefault', language)}
        </div>
      </div>

      <div className="space-y-2">
        <label className="text-sm font-semibold" style={{ color: '#1A1813' }}>
          {language === 'zh' ? '默认模型名称' : 'Primary model name'}
        </label>
        <input
          type="text"
          value={modelName}
          onChange={(event) => onModelNameChange(event.target.value)}
          placeholder={providerConfig?.defaultModel || selectedModel.id}
          className="w-full rounded-xl px-4 py-3"
          style={{
            background: '#F1ECE2',
            border: '1px solid rgba(26,24,19,0.14)',
            color: '#1A1813',
          }}
        />
      </div>

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <label className="text-sm font-semibold" style={{ color: '#1A1813' }}>
            {language === 'zh' ? 'API 模型列表' : 'API model catalog'}
          </label>
          <button
            type="button"
            onClick={onDiscoverModels}
            disabled={isDiscoveringModels || (!apiKey.trim() && !hasSavedKey)}
            className="rounded-lg bg-[#E8E2D5] px-3 py-1.5 text-xs font-semibold disabled:opacity-50"
          >
            {isDiscoveringModels
              ? language === 'zh'
                ? '读取中…'
                : 'Loading…'
              : language === 'zh'
                ? '读取模型列表'
                : 'Load models'}
          </button>
        </div>
        {modelDiscoveryError && (
          <p className="text-xs text-red-600">{modelDiscoveryError}</p>
        )}
        {availableModelNames.length > 0 && (
          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-nofx-bg px-3 py-2">
              <span className="text-xs text-nofx-text-muted">
                {language === 'zh'
                  ? `已选择 ${modelNames.length}/${availableModelNames.length}`
                  : `${modelNames.length}/${availableModelNames.length} selected`}
              </span>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => {
                    onModelNamesChange(availableModelNames)
                    if (!availableModelNames.includes(modelName)) {
                      onModelNameChange(availableModelNames[0] || '')
                    }
                  }}
                  className="rounded px-2 py-1 text-xs font-semibold text-nofx-gold hover:bg-nofx-gold/10"
                >
                  {language === 'zh' ? '全选' : 'Select all'}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    const nextModelNames = availableModelNames.filter(
                      (name) => !modelNames.includes(name)
                    )
                    onModelNamesChange(nextModelNames)
                    if (!nextModelNames.includes(modelName)) {
                      onModelNameChange(nextModelNames[0] || '')
                    }
                  }}
                  className="rounded px-2 py-1 text-xs font-semibold text-nofx-gold hover:bg-nofx-gold/10"
                >
                  {language === 'zh' ? '反选' : 'Invert selection'}
                </button>
              </div>
            </div>
            <div className="max-h-64 space-y-2 overflow-y-auto rounded-xl border border-nofx-gold/20 p-2">
              {availableModelNames.map((name) => {
                const selected = modelNames.includes(name)
                const isPrimary = displayedPrimaryModelName === name
                return (
                  <div
                    key={name}
                    className="flex items-center justify-between gap-3 rounded-lg bg-nofx-bg px-3 py-2"
                  >
                    <label className="flex min-w-0 items-center gap-2">
                      <input
                        type="checkbox"
                        checked={selected}
                        onChange={() => {
                          const nextModelNames = selected
                            ? modelNames.filter((item) => item !== name)
                            : [...modelNames, name]
                          onModelNamesChange(nextModelNames)
                          if (name === modelName && !nextModelNames.includes(name)) {
                            onModelNameChange(nextModelNames[0] || '')
                          } else if (!modelName && nextModelNames.length > 0) {
                            onModelNameChange(nextModelNames[0])
                          }
                        }}
                      />
                      <span className="truncate text-sm">{name}</span>
                    </label>
                    <label className="flex shrink-0 items-center gap-1 text-xs text-nofx-text-muted">
                      <input
                        type="radio"
                        name="primary-model"
                        checked={isPrimary}
                        onChange={() => onModelNameChange(name)}
                      />
                      {language === 'zh' ? '主模型' : 'Primary'}
                    </label>
                  </div>
                )
              })}
            </div>
            {displayedPrimaryModelName && (
              <div className="text-xs text-nofx-text-muted">
                {language === 'zh'
                  ? `当前主模型：${displayedPrimaryModelName}`
                  : `Current primary: ${displayedPrimaryModelName}`}
              </div>
            )}
          </div>
        )}
      </div>

      <div className="flex items-center justify-between gap-3 pt-2">
        <button
          type="button"
          onClick={onBack}
          className="rounded-xl border border-black/10 px-5 py-3 text-sm font-semibold text-nofx-text-muted"
        >
          {t('back', language)}
        </button>
        <button
          type="submit"
          className="rounded-xl bg-nofx-gold px-5 py-3 text-sm font-bold text-white"
        >
          {t('saveConfiguration', language)}
        </button>
      </div>
    </form>
  )
}
