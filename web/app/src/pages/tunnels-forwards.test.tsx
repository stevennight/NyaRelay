import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ForwardNewPage, ForwardsPage } from './forwards'
import { TrafficPage } from './traffic'
import { TunnelNewPage, TunnelsPage } from './tunnels'
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
    public_host: 'cn.example.com',
    port_min: 10000,
    port_max: 20000,
    created_at: '2026-06-25T09:00:00Z',
    updated_at: '2026-06-25T09:00:00Z',
  },
  {
    id: 'sg-1',
    name: 'sg-1',
    status: 'online' as const,
    version: '1.0.0',
    labels: {},
    approved: true,
    revoked: false,
    public_host: 'sg.example.com',
    port_min: 10000,
    port_max: 20000,
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
    public_host: 'hk.example.com',
    port_min: 10000,
    port_max: 20000,
    created_at: '2026-06-25T09:00:00Z',
    updated_at: '2026-06-25T09:00:00Z',
  },
  {
    id: 'revoked-node',
    name: 'revoked-node',
    status: 'revoked' as const,
    version: '1.0.0',
    labels: {},
    approved: true,
    revoked: true,
    public_host: 'revoked.example.com',
    port_min: 10000,
    port_max: 20000,
    created_at: '2026-06-25T09:00:00Z',
    updated_at: '2026-06-25T09:00:00Z',
  },
] satisfies Array<Parameters<typeof jsonResponse>[0]>

const tunnel = {
  id: 'tun-1',
  name: 'cn-sg-hk',
  type: 'chain' as const,
  transport: 'tls' as const,
  enabled: true,
  settings: {},
  stages: [
    {
      id: 'stage-1',
      tunnel_id: 'tun-1',
      index: 0,
      role: 'entry' as const,
      strategy: 'single',
      nodes: [
        {
          id: 'stage-node-1',
          tunnel_id: 'tun-1',
          stage_id: 'stage-1',
          node_id: 'cn-1',
          created_at: '2026-06-25T09:00:00Z',
          updated_at: '2026-06-25T09:00:00Z',
        },
      ],
      created_at: '2026-06-25T09:00:00Z',
      updated_at: '2026-06-25T09:00:00Z',
    },
  ],
  created_at: '2026-06-25T09:00:00Z',
  updated_at: '2026-06-25T09:00:00Z',
}

describe('tunnels and forwards pages', () => {
  it('shows the empty tunnels state', async () => {
    installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([]),
      },
    ])

    renderWithClient(<TunnelsPage />)

    expect(await screen.findByText('还没有隧道')).toBeInTheDocument()
  })

  it('hides revoked nodes in the tunnel selector', async () => {
    installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
    ])

    renderWithClient(<TunnelNewPage />)

    expect(await screen.findByText('新建隧道')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '入口节点' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'cn-1' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'revoked-node' })).not.toBeInTheDocument()
  })

  it('creates a chain tunnel and posts staged nodes', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
      {
        path: '/api/tunnels',
        method: 'POST',
        response: jsonResponse({
          ...tunnel,
          id: 'tun-2',
          name: 'cn-sg-hk',
        }),
      },
    ])

    renderWithClient(<TunnelNewPage />)

    expect(await screen.findByRole('option', { name: 'cn-1' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'hk-1' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'cn-sg-hk' } })
    fireEvent.change(screen.getByLabelText('类型'), { target: { value: 'chain' } })
    fireEvent.change(screen.getByLabelText('传输'), { target: { value: 'tls' } })
    fireEvent.change(screen.getByLabelText('入口节点'), { target: { value: 'cn-1' } })
    fireEvent.change(screen.getByLabelText('中间节点'), { target: { value: 'sg-1' } })
    fireEvent.change(screen.getByLabelText('出口节点'), { target: { value: 'hk-1' } })
    fireEvent.click(screen.getByRole('button', { name: '保存隧道' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({
        to: '/tunnels/$tunnelId',
        params: { tunnelId: 'tun-2' },
        replace: true,
      })
    })

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/tunnels' && init?.method === 'POST')
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      id: '',
      name: 'cn-sg-hk',
      type: 'chain',
      transport: 'tls',
      entry_node: 'cn-1',
      middle_nodes: ['sg-1'],
      exit_node: 'hk-1',
      enabled: true,
    })
  })

  it('shows the empty forwards state', async () => {
    installFetch([
      {
        path: '/api/forwards',
        method: 'GET',
        response: jsonResponse([]),
      },
    ])

    renderWithClient(<ForwardsPage />)

    expect(await screen.findByText('还没有转发')).toBeInTheDocument()
  })

  it('creates a tcp+udp forward and normalizes protocols', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([
          tunnel,
          {
            ...tunnel,
            id: 'tun-disabled',
            name: 'disabled',
            enabled: false,
          },
        ]),
      },
      {
        path: '/api/forwards',
        method: 'POST',
        response: jsonResponse({
          id: 'fwd-1',
          name: 'web',
          tunnel_id: 'tun-1',
          protocols: ['tcp', 'udp'],
          listen: ':8443',
          target: '10.0.0.8:443',
          enabled: true,
          created_at: '2026-06-25T09:00:00Z',
          updated_at: '2026-06-25T09:00:00Z',
        }),
      },
    ])

    renderWithClient(<ForwardNewPage />)

    expect(await screen.findByRole('option', { name: 'cn-sg-hk' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'web' } })
    fireEvent.change(screen.getByLabelText('协议'), { target: { value: 'tcp_udp' } })
    fireEvent.change(screen.getByLabelText('隧道'), { target: { value: 'tun-1' } })
    fireEvent.change(screen.getByLabelText('监听地址'), { target: { value: ':8443' } })
    fireEvent.change(screen.getByLabelText('目标地址'), { target: { value: '10.0.0.8:443' } })
    fireEvent.click(screen.getByRole('button', { name: '保存转发' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({
        to: '/forwards/$forwardId',
        params: { forwardId: 'fwd-1' },
        replace: true,
      })
    })

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/forwards' && init?.method === 'POST')
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      id: '',
      name: 'web',
      tunnel_id: 'tun-1',
      protocols: ['tcp', 'udp'],
      listen: ':8443',
      target: '10.0.0.8:443',
      enabled: true,
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
