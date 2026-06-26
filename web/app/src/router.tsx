import { createRootRoute, createRoute, createRouter, Navigate } from '@tanstack/react-router'
import { AppFrame } from './components/layout'
import { AuditPage } from './pages/audit'
import { DashboardPage } from './pages/dashboard'
import { LoginPage, SetupPage } from './pages/auth'
import { LinkDetailPage, LinkNewPage, LinksPage } from './pages/links'
import { NodeDetailPage, NodeNewPage, NodesPage } from './pages/nodes'
import { ControllerSettingsPage, SecuritySettingsPage, SettingsIndexPage } from './pages/settings'
import { RouteDetailPage, RouteNewPage, RoutesPage } from './pages/routes'
import { TrafficPage } from './pages/traffic'

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

const linksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'links',
  component: LinksPage,
})

const linkNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'links/new',
  component: LinkNewPage,
})

const linkDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'links/$linkId',
  component: LinkDetailRouteComponent,
})

function LinkDetailRouteComponent() {
  const { linkId } = linkDetailRoute.useParams()
  return <LinkDetailPage linkId={linkId} />
}

const routesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'routes',
  component: RoutesPage,
})

const routeNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'routes/new',
  component: RouteNewPage,
})

const routeDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'routes/$routeId',
  component: RouteDetailRouteComponent,
})

function RouteDetailRouteComponent() {
  const { routeId } = routeDetailRoute.useParams()
  return <RouteDetailPage routeId={routeId} />
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
  linksRoute,
  linkNewRoute,
  linkDetailRoute,
  routesRoute,
  routeNewRoute,
  routeDetailRoute,
  trafficRoute,
  auditRoute,
  settingsIndexRoute,
  settingsSecurityRoute,
  settingsControllerRoute,
])

export const router = createRouter({ routeTree })
