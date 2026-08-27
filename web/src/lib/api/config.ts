import type {
  AIModel,
  Exchange,
  ExchangeAccountStateResponse,
  UpdateModelConfigRequest,
  UpdateExchangeConfigRequest,
  CreateExchangeRequest,
} from '../../types'
import { API_BASE, httpClient, CryptoService } from './helpers'
import { diagnoseWebCryptoEnvironment } from '../crypto'

const RETAINED_MODEL_PROVIDERS = new Set([
  'deepseek',
  'openai',
  'claude',
  'qwen',
  'gemini',
  'grok',
  'kimi',
  'minimax',
])

function keepDirectModelProvider(model: AIModel): boolean {
  return RETAINED_MODEL_PROVIDERS.has(model.provider.toLowerCase())
}

async function encryptSensitivePayload(request: unknown): Promise<unknown> {
  const config = await CryptoService.fetchCryptoConfig()
  if (config.transport_encryption !== true) {
    throw new Error(
      'Sensitive configuration writes are disabled until transport encryption is enabled'
    )
  }

  const environment = diagnoseWebCryptoEnvironment()
  if (
    !environment.hasSubtleCrypto ||
    (!environment.isSecureContext && !environment.isLocalhost)
  ) {
    throw new Error(
      'Sensitive configuration writes require HTTPS or a localhost secure context'
    )
  }

  const publicKey = await CryptoService.fetchPublicKey()
  if (!publicKey) {
    throw new Error('Server did not provide a transport-encryption public key')
  }
  await CryptoService.initialize(publicKey)
  return CryptoService.encryptSensitiveData(
    JSON.stringify(request),
    localStorage.getItem('user_id') || '',
    sessionStorage.getItem('session_id') || ''
  )
}

export const configApi = {
  async getModelConfigs(): Promise<AIModel[]> {
    const result = await httpClient.get<AIModel[]>(`${API_BASE}/models`)
    if (!result.success) throw new Error('Failed to fetch model configs')
    return Array.isArray(result.data)
      ? result.data.filter(keepDirectModelProvider)
      : []
  },

  async getSupportedModels(): Promise<AIModel[]> {
    const result = await httpClient.get<AIModel[]>(
      `${API_BASE}/supported-models`
    )
    if (!result.success) throw new Error('Failed to fetch supported models')
    return (result.data || []).filter(keepDirectModelProvider)
  },

  async discoverAIModels(request: {
    model_id: string
    provider: string
    api_key: string
    custom_api_url: string
  }): Promise<string[]> {
    const payload = await encryptSensitivePayload(request)
    const result = await httpClient.post<{ models: string[] }>(
      `${API_BASE}/models/discover`,
      payload
    )
    if (!result.success) throw new Error('Failed to discover API models')
    return result.data?.models || []
  },

  async getPromptTemplates(): Promise<string[]> {
    const res = await fetch(`${API_BASE}/prompt-templates`)
    if (!res.ok) throw new Error('Failed to fetch prompt templates')
    const data = await res.json()
    if (Array.isArray(data.templates)) {
      return data.templates.map((item: { name: string }) => item.name)
    }
    return []
  },

  async updateModelConfigs(request: UpdateModelConfigRequest): Promise<void> {
    const encryptedPayload = await encryptSensitivePayload(request)
    const result = await httpClient.put(`${API_BASE}/models`, encryptedPayload)
    if (!result.success) throw new Error('Failed to update model configs')
  },

  async getExchangeConfigs(): Promise<Exchange[]> {
    const result = await httpClient.get<Exchange[]>(`${API_BASE}/exchanges`)
    if (!result.success) throw new Error('Failed to fetch exchange configs')
    return result.data!
  },

  async getExchangeAccountState(): Promise<ExchangeAccountStateResponse> {
    const result = await httpClient.get<ExchangeAccountStateResponse>(
      `${API_BASE}/exchanges/account-state`
    )
    if (!result.success || !result.data) {
      throw new Error('Failed to fetch exchange account states')
    }
    return result.data
  },

  async getSupportedExchanges(): Promise<Exchange[]> {
    const result = await httpClient.get<Exchange[]>(
      `${API_BASE}/supported-exchanges`
    )
    if (!result.success) throw new Error('Failed to fetch supported exchanges')
    return result.data!
  },

  async updateExchangeConfigs(
    request: UpdateExchangeConfigRequest
  ): Promise<void> {
    return configApi.updateExchangeConfigsEncrypted(request)
  },

  async createExchange(
    request: CreateExchangeRequest
  ): Promise<{ id: string }> {
    return configApi.createExchangeEncrypted(request)
  },

  async createExchangeEncrypted(
    request: CreateExchangeRequest
  ): Promise<{ id: string }> {
    const encryptedPayload = await encryptSensitivePayload(request)
    const result = await httpClient.post<{ id: string }>(
      `${API_BASE}/exchanges`,
      encryptedPayload
    )
    if (!result.success) throw new Error('Failed to create exchange account')
    return result.data!
  },

  async deleteExchange(exchangeId: string): Promise<void> {
    const result = await httpClient.delete(
      `${API_BASE}/exchanges/${exchangeId}`
    )
    if (!result.success) throw new Error('Failed to delete exchange account')
  },

  async updateExchangeConfigsEncrypted(
    request: UpdateExchangeConfigRequest
  ): Promise<void> {
    const encryptedPayload = await encryptSensitivePayload(request)
    const result = await httpClient.put(
      `${API_BASE}/exchanges`,
      encryptedPayload
    )
    if (!result.success) throw new Error('Failed to update exchange configs')
  },

  async getServerIP(): Promise<{
    public_ip: string
    message: string
  }> {
    const result = await httpClient.get<{
      public_ip: string
      message: string
    }>(`${API_BASE}/server-ip`)
    if (!result.success) throw new Error('Failed to fetch server IP')
    return result.data!
  },
}
