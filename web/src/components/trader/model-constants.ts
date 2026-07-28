// Constants for AI model and provider configuration

export interface Claw402Model {
  id: string
  name: string
  provider: string
  desc: string
  brand: string  // key for getModelIcon / getModelColor
  price: number  // USD per call
  isNew?: boolean
}

export interface AIProviderConfig {
  defaultModel: string
  apiUrl: string
  apiName: string
}

// Get friendly AI model display name
export function getModelDisplayName(modelId: string): string {
  switch (modelId.toLowerCase()) {
    case 'deepseek':
      return 'DeepSeek'
    case 'qwen':
      return 'Qwen'
    case 'claude':
      return 'Claude'
    default:
      return modelId.toUpperCase()
  }
}

// Extract name part after underscore
export function getShortName(fullName: string): string {
  const parts = fullName.split('_')
  return parts.length > 1 ? parts[parts.length - 1] : fullName
}

export const DEFAULT_CLAW402_MODEL = 'gpt-5.6'

// Models available through Claw402 (x402 USDC payment protocol)
// Must stay in sync with the claw402 catalog (GET /api/v1/catalog)
export const CLAW402_MODELS: Claw402Model[] = [
  { id: 'gpt-5.6', name: 'GPT-5.6 Sol', provider: 'OpenAI', desc: 'Flagship', brand: 'openai', price: 0.06 },
  { id: 'gpt-5.6-terra', name: 'GPT-5.6 Terra', provider: 'OpenAI', desc: 'Balanced', brand: 'openai', price: 0.03 },
  { id: 'gpt-5.6-luna', name: 'GPT-5.6 Luna', provider: 'OpenAI', desc: 'Cost-efficient', brand: 'openai', price: 0.012 },
  { id: 'claude-fable', name: 'Claude Fable 5', provider: 'Anthropic', desc: 'Most capable', brand: 'claude', price: 0.24 },
  { id: 'claude-opus', name: 'Claude Opus 4.8', provider: 'Anthropic', desc: 'Coding & agents flagship', brand: 'claude', price: 0.12 },
  { id: 'deepseek-v4-flash', name: 'DeepSeek-V4 Flash', provider: 'DeepSeek', desc: 'Fast general model', brand: 'deepseek', price: 0.003 },
  { id: 'deepseek-v4-pro', name: 'DeepSeek-V4 Pro', provider: 'DeepSeek', desc: 'Advanced reasoning', brand: 'deepseek', price: 0.01 },
  { id: 'glm-5', name: 'GLM-5', provider: 'Z.ai', desc: 'Deep reasoning flagship', brand: 'zhipu', price: 0.003 },
]

// AI Provider configuration - default models and API links
export const AI_PROVIDER_CONFIG: Record<string, AIProviderConfig> = {
  claw402: {
    defaultModel: DEFAULT_CLAW402_MODEL,
    apiUrl: 'https://claw402.ai',
    apiName: 'Claw402',
  },
}

// Helper function to get exchange display name from exchange ID (UUID)
export function getExchangeDisplayName(exchangeId: string | undefined, exchanges: { id: string; exchange_type?: string; name: string; account_name?: string }[]): string {
  if (!exchangeId) return 'Unknown'
  const exchange = exchanges.find(e => e.id === exchangeId)
  if (!exchange) return exchangeId.substring(0, 8).toUpperCase() + '...' // Show truncated UUID if not found
  const typeName = exchange.exchange_type?.toUpperCase() || exchange.name
  return exchange.account_name ? `${typeName} - ${exchange.account_name}` : typeName
}

// Helper function to check if exchange is a perp-dex type (wallet-based)
export function isPerpDexExchange(exchangeType: string | undefined): boolean {
  if (!exchangeType) return false
  const perpDexTypes = ['hyperliquid', 'lighter', 'aster']
  return perpDexTypes.includes(exchangeType.toLowerCase())
}

// Helper function to get wallet address for perp-dex exchanges
export function getWalletAddress(exchange: { exchange_type?: string; hyperliquidWalletAddr?: string; lighterWalletAddr?: string; asterSigner?: string } | undefined): string | undefined {
  if (!exchange) return undefined
  const type = exchange.exchange_type?.toLowerCase()
  switch (type) {
    case 'hyperliquid':
      return exchange.hyperliquidWalletAddr
    case 'lighter':
      return exchange.lighterWalletAddr
    case 'aster':
      return exchange.asterSigner
    default:
      return undefined
  }
}

// Helper function to truncate wallet address for display
export function truncateAddress(address: string, startLen = 6, endLen = 4): string {
  if (address.length <= startLen + endLen + 3) return address
  return `${address.slice(0, startLen)}...${address.slice(-endLen)}`
}
