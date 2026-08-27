import { useState } from 'react'
import type { DecisionRecord, DecisionAction } from '../../types'
import { t, type Language } from '../../i18n/translations'

interface DecisionCardProps {
  decision: DecisionRecord
  language: Language
  onSymbolClick?: (symbol: string) => void
}

// Action type configuration
const ACTION_CONFIG: Record<
  string,
  { color: string; bg: string; icon: string; label: string }
> = {
  open_long: {
    color: 'var(--binance-green)',
    bg: 'var(--binance-green-bg)',
    icon: '📈',
    label: 'LONG',
  },
  open_short: {
    color: 'var(--binance-red)',
    bg: 'var(--binance-red-bg)',
    icon: '📉',
    label: 'SHORT',
  },
  close_long: {
    color: 'var(--nofx-gold)',
    bg: 'var(--nofx-gold-dim)',
    icon: '💰',
    label: 'CLOSE',
  },
  close_short: {
    color: 'var(--nofx-gold)',
    bg: 'var(--nofx-gold-dim)',
    icon: '💰',
    label: 'CLOSE',
  },
  hold: {
    color: 'var(--text-tertiary)',
    bg: 'var(--nofx-gold-dim)',
    icon: '⏸️',
    label: 'HOLD',
  },
  wait: {
    color: 'var(--text-tertiary)',
    bg: 'var(--nofx-gold-dim)',
    icon: '⏳',
    label: 'WAIT',
  },
}

// Format price with proper decimals
function formatPrice(price: number | undefined): string {
  if (!price || price === 0) return '-'
  if (price >= 1000) return price.toFixed(2)
  if (price >= 1) return price.toFixed(4)
  return price.toFixed(6)
}

// Calculate percentage change
function calcPctChange(
  entry: number | undefined,
  target: number | undefined,
  isLong: boolean
): string {
  if (!entry || !target || entry === 0) return '-'
  const pct = ((target - entry) / entry) * 100
  const adjustedPct = isLong ? pct : -pct
  return `${adjustedPct >= 0 ? '+' : ''}${adjustedPct.toFixed(2)}%`
}

// Get confidence color
function getConfidenceColor(confidence: number | undefined): string {
  if (!confidence) return 'var(--text-tertiary)'
  if (confidence >= 80) return 'var(--binance-green)'
  if (confidence >= 60) return 'var(--nofx-gold)'
  return 'var(--binance-red)'
}

