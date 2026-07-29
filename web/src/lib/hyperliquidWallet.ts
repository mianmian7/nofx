/**
 * Shared helpers for Hyperliquid wallet flows: injected EVM provider
 * discovery, EIP-712 typed-data construction for Hyperliquid user-signed
 * actions, and signature handling. Used by the connect/onboarding flow and
 * the deposit/transfer funds panel.
 */

declare global {
  interface Window {
    ethereum?: WalletProvider & { providers?: WalletProvider[] }
  }
}

export type WalletProvider = {
  request: (args: { method: string; params?: unknown[] }) => Promise<unknown>
  on?: (event: string, handler: (...args: unknown[]) => void) => void
  removeListener?: (
    event: string,
    handler: (...args: unknown[]) => void
  ) => void
  isMetaMask?: boolean
  isRabby?: boolean
  isOkxWallet?: boolean
  isCoinbaseWallet?: boolean
  isTrust?: boolean
  isPhantom?: boolean
  isBackpack?: boolean
  isBraveWallet?: boolean
  isExodus?: boolean
  isFrame?: boolean
}

export function getWalletProviders(): WalletProvider[] {
  const injected = window.ethereum
  if (!injected) return []
  const providers =
    Array.isArray(injected.providers) && injected.providers.length > 0
      ? injected.providers
      : [injected]
  const seen = new Set<WalletProvider>()
  return providers.filter((provider) => {
    if (!provider || seen.has(provider)) return false
    seen.add(provider)
    return true
  })
}

export function getPreferredWalletProvider(): WalletProvider | undefined {
  const providers = getWalletProviders()
  return (
    providers.find((provider) => provider.isRabby) ||
    providers.find((provider) => provider.isMetaMask) ||
    providers.find((provider) => provider.isCoinbaseWallet) ||
    providers.find((provider) => provider.isPhantom) ||
    providers.find((provider) => provider.isBraveWallet) ||
    providers.find((provider) => provider.isBackpack) ||
    providers.find((provider) => provider.isOkxWallet) ||
    providers.find((provider) => provider.isTrust) ||
    providers.find((provider) => provider.isExodus) ||
    providers.find((provider) => provider.isFrame) ||
    providers[0]
  )
}

export function normalizeAddress(address: string) {
  return address.trim().toLowerCase()
}

export function shortAddress(address?: string) {
  if (!address) return ''
  return `${address.slice(0, 6)}…${address.slice(-4)}`
}

export function formatUSDC(value?: number) {
  if (typeof value !== 'number' || Number.isNaN(value)) return '--'
  return new Intl.NumberFormat('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value)
}

export function splitSignature(signature: string) {
  const hex = signature.startsWith('0x') ? signature.slice(2) : signature
  if (hex.length !== 130) {
    throw new Error('Invalid wallet signature length')
  }
  const v = parseInt(hex.slice(128, 130), 16)
  return {
    r: `0x${hex.slice(0, 64)}`,
    s: `0x${hex.slice(64, 128)}`,
    v: v < 27 ? v + 27 : v,
  }
}

/**
 * Chain id of the wallet's currently active chain, in hex (e.g. "0xa4b1").
 * Hyperliquid accepts any signing chain — the network is selected by
 * `hyperliquidChain`, not by this value — as long as the action's
 * `signatureChainId` matches the EIP-712 domain the wallet signed. Strict
 * EIP-712 wallets (e.g. Rabby) additionally refuse to sign for a domain that
 * differs from the active chain, so the domain must follow the wallet.
 */
export async function getWalletChainIdHex(
  provider: WalletProvider
): Promise<string> {
  const raw = await provider.request({ method: 'eth_chainId' })
  const hex = typeof raw === 'string' ? raw.toLowerCase() : ''
  if (!/^0x[0-9a-f]{1,16}$/.test(hex)) {
    throw new Error('Wallet returned an invalid chain id')
  }
  return hex
}

export function buildTypedData(
  primaryType: string,
  fields: { name: string; type: string }[],
  message: Record<string, unknown>,
  chainIdHex: string
) {
  return {
    domain: {
      name: 'HyperliquidSignTransaction',
      version: '1',
      chainId: parseInt(chainIdHex, 16),
      verifyingContract: '0x0000000000000000000000000000000000000000',
    },
    types: {
      EIP712Domain: [
        { name: 'name', type: 'string' },
        { name: 'version', type: 'string' },
        { name: 'chainId', type: 'uint256' },
        { name: 'verifyingContract', type: 'address' },
      ],
      [primaryType]: fields,
    },
    primaryType,
    message,
  }
}

/**
 * Sign a Hyperliquid user-signed action with the connected wallet.
 * `signerAddress` must be the wallet that owns the Hyperliquid account —
 * user-signed actions derive the acting account from the signature itself.
 *
 * The wallet's active chain is injected as `signatureChainId` and used as the
 * EIP-712 domain chain, keeping the two consistent as Hyperliquid requires.
 * Returns the final action (submit exactly this object) plus the signature.
 */
export async function signHyperliquidUserAction(
  provider: WalletProvider,
  signerAddress: string,
  action: Record<string, unknown>,
  primaryType: string,
  fields: { name: string; type: string }[]
) {
  const chainIdHex = await getWalletChainIdHex(provider)
  const signedAction: Record<string, unknown> = {
    ...action,
    signatureChainId: chainIdHex,
  }
  const typedData = buildTypedData(primaryType, fields, signedAction, chainIdHex)
  const raw = await provider.request({
    method: 'eth_signTypedData_v4',
    params: [signerAddress, JSON.stringify(typedData)],
  })
  if (typeof raw !== 'string') {
    throw new Error('Wallet returned an invalid signature')
  }
  return { action: signedAction, signature: splitSignature(raw) }
}
