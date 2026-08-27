import { render, screen, fireEvent } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { ThemeSwitcher } from './ThemeSwitcher'
import { ThemeProvider } from '../../contexts/ThemeContext'
import { LanguageProvider } from '../../contexts/LanguageContext'

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <ThemeProvider>
      <LanguageProvider>{ui}</LanguageProvider>
    </ThemeProvider>
  )
}

describe('ThemeSwitcher', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.className = ''
    document.documentElement.removeAttribute('data-theme')
  })

  it('renders icon button with dark mode as default and switches on click', () => {
    renderWithProviders(<ThemeSwitcher />)

    const button = screen.getByRole('button')
    expect(button).toBeDefined()
    // Default language is 'zh', so aria-label is '切换到浅色模式'
    expect(button.getAttribute('aria-label')).toBe('切换到浅色模式')
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    // Click to switch to light mode
    fireEvent.click(button)

    expect(button.getAttribute('aria-label')).toBe('切换到深色模式')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')

    // Click again to switch back to dark mode
    fireEvent.click(button)

    expect(button.getAttribute('aria-label')).toBe('切换到浅色模式')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })

  it('renders with label when showLabel is true', () => {
    renderWithProviders(<ThemeSwitcher showLabel />)

    const button = screen.getByRole('button')
    expect(button.textContent).toContain('深色模式')

    fireEvent.click(button)
    expect(button.textContent).toContain('浅色模式')
  })
})
