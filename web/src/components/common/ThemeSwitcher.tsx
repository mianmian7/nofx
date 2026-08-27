import { Sun, Moon } from 'lucide-react'
import { useTheme } from '../../contexts/ThemeContext'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'

interface ThemeSwitcherProps {
  className?: string
  showLabel?: boolean
  size?: 'sm' | 'md' | 'lg'
}

export function ThemeSwitcher({
  className = '',
  showLabel = false,
  size = 'md',
}: ThemeSwitcherProps) {
  const { toggleTheme, isDark } = useTheme()
  const { language } = useLanguage()

  const tooltipText = isDark
    ? t('switchToLightMode', language)
    : t('switchToDarkMode', language)

  const labelText = isDark ? t('darkMode', language) : t('lightMode', language)

  const iconSize = size === 'sm' ? 14 : size === 'lg' ? 18 : 16

  if (showLabel) {
    return (
      <button
        type="button"
        onClick={toggleTheme}
        className={`inline-flex items-center gap-2 rounded-lg border border-nofx-border px-3 py-2 text-xs font-semibold text-nofx-text-muted hover:text-nofx-gold hover:border-nofx-gold/40 hover:bg-nofx-gold/10 transition-all ${className}`}
        title={tooltipText}
        aria-label={tooltipText}
      >
        {isDark ? (
          <Sun className="text-nofx-gold animate-spin-slow" size={iconSize} />
        ) : (
          <Moon className="text-nofx-gold" size={iconSize} />
        )}
        <span className="font-mono">{labelText}</span>
      </button>
    )
  }

  return (
    <button
      type="button"
      onClick={toggleTheme}
      className={`relative inline-flex items-center justify-center rounded-lg border border-nofx-border p-2 text-nofx-text-muted hover:text-nofx-gold hover:border-nofx-gold/40 hover:bg-nofx-gold/10 transition-all focus:outline-none focus:ring-1 focus:ring-nofx-gold/30 ${className}`}
      title={tooltipText}
      aria-label={tooltipText}
    >
      {isDark ? (
        <Sun
          size={iconSize}
          className="text-nofx-gold transition-transform duration-300 hover:rotate-45"
        />
      ) : (
        <Moon
          size={iconSize}
          className="text-nofx-gold transition-transform duration-300 hover:-rotate-12"
        />
      )}
    </button>
  )
}
