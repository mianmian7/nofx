import { useCallback, useEffect, useState } from 'react'
import { ArrowDownUp, Loader2, QrCode, RefreshCw } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { toast } from 'sonner'
import { api } from '../../lib/api'
import type { HyperliquidAccountSummary } from '../../lib/api/wallet'
import type { Language } from '../../i18n/translations'
import { copyWithToast } from '../../lib/clipboard'
import {
  formatUSDC,
  getPreferredWalletProvider,
  normalizeAddress,
  shortAddress,
  signHyperliquidUserAction,
} from '../../lib/hyperliquidWallet'

interface HyperliquidFundsPanelProps {
  language: Language
  walletAddress: string
  onTransferred?: () => void | Promise<void>
}

const TEXT = {
  zh: {
    deposit: '充值',
    transfer: '划转',
    balances: '余额',
    spot: '现货(主钱包)',
    perp: '合约(交易账户)',
    available: '可用',
    withdrawable: '可提取',
    depositHint:
      '向下方地址转入 USDC 即可充值:通过 Arbitrum One 链转入会自动进入合约账户(最低 5 USDC);从其他 Hyperliquid 账户现货转账则进入现货,需再划转到合约。',
    depositWarn: '仅支持 USDC。请勿从交易所直接提现其他币种或其他链到此地址。',
    copyAddress: '复制地址',
    addressCopied: '地址已复制',
    spotToPerp: '现货 → 合约',
    perpToSpot: '合约 → 现货',
    amount: '划转数量 (USDC)',
    max: '全部',
    submit: '签名并划转',
    submitting: '划转中…',
    connectFirst: '连接主钱包以签名划转',
    connect: '连接钱包',
    noProvider: '未检测到浏览器钱包插件(如 MetaMask / OKX)',
    wrongWallet: (want: string, got: string) =>
      `钱包地址不匹配:需要 ${want},当前连接 ${got}。划转签名必须来自主钱包本身。`,
    invalidAmount: '请输入有效的划转数量',
    exceedsBalance: '超出可用余额',
    success: '划转成功,余额稍后刷新',
    failed: '划转失败',
  },
  en: {
    deposit: 'Deposit',
    transfer: 'Transfer',
    balances: 'Balances',
    spot: 'Spot (main wallet)',
    perp: 'Perp (trading account)',
    available: 'available',
    withdrawable: 'withdrawable',
    depositHint:
      'Send USDC to the address below: transfers on Arbitrum One are auto-credited to the perp account (min 5 USDC); spot transfers from another Hyperliquid account arrive in spot and need a transfer to perp.',
    depositWarn:
      'USDC only. Do not withdraw other tokens or use other chains to this address.',
    copyAddress: 'Copy address',
    addressCopied: 'Address copied',
    spotToPerp: 'Spot → Perp',
    perpToSpot: 'Perp → Spot',
    amount: 'Amount (USDC)',
    max: 'Max',
    submit: 'Sign & transfer',
    submitting: 'Transferring…',
    connectFirst: 'Connect the main wallet to sign the transfer',
    connect: 'Connect wallet',
    noProvider: 'No browser wallet extension detected (MetaMask / OKX etc.)',
    wrongWallet: (want: string, got: string) =>
      `Wallet mismatch: expected ${want} but connected ${got}. The transfer must be signed by the main wallet itself.`,
    invalidAmount: 'Enter a valid transfer amount',
    exceedsBalance: 'Amount exceeds the available balance',
    success: 'Transfer submitted, balances refresh shortly',
    failed: 'Transfer failed',
  },
}

