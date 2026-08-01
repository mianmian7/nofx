import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { HyperliquidWalletConnect } from './HyperliquidWalletConnect'

const mocks = vi.hoisted(() => ({
  getExchangeConfigs: vi.fn(),
  generateWallet: vi.fn(),
  submitHyperliquidApproval: vi.fn(),
  createExchangeEncrypted: vi.fn(),
  updateExchangeConfigsEncrypted: vi.fn(),
  getProvider: vi.fn(),
}))

vi.mock('../../lib/api', () => ({
  api: {
    getExchangeConfigs: mocks.getExchangeConfigs,
    generateWallet: mocks.generateWallet,
    submitHyperliquidApproval: mocks.submitHyperliquidApproval,
    createExchangeEncrypted: mocks.createExchangeEncrypted,
    updateExchangeConfigsEncrypted: mocks.updateExchangeConfigsEncrypted,
  },
}))

vi.mock('../../lib/hyperliquidWallet', () => ({
  formatUSDC: (value?: number) =>
    typeof value === 'number' ? value.toFixed(2) : '--',
  getPreferredWalletProvider: () => mocks.getProvider(),
  normalizeAddress: (value: string) => value.toLowerCase(),
  shortAddress: (value?: string) => value || '--',
  signHyperliquidUserAction: async (
    provider: {
      request: (args: {
        method: string
        params?: unknown[]
      }) => Promise<unknown>
    },
    signerAddress: string,
    action: Record<string, unknown>
  ) => {
    const raw = await provider.request({
      method: 'eth_signTypedData_v4',
      params: [signerAddress, '{}'],
    })
    if (typeof raw !== 'string') throw new Error('Invalid test signature')
    return {
      action: { ...action, signatureChainId: '0xa4b1' },
      signature: { r: '0x1', s: '0x2', v: 27 },
    }
  },
}))

