import { describe, expect, it, vi } from 'vitest'
import {
  getWalletChainIdHex,
  signHyperliquidUserAction,
  type WalletProvider,
} from './hyperliquidWallet'

const SIGNATURE = `0x${'11'.repeat(64)}01`

describe('Hyperliquid wallet signing chain', () => {
  it('uses the active wallet chain for both the action and EIP-712 domain', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'eth_chainId') return '0xA4B1'
      if (method === 'eth_signTypedData_v4') return SIGNATURE
      throw new Error(`Unexpected method: ${method}`)
    })
    const provider: WalletProvider = { request }
    const action = {
      type: 'approveAgent',
      hyperliquidChain: 'Mainnet',
      agentAddress: '0x0000000000000000000000000000000000000001',
      nonce: 1,
    }

    const result = await signHyperliquidUserAction(
      provider,
      '0x0000000000000000000000000000000000000002',
      action,
      'HyperliquidTransaction:ApproveAgent',
      [
        { name: 'hyperliquidChain', type: 'string' },
        { name: 'agentAddress', type: 'address' },
        { name: 'agentName', type: 'string' },
        { name: 'nonce', type: 'uint64' },
      ]
    )

    expect(result.action).toEqual({ ...action, signatureChainId: '0xa4b1' })
    expect(action).not.toHaveProperty('signatureChainId')
    expect(request).toHaveBeenNthCalledWith(1, { method: 'eth_chainId' })

    const signCall = request.mock.calls[1]?.[0] as {
      method: string
      params: unknown[]
    }
    expect(signCall.method).toBe('eth_signTypedData_v4')
    expect(signCall.params[0]).toBe(
      '0x0000000000000000000000000000000000000002'
    )
    const typedData = JSON.parse(String(signCall.params[1]))
    expect(typedData.domain.chainId).toBe(42161)
    expect(typedData.message.signatureChainId).toBe('0xa4b1')
    expect(result.signature.v).toBe(28)
  })

  it.each([null, 42161, '', 'a4b1', '0x', '0xxyz', `0x${'1'.repeat(17)}`])(
    'rejects malformed wallet chain id %j before requesting a signature',
    async (chainId) => {
      const request = vi.fn(async ({ method }: { method: string }) => {
        if (method === 'eth_chainId') return chainId
        if (method === 'eth_signTypedData_v4') return SIGNATURE
        throw new Error(`Unexpected method: ${method}`)
      })
      const provider: WalletProvider = { request }

      await expect(getWalletChainIdHex(provider)).rejects.toThrow(
        'Wallet returned an invalid chain id'
      )
      expect(request).toHaveBeenCalledOnce()
    }
  )
})
