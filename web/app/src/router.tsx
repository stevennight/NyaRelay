import { createRootRoute, createRoute, createRouter, Navigate } from '@tanstack/react-router'
import { AppFrame } from './components/layout'
import { AuditPage } from './pages/audit'
import { DashboardPage } from './pages/dashboard'
import { LoginPage, SetupPage } from './pages/auth'
import { ForwardDetailPage, ForwardNewPage, ForwardsPage } from './pages/forwards'
import { NodeDetailPage, NodeNewPage, NodesPage } from './pages/nodes'
import { ControllerSettingsPage, SecuritySettingsPage, SettingsIndexPage } from './pages/settings'
import { TrafficPage } from './pages/traffic'
import { TunnelDetailPage, TunnelNewPage, TunnelsPage } from './pages/tunnels'

const rootRoute = createRootRoute({
  component: AppFrame,
})

const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => <Navigate to="/dashboard" replace />,
})

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'setup',
  component: SetupPage,
})

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'login',
  component: LoginPage,
})

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'dashboard',
  component: DashboardPage,
})

const nodesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'nodes',
  component: NodesPage,
})

const nodeNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'nodes/new',
  component: NodeNewPage,
})

const nodeDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'nodes/$nodeId',
  component: NodeDetailRouteComponent,
})

function NodeDetailRouteComponent() {
  const { nodeId } = nodeDetailRoute.useParams()
  return <NodeDetailPage nodeId={nodeId} />
}

const tunnelsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'tunnels',
  component: TunnelsPage,
})

const tunnelNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'tunnels/new',
  component: TunnelNewPage,
})

const tunnelDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'tunnels/$tunnelId',
  component: TunnelDetailRouteComponent,
})

function TunnelDetailRouteComponent() {
  const { tunnelId } = tunnelDetailRoute.useParams()
  return <TunnelDetailPage tunnelId={tunnelId} />
}

const forwardsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'forwards',
  component: ForwardsPage,
})

const forwardNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'forwards/new',
  component: ForwardNewPage,
})

const forwardDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'forwards/$forwardId',
  component: ForwardDetailRouteComponent,
})

function ForwardDetailRouteComponent() {
  const { forwardId } = forwardDetailRoute.useParams()
  return <ForwardDetailPage forwardId={forwardId} />
}

const trafficRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'traffic',
  component: TrafficPage,
})

const auditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'audit',
  component: AuditPage,
})

const settingsIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'settings',
  component: SettingsIndexPage,
})

const settingsSecurityRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'settings/security',
  component: SecuritySettingsPage,
})

const settingsControllerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'settings/controller',
  component: ControllerSettingsPage,
})

const routeTree = rootRoute.addChildren([
  homeRoute,
  setupRoute,
  loginRoute,
  dashboardRoute,
  nodesRoute,
  nodeNewRoute,
  nodeDetailRoute,
  tunnelsRoute,
  tunnelNewRoute,
  tunnelDetailRoute,
  forwardsRoute,
  forwardNewRoute,
  forwardDetailRoute,
  trafficRoute,
  auditRoute,
  settingsIndexRoute,
  settingsSecurityRoute,
  settingsControllerRoute,
])

export const router = createRouter({
  routeTree,
  scrollRestoration: true,
  scrollToTopSelectors: ['.content'],
})
