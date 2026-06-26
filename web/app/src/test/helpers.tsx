import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { vi } from 'vitest'

type FetchResponse = {
  ok: boolean
  status: number
  statusText: string
  json: () => Promise<unknown>
}

type FetchHandler = {
  method?: string
  path: string
  response: FetchResponse | ((path: string, init?: RequestInit) => FetchResponse)
}

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        refetchOnWindowFocus: false,
        staleTime: 10_000,
      },
      mutations: {
        retry: false,
      },
    },
  })
}

export function renderWithClient(element: ReactElement) {
  const queryClient = createQueryClient()
  return {
    queryClient,
    ...render(<QueryClientProvider client={queryClient}>{element}</QueryClientProvider>),
  }
}

export function jsonResponse(body: unknown, status = 200): FetchResponse {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? 'OK' : 'Error',
    json: async () => body,
  }
}

export function errorResponse(message: string, status = 400): FetchResponse {
  return jsonResponse({ error: message }, status)
}

export function installFetch(handlers: FetchHandler[]) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((item) => item.path === path && (item.method ?? 'GET').toUpperCase() === method)
    if (!handler) {
      throw new Error(`unexpected fetch: ${method} ${path}`)
    }
    return typeof handler.response === 'function' ? handler.response(path, init) : handler.response
  })

  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}
