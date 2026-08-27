import { useIsFetching, useIsMutating, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate, Outlet, useLocation, useNavigate } from '@tanstack/react-router'
import {
  Activity,
  ChevronRight,
  History,
  LayoutDashboard,
  LoaderCircle,
  LogOut,
  Menu,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  Route,
  Search,
  Server,
  Settings,
  Waypoints,
  X,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../api'
import { useBootstrap } from '../bootstrap'

type NavPath = '/dashboard' | '/nodes' | '/tunnels' | '/forwards' | '/traffic' | '/audit' | '/settings/security'

type NavItem = {
  to: NavPath
  label: string
  icon: LucideIcon
}

type NavGroup = {
  label: string
  items: NavItem[]
}

const navGroups: NavGroup[] = [
  {
    label: '资源',
    items: [
      { to: '/dashboard', label: '概览', icon: LayoutDashboard },
      { to: '/nodes', label: '节点', icon: Server },
      { to: '/tunnels', label: '隧道', icon: Waypoints },
      { to: '/forwards', label: '转发', icon: Route },
    ],
  },
  {
    label: '运行',
    items: [
      { to: '/traffic', label: '流量', icon: Activity },
      { to: '/audit', label: '审计', icon: History },
      { to: '/settings/security', label: '设置', icon: Settings },
    ],
  },
]

const navItems: NavItem[] = navGroups.flatMap((group) => group.items)

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
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const fetching = useIsFetching()
  const mutating = useIsMutating()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [quickNavOpen, setQuickNavOpen] = useState(false)
  const [isMobile, setIsMobile] = useState(() => (
    typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 860px)').matches
  ))
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem('nyarelay:sidebar-collapsed') === 'true'
    } catch {
      return false
    }
  })
  const logout = useMutation({
    mutationFn: () => post('/api/auth/logout', {}),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
      navigate({ to: '/login', replace: true })
    },
  })

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const media = window.matchMedia('(max-width: 860px)')
    const handleChange = (event: MediaQueryListEvent) => setIsMobile(event.matches)
    setIsMobile(media.matches)
    media.addEventListener('change', handleChange)
    return () => media.removeEventListener('change', handleChange)
  }, [])

  useEffect(() => {
    setMobileNavOpen(false)
    setQuickNavOpen(false)
  }, [location.pathname])

  useEffect(() => {
    try {
      localStorage.setItem('nyarelay:sidebar-collapsed', String(collapsed))
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
  }, [collapsed])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setQuickNavOpen((open) => !open)
      }
      if (event.key === 'Escape') {
        setMobileNavOpen(false)
        setQuickNavOpen(false)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  const activeItem = navItems.find((item) => (
    item.to === '/dashboard'
      ? location.pathname === item.to
      : location.pathname.startsWith(item.to.replace('/security', ''))
  )) ?? navItems[0]
  const isBusy = fetching + mutating > 0

  return (
    <div className={`shell${collapsed ? ' sidebar-collapsed' : ''}`}>
      <button
        className={`nav-backdrop${mobileNavOpen ? ' visible' : ''}`}
        type="button"
        aria-label="关闭导航"
        tabIndex={mobileNavOpen ? 0 : -1}
        onClick={() => setMobileNavOpen(false)}
      />
      <aside
        className={`sidebar${mobileNavOpen ? ' open' : ''}`}
        aria-label="主导航"
        aria-hidden={isMobile && !mobileNavOpen}
        inert={isMobile && !mobileNavOpen ? true : undefined}
      >
        <div className="brand-row">
          <div className="brand">
            <span className="brand-mark"><Network size={20} /></span>
            <span className="brand-name">NyaRelay</span>
          </div>
          <button
            className="mobile-nav-close icon-button"
            type="button"
            aria-label="关闭导航"
            title="关闭导航"
            onClick={() => setMobileNavOpen(false)}
          >
            <X size={18} />
          </button>
        </div>
        <nav>
          {navGroups.map((group) => (
            <div className="nav-group" key={group.label}>
              <span className="nav-group-label">{group.label}</span>
              {group.items.map((item) => {
                const Icon = item.icon
                return (
                  <Link
                    key={item.to}
                    to={item.to}
                    className="nav-link"
                    activeProps={{ className: 'nav-link active' }}
                    title={collapsed ? item.label : undefined}
                  >
                    <Icon size={18} />
                    <span className="nav-label">{item.label}</span>
                  </Link>
                )
              })}
            </div>
          ))}
        </nav>
        <div className="shell-footer">
          <div className="controller-address" title={publicUrl || '控制器仅内网可见'}>
            <span className="connection-dot" />
            <small>{publicUrl || '控制器仅内网可见'}</small>
          </div>
          <button
            className="logout"
            type="button"
            title={collapsed ? '退出登录' : undefined}
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            {logout.isPending ? <LoaderCircle className="spin" size={18} /> : <LogOut size={18} />}
            <span className="nav-label">退出登录</span>
          </button>
        </div>
      </aside>
      <section className="workspace">
        <header className="topbar">
          <div className="topbar-leading">
            <button
              className="mobile-nav-trigger icon-button ghost"
              type="button"
              aria-label="打开导航"
              title="打开导航"
              onClick={() => setMobileNavOpen(true)}
            >
              <Menu size={19} />
            </button>
            <button
              className="desktop-nav-trigger icon-button ghost"
              type="button"
              aria-label={collapsed ? '展开侧栏' : '收起侧栏'}
              title={collapsed ? '展开侧栏' : '收起侧栏'}
              onClick={() => setCollapsed((value) => !value)}
            >
              {collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
            </button>
            <div className="topbar-location" aria-label={`当前位置：${activeItem.label}`}>
              <span>NyaRelay</span>
              <ChevronRight size={14} />
              <strong>{activeItem.label}</strong>
            </div>
          </div>
          <div className="topbar-actions">
            <div className={`sync-status${isBusy ? ' busy' : ''}`} aria-live="polite">
              {isBusy ? <LoaderCircle className="spin" size={14} /> : <span className="connection-dot" />}
              <span>{isBusy ? '同步中' : '已连接'}</span>
            </div>
            <button
              className="quick-nav-trigger ghost"
              type="button"
              aria-label="快速导航"
              title="快速导航"
              onClick={() => setQuickNavOpen(true)}
            >
              <Search size={17} />
              <span>快速导航</span>
            </button>
          </div>
        </header>
        <section className="content">{children}</section>
      </section>
      {quickNavOpen && <QuickNav onClose={() => setQuickNavOpen(false)} />}
    </div>
  )
}

function QuickNav({ onClose }: { onClose: () => void }) {
  const [query, setQuery] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const filteredItems = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return navItems
    return navItems.filter((item) => item.label.toLowerCase().includes(needle) || item.to.includes(needle))
  }, [query])

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  return (
    <div
      className="quick-nav-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <section className="quick-nav" role="dialog" aria-modal="true" aria-label="快速导航">
        <div className="quick-nav-search">
          <Search size={18} aria-hidden="true" />
          <input
            ref={inputRef}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索页面"
            aria-label="搜索页面"
          />
          <button className="icon-button ghost" type="button" aria-label="关闭" onClick={onClose}>
            <X size={17} />
          </button>
        </div>
        <div className="quick-nav-list">
          {filteredItems.map((item) => {
            const Icon = item.icon
            return (
              <Link key={item.to} to={item.to} className="quick-nav-item">
                <span><Icon size={18} /></span>
                <strong>{item.label}</strong>
                <ChevronRight size={16} />
              </Link>
            )
          })}
          {filteredItems.length === 0 && <p className="quick-nav-empty">没有匹配的页面</p>}
        </div>
      </section>
    </div>
  )
}

function Splash() {
  return (
    <main className="auth-screen splash-screen">
      <div className="splash-mark"><Network size={24} /></div>
      <h1>NyaRelay</h1>
      <div className="splash-progress" aria-label="正在连接控制器"><span /></div>
      <p>正在连接控制器...</p>
    </main>
  )
}
