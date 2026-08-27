import { useEffect, useState, type FormEvent } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { BarChart3, CandlestickChart } from 'lucide-react'
import { EquityChart } from './EquityChart'
import { AdvancedChart } from './AdvancedChart'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

interface ChartTabsProps {
  traderId: string
  selectedSymbol?: string
  updateKey?: number
  exchangeId?: string
}

type ChartTab = 'equity' | 'kline'
type Interval = '1m' | '5m' | '15m' | '30m' | '1h' | '4h' | '1d'
type MarketType = 'crypto' | 'stocks' | 'forex' | 'metals'

const INTERVALS: Interval[] = ['1m', '5m', '15m', '30m', '1h', '4h', '1d']
const MARKET_CONFIG: Record<
  MarketType,
  { exchange: string; symbol: string; label: string }
> = {
  crypto: { exchange: 'binance', symbol: 'BTCUSDT', label: 'Binance' },
  stocks: { exchange: 'alpaca', symbol: 'AAPL', label: 'Stocks' },
  forex: { exchange: 'forex', symbol: 'EUR/USD', label: 'Forex' },
  metals: { exchange: 'metals', symbol: 'XAU/USD', label: 'Metals' },
}

function cryptoSymbol(raw?: string): string {
  const normalized = (raw || '')
    .toUpperCase()
    .trim()
    .replace(/[-_]/g, '')
    .replace(/(SWAP|PERP)$/, '')
  if (normalized.endsWith('USDT')) return normalized
  return normalized ? `${normalized}USDT` : 'BTCUSDT'
}

function cryptoExchange(raw?: string): string {
  const normalized = (raw || '').toLowerCase().trim()
  return normalized === 'okx' ||
    normalized === 'bitget' ||
    normalized === 'binance'
    ? normalized
    : 'binance'
}

function cryptoExchangeLabel(exchange: string): string {
  if (exchange === 'okx') return 'OKX'
  if (exchange === 'bitget') return 'Bitget'
  return 'Binance'
}

