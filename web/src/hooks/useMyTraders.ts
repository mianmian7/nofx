import useSWR from 'swr'
import { useAuth } from '../contexts/AuthContext'
import { api } from '../lib/api'
import type { TraderInfo } from '../types'

interface MyTradersOptions {
  refreshInterval?: number
  silent?: boolean
}

// All consumers share the authenticated user's list and bound mutations.
// Passive consumers can disable their timer while still receiving updates.
export function useMyTraders({
  refreshInterval = 5000,
  silent = false,
}: MyTradersOptions = {}) {
  const { user, token } = useAuth()
  return useSWR<TraderInfo[]>(
    user && token ? ['my-traders', user.id] : null,
    () => api.getTraders(silent),
    { refreshInterval, shouldRetryOnError: false }
  )
}
