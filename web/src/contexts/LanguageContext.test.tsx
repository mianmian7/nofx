import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { LanguageProvider, useLanguage } from './LanguageContext'

function LanguageProbe() {
  const { language } = useLanguage()
  return <div>{language}</div>
}

describe('LanguageProvider', () => {
  beforeEach(() => localStorage.clear())

  it('defaults new users to Chinese', () => {
    render(
      <LanguageProvider>
        <LanguageProbe />
      </LanguageProvider>
    )
    expect(screen.getByText('zh')).toBeVisible()
  })

  it('preserves an explicit saved language', () => {
    localStorage.setItem('language', 'en')
    render(
      <LanguageProvider>
        <LanguageProbe />
      </LanguageProvider>
    )
    expect(screen.getByText('en')).toBeVisible()
  })
})