export function HyperliquidFundsPanel({
  language,
  walletAddress,
  onTransferred,
}: HyperliquidFundsPanelProps) {
  const t = TEXT[language === 'zh' ? 'zh' : 'en']
  const address = normalizeAddress(walletAddress)
  const [tab, setTab] = useState<'deposit' | 'transfer'>('deposit')
  const [account, setAccount] = useState<HyperliquidAccountSummary | null>(null)
  const [loading, setLoading] = useState(false)
  const [toPerp, setToPerp] = useState(true)
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    if (!address) return
    setLoading(true)
    try {
      setAccount(await api.getHyperliquidAccount(address))
    } catch {
      // keep the previous snapshot; the panel stays usable
    } finally {
      setLoading(false)
    }
  }, [address])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const availableFrom = toPerp
    ? (account?.spotUsdcAvailable ?? 0)
    : (account?.withdrawable ?? 0)

  async function submitTransfer() {
    setError('')
    const parsed = Number(amount)
    if (!Number.isFinite(parsed) || parsed <= 0) {
      setError(t.invalidAmount)
      return
    }
    if (parsed > availableFrom + 1e-9) {
      setError(t.exceedsBalance)
      return
    }
    const provider = getPreferredWalletProvider()
    if (!provider) {
      setError(t.noProvider)
      return
    }
    setBusy(true)
    try {
      const accounts = (await provider.request({
        method: 'eth_requestAccounts',
      })) as string[]
      const signer = normalizeAddress(accounts?.[0] ?? '')
      if (!signer) throw new Error(t.connectFirst)
      if (signer !== address) {
        throw new Error(t.wrongWallet(shortAddress(address), shortAddress(signer)))
      }
      const nonce = Date.now()
      const action = {
        type: 'usdClassTransfer',
        signatureChainId: '0x66eee',
        hyperliquidChain: 'Mainnet',
        amount: String(parsed),
        toPerp,
        nonce,
      }
      const signature = await signHyperliquidUserAction(
        provider,
        signer,
        action,
        'HyperliquidTransaction:UsdClassTransfer',
        [
          { name: 'hyperliquidChain', type: 'string' },
          { name: 'amount', type: 'string' },
          { name: 'toPerp', type: 'bool' },
          { name: 'nonce', type: 'uint64' },
        ]
      )
      await api.submitHyperliquidApproval(action, nonce, signature)
      toast.success(t.success)
      setAmount('')
      await onTransferred?.()
      // Hyperliquid settles the class transfer near-instantly; one refresh
      // shortly after covers indexing lag.
      setTimeout(() => void refresh(), 1500)
    } catch (err) {
      setError(err instanceof Error ? err.message : t.failed)
    } finally {
      setBusy(false)
    }
  }

  if (!address) return null

  return (
    <div className="rounded-xl border border-[rgba(26,24,19,0.14)] bg-nofx-bg-deeper p-4 space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => setTab('deposit')}
            className={`px-3 py-1.5 rounded-xl text-sm font-semibold flex items-center gap-1.5 ${
              tab === 'deposit'
                ? 'bg-nofx-gold text-white'
                : 'bg-nofx-bg text-nofx-text-muted hover:text-nofx-text'
            }`}
          >
            <QrCode size={14} />
            {t.deposit}
          </button>
          <button
            type="button"
            onClick={() => setTab('transfer')}
            className={`px-3 py-1.5 rounded-xl text-sm font-semibold flex items-center gap-1.5 ${
              tab === 'transfer'
                ? 'bg-nofx-gold text-white'
                : 'bg-nofx-bg text-nofx-text-muted hover:text-nofx-text'
            }`}
          >
            <ArrowDownUp size={14} />
            {t.transfer}
          </button>
        </div>
        <button
          type="button"
          onClick={() => void refresh()}
          className="text-nofx-text-muted hover:text-nofx-text"
          title={t.balances}
        >
          {loading ? (
            <Loader2 size={16} className="animate-spin" />
          ) : (
            <RefreshCw size={16} />
          )}
        </button>
      </div>

      <div className="grid grid-cols-2 gap-3 text-sm">
        <div className="rounded-xl border border-[rgba(26,24,19,0.14)] bg-nofx-bg p-3">
          <div className="text-nofx-text-muted text-xs">{t.spot}</div>
          <div className="font-mono font-medium text-nofx-text">
            {formatUSDC(account?.spotUsdc)} USDC
          </div>
          <div className="text-xs text-nofx-text-muted">
            {t.available}: {formatUSDC(account?.spotUsdcAvailable)}
          </div>
        </div>
        <div className="rounded-xl border border-[rgba(26,24,19,0.14)] bg-nofx-bg p-3">
          <div className="text-nofx-text-muted text-xs">{t.perp}</div>
          <div className="font-mono font-medium text-nofx-text">
            {formatUSDC(account?.accountValue)} USDC
          </div>
          <div className="text-xs text-nofx-text-muted">
            {t.withdrawable}: {formatUSDC(account?.withdrawable)}
          </div>
        </div>
      </div>

      {tab === 'deposit' ? (
        <div className="space-y-3">
          <div className="flex justify-center rounded-xl bg-white p-4">
            <QRCodeSVG value={walletAddress} size={168} marginSize={1} />
          </div>
          <button
            type="button"
            onClick={() => void copyWithToast(walletAddress, t.addressCopied)}
            className="w-full font-mono text-xs text-center break-all text-nofx-text-muted hover:text-nofx-gold"
            title={t.copyAddress}
          >
            {walletAddress}
          </button>
          <p className="text-xs text-nofx-text-muted leading-5">{t.depositHint}</p>
          <p className="text-xs text-amber-500">{t.depositWarn}</p>
        </div>
      ) : (
        <div className="space-y-3">
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setToPerp(true)}
              className={`flex-1 px-3 py-1.5 rounded-xl text-sm font-semibold ${
                toPerp
                  ? 'bg-nofx-gold text-white'
                  : 'bg-nofx-bg text-nofx-text-muted hover:text-nofx-text'
              }`}
            >
              {t.spotToPerp}
            </button>
            <button
              type="button"
              onClick={() => setToPerp(false)}
              className={`flex-1 px-3 py-1.5 rounded-xl text-sm font-semibold ${
                !toPerp
                  ? 'bg-nofx-gold text-white'
                  : 'bg-nofx-bg text-nofx-text-muted hover:text-nofx-text'
              }`}
            >
              {t.perpToSpot}
            </button>
          </div>
          <div>
            <label className="text-xs text-nofx-text-muted">{t.amount}</label>
            <div className="flex gap-2 mt-1">
              <input
                type="number"
                min="0"
                step="0.01"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                className="flex-1 rounded-xl border border-[rgba(26,24,19,0.14)] bg-nofx-bg px-3 py-1.5 text-sm font-mono text-nofx-text"
                placeholder="0.00"
              />
              <button
                type="button"
                onClick={() => setAmount(availableFrom.toFixed(2))}
                className="px-3 py-1.5 rounded-xl border border-[rgba(26,24,19,0.14)] bg-nofx-bg text-sm text-nofx-text-muted hover:text-nofx-text"
              >
                {t.max}
              </button>
            </div>
          </div>
          {error && <p className="text-xs text-red-500">{error}</p>}
          <button
            type="button"
            disabled={busy}
            onClick={() => void submitTransfer()}
            className="w-full flex items-center justify-center gap-2 rounded-xl border border-nofx-gold/30 bg-nofx-gold/10 px-4 py-2.5 text-sm font-bold text-nofx-gold transition hover:bg-nofx-gold/20 disabled:opacity-60 disabled:cursor-not-allowed"
          >
            {busy && <Loader2 size={14} className="animate-spin" />}
            {busy ? t.submitting : t.submit}
          </button>
          <p className="text-xs text-nofx-text-muted">{t.connectFirst}</p>
        </div>
      )}
    </div>
  )
}
