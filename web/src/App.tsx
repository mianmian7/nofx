import { ConfirmDialogProvider } from './components/common/ConfirmDialog'
import { AuthProvider } from './contexts/AuthContext'
import { LanguageProvider } from './contexts/LanguageContext'
import { ThemeProvider, useTheme } from './contexts/ThemeContext'
import { AppRoutes } from './router/AppRoutes'
import { Toaster } from 'sonner'

function ThemedToaster() {
  const { isDark } = useTheme()
  return (
    <Toaster
      theme={isDark ? 'dark' : 'light'}
      closeButton
      position="top-center"
      duration={2200}
      toastOptions={{
        className: 'nofx-toast',
      }}
    />
  )
}

export default function App() {
  return (
    <ThemeProvider>
      <LanguageProvider>
        <AuthProvider>
          <ConfirmDialogProvider>
            <ThemedToaster />
            <AppRoutes />
          </ConfirmDialogProvider>
        </AuthProvider>
      </LanguageProvider>
    </ThemeProvider>
  )
}
