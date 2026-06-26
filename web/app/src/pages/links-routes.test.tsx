import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { LinkDetailPage } from './links'
import { RouteNewPage, RoutesPage } from './routes'
import { TrafficPage } from './traffic'
import { errorResponse, installFetch, jsonResponse, renderWithClient } from '../test/helpers'
import { routerMocks } from '../test/router-mocks'

const nodes = [
  {
    id: 'cn-1',
    name: 'cn-1',
    status: 'online' as const,
    version: '1.0.0',
    labels: {},
    approved: true,
    revoked: false,
    created_at: '2026-06-25T09:00:00Z',
    updated_at: '2026-06-25T09:00:00Z',
  },
  {
    id: 'hk-1',
    name: 'hk-1',
    status: 'online' as const,
    version: '1.0.0',
    labels: {},
    approved: true,
    revoked: false,
    created_at: '2026-06-25T09:00:00Z',
    updated_at: '2026-06-25T09:00:00Z',
  },
] satisfies Array<Parameters<typeof jsonResponse>[0]>

const link = {
  id: 'link-1',
  name: 'cn-hk-tunnel',
  type: 'mtls' as const,
  from_node: 'cn-1',
  to_node: 'hk-1',
  bind_addr: '0.0.0.0:9443',
  public_addr: '1.2.3.4:9443',
  server_name: 'old.example.com',
  enabled: true,
  settings: {
    secret: 'secret-value',
    server_cert: 'server-cert',
    server_key: 'server-key',
  },
  created_at: '2026-06-25T09:00:00Z',
  updated_at: '2026-06-25T09:00:00Z',
}

describe('route and link pages', () => {
  it('shows the empty routes state', async () => {
    installFetch([
      {
        path: '/api/routes',
        method: 'GET',
        response: jsonResponse([]),
      },
    ])

    renderWithClient(<RoutesPage />)

    expect(await screen.findByText('还没有路由')).toBeInTheDocument()
  })

  it('creates a route with a hop and navigates to details', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
      {
        path: '/api/links',
        method: 'GET',
        response: jsonResponse([]),
      },
      {
        path: '/api/routes',
        method: 'POST',
        response: jsonResponse({
          id: 'route-1',
          name: 'cn-hk-us',
          protocol: 'udp',
          entry_node: 'cn-1',
          listen: '0.0.0.0:8443',
          hops: [],
          target: '10.0.0.8:443',
          enabled: true,
          created_at: '2026-06-25T09:00:00Z',
          updated_at: '2026-06-25T09:00:00Z',
        }),
      },
    ])

    renderWithClient(<RouteNewPage />)

    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'cn-hk-us' } })
    fireEvent.change(screen.getByLabelText('协议'), { target: { value: 'udp' } })
    fireEvent.change(screen.getByLabelText('目标地址'), { target: { value: '10.0.0.8:443' } })
    fireEvent.click(screen.getByRole('button', { name: '保存路由' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({
        to: '/routes/$routeId',
        params: { routeId: 'route-1' },
        replace: true,
      })
    })

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/routes' && init?.method === 'POST')
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      id: '',
      name: 'cn-hk-us',
      protocol: 'udp',
      entry_node: '',
      listen: '0.0.0.0:8443',
      target: '10.0.0.8:443',
      enabled: true,
      hops: [],
    })
  })

  it('edits an existing link and keeps secret material', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/links/link-1',
        method: 'GET',
        response: jsonResponse(link),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
      {
        path: '/api/links',
        method: 'POST',
        response: jsonResponse({
          ...link,
          server_name: 'new.example.com',
        }),
      },
    ])

    renderWithClient(<LinkDetailPage linkId="link-1" />)

    expect(await screen.findByDisplayValue('old.example.com')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Server Name'), { target: { value: 'new.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: '保存链路' }))

    expect(await screen.findByText('已保存')).toBeInTheDocument()

    const saveCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/links' && init?.method === 'POST')
    expect(JSON.parse(String(saveCall?.[1]?.body))).toMatchObject({
      id: 'link-1',
      name: 'cn-hk-tunnel',
      type: 'mtls',
      from_node: 'cn-1',
      to_node: 'hk-1',
      bind_addr: '0.0.0.0:9443',
      public_addr: '1.2.3.4:9443',
      server_name: 'new.example.com',
      enabled: true,
      settings: {
        secret: 'secret-value',
        server_cert: 'server-cert',
        server_key: 'server-key',
      },
    })
  })

  it('shows traffic errors clearly', async () => {
    installFetch([
      {
        path: '/api/traffic',
        method: 'GET',
        response: errorResponse('统计加载失败', 503),
      },
    ])

    renderWithClient(<TrafficPage />)

    expect(await screen.findByText('统计加载失败')).toBeInTheDocument()
  })
})