export function ChartTabs({
  traderId,
  selectedSymbol,
  updateKey,
  exchangeId,
}: ChartTabsProps) {
  const { language } = useLanguage()
  const [activeTab, setActiveTab] = useState<ChartTab>('equity')
  const [chartSymbol, setChartSymbol] = useState('BTCUSDT')
  const [interval, setInterval] = useState<Interval>('5m')
  const [symbolInput, setSymbolInput] = useState('')
  const [marketType, setMarketType] = useState<MarketType>('crypto')
  const traderExchange = cryptoExchange(exchangeId)
  const market =
    marketType === 'crypto'
      ? {
          exchange: traderExchange,
          symbol: 'BTCUSDT',
          label: cryptoExchangeLabel(traderExchange),
        }
      : MARKET_CONFIG[marketType]

  useEffect(() => {
    if (selectedSymbol) {
      setChartSymbol(cryptoSymbol(selectedSymbol))
      setMarketType('crypto')
      setActiveTab('kline')
    }
  }, [selectedSymbol, updateKey])

  const handleSymbolSubmit = (event: FormEvent) => {
    event.preventDefault()
    const raw = symbolInput.trim().toUpperCase()
    if (!raw) return
    setChartSymbol(marketType === 'crypto' ? cryptoSymbol(raw) : raw)
    setSymbolInput('')
  }

  return (
    <div
      className={`nofx-glass rounded-lg border border-nofx-border relative z-10 w-full flex flex-col transition-all duration-300 ${typeof window !== 'undefined' && window.innerWidth < 768 ? 'h-[500px]' : 'h-[600px]'}`}
    >
      <div className="relative z-20 flex flex-wrap md:flex-nowrap items-center justify-between gap-y-2 px-3 py-2 shrink-0 backdrop-blur-md bg-nofx-bg/80 rounded-t-lg border-b border-nofx-border">
        <div className="flex items-center gap-1">
          <button
            onClick={() => setActiveTab('equity')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-[11px] font-medium transition-all ${
              activeTab === 'equity'
                ? 'bg-nofx-gold/15 text-nofx-gold border border-nofx-gold/30 font-bold'
                : 'text-nofx-text-muted hover:text-nofx-text hover:bg-nofx-gold/10'
            }`}
          >
            <BarChart3 className="w-3.5 h-3.5" />
            <span>{t('accountEquityCurve', language)}</span>
          </button>
          <button
            onClick={() => setActiveTab('kline')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-[11px] font-medium transition-all ${
              activeTab === 'kline'
                ? 'bg-nofx-gold/15 text-nofx-gold border border-nofx-gold/30 font-bold'
                : 'text-nofx-text-muted hover:text-nofx-text hover:bg-nofx-gold/10'
            }`}
          >
            <CandlestickChart className="w-3.5 h-3.5" />
            <span>{t('marketChart', language)}</span>
          </button>
          {activeTab === 'kline' &&
            (Object.keys(MARKET_CONFIG) as MarketType[]).map((type) => (
              <button
                key={type}
                onClick={() => {
                  setMarketType(type)
                  setChartSymbol(MARKET_CONFIG[type].symbol)
                }}
                className={`hidden md:inline-flex px-2 py-1 rounded text-[10px] ${
                  marketType === type
                    ? 'bg-nofx-gold/15 text-nofx-gold font-bold'
                    : 'text-nofx-text-muted hover:text-nofx-text'
                }`}
              >
                {MARKET_CONFIG[type].label}
              </button>
            ))}
        </div>

        {activeTab === 'kline' && (
          <div className="flex items-center gap-2 md:gap-3 w-full md:w-auto min-w-0">
            <span className="px-2.5 py-1 bg-nofx-bg-deeper border border-nofx-border rounded text-[11px] font-bold text-nofx-text font-mono">
              {market.label} · {chartSymbol}
            </span>
            <div className="flex items-center bg-nofx-bg-deeper rounded border border-nofx-border overflow-x-auto no-scrollbar">
              {INTERVALS.map((value) => (
                <button
                  key={value}
                  onClick={() => setInterval(value)}
                  className={`px-2 py-1 text-[10px] font-medium transition-all ${
                    interval === value
                      ? 'bg-nofx-gold/20 text-nofx-gold font-bold'
                      : 'text-nofx-text-muted hover:text-nofx-text hover:bg-nofx-gold/10'
                  }`}
                >
                  {value}
                </button>
              ))}
            </div>
            <form
              onSubmit={handleSymbolSubmit}
              className="hidden md:flex items-center shrink-0"
            >
              <input
                value={symbolInput}
                onChange={(event) => setSymbolInput(event.target.value)}
                placeholder="BTC"
                className="w-20 px-2 py-1 bg-nofx-bg-deeper border border-nofx-border rounded-l text-[10px] text-nofx-text placeholder-nofx-text-muted focus:outline-none focus:border-nofx-gold/50 font-mono"
              />
              <button
                type="submit"
                className="px-2 py-1 bg-nofx-bg-lighter border border-nofx-border border-l-0 rounded-r text-[10px] text-nofx-text-muted hover:text-nofx-text"
              >
                Go
              </button>
            </form>
          </div>
        )}
      </div>

      <div className="relative flex-1 bg-nofx-bg/50 rounded-b-lg overflow-hidden h-full min-h-0">
        <AnimatePresence mode="wait">
          {activeTab === 'equity' ? (
            <motion.div
              key="equity"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              className="h-full w-full absolute inset-0"
            >
              <EquityChart traderId={traderId} embedded />
            </motion.div>
          ) : (
            <motion.div
              key={`kline-${market.exchange}-${chartSymbol}-${interval}`}
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              className="h-full w-full absolute inset-0"
            >
              <AdvancedChart
                symbol={chartSymbol}
                interval={interval}
                traderID={traderId}
                exchange={market.exchange}
                onSymbolChange={(symbol) =>
                  setChartSymbol(
                    marketType === 'crypto' ? cryptoSymbol(symbol) : symbol
                  )
                }
              />
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </div>
  )
}
