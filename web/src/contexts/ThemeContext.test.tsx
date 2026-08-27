import { render, screen, act } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { ThemeProvider, useTheme, THEME_STORAGE_KEY } from './ThemeContext'

function ThemeProbe() {
  const { theme, isDark, toggleTheme, setTheme } = useTheme()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="isDark">{isDark ? 'true' : 'false'}</span>
      <button onClick={toggleTheme}>Toggle</button>
      <button onClick={() => setTheme('light')}>Set Light</button>
      <button onClick={() => setTheme('dark')}>Set Dark</button>
    </div>
  )
}

describe('ThemeProvider', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.className = ''
    document.documentElement.removeAttribute('data-theme')
  })

  it('defaults new users to dark mode', () => {
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>
    )
    expect(screen.getByTestId('theme').textContent).toBe('dark')
    expect(screen.getByTestId('isDark').textContent).toBe('true')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })

  it('preserves an explicit saved light theme', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>
    )
    expect(screen.getByTestId('theme').textContent).toBe('light')
    expect(screen.getByTestId('isDark').textContent).toBe('false')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('toggles theme correctly and updates DOM & localStorage', () => {
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>
    )

    expect(screen.getByTestId('theme').textContent).toBe('dark')

    act(() => {
      screen.getByText('Toggle').click()
    })

    expect(screen.getByTestId('theme').textContent).toBe('light')
    expect(screen.getByTestId('isDark').textContent).toBe('false')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')

    act(() => {
      screen.getByText('Toggle').click()
    })

    expect(screen.getByTestId('theme').textContent).toBe('dark')
    expect(screen.getByTestId('isDark').textContent).toBe('true')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })
})
