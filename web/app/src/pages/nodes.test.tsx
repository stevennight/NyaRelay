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
  public_host: 'hk.example.com',
  port_min: 10000,
  port_max: 65535,
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

const controllerInfo = {
  signing_key: 'pub-key',
  public_url: 'https://relay.example.com',
  revision: 7,
  build: {
    version: 'v0.1.3',
  },
  node_release: {
    manifest: {
      version: 'v0.1.3',
      artifacts: [],
    },
    update_enabled: false,
    disabled_reason: 'node release manifest signature is not configured',
  },
}

const updateEnabledControllerInfo = {
  ...controllerInfo,
  node_release: {
    manifest: {
      version: '1.2.4',
      artifacts: [],
    },
    signature: 'sig',
    signing_key_id: 'update-pub-key',
    update_enabled: true,
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
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodesPage />)

    expect(await screen.findByText('还没有节点')).toBeInTheDocument()
    expect(screen.getByText('先添加节点，然后让 node 服务主动连到控制器。')).toBeInTheDocument()
  })

  it('hides revoked nodes from the list', async () => {
    installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([
          node,
          {
            ...node,
            id: 'node-2',
            name: 'revoked-node',
            revoked: true,
            status: 'revoked',
          },
        ]),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodesPage />)

    expect(await screen.findByText('hk-1')).toBeInTheDocument()
    expect(screen.queryByText('revoked-node')).not.toBeInTheDocument()
  })

  it('sorts nodes by name and reverses the order on a second click', async () => {
    installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([
          { ...node, id: 'node-z', name: 'z-node' },
          { ...node, id: 'node-a', name: 'a-node' },
        ]),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodesPage />)

    const rows = await screen.findAllByRole('row')
    expect(rows[1]).toHaveTextContent('a-node')
    expect(rows[2]).toHaveTextContent('z-node')

    fireEvent.click(screen.getByRole('button', { name: /descending$/ }))
    const reversedRows = screen.getAllByRole('row')
    expect(reversedRows[1]).toHaveTextContent('z-node')
  })

  it('filters nodes and restores the filter after the list remounts', async () => {
    installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([
          node,
          { ...node, id: 'node-2', name: 'sg-2', public_host: 'sg.example.com', status: 'offline' },
        ]),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    const list = renderWithClient(<NodesPage />)

    expect(await screen.findByText('hk-1')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('搜索节点'), { target: { value: 'sg.example.com' } })
    expect(screen.getByText('sg-2')).toBeInTheDocument()
    expect(screen.queryByText('hk-1')).not.toBeInTheDocument()
    expect(screen.getByText('显示 1 / 2 个节点')).toBeInTheDocument()

    list.unmount()
    renderWithClient(<NodesPage />)

    expect(await screen.findByDisplayValue('sg.example.com')).toBeInTheDocument()
    expect(screen.getByText('sg-2')).toBeInTheDocument()
    expect(screen.queryByText('hk-1')).not.toBeInTheDocument()
  })

  it('creates a node and shows the launch command', async () => {
    const fetchMock = installFetch([
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
          script_url: 'https://relay.example.com/install.sh',
          binary_url: 'https://relay.example.com/downloads/nyarelay-node',
          command: 'curl -fsS https://relay.example.com/install.sh | sudo sh -s -- --controller https://relay.example.com --id node-2 --token node-token-123 --signing-key pub-key',
        }),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([]),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodeNewPage />)

    expect(await screen.findByRole('dialog', { name: '添加节点' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'hk-2' } })
    fireEvent.change(screen.getByLabelText('标签 JSON'), {
      target: { value: '{"region":"hk","tier":"premium"}' },
    })
    fireEvent.click(screen.getByRole('button', { name: '生成节点凭据' }))

    expect(await screen.findByText('节点安装命令')).toBeInTheDocument()
    expect(screen.getByText(/curl -fsS https:\/\/relay\.example\.com\/install\.sh/)).toBeInTheDocument()
    expect(screen.getByText('下载脚本')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '节点详情' })).toHaveAttribute('href', '/nodes/node-2')

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/nodes' && init?.method === 'POST')
    expect(createCall).toBeDefined()
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      name: 'hk-2',
      labels: {
        region: 'hk',
        tier: 'premium',
      },
      public_host: '',
      port_min: 10000,
      port_max: 65535,
    })
  })

  it('allows node port ranges below the default range', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/nodes',
        method: 'POST',
        response: jsonResponse({
          node: {
            ...node,
            id: 'node-2',
            name: 'edge-80',
            port_min: 80,
            port_max: 443,
          },
          token: 'node-token-123',
          script_url: 'https://relay.example.com/install.sh',
          binary_url: 'https://relay.example.com/downloads/nyarelay-node',
          command: 'curl -fsS https://relay.example.com/install.sh | sudo sh -s -- --controller https://relay.example.com --id node-2 --token node-token-123 --signing-key pub-key',
        }),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([]),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodeNewPage />)

    expect(await screen.findByRole('dialog', { name: '添加节点' })).toBeInTheDocument()
    expect(screen.getByLabelText('可用端口起始')).toHaveAttribute('min', '1')
    expect(screen.getByLabelText('可用端口结束')).toHaveAttribute('min', '1')
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: 'edge-80' } })
    fireEvent.change(screen.getByLabelText('可用端口起始'), { target: { value: '80' } })
    fireEvent.change(screen.getByLabelText('可用端口结束'), { target: { value: '443' } })
    fireEvent.click(screen.getByRole('button', { name: '生成节点凭据' }))

    expect(await screen.findByText('节点安装命令')).toBeInTheDocument()

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/nodes' && init?.method === 'POST')
    expect(createCall).toBeDefined()
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({
      name: 'edge-80',
      port_min: 80,
      port_max: 443,
    })
  })

  it('renders node details, shows install command and revokes the node', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/nodes/node-1',
        method: 'GET',
        response: jsonResponse(node),
      },
      {
        path: '/api/nodes/node-1/install',
        method: 'GET',
        response: jsonResponse({
          node,
          token: 'node-token-123',
          script_url: 'https://relay.example.com/install.sh',
          binary_url: 'https://relay.example.com/downloads/nyarelay-node',
          command: 'curl -fsS https://relay.example.com/install.sh | sudo sh -s -- --controller https://relay.example.com --id node-1 --token node-token-123 --signing-key pub-key',
        }),
      },
      {
        path: '/api/nodes/node-1',
        method: 'PATCH',
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
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodeDetailPage nodeId="node-1" />)

    expect(await screen.findByRole('dialog', { name: 'hk-1' })).toBeInTheDocument()
    expect(screen.getByText('hk-1.local')).toBeInTheDocument()
    expect(screen.getByText('hk.example.com:10000-65535')).toBeInTheDocument()
    expect(screen.getByText('region=hk')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '编辑' }))
    fireEvent.change(screen.getByLabelText('节点 IP / 域名'), { target: { value: 'hk-new.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: '保存节点设置' }))
    expect(await screen.findByText('已保存')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '安装命令' }))
    expect(await screen.findByText('节点安装命令')).toBeInTheDocument()
    expect(screen.getByText(/curl -fsS https:\/\/relay\.example\.com\/install\.sh/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '吊销' }))

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
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(controllerInfo),
      },
    ])

    renderWithClient(<NodesPage />)

    expect(await screen.findByText('节点加载失败')).toBeInTheDocument()
  })

  it('requests a node binary update when a newer bundled release exists', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse([node]),
      },
      {
        path: '/api/controller/info',
        method: 'GET',
        response: jsonResponse(updateEnabledControllerInfo),
      },
      {
        path: '/api/nodes/node-1/update',
        method: 'POST',
        response: jsonResponse({
          ...node,
          desired_version: '1.2.4',
          update_status: 'requested',
        }),
      },
    ])

    renderWithClient(<NodesPage />)

    expect(await screen.findByText('可更新到 1.2.4')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '更新' }))

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([path, init]) => path === '/api/nodes/node-1/update' && init?.method === 'POST')).toBe(true)
    })
  })
})
