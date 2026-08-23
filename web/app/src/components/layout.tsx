import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate, Outlet, useLocation, useNavigate } from '@tanstack/react-router'
import {
  Activity,
  History,
  LayoutDashboard,
  LogOut,
  Network,
  Route,
  Server,
  Settings,
  Waypoints,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { post } from '../api'
import { useBootstrap } from '../bootstrap'
import { useListScrollRestoration } from './ui'

const navItems = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/nodes', label: 'Nodes', icon: Server },
  { to: '/tunnels', label: 'Tunnels', icon: Waypoints },
  { to: '/forwards', label: 'Forwards', icon: Route },
  { to: '/traffic', label: 'Traffic', icon: Activity },
  { to: '/audit', label: 'Audit', icon: History },
  { to: '/settings/security', label: 'Settings', icon: Settings },
] as const

export function AppFrame() {
  const location = useLocation()
  const bootstrap = useBootstrap()

  if (bootstrap.isLoading) {
    return <Splash />
  }

  const state = bootstrap.data
  if (!state) {
    return <Splash />
  }

  const isAuthPath = location.pathname === '/setup' || location.pathname === '/login'
  if (state.needsSetup && location.pathname !== '/setup') {
    return <Navigate to="/setup" replace />
  }
  if (!state.needsSetup && !state.authed && location.pathname !== '/login') {
    return <Navigate to="/login" replace />
  }
  if (!state.needsSetup && state.authed && isAuthPath) {
    return <Navigate to="/dashboard" replace />
  }

  if (isAuthPath) {
    return <Outlet />
  }

  return (
    <Shell publicUrl={state.publicUrl}>
      <Outlet />
    </Shell>
  )
}

function Shell({ publicUrl, children }: { publicUrl?: string; children: ReactNode }) {
  const location = useLocation()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  useListScrollRestoration(scrollKeyForPath(location.pathname))
  const logout = useMutation({
    mutationFn: () => post('/api/auth/logout', {}),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
      navigate({ to: '/login', replace: true })
    },
  })

  return (
    <div className="shell">
      <aside>
        <div className="brand">
          <Network size={24} />
          <span>NyaRelay</span>
        </div>
        <nav>
          {navItems.map((item) => {
            const Icon = item.icon
            return (
              <Link
                key={item.to}
                to={item.to}
                className="nav-link"
                activeProps={{ className: 'nav-link active' }}
              >
                <Icon size={18} />
                {item.label}
              </Link>
            )
          })}
        </nav>
        <div className="shell-footer">
          <small>{publicUrl || '控制器仅内网可见'}</small>
          <button className="logout" onClick={() => logout.mutate()} disabled={logout.isPending}>
            <LogOut size={18} />
            退出
          </button>
        </div>
      </aside>
      <section className="content">{children}</section>
    </div>
  )
}

function scrollKeyForPath(pathname: string) {
  const root = pathname.split('/').filter(Boolean)[0]
  if (root === 'nodes' || root === 'tunnels' || root === 'forwards') return root
  return `route:${pathname}`
}

function Splash() {
  return (
    <main className="auth-screen">
      <div className="auth-card">
        <h1>NyaRelay</h1>
        <p>正在连接控制器...</p>
      </div>
    </main>
  )
}
