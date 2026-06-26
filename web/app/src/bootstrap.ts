import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import type { SetupStatus } from './types'

export interface BootstrapState {
  needsSetup: boolean
  authed: boolean
  publicUrl: string
}

async function loadBootstrap(): Promise<BootstrapState> {
  const setup = await api<SetupStatus>('/api/setup/status')
  if (setup.needs_setup) {
    return { needsSetup: true, authed: false, publicUrl: setup.public_url }
  }
  try {
    await api('/api/me')
    return { needsSetup: false, authed: true, publicUrl: setup.public_url }
  } catch {
    return { needsSetup: false, authed: false, publicUrl: setup.public_url }
  }
}

export function useBootstrap() {
  return useQuery({
    queryKey: ['bootstrap'],
    queryFn: loadBootstrap,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 15_000,
  })
}