// Single Action Card Component
function ActionCard({
  action,
  language,
  onSymbolClick,
}: {
  action: DecisionAction
  language: Language
  onSymbolClick?: (symbol: string) => void
}) {
  const config = ACTION_CONFIG[action.action] || ACTION_CONFIG.wait
  const isLong = action.action.includes('long')
  const isOpen = action.action.includes('open')

  return (
    <div className="rounded-lg p-4 transition-all duration-200 hover:scale-[1.01] bg-nofx-bg-lighter border border-nofx-border">
      {/* Header Row */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-3">
          <span className="text-xl">{config.icon}</span>
          <span
            className="font-mono font-bold text-lg cursor-pointer transition-all duration-200 hover:scale-110 text-nofx-text hover:text-nofx-gold"
            onClick={() => onSymbolClick?.(action.symbol)}
            title="Click to view chart"
          >
            {action.symbol.replace('USDT', '')}
          </span>
          <span
            className="px-3 py-1 rounded-full text-xs font-bold uppercase tracking-wider border"
            style={{
              background: config.bg,
              color: config.color,
              borderColor: config.color,
            }}
          >
            {config.label}
          </span>
        </div>

        {/* Status Badge */}
        <div className="flex items-center gap-2">
          {action.confidence !== undefined && action.confidence > 0 && (
            <div
              className="px-2 py-1 rounded text-xs font-semibold"
              style={{
                color: getConfidenceColor(action.confidence),
              }}
            >
              {action.confidence.toFixed(0)}%
            </div>
          )}
          <div
            className={`w-2 h-2 rounded-full ${
              action.success ? 'bg-nofx-success' : 'bg-nofx-danger'
            }`}
          />
        </div>
      </div>

      {/* Trading Details Grid */}
      {isOpen && (
        <div className="grid grid-cols-4 gap-3 mt-3 pt-3 border-t border-nofx-border">
          {/* Entry Price */}
          <div className="text-center">
            <div className="text-xs mb-1 text-nofx-text-muted">
              {t('entryPrice', language)}
            </div>
            <div className="font-mono font-semibold text-nofx-text">
              {formatPrice(action.price)}
            </div>
          </div>

          {/* Stop Loss */}
          <div className="text-center">
            <div className="text-xs mb-1 text-nofx-danger">
              {t('stopLoss', language)}
            </div>
            <div className="font-mono font-semibold text-nofx-danger">
              {formatPrice(action.stop_loss)}
            </div>
            {action.stop_loss && action.price && (
              <div className="text-xs mt-0.5 text-nofx-text-muted">
                {calcPctChange(action.price, action.stop_loss, isLong)}
              </div>
            )}
          </div>

          {/* Take Profit */}
          <div className="text-center">
            <div className="text-xs mb-1 text-nofx-success">
              {t('takeProfit', language)}
            </div>
            <div className="font-mono font-semibold text-nofx-success">
              {formatPrice(action.take_profit)}
            </div>
            {action.take_profit && action.price && (
              <div className="text-xs mt-0.5 text-nofx-text-muted">
                {calcPctChange(action.price, action.take_profit, isLong)}
              </div>
            )}
          </div>

          {/* Leverage */}
          <div className="text-center">
            <div className="text-xs mb-1 text-nofx-text-muted">
              {t('leverage', language)}
            </div>
            <div className="font-mono font-semibold text-nofx-gold">
              {action.leverage || 1}x
            </div>
          </div>
        </div>
      )}

      {/* Risk/Reward Ratio */}
      {isOpen && action.stop_loss && action.take_profit && action.price && (
        <div className="mt-3 pt-3 flex items-center justify-between border-t border-nofx-border">
          <span className="text-xs text-nofx-text-muted">
            {t('riskReward', language)}
          </span>
          <div className="flex items-center gap-2">
            {(() => {
              const slDist = Math.abs(action.price - action.stop_loss)
              const tpDist = Math.abs(action.take_profit - action.price)
              const ratio = slDist > 0 ? tpDist / slDist : 0
              return (
                <>
                  <div className="flex gap-1 text-xs">
                    <span className="text-nofx-danger">1</span>
                    <span className="text-nofx-text-muted">:</span>
                    <span className="text-nofx-success">
                      {ratio.toFixed(1)}
                    </span>
                  </div>
                  <div
                    className="h-1.5 rounded-full overflow-hidden flex bg-nofx-bg-deeper"
                    style={{ width: '60px' }}
                  >
                    <div
                      className="h-full rounded-full transition-all duration-300 bg-nofx-gold"
                      style={{
                        width: `${Math.min((ratio / 5) * 100, 100)}%`,
                      }}
                    />
                  </div>
                </>
              )
            })()}
          </div>
        </div>
      )}

      {/* Reasoning */}
      {action.reasoning && (
        <div className="mt-3 pt-3 border-t border-nofx-border">
          <div className="text-xs line-clamp-2 text-nofx-text-muted">
            💡 {action.reasoning}
          </div>
        </div>
      )}

      {/* Error Message */}
      {action.error && (
        <div className="mt-3 rounded p-2 text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger font-mono">
          ❌ {action.error}
        </div>
      )}
    </div>
  )
}

export function DecisionCard({
  decision,
  language,
  onSymbolClick,
}: DecisionCardProps) {
  const [showSystemPrompt, setShowSystemPrompt] = useState(false)
  const [showInputPrompt, setShowInputPrompt] = useState(false)
  const [showCoT, setShowCoT] = useState(false)

  // Copy text to clipboard
  const copyToClipboard = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      alert(`${label} copied!`)
    } catch (err) {
      console.error('Failed to copy:', err)
    }
  }

  // Download text as file
  const downloadAsFile = (text: string, filename: string) => {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
  }

  return (
    <div className="rounded-xl p-5 transition-all duration-300 hover:translate-y-[-2px] bg-nofx-bg-lighter border border-nofx-border shadow-sm">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg flex items-center justify-center bg-nofx-gold/15 border border-nofx-gold/30">
            <span className="text-xl">🤖</span>
          </div>
          <div>
            <div className="font-bold text-nofx-text">
              {t('cycle', language)} #{decision.cycle_number}
            </div>
            <div className="text-xs text-nofx-text-muted">
              {new Date(decision.timestamp).toLocaleString()}
            </div>
          </div>
        </div>
        <div
          className={`px-4 py-1.5 rounded-full text-xs font-bold tracking-wider border ${
            decision.success
              ? 'bg-nofx-success/15 text-nofx-success border-nofx-success/30'
              : 'bg-nofx-danger/15 text-nofx-danger border-nofx-danger/30'
          }`}
        >
          {t(decision.success ? 'success' : 'failed', language)}
        </div>
      </div>

      {/* Decision Actions */}
      {decision.decisions && decision.decisions.length > 0 && (
        <div className="space-y-3 mb-4">
          {decision.decisions.map((action, index) => (
            <ActionCard
              key={`${action.symbol}-${index}`}
              action={action}
              language={language}
              onSymbolClick={onSymbolClick}
            />
          ))}
        </div>
      )}

      {/* Collapsible Sections */}
      <div className="space-y-2">
        {/* System Prompt */}
        {decision.system_prompt && (
          <div>
            <button
              onClick={() => setShowSystemPrompt(!showSystemPrompt)}
              className="flex items-center gap-2 text-sm transition-colors w-full justify-between p-2 rounded hover:bg-nofx-gold/10"
            >
              <div className="flex items-center gap-2">
                <span className="text-base">⚙️</span>
                <span className="font-semibold text-nofx-gold">
                  System Prompt
                </span>
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    copyToClipboard(decision.system_prompt, 'System Prompt')
                  }}
                  className="text-xs px-2.5 py-1 rounded hover:opacity-80 transition-opacity flex items-center gap-1 bg-nofx-gold/20 text-nofx-gold border border-nofx-gold/30"
                  title="Copy to clipboard"
                >
                  <span>📋</span>
                </button>
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    downloadAsFile(
                      decision.system_prompt,
                      `system-prompt-cycle-${decision.cycle_number}.txt`
                    )
                  }}
                  className="text-xs px-2.5 py-1 rounded hover:opacity-80 transition-opacity flex items-center gap-1 bg-nofx-gold/20 text-nofx-gold border border-nofx-gold/30"
                  title="Download as file"
                >
                  <span>💾</span>
                </button>
                <span className="text-xs px-2 py-0.5 rounded bg-nofx-gold/15 text-nofx-gold">
                  {showSystemPrompt
                    ? t('collapse', language)
                    : t('expand', language)}
                </span>
              </div>
            </button>
            {showSystemPrompt && (
              <div className="mt-2 rounded-lg p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto bg-nofx-bg-deeper border border-nofx-border text-nofx-text">
                {decision.system_prompt}
              </div>
            )}
          </div>
        )}

        {/* User Prompt */}
        {decision.input_prompt && (
          <div>
            <button
              onClick={() => setShowInputPrompt(!showInputPrompt)}
              className="flex items-center gap-2 text-sm transition-colors w-full justify-between p-2 rounded hover:bg-nofx-gold/10"
            >
              <div className="flex items-center gap-2">
                <span className="text-base">📥</span>
                <span className="font-semibold text-nofx-gold">
                  User Prompt
                </span>
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    copyToClipboard(decision.input_prompt, 'User Prompt')
                  }}
                  className="text-xs px-2.5 py-1 rounded hover:opacity-80 transition-opacity flex items-center gap-1 bg-nofx-gold/20 text-nofx-gold border border-nofx-gold/30"
                  title="Copy to clipboard"
                >
                  <span>📋</span>
                </button>
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    downloadAsFile(
                      decision.input_prompt,
                      `user-prompt-cycle-${decision.cycle_number}.txt`
                    )
                  }}
                  className="text-xs px-2.5 py-1 rounded hover:opacity-80 transition-opacity flex items-center gap-1 bg-nofx-gold/20 text-nofx-gold border border-nofx-gold/30"
                  title="Download as file"
                >
                  <span>💾</span>
                </button>
                <span className="text-xs px-2 py-0.5 rounded bg-nofx-gold/15 text-nofx-gold">
                  {showInputPrompt
                    ? t('collapse', language)
                    : t('expand', language)}
                </span>
              </div>
            </button>
            {showInputPrompt && (
              <div className="mt-2 rounded-lg p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto bg-nofx-bg-deeper border border-nofx-border text-nofx-text">
                {decision.input_prompt}
              </div>
            )}
          </div>
        )}

        {/* AI Thinking */}
        {decision.cot_trace && (
          <div>
            <button
              onClick={() => setShowCoT(!showCoT)}
              className="flex items-center gap-2 text-sm transition-colors w-full justify-between p-2 rounded hover:bg-nofx-gold/10"
            >
              <div className="flex items-center gap-2">
                <span className="text-base">🧠</span>
                <span className="font-semibold text-nofx-gold">
                  {t('aiThinking', language)}
                </span>
              </div>
              <span className="text-xs px-2 py-0.5 rounded bg-nofx-gold/15 text-nofx-gold">
                {showCoT ? t('collapse', language) : t('expand', language)}
              </span>
            </button>
            {showCoT && (
              <div className="mt-2 rounded-lg p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto bg-nofx-bg-deeper border border-nofx-border text-nofx-text">
                {decision.cot_trace}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Execution Log */}
      {decision.execution_log && decision.execution_log.length > 0 && (
        <div className="mt-4 pt-3 border-t border-nofx-border">
          <div className="text-xs font-semibold mb-2 text-nofx-text-muted">
            {t('executionLogs', language)}
          </div>
          <div className="rounded-lg p-3 font-mono text-xs max-h-48 overflow-y-auto space-y-1 bg-nofx-bg-deeper border border-nofx-border">
            {decision.execution_log.map((log: string, index: number) => (
              <div key={`${log}-${index}`} className="text-nofx-text">
                {log}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Error Message */}
      {decision.error_message && (
        <div className="mt-4 rounded-lg p-3 text-sm bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger font-mono">
          ❌ {decision.error_message}
        </div>
      )}
    </div>
  )
}