describe('Hyperliquid guided connection', () => {
  beforeEach(() => {
    localStorage.clear()
    mocks.getExchangeConfigs.mockReset()
    mocks.getExchangeConfigs.mockResolvedValue([])
    mocks.generateWallet.mockReset()
    mocks.generateWallet.mockResolvedValue({
      address: '0xagent',
      private_key: 'test-agent-key',
    })
    mocks.submitHyperliquidApproval.mockReset()
    mocks.submitHyperliquidApproval.mockResolvedValue(undefined)
    mocks.createExchangeEncrypted.mockReset()
    mocks.createExchangeEncrypted.mockResolvedValue({ id: 'hyperliquid-1' })
    mocks.updateExchangeConfigsEncrypted.mockReset()
    mocks.updateExchangeConfigsEncrypted.mockResolvedValue(undefined)
    mocks.getProvider.mockReset()
    mocks.getProvider.mockReturnValue(undefined)
  })

  it('shows one clear current action instead of internal implementation steps', () => {
    render(
      <HyperliquidWalletConnect language="en" isLoggedIn variant="inline" />
    )

    expect(
      screen.getByRole('heading', { name: 'Connect Hyperliquid' })
    ).toBeTruthy()
    expect(screen.getByText('Connect your wallet to begin')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Connect wallet' })).toBeTruthy()

    expect(screen.queryByText('Create a trading key for NOFX')).toBeNull()
    expect(
      screen.queryByText('Approve the small per-trade builder fee')
    ).toBeNull()
    expect(screen.queryByText('Save to NOFX — done')).toBeNull()
  })

  it('explains the authorization outcome before asking for a signature', () => {
    render(
      <HyperliquidWalletConnect language="en" isLoggedIn variant="inline" />
    )

    expect(
      screen.getByText(/trade-only access; it can never withdraw your funds/i)
    ).toBeTruthy()
    expect(screen.getByText(/two wallet approvals/i)).toBeTruthy()
  })

  it('prepares the agent after wallet connect and saves automatically after both approvals', async () => {
    const provider = {
      request: vi.fn(async ({ method }: { method: string }) => {
        if (method === 'eth_requestAccounts') return ['0xMAIN']
        if (method === 'eth_signTypedData_v4') return `0x${'11'.repeat(65)}`
        throw new Error(`Unexpected method: ${method}`)
      }),
      on: vi.fn(),
      removeListener: vi.fn(),
    }
    mocks.getProvider.mockReturnValue(provider)

    render(
      <HyperliquidWalletConnect language="en" isLoggedIn variant="inline" />
    )

    fireEvent.click(screen.getByRole('button', { name: 'Connect wallet' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Approve trade-only access' })
      ).toBeTruthy()
    )
    expect(mocks.generateWallet).toHaveBeenCalledOnce()

    fireEvent.click(
      screen.getByRole('button', { name: 'Approve trade-only access' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Approve fee & finish' })
      ).toBeTruthy()
    )

    fireEvent.click(
      screen.getByRole('button', { name: 'Approve fee & finish' })
    )
    await waitFor(() =>
      expect(screen.getByText('Hyperliquid is ready')).toBeTruthy()
    )
    expect(mocks.createExchangeEncrypted).toHaveBeenCalledOnce()
    expect(screen.queryByRole('button', { name: 'Save connection' })).toBeNull()
  })

  it('clears wallet-bound authorization when the browser wallet account changes', async () => {
    localStorage.setItem(
      'nofx.hyperliquid.connection.v6',
      JSON.stringify({
        mainWallet: '0xmain',
        agentAddress: '0xagent',
        agentApproved: true,
        builderApproved: true,
        savedExchangeId: 'hyperliquid-1',
      })
    )
    let accountsChanged: ((accounts: unknown) => void) | undefined
    mocks.getProvider.mockReturnValue({
      request: vi.fn(),
      on: vi.fn((event, handler) => {
        if (event === 'accountsChanged') accountsChanged = handler
      }),
      removeListener: vi.fn(),
    })

    render(
      <HyperliquidWalletConnect language="en" isLoggedIn variant="inline" />
    )
    expect(screen.getByText('Hyperliquid is ready')).toBeTruthy()

    await act(async () => {
      accountsChanged?.(['0xNEW'])
      await Promise.resolve()
    })

    expect(screen.queryByText('Hyperliquid is ready')).toBeNull()
    expect(screen.getByText('Prepare secure trading access')).toBeTruthy()
    expect(screen.getByText('0xnew')).toBeTruthy()
  })

  it('does not submit a signature if the wallet account changes while approval is open', async () => {
    let accountsChanged: ((accounts: unknown) => void) | undefined
    let resolveSignature: ((signature: string) => void) | undefined
    const signature = new Promise<string>((resolve) => {
      resolveSignature = resolve
    })
    const provider = {
      request: vi.fn(({ method }: { method: string }) => {
        if (method === 'eth_requestAccounts') return Promise.resolve(['0xMAIN'])
        if (method === 'eth_signTypedData_v4') return signature
        throw new Error(`Unexpected method: ${method}`)
      }),
      on: vi.fn((event, handler) => {
        if (event === 'accountsChanged') accountsChanged = handler
      }),
      removeListener: vi.fn(),
    }
    mocks.getProvider.mockReturnValue(provider)

    render(
      <HyperliquidWalletConnect language="en" isLoggedIn variant="inline" />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Connect wallet' }))
    await screen.findByRole('button', { name: 'Approve trade-only access' })
    fireEvent.click(
      screen.getByRole('button', { name: 'Approve trade-only access' })
    )
    act(() => accountsChanged?.(['0xNEW']))
    await act(async () => resolveSignature?.(`0x${'11'.repeat(65)}`))

    await waitFor(() =>
      expect(mocks.submitHyperliquidApproval).not.toHaveBeenCalled()
    )
    expect(screen.getByText(/wallet account changed/i)).toBeTruthy()
    expect(screen.queryByText('Hyperliquid is ready')).toBeNull()
  })

  it('keeps the successful save state when post-save refresh fails', async () => {
    const provider = {
      request: vi.fn(async ({ method }: { method: string }) => {
        if (method === 'eth_requestAccounts') return ['0xMAIN']
        if (method === 'eth_signTypedData_v4') return `0x${'11'.repeat(65)}`
        throw new Error(`Unexpected method: ${method}`)
      }),
      on: vi.fn(),
      removeListener: vi.fn(),
    }
    mocks.getProvider.mockReturnValue(provider)

    render(
      <HyperliquidWalletConnect
        language="en"
        isLoggedIn
        variant="inline"
        onSaved={async () => {
          throw new Error('refresh failed')
        }}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Connect wallet' }))
    await screen.findByRole('button', { name: 'Approve trade-only access' })
    fireEvent.click(
      screen.getByRole('button', { name: 'Approve trade-only access' })
    )
    await screen.findByRole('button', { name: 'Approve fee & finish' })
    fireEvent.click(
      screen.getByRole('button', { name: 'Approve fee & finish' })
    )

    await screen.findByText('Hyperliquid is ready')
    expect(screen.queryByRole('button', { name: 'Save connection' })).toBeNull()
  })
})
