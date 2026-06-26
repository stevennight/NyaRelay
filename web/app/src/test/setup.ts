import '@testing-library/jest-dom/vitest'

import * as React from 'react'
import { afterEach, vi } from 'vitest'
import { routerMocks } from './router-mocks'

function applyParams(to: string, params?: Record<string, string>) {
  let path = to
  for (const [key, value] of Object.entries(params ?? {})) {
    path = path.replace(`$${key}`, value)
  }
  return path
}

vi.mock('@tanstack/react-router', () => {
  return {
    Link: ({ to, params, children, className, activeProps, ...rest }: any) =>
      React.createElement(
        'a',
        {
          ...rest,
          className,
          href: applyParams(to, params),
        },
        children,
      ),
    Navigate: ({ to }: { to: string }) => React.createElement('div', { 'data-testid': 'navigate', 'data-to': to }),
    Outlet: () => React.createElement(React.Fragment, null),
    useNavigate: () => routerMocks.navigate,
    useLocation: () => ({ pathname: window.location.pathname }),
  }
})

afterEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
})
