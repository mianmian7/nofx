import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { Container } from './Container'
import { ThemeSwitcher } from './ThemeSwitcher'

interface HeaderProps {
  simple?: boolean // For login/register pages
}

export function Header({ simple = false }: HeaderProps) {
  const { language } = useLanguage()

  return (
    <header className="glass sticky top-0 z-50 backdrop-blur-xl">
      <Container className="py-4">
        <div className="flex items-center justify-between">
          {/* Left - Logo and Title */}
          <div className="flex items-center gap-3">
            <div className="flex items-center justify-center">
              <img src="/icons/nofx.svg" alt="NoFx Logo" className="w-8 h-8" />
            </div>
            <div>
              <h1 className="text-xl font-bold text-nofx-text">
                {t('appTitle', language)}
              </h1>
              {!simple && (
                <p className="text-xs mono text-nofx-text-muted">
                  {t('subtitle', language)}
                </p>
              )}
            </div>
          </div>

          {/* Right - Theme Switcher */}
          <div className="flex items-center gap-2">
            <ThemeSwitcher />
          </div>
        </div>
      </Container>
    </header>
  )
}
