import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import useSWR from 'swr'
import {
  AlertCircle,
  ArrowRight,
  Loader2,
  RefreshCw,
  Search,
  ShieldCheck,
} from 'lucide-react'
import { useLanguage } from '../contexts/LanguageContext'
import { api } from '../lib/api'
import {
  getLocalSignals,
  getLocalSignalHistory,
  signalIsCurrent,
  type LocalSignal,
} from '../lib/api/localSignals'
import { ROUTES } from '../router/paths'

const field =
  'rounded-lg border border-nofx-border bg-nofx-bg px-3 py-2 text-sm text-nofx-text focus:outline-none focus:ring-2 focus:ring-nofx-gold'
const panel =
  'rounded-2xl border border-nofx-border bg-nofx-bg-lighter p-4 sm:p-6'

export function LocalSignalsPage() {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const text = (cn: string, en: string) => (zh ? cn : en)
  const navigate = useNavigate()
  const [exchange, setExchange] = useState('binance')
  const [query, setQuery] = useState('')
  const [side, setSide] = useState('all')
  const [selected, setSelected] = useState('')
  const detailRef = useRef<HTMLElement>(null)
  useEffect(() => {
    if (selected && window.innerWidth < 1024) {
      detailRef.current?.scrollIntoView?.({ block: 'start', behavior: 'auto' })
      detailRef.current?.focus({ preventScroll: true })
    }
  }, [selected])
  const [before, setBefore] = useState(0)
  const [now, setNow] = useState(Date.now())
  const [minScore, setMinScore] = useState(60)
  const [limit, setLimit] = useState(5)
  const [name, setName] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const { data, error, isLoading, isValidating, mutate } = useSWR(
    ['local-analysis', exchange],
    () => getLocalSignals(exchange),
    { refreshInterval: 15000, revalidateOnFocus: false, errorRetryCount: 2 }
  )
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 15000)
    return () => clearInterval(timer)
  }, [])
  const rows = useMemo(
    () =>
      (data?.results || []).filter(
        (row) =>
          row.symbol.includes(query.trim().toUpperCase()) &&
          (side === 'all' || row.direction === side)
      ),
    [data, query, side]
  )
  const detail = data?.results.find((row) => row.symbol === selected)
  const {
    data: historyData,
    error: historyError,
    isLoading: historyLoading,
    mutate: refreshHistory,
  } = useSWR(
    detail ? ['local-analysis-history', exchange, detail.symbol, before] : null,
    () => getLocalSignalHistory(exchange, detail!.symbol, before),
    {
      refreshInterval: before ? 0 : 60000,
      revalidateOnFocus: false,
      errorRetryCount: 2,
    }
  )
  const available = !!data?.ready && !error && now - data.observed_at <= 600000
  const current = (row: LocalSignal) => available && signalIsCurrent(row, now)
  const direction = (value: string) =>
    value === 'long'
      ? text('看多', 'Long')
      : value === 'short'
        ? text('看空', 'Short')
        : text('观望', 'Wait')
  const color = (value: string) =>
    value === 'long'
      ? 'text-nofx-success'
      : value === 'short'
        ? 'text-nofx-danger'
        : 'text-nofx-text-muted'
  const date = (value?: number) =>
    value
      ? new Date(value).toLocaleString(zh ? 'zh-CN' : 'en-US', {
          hour12: false,
        })
      : '—'
  const price = (value: number) =>
    value.toLocaleString(undefined, { maximumSignificantDigits: 8 })
  const qualified =
    data?.results.filter(
      (row) => current(row) && row.confirmed && row.score >= minScore
    ).length || 0

  async function createStrategy() {
    if (
      creating ||
      !Number.isFinite(minScore) ||
      minScore < 35 ||
      minScore > 95 ||
      !Number.isInteger(limit) ||
      limit < 1 ||
      limit > 10
    )
      return
    setCreating(true)
    setCreateError('')
    try {
      const config = await api.getDefaultStrategyConfig()
      const ai =
        config.ai_config ||
        (config.coin_source && config.indicators && config.risk_control
          ? {
              coin_source: config.coin_source,
              indicators: config.indicators,
              risk_control: config.risk_control,
            }
          : undefined)
      if (!ai)
        throw new Error(
          text(
            '默认策略配置不完整，请刷新后重试',
            'Default strategy configuration is incomplete'
          )
        )
      const source = {
        ...ai.coin_source,
        source_type: 'local_signals' as const,
        local_signal_min_score: minScore,
        local_signal_limit: limit,
        use_watchlist: false,
      }
      const indicators = {
        ...ai.indicators,
        enable_ema: true,
        enable_rsi: true,
        enable_atr: true,
        enable_volume: true,
        klines: {
          ...ai.indicators.klines,
          primary_timeframe: '15m',
          enable_multi_timeframe: true,
          selected_timeframes: ['15m', '1h', '4h'],
        },
      }
      const created = await api.createStrategy({
        name: name.trim() || text('本地行情分析策略', 'Local market analysis'),
        description: text(
          '使用交易所公开行情的本地分析，交给现有 AI 判断，并保留独立开仓风控。',
          'Local public-market analysis with the configured AI and independent execution risk controls.'
        ),
        config: {
          ...config,
          language: 'en',
          strategy_type: 'ai_trading',
          coin_source: source,
          indicators,
          ai_config: { ...ai, coin_source: source, indicators },
        },
      })
      navigate(`${ROUTES.strategy}?strategy=${encodeURIComponent(created.id)}`)
    } catch (err) {
      setCreateError(
        err instanceof Error
          ? err.message
          : text('创建失败，请重试', 'Could not create strategy')
      )
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="mx-auto max-w-7xl space-y-5 px-4 py-7 sm:px-6">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-nofx-gold">
            NOFX · {text('本地计算', 'Self-hosted analysis')}
          </p>
          <h1 className="text-2xl font-bold sm:text-3xl">
            {text('行情分析', 'Market analysis')}
          </h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-nofx-text-muted">
            {text(
              '自动读取交易所公开行情，比较短期和长期走势，给出看多、看空或观望。AI 仍负责最终交易判断，不购买外部信号。',
              'Public exchange data is compared across timeframes to identify long, short or wait conditions. Your AI still makes the final trading decision, without buying external signals.'
            )}
          </p>
        </div>
        <div className="flex shrink-0 items-end gap-2">
          <label className="flex flex-col gap-1 text-xs text-nofx-text-muted">
            {text('行情来源', 'Market venue')}
            <select
              className={field}
              value={exchange}
              onChange={(e) => {
                setExchange(e.target.value)
                setSelected('')
                setBefore(0)
              }}
            >
              <option value="binance">Binance</option>
              <option value="okx">OKX</option>
              <option value="bitget">Bitget</option>
            </select>
          </label>
          <button
            type="button"
            onClick={() => void mutate()}
            disabled={isValidating}
            className={`${field} inline-flex items-center gap-2 disabled:opacity-50`}
          >
            <RefreshCw
              className={`h-4 w-4 ${isValidating ? 'animate-spin' : ''}`}
            />
            {text('刷新', 'Refresh')}
          </button>
        </div>
      </header>

      <section
        aria-live="polite"
        className={`${panel} flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between`}
      >
        <div className="flex items-start gap-3">
          {available ? (
            <ShieldCheck className="mt-0.5 h-5 w-5 shrink-0 text-nofx-success" />
          ) : (
            <AlertCircle className="mt-0.5 h-5 w-5 shrink-0 text-nofx-gold" />
          )}
          <div>
            <p className="font-semibold">
              {isLoading
                ? text('正在读取分析结果…', 'Loading analysis…')
                : error
                  ? text(
                      '暂时无法连接分析服务',
                      'Cannot reach analysis service'
                    )
                  : available
                    ? text('行情分析正常', 'Analysis is current')
                    : data?.scanning
                      ? text(
                          '正在重新分析，暂不用于开仓',
                          'Refreshing; entries are not enabled yet'
                        )
                      : text(
                          '数据未就绪或已过期，暂不用于开仓',
                          'Data unavailable or expired; no new entries'
                        )}
            </p>
            <p className="mt-1 text-xs leading-5 text-nofx-text-muted">
              {text(
                '每 5 分钟自动检查，使用已收盘的 15 分钟、1 小时和 4 小时行情。',
                'Checked every five minutes using closed 15m, 1h and 4h candles.'
              )}{' '}
              {text('更新于：', 'Updated: ')}
              {date(data?.observed_at)}
            </p>
          </div>
        </div>
        <div className="text-sm text-nofx-text-muted">
          {text('完整数据', 'Complete markets')}{' '}
          <strong className="text-nofx-text">
            {data?.succeeded || 0} / {data?.expected || 0}
          </strong>
        </div>
      </section>
      {data?.error && (
        <p
          role="status"
          className="rounded-lg border border-nofx-gold/30 p-3 text-sm text-nofx-gold"
        >
          {text(
            '本轮获取的有效数据不足，系统会自动重试。旧结果仅供查看，不会据此新开仓。',
            'This scan is incomplete. Automatic retry is scheduled. Older observations are display-only and cannot approve new entries.'
          )}
        </p>
      )}
      {!!data?.failures.length && (
        <details className="text-xs text-nofx-text-muted">
          <summary className="cursor-pointer py-2">
            {text('查看未取得完整行情的交易对', 'Markets with incomplete data')}{' '}
            ({data.failures.length})
          </summary>
          <ul className="space-y-1 break-words">
            {data.failures.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
        </details>
      )}

      <div className="grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
        <section
          className={`${panel} min-w-0`}
          aria-label={text('行情排行榜', 'Market ranking')}
        >
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-lg font-semibold">
              {text('当前走势', 'Current direction')}
            </h2>
            <span className="text-xs text-nofx-text-muted">
              {text(
                '分数是规则一致程度，不是胜率',
                'Score means rule agreement, not win rate'
              )}
            </span>
          </div>
          <div className="mb-3 flex flex-wrap gap-2">
            <label className="relative min-w-0 flex-1">
              <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-nofx-text-muted" />
              <input
                type="search"
                aria-label={text('搜索交易对', 'Search symbols')}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="BTC, ETH…"
                className={`${field} w-full pl-9`}
              />
            </label>
            <select
              aria-label={text('筛选方向', 'Filter direction')}
              className={field}
              value={side}
              onChange={(e) => setSide(e.target.value)}
            >
              <option value="all">{text('全部方向', 'All directions')}</option>
              <option value="long">{text('看多', 'Long')}</option>
              <option value="short">{text('看空', 'Short')}</option>
              <option value="wait">{text('观望', 'Wait')}</option>
            </select>
          </div>
          {isLoading && (
            <div className="flex items-center justify-center gap-2 py-12 text-nofx-text-muted">
              <Loader2 className="h-5 w-5 animate-spin" />
              {text('读取中', 'Loading')}
            </div>
          )}
          {!isLoading && rows.length === 0 && (
            <p className="py-10 text-center text-sm text-nofx-text-muted">
              {data?.results.length
                ? text(
                    '没有符合筛选条件的交易对',
                    'No markets match these filters'
                  )
                : text(
                    '尚无完整分析结果。采集完成后会自动显示。',
                    'No complete observations yet. Results appear automatically after collection.'
                  )}
            </p>
          )}
          <div className="max-h-[36rem] overflow-y-auto divide-y divide-nofx-border">
            {rows.map((row, index) => (
              <button
                type="button"
                key={row.symbol}
                aria-pressed={selected === row.symbol}
                aria-controls="local-analysis-detail"
                onClick={() => {
                  setSelected(row.symbol)
                  setBefore(0)
                }}
                className={`flex w-full min-w-0 items-center gap-3 rounded-lg px-2 py-3 text-left transition-colors focus:outline-none focus:ring-2 focus:ring-nofx-gold ${selected === row.symbol ? 'bg-nofx-gold/10' : 'hover:bg-nofx-bg'}`}
              >
                <span className="w-5 shrink-0 text-xs text-nofx-text-muted">
                  {index + 1}
                </span>
                <div className="min-w-0 flex-1">
                  <span className="break-all text-sm font-semibold">
                    {row.symbol}
                  </span>
                  <span className="mt-0.5 block text-xs text-nofx-text-muted">
                    {price(row.frames[0]?.price || 0)} USDT
                  </span>
                </div>
                <div className="shrink-0 text-right">
                  <span
                    className={`text-sm font-semibold ${current(row) ? color(row.direction) : 'text-nofx-text-muted'}`}
                  >
                    {current(row)
                      ? direction(row.direction)
                      : text('已过期', 'Expired')}
                  </span>
                  <span className="mt-0.5 block text-xs text-nofx-text-muted">
                    {row.score.toFixed(1)} / 100
                  </span>
                </div>
                <ArrowRight className="h-4 w-4 shrink-0 text-nofx-text-muted" />
              </button>
            ))}
          </div>
        </section>

        <section
          ref={detailRef}
          id="local-analysis-detail"
          tabIndex={-1}
          className={`${panel} min-w-0 self-start scroll-mt-24 focus:outline-none`}
          aria-label={text('分析详情', 'Analysis details')}
        >
          {!detail ? (
            <div className="flex min-h-56 flex-col items-center justify-center text-center">
              <Search className="mb-3 h-7 w-7 text-nofx-gold" />
              <h2 className="font-semibold">
                {text('点击交易对查看依据', 'Select a market for details')}
              </h2>
              <p className="mt-2 max-w-sm text-sm leading-6 text-nofx-text-muted">
                {text(
                  '查看各周期走势、资金费率、持仓量和历史判断。没有数据的项目会明确标注，不用估算值冒充真实数据。',
                  'Inspect timeframe trends, funding, open interest and past observations. Missing data is explicitly marked, never presented as a fabricated value.'
                )}
              </p>
            </div>
          ) : (
            <>
              <div className="flex flex-wrap items-start justify-between gap-2">
                <h2 className="break-all text-lg font-semibold">
                  {detail.symbol}
                </h2>
                <span
                  className={`text-sm font-semibold ${color(detail.direction)}`}
                >
                  {direction(detail.direction)} · {detail.score.toFixed(1)}
                </span>
              </div>
              {!current(detail) && (
                <p className="mt-2 text-sm text-nofx-gold">
                  {text(
                    '这是历史结果，不能用于当前开仓。',
                    'Historical observation; not valid for a new entry.'
                  )}
                </p>
              )}
              <p className="mt-2 text-xs leading-5 text-nofx-text-muted">
                {text(
                  '方向需要连续两个 15 分钟收盘确认；不一致或波动过大时观望。',
                  'Direction must agree at two consecutive 15m closes; mixed conditions or extreme volatility mean wait.'
                )}
              </p>
              <div className="mt-4 grid gap-3">
                {detail.frames.map((frame) => (
                  <div
                    key={frame.timeframe}
                    className="rounded-xl border border-nofx-border p-3"
                  >
                    <div className="flex flex-wrap justify-between gap-2 text-sm">
                      <strong>{frame.timeframe}</strong>
                      <span
                        className={color(
                          frame.trend > 0
                            ? 'long'
                            : frame.trend < 0
                              ? 'short'
                              : 'wait'
                        )}
                      >
                        {frame.trend > 0
                          ? text('短期均价高于长期', 'Short average above long')
                          : frame.trend < 0
                            ? text(
                                '短期均价低于长期',
                                'Short average below long'
                              )
                            : text('均价持平', 'Flat averages')}
                      </span>
                    </div>
                    <dl className="mt-2 grid grid-cols-2 gap-2 text-xs">
                      <div>
                        <dt className="text-nofx-text-muted">EMA20 / EMA50</dt>
                        <dd className="mt-1 break-all">
                          {price(frame.ema20)} / {price(frame.ema50)}
                        </dd>
                      </div>
                      <div>
                        <dt className="text-nofx-text-muted">
                          RSI · {text('动量指标', 'Momentum')}
                        </dt>
                        <dd className="mt-1">{frame.rsi.toFixed(1)}</dd>
                      </div>
                      <div>
                        <dt className="text-nofx-text-muted">
                          {text('平均波幅 / 价格', 'Average range / price')}
                        </dt>
                        <dd className="mt-1">
                          {frame.atr_percent.toFixed(2)}%
                        </dd>
                      </div>
                      <div>
                        <dt className="text-nofx-text-muted">
                          {text(
                            '成交量 / 过去20根均量',
                            'Volume / 20-bar average'
                          )}
                        </dt>
                        <dd className="mt-1">
                          {frame.volume_ratio.toFixed(2)}×
                        </dd>
                      </div>
                    </dl>
                    <p className="mt-2 text-[11px] text-nofx-text-muted">
                      {text('收盘：', 'Closed: ')}
                      {date(frame.closed_at)}
                    </p>
                  </div>
                ))}
              </div>
              <dl className="mt-4 grid grid-cols-2 gap-3 text-sm">
                <div>
                  <dt className="text-xs text-nofx-text-muted">
                    {text('资金费率', 'Funding rate')}
                  </dt>
                  <dd className="mt-1">
                    {detail.funding_rate === null
                      ? text('暂无有效数据', 'Unavailable')
                      : `${(detail.funding_rate * 100).toFixed(4)}%`}
                  </dd>
                  {detail.funding_time && (
                    <p className="mt-1 text-[11px] text-nofx-text-muted">
                      {date(detail.funding_time)}
                    </p>
                  )}
                </div>
                <div>
                  <dt className="text-xs text-nofx-text-muted">
                    {text('未平仓合约量', 'Open interest')}
                  </dt>
                  <dd className="mt-1 break-words">
                    {detail.open_interest === null
                      ? text('暂无有效数据', 'Unavailable')
                      : `${price(detail.open_interest)} ${detail.open_interest_unit === 'base' ? detail.symbol.replace(/USDT$/, '') : detail.open_interest_unit === 'contracts' ? text('张', 'contracts') : detail.open_interest_unit || ''}`}
                  </dd>
                  {detail.open_interest_observed_at && (
                    <p className="mt-1 text-[11px] text-nofx-text-muted">
                      {text('读取于：', 'Read: ')}
                      {date(detail.open_interest_observed_at)}
                    </p>
                  )}
                </div>
              </dl>
              <p className="mt-3 text-xs leading-5 text-nofx-text-muted">
                {text(
                  '资金费率和持仓量仅作补充展示。这里不提供无法从公开行情准确取得的全市场持仓成本或清算分布。',
                  'Funding and open interest are supplementary observations. This does not claim to reconstruct hidden account cost bases or market-wide liquidation levels.'
                )}
              </p>
            </>
          )}
        </section>
      </div>

      {detail && (
        <section className={panel}>
          <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-lg font-semibold">
              {detail.symbol} · {text('历史判断', 'History')}
            </h2>
            <span className="text-xs text-nofx-text-muted">
              {text(
                '保留 30 天；同一根收盘行情只记录一次',
                '30-day retention; one observation per closed candle'
              )}
            </span>
          </div>
          {historyError ? (
            <div role="alert" className="text-sm text-nofx-gold">
              {text('历史记录读取失败', 'History could not be loaded')}{' '}
              <button
                className="underline"
                onClick={() => void refreshHistory()}
              >
                {text('重试', 'Retry')}
              </button>
            </div>
          ) : historyLoading ? (
            <p className="text-sm text-nofx-text-muted">
              {text('读取中…', 'Loading…')}
            </p>
          ) : (
            <>
              <div className="max-h-72 overflow-auto">
                <table className="w-full text-left text-sm">
                  <thead className="sticky top-0 bg-nofx-bg-lighter text-xs text-nofx-text-muted">
                    <tr>
                      <th className="py-2 pr-3">
                        {text('收盘时间', 'Candle close')}
                      </th>
                      <th className="py-2 pr-3">{text('方向', 'Direction')}</th>
                      <th className="py-2">{text('分数', 'Score')}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-nofx-border">
                    {historyData?.history.map((row) => (
                      <tr key={`${row.closed_at}-${row.version}`}>
                        <td className="py-2 pr-3">{date(row.closed_at)}</td>
                        <td
                          className={`whitespace-nowrap py-2 pr-3 ${color(row.direction)}`}
                        >
                          {direction(row.direction)}
                        </td>
                        <td className="py-2">{row.score.toFixed(1)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {!historyData?.history.length && (
                <p className="py-4 text-sm text-nofx-text-muted">
                  {text('这个时间范围还没有记录', 'No records in this period')}
                </p>
              )}
              <div className="mt-3 flex gap-3">
                <button
                  className={`${field} disabled:opacity-40`}
                  disabled={!before}
                  onClick={() => setBefore(0)}
                >
                  {text('最新记录', 'Latest')}
                </button>
                <button
                  className={`${field} disabled:opacity-40`}
                  disabled={!historyData || historyData.history.length < 96}
                  onClick={() =>
                    setBefore(
                      historyData!.history[historyData!.history.length - 1]
                        .closed_at
                    )
                  }
                >
                  {text('更早记录', 'Older')}
                </button>
              </div>
            </>
          )}
        </section>
      )}

      <section
        className={panel}
        aria-label={text(
          '使用分析进行模拟交易',
          'Use analysis for paper trading'
        )}
      >
        <h2 className="text-lg font-semibold">
          {text(
            '用这套分析建立交易策略',
            'Create a strategy using this analysis'
          )}
        </h2>
        <p className="mt-2 text-sm leading-6 text-nofx-text-muted">
          {text(
            '创建一个独立策略，随后可以调整风险设置、选择现有 AI 并启动模拟交易。不修改已有交易员，不清空任何交易记录。策略使用交易员所选交易所的行情，不混用不同交易所的数据。',
            'Create a separate strategy, review its risk settings and use an existing AI for paper trading. Existing traders and records remain unchanged. Each trader uses analysis from its own selected exchange.'
          )}
        </p>
        <div className="mt-4 grid gap-3 sm:grid-cols-3">
          <label className="flex flex-col gap-2 text-xs text-nofx-text-muted">
            {text('策略名称', 'Strategy name')}
            <input
              className={field}
              maxLength={80}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={text('本地行情分析策略', 'Local market analysis')}
            />
          </label>
          <label className="flex flex-col gap-2 text-xs text-nofx-text-muted">
            {text('最低分数（35—95）', 'Minimum score (35–95)')}
            <input
              className={field}
              type="number"
              min={35}
              max={95}
              step={1}
              value={minScore}
              onChange={(e) => setMinScore(Number(e.target.value))}
            />
          </label>
          <label className="flex flex-col gap-2 text-xs text-nofx-text-muted">
            {text(
              '最多交给 AI 的交易对（1—10）',
              'Maximum AI candidates (1–10)'
            )}
            <input
              className={field}
              type="number"
              min={1}
              max={10}
              step={1}
              value={limit}
              onChange={(e) => setLimit(Number(e.target.value))}
            />
          </label>
        </div>
        <p className="mt-3 text-xs text-nofx-text-muted">
          {available
            ? text(
                `当前 ${exchange.toUpperCase()} 有 ${qualified} 个已确认方向达到此分数。没有符合条件的交易对时，系统会等待，不会强行开仓。`,
                `${qualified} confirmed ${exchange.toUpperCase()} markets meet this score. No qualifying markets means waiting, not forcing a trade.`
              )
            : text(
                '分析尚未就绪，仍可保存策略，但新开仓会等待有效数据。',
                'You may save a strategy now; entries will wait for valid analysis.'
              )}
        </p>
        {createError && (
          <p role="alert" className="mt-3 text-sm text-nofx-gold">
            {createError}
          </p>
        )}
        <div className="mt-4 flex flex-wrap items-center gap-4">
          <button
            type="button"
            onClick={() => void createStrategy()}
            disabled={
              creating ||
              minScore < 35 ||
              minScore > 95 ||
              !Number.isInteger(limit) ||
              limit < 1 ||
              limit > 10
            }
            className="inline-flex items-center gap-2 rounded-lg bg-nofx-gold px-4 py-2.5 text-sm font-semibold text-black disabled:opacity-40"
          >
            {creating && <Loader2 className="h-4 w-4 animate-spin" />}
            {text('创建策略并设置', 'Create and configure strategy')}
            <ArrowRight className="h-4 w-4" />
          </button>
          <Link
            className="text-sm text-nofx-gold underline"
            to={ROUTES.traders}
          >
            {text('查看我的交易员', 'My traders')}
          </Link>
        </div>
      </section>
      <footer className="text-xs leading-6 text-nofx-text-muted">
        {text(
          '计算方式：按 15m / 1h / 4h 的 25% / 35% / 40% 权重，合并均线走势、RSI 动量和区间突破。分数只说明这些规则有多一致，不表示未来收益或胜率。行情接口有覆盖范围和请求限制。',
          'Calculation: 15m / 1h / 4h weights of 25% / 35% / 40%, combining EMA trend, RSI momentum and range breakouts. Scores describe agreement among rules, not future returns or win probability. Public data has coverage and request limits.'
        )}{' '}
        · {data?.version}
      </footer>
    </div>
  )
}
