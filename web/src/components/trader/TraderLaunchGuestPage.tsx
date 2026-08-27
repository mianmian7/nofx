import { Link } from 'react-router-dom'
import {
  ArrowRight,
  CheckCircle2,
  ExternalLink,
  KeyRound,
  ShieldCheck,
  Wallet,
  Zap,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { ROUTES } from '../../router/paths'

const setupSteps: Array<{
  title: string
  detail: string
  icon: LucideIcon
  action: string
  to?: string
  returnUrl?: string
  href?: string
}> = [
  {
    title: 'Create your NOFX account',
    detail:
      'Your account keeps the Autopilot configuration and trading dashboard in one place.',
    icon: KeyRound,
    action: 'Create account',
    to: ROUTES.register,
  },
  {
    title: 'Connect your AI model',
    detail:
      'Use your own OpenAI-compatible, DeepSeek, Claude, Gemini, Qwen, or other configured provider.',
    icon: Zap,
    action: 'Configure model',
    to: ROUTES.login,
    returnUrl: ROUTES.traders,
  },
  {
    title: 'Connect an exchange',
    detail:
      'Choose a supported exchange account. Exchange-specific permissions and balances are checked before launch.',
    icon: Wallet,
    action: 'Connect exchange',
    to: ROUTES.login,
    returnUrl: ROUTES.traders,
  },
  {
    title: 'Review and launch',
    detail:
      'Confirm the local dynamic strategy, conservative risk limits, exchange balance, and model before starting.',
    icon: Zap,
    action: 'Open NOFX',
    to: ROUTES.login,
  },
]

const pipeline = [
  'Rank liquid Binance perpetual candidates from public market data every cycle.',
  'Pass the candidates and raw OHLCV candles to the AI model you configured.',
  'Trade only above the confidence and risk/reward thresholds, with bounded 3x defaults.',
]

export function TraderLaunchGuestPage() {
  return (
    <div className="min-h-[calc(100vh-4rem)] overflow-hidden bg-nofx-bg px-4 py-10 md:px-8">
      <div className="mx-auto flex w-full max-w-7xl flex-col gap-8">
        <section className="grid gap-8 rounded-2xl border border-nofx-gold/20 bg-nofx-bg-lighter p-6 md:p-8 xl:grid-cols-[1.02fr_0.98fr]">
          <div className="flex flex-col justify-center">
            <div className="mb-5 inline-flex w-fit items-center gap-2 rounded-full border border-nofx-gold/25 bg-nofx-gold/10 px-3 py-1 text-[11px] font-semibold uppercase tracking-[0.2em] text-nofx-gold">
              <ShieldCheck className="h-3.5 w-3.5" />
              NOFX Autopilot
            </div>
            <h1 className="max-w-3xl text-4xl font-bold tracking-tight text-nofx-text md:text-5xl">
              One strategy. Four setup steps. Then it trades.
            </h1>
            <p className="mt-5 max-w-2xl text-base leading-7 text-nofx-text-muted">
              NOFX uses public Binance market data for dynamic candidates, then
              sends closed candles to the AI model you configured, then executes
              only on Binance Futures.
            </p>
            <div className="mt-7 flex flex-col gap-3 sm:flex-row">
              <Link
                to={ROUTES.login}
                onClick={() =>
                  sessionStorage.setItem('returnUrl', ROUTES.traders)
                }
                className="inline-flex items-center justify-center gap-2 rounded-xl bg-nofx-gold px-5 py-3 text-sm font-bold text-white transition hover:bg-nofx-gold/90"
              >
                Start setup
                <ArrowRight className="h-4 w-4" />
              </Link>
              <Link
                to={ROUTES.register}
                className="inline-flex items-center justify-center rounded-xl border border-nofx-gold/20 bg-nofx-bg-deeper px-5 py-3 text-sm font-semibold text-nofx-text transition hover:border-nofx-gold/40 hover:bg-nofx-bg-deeper"
              >
                Create account
              </Link>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            {setupSteps.map((step, index) => {
              const Icon = step.icon
              const cardClass =
                'group rounded-xl border border-nofx-gold/20 bg-nofx-bg-deeper p-4 text-left transition hover:border-nofx-gold/35 hover:bg-nofx-gold/[0.06]'
              const content = (
                <>
                  <div className="mb-4 flex items-center justify-between">
                    <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-nofx-gold/20 bg-nofx-gold/10 text-nofx-gold">
                      <Icon className="h-4 w-4" />
                    </div>
                    <span className="font-mono text-xs text-nofx-text-muted">
                      0{index + 1}
                    </span>
                  </div>
                  <h2 className="text-base font-semibold text-nofx-text">
                    {step.title}
                  </h2>
                  <p className="mt-2 text-sm leading-6 text-nofx-text-muted">
                    {step.detail}
                  </p>
                  <div className="mt-4 inline-flex items-center gap-2 text-xs font-bold text-nofx-gold transition group-hover:text-nofx-gold/80">
                    {step.action}
                    {step.href ? (
                      <ExternalLink className="h-3.5 w-3.5" />
                    ) : (
                      <ArrowRight className="h-3.5 w-3.5" />
                    )}
                  </div>
                </>
              )

              if (step.href) {
                return (
                  <a
                    key={step.title}
                    href={step.href}
                    target="_blank"
                    rel="noreferrer"
                    className={cardClass}
                  >
                    {content}
                  </a>
                )
              }

              return (
                <Link
                  key={step.title}
                  to={step.to || ROUTES.login}
                  onClick={() => {
                    if (step.returnUrl) {
                      sessionStorage.setItem('returnUrl', step.returnUrl)
                    }
                  }}
                  className={cardClass}
                >
                  {content}
                </Link>
              )
            })}
          </div>
        </section>

        <section className="grid gap-5 rounded-2xl border border-nofx-gold/20 bg-nofx-bg-lighter p-5 md:grid-cols-[0.78fr_1.22fr] md:p-6">
          <div>
            <div className="text-sm font-semibold uppercase tracking-[0.18em] text-nofx-gold">
              No trading wallet yet?
            </div>
            <p className="mt-3 text-sm leading-6 text-nofx-text-muted">
              Create a Binance USDⓈ-M Futures API key with trading permission,
              no withdrawal permission, and an IP allowlist for this server.
            </p>
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            {[
              ['API key', 'Create a dedicated Binance Futures API key.'],
              [
                'Permissions',
                'Enable futures trading and keep withdrawals disabled.',
              ],
              [
                'IP allowlist',
                'Restrict the key to the NOFX server public IP.',
              ],
            ].map(([title, detail]) => (
              <div
                key={title}
                className="rounded-xl border border-nofx-gold/20 bg-nofx-bg-deeper p-4"
              >
                <ShieldCheck className="mb-3 h-4 w-4 text-nofx-gold" />
                <div className="font-semibold text-nofx-text">{title}</div>
                <p className="mt-2 text-sm leading-6 text-nofx-text-muted">
                  {detail}
                </p>
              </div>
            ))}
          </div>
        </section>

        <section className="grid gap-4 rounded-2xl border border-nofx-gold/20 bg-nofx-bg-lighter p-5 md:grid-cols-[0.72fr_1.28fr] md:p-6">
          <div>
            <div className="text-sm font-semibold uppercase tracking-[0.18em] text-nofx-gold">
              What runs after launch
            </div>
            <p className="mt-3 text-sm leading-6 text-nofx-text-muted">
              The same production path runs every cycle. The interface only asks
              you to fund, authorize, and start.
            </p>
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            {pipeline.map((item) => (
              <div
                key={item}
                className="flex gap-3 rounded-xl border border-nofx-gold/20 bg-nofx-bg-deeper p-4"
              >
                <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-nofx-success" />
                <p className="text-sm leading-6 text-nofx-text">{item}</p>
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  )
}
