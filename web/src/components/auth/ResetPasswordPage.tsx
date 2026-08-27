import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { Header } from '../common/Header'
import { ArrowLeft, KeyRound, Copy, Check } from 'lucide-react'
import { toast } from 'sonner'

const RESET_PASSWORD_COMMAND = 'nofx reset-password --email you@example.com'

export function ResetPasswordPage() {
  const { language } = useLanguage()
  const navigate = useNavigate()
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(RESET_PASSWORD_COMMAND)
      setCopied(true)
      toast.success(t('copy', language))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error(t('copy', language))
    }
  }

  return (
    <div className="min-h-screen bg-nofx-bg text-nofx-text">
      <Header simple />

      <div
        className="flex items-center justify-center px-4"
        style={{ minHeight: 'calc(100vh - 80px)' }}
      >
        <div className="w-full max-w-md">
          {/* Back to Login */}
          <button
            onClick={() => navigate('/login')}
            className="flex items-center gap-2 mb-6 text-sm text-nofx-text-muted hover:text-nofx-gold transition-colors"
          >
            <ArrowLeft className="w-4 h-4" />
            {t('backToLogin', language)}
          </button>

          {/* Logo */}
          <div className="text-center mb-8">
            <div className="w-16 h-16 mx-auto mb-4 flex items-center justify-center rounded-full bg-nofx-gold/10 border border-nofx-gold/20">
              <KeyRound className="w-8 h-8 text-nofx-gold" />
            </div>
            <h1 className="text-2xl font-bold text-nofx-text">
              {t('resetPasswordTitle', language)}
            </h1>
          </div>

          {/* CLI recovery instructions */}
          <div className="rounded-lg p-6 bg-nofx-bg-lighter border border-nofx-border shadow-md">
            <p className="text-sm leading-relaxed mb-4 text-nofx-text">
              {t('resetPasswordCliIntro', language)}
            </p>

            <div className="flex items-center justify-between gap-3 rounded px-3 py-3 font-mono text-xs bg-nofx-bg-deeper border border-nofx-border">
              <code className="break-all text-nofx-gold">
                {RESET_PASSWORD_COMMAND}
              </code>
              <button
                type="button"
                onClick={handleCopy}
                className="shrink-0 btn-icon text-nofx-text-muted hover:text-nofx-text"
                aria-label={t('copy', language)}
              >
                {copied ? (
                  <Check className="w-4 h-4 text-nofx-success" />
                ) : (
                  <Copy className="w-4 h-4" />
                )}
              </button>
            </div>

            <p className="text-xs leading-relaxed mt-4 text-nofx-text-muted">
              {t('resetPasswordCliSecurityNote', language)}
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
