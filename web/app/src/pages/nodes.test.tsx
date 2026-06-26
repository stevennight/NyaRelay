import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { NodeDetailPage, NodeNewPage, NodesPage } from './nodes'
import { errorResponse, installFetch, jsonResponse, renderWithClient } from '../test/helpers'
import { routerMocks } from '../test/router-mocks'

const node = {
  id: 'node-1',
  name: 'hk-1',
  status: 'online' as const,
  version: '1.2.3',
  labels: { region: 'hk', tier: 'premium' },
  approved: true,
  revoked: false,
  last_seen: '2026-06-25T10:00:00Z',
  created_at: '2026-06-25T09:00:00Z',
  updated_at: '2026-06-25T10:00:00Z',
  system: {
    hostname: 'hk-1.local',
    os: 'linux',
    arch: 'amd64',
    ip: '10.0.0.5',
  },
}

describe('node pages', () => {
  it('shows the empty state when no nodes exist', async () => {
    installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([]),
      },
    ])

    renderWithClient(<NodesPage />)

    expect(await screen.findByText('还没有节点')).toBeInTheDocument()
    expect(screen.getByText('先添加节点，然后让 node 服务主动连到控制器。')).toBeInTheDocument()
  })

  it('creates a node and shows the launch command', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse({
          signing_key: 'pub-key',
          public_url: 'https://relay.example.com',
          revision: 12,
        }),
      },
      {
        path: '/api/nodes',
        method: 'POST',
        response: jsonResponse({
          node: {
            ...node,
            id: 'node-2',
            name: 'hk-2',
          },
          token: 'node-token-123',
        }),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([]),
      },
    ])

    renderWithClient(<NodeNewPage />)

    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'hk-2' } })
    fireEvent.change(screen.getByLabelText('标签 JSON'), {
      target: { value: '{"region":"hk","tier":"premium"}' },
    })
    fireEvent.click(screen.getByRole('button', { name: '生成节点凭据' }))

    expect(await screen.findByText('systemd 节点启动参数')).toBeInTheDocument()
    expect(screen.getByText(/nyarelay-node --controller https:\/\/relay\.example\.com/)).toBeInTheDocument()
    expect(screen.getByText(/--id node-2/)).toBeInTheDocument()
    expect(screen.getByText(/--token node-token-123/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '节点详情' })).toHaveAttribute('href', '/nodes/node-2')

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/nodes' && init?.method === 'POST')
    expect(createCall).toBeDefined()
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      name: 'hk-2',
        labels: {
          region: 'hk',
          tier: 'premium',
        },
      })
  })

  it('renders node details and revokes the node', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/nodes/node-1',
        method: 'GET',
        response: jsonResponse(node),
      },
      {
        path: '/api/nodes/revoke',
        method: 'POST',
        response: jsonResponse({ ok: true }),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([node]),
      },
    ])

    renderWithClient(<NodeDetailPage nodeId="node-1" />)

    expect(await screen.findByText('hk-1')).toBeInTheDocument()
    expect(screen.getByText('hk-1.local')).toBeInTheDocument()
    expect(screen.getByText('region=hk')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '吊销节点' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({ to: '/nodes', replace: true })
    })

    const revokeCall = fetchMock.mock.calls.find(([path]) => path === '/api/nodes/revoke')
    expect(JSON.parse(String(revokeCall?.[1]?.body))).toEqual({ id: 'node-1' })
  })

  it('shows an error banner when node loading fails', async () => {
    installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: errorResponse('节点加载失败', 500),
      },
    ])

    renderWithClient(<NodesPage />)

    expect(await screen.findByText('节点加载失败')).toBeInTheDocument()
  })
})
