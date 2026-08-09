import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ForwardNewPage, ForwardsPage } from './forwards'
import { TrafficPage } from './traffic'
import { TunnelDetailPage, TunnelNewPage, TunnelsPage } from './tunnels'
import { errorResponse, installFetch, jsonResponse, renderWithClient } from '../test/helpers'
import { routerMocks } from '../test/router-mocks'

const nodes = [
  {
    id: 'cn-1',
    name: 'China Edge',
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
    name: 'Singapore Relay',
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
    name: 'Hong Kong Exit',
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
    name: 'Revoked Node',
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
  entry_address: 'edge.example.com',
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

function createDataTransfer(): DataTransfer {
  const values = new Map<string, string>()
  return {
    effectAllowed: '',
    dropEffect: '',
    files: [] as unknown as FileList,
    items: [] as unknown as DataTransferItemList,
    types: [],
    clearData: (format?: string) => {
      if (format) {
        values.delete(format)
      } else {
        values.clear()
      }
    },
    getData: (format: string) => values.get(format) ?? '',
    setData: (format: string, value: string) => {
      values.set(format, value)
    },
    setDragImage: () => undefined,
  } as unknown as DataTransfer
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

  it('sorts tunnels by name and reverses the order on a second click', async () => {
    installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([
          { ...tunnel, id: 'tun-z', name: 'z-tunnel' },
          { ...tunnel, id: 'tun-a', name: 'a-tunnel' },
        ]),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
    ])

    renderWithClient(<TunnelsPage />)

    const rows = await screen.findAllByRole('row')
    expect(rows[1]).toHaveTextContent('a-tunnel')
    expect(rows[2]).toHaveTextContent('z-tunnel')

    fireEvent.click(screen.getByRole('button', { name: /descending$/ }))
    const reversedRows = screen.getAllByRole('row')
    expect(reversedRows[1]).toHaveTextContent('z-tunnel')
  })
  it('hides revoked nodes in the tunnel selector', async () => {
    installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([]),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
    ])

    renderWithClient(<TunnelNewPage />)

    expect(await screen.findByRole('dialog', { name: '新建隧道' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('类型'), { target: { value: 'chain' } })
    expect(screen.getByRole('button', { name: '添加中间层' })).toBeInTheDocument()
    expect((await screen.findAllByRole('option', { name: 'China Edge' })).length).toBeGreaterThan(0)
    expect(screen.queryByRole('option', { name: 'Revoked Node' })).not.toBeInTheDocument()
  })

  it('creates a chain tunnel and posts staged nodes', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([]),
      },
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

    expect(await screen.findByRole('dialog', { name: '新建隧道' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'China Edge' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'Hong Kong Exit' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'cn-sg-hk' } })
    fireEvent.change(screen.getByLabelText('类型'), { target: { value: 'chain' } })
    fireEvent.change(screen.getByLabelText('传输'), { target: { value: 'tls' } })
    fireEvent.change(screen.getByLabelText('入口地址'), { target: { value: 'portal.example.com' } })
    fireEvent.change(screen.getByLabelText('候选节点 1-1'), { target: { value: 'cn-1' } })
    fireEvent.click(screen.getByRole('button', { name: '添加中间层' }))
    fireEvent.change(screen.getByLabelText('候选节点 2-1'), { target: { value: 'sg-1' } })
    fireEvent.change(screen.getByLabelText('候选节点 3-1'), { target: { value: 'hk-1' } })
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
      entry_address: 'portal.example.com',
      enabled: true,
      stages: [
        {
          id: '',
          index: 0,
          role: 'entry',
          strategy: 'single',
          tcp_strategy: 'single',
          udp_strategy: 'single',
          nodes: [
            {
              id: '',
              node_id: 'cn-1',
              weight: 1,
              protocols: ['tcp', 'udp'],
            },
          ],
        },
        {
          id: '',
          index: 1,
          role: 'middle',
          strategy: 'single',
          tcp_strategy: 'single',
          udp_strategy: 'single',
          nodes: [
            {
              id: '',
              node_id: 'sg-1',
              weight: 1,
              protocols: ['tcp', 'udp'],
            },
          ],
        },
        {
          id: '',
          index: 2,
          role: 'exit',
          strategy: 'single',
          tcp_strategy: 'single',
          udp_strategy: 'single',
          nodes: [
            {
              id: '',
              node_id: 'hk-1',
              weight: 1,
              protocols: ['tcp', 'udp'],
            },
          ],
        },
      ],
    })
  })

  it('saves tunnel tcp/udp strategies and candidate protocol masks', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([]),
      },
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
          id: 'tun-protocols',
          name: 'protocols',
        }),
      },
    ])

    renderWithClient(<TunnelNewPage />)

    expect(await screen.findByRole('dialog', { name: '新建隧道' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'protocols' } })
    fireEvent.change(screen.getByLabelText('候选节点 1-1'), { target: { value: 'cn-1' } })
    fireEvent.change(screen.getByLabelText('TCP 策略'), { target: { value: 'round_robin' } })
    fireEvent.change(screen.getByLabelText('UDP 策略'), { target: { value: 'failover' } })
    fireEvent.click(screen.getByLabelText('UDP'))
    fireEvent.click(screen.getByRole('button', { name: '保存隧道' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({
        to: '/tunnels/$tunnelId',
        params: { tunnelId: 'tun-protocols' },
        replace: true,
      })
    })

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/tunnels' && init?.method === 'POST')
    const body = JSON.parse(String(createCall?.[1]?.body))
    expect(body.stages[0]).toMatchObject({
      strategy: 'single',
      tcp_strategy: 'round_robin',
      udp_strategy: 'failover',
      nodes: [
        {
          node_id: 'cn-1',
          protocols: ['tcp'],
        },
      ],
    })
  })

  it('reorders tunnel candidates by drag and drop before saving', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([]),
      },
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
          id: 'tun-dragged',
          name: 'candidate-order',
        }),
      },
    ])

    renderWithClient(<TunnelNewPage />)

    expect(await screen.findByRole('dialog', { name: '新建隧道' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'China Edge' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'candidate-order' } })
    fireEvent.change(screen.getByLabelText('候选节点 1-1'), { target: { value: 'cn-1' } })
    fireEvent.click(screen.getByRole('button', { name: '添加候选' }))
    fireEvent.change(screen.getByLabelText('候选节点 1-2'), { target: { value: 'sg-1' } })

    const dataTransfer = createDataTransfer()
    const sourceHandle = screen.getByRole('button', { name: '拖拽排序候选 1-2' })
    const targetRow = screen.getByRole('button', { name: '拖拽排序候选 1-1' }).closest('.candidate-row')
    expect(targetRow).not.toBeNull()
    fireEvent.dragStart(sourceHandle, { dataTransfer })
    fireEvent.dragOver(targetRow as Element, { dataTransfer })
    fireEvent.drop(targetRow as Element, { dataTransfer })

    expect(screen.getByLabelText('候选节点 1-1')).toHaveValue('sg-1')
    expect(screen.getByLabelText('候选节点 1-2')).toHaveValue('cn-1')

    fireEvent.click(screen.getByRole('button', { name: '保存隧道' }))

    await waitFor(() => {
      expect(routerMocks.navigate).toHaveBeenCalledWith({
        to: '/tunnels/$tunnelId',
        params: { tunnelId: 'tun-dragged' },
        replace: true,
      })
    })

    const createCall = fetchMock.mock.calls.find(([path, init]) => path === '/api/tunnels' && init?.method === 'POST')
    const body = JSON.parse(String(createCall?.[1]?.body))
    expect(body.stages[0].nodes.map((node: { node_id: string }) => node.node_id)).toEqual(['sg-1', 'cn-1'])
  })

  it('shows tunnel node names in the list and detail modal', async () => {
    const namedTunnel = {
      ...tunnel,
      stages: [
        tunnel.stages[0],
        {
          ...tunnel.stages[0],
          id: 'stage-2',
          index: 1,
          role: 'exit' as const,
          nodes: [
            {
              ...tunnel.stages[0].nodes[0],
              id: 'stage-node-2',
              stage_id: 'stage-2',
              node_id: 'hk-1',
            },
          ],
        },
      ],
    }

    installFetch([
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([namedTunnel]),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
      {
        path: '/api/tunnels/tun-1',
        method: 'GET',
        response: jsonResponse(namedTunnel),
      },
      {
        path: '/api/dashboard',
        method: 'GET',
        response: jsonResponse({ revision: 7 }),
      },
    ])

    const list = renderWithClient(<TunnelsPage />)

    expect(await screen.findByText('China Edge -> Hong Kong Exit')).toBeInTheDocument()
    expect(screen.queryByText('cn-1 -> hk-1')).not.toBeInTheDocument()

    list.unmount()
    renderWithClient(<TunnelDetailPage tunnelId="tun-1" />)

    const dialog = await screen.findByRole('dialog', { name: 'cn-sg-hk' })
    expect(dialog).toBeInTheDocument()
    expect(await within(dialog).findByText('China Edge -> Hong Kong Exit')).toBeInTheDocument()
    expect(within(dialog).getByText('China Edge (TCP+UDP)')).toBeInTheDocument()
    expect(within(dialog).getByText('Hong Kong Exit (TCP+UDP)')).toBeInTheDocument()
    expect(within(dialog).queryByText('cn-1')).not.toBeInTheDocument()
    expect(within(dialog).queryByText('hk-1')).not.toBeInTheDocument()
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

  it('filters forwards by name, tunnel, entry address, and target address', async () => {
    const backupTunnel = {
      ...tunnel,
      id: 'tun-2',
      name: 'sg-entry',
      stages: [
        {
          ...tunnel.stages[0],
          id: 'stage-2',
          tunnel_id: 'tun-2',
          nodes: [
            {
              ...tunnel.stages[0].nodes[0],
              id: 'stage-node-2',
              tunnel_id: 'tun-2',
              stage_id: 'stage-2',
              node_id: 'sg-1',
            },
          ],
        },
      ],
    }
    const forwards = [
      {
        id: 'fwd-web',
        name: 'web-public',
        tunnel_id: 'tun-1',
        protocols: ['tcp'],
        listen: ':8443',
        target: '10.0.0.8:443',
        enabled: true,
        created_at: '2026-06-25T09:00:00Z',
        updated_at: '2026-06-25T09:00:00Z',
      },
      {
        id: 'fwd-admin',
        name: 'admin-panel',
        tunnel_id: 'tun-2',
        protocols: ['tcp'],
        listen: ':9443',
        target: '10.0.0.9:443',
        enabled: true,
        created_at: '2026-06-25T09:00:00Z',
        updated_at: '2026-06-25T09:00:00Z',
      },
    ]

    installFetch([
      {
        path: '/api/forwards',
        method: 'GET',
        response: jsonResponse(forwards),
      },
      {
        path: '/api/tunnels',
        method: 'GET',
        response: jsonResponse([tunnel, backupTunnel]),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
    ])

    renderWithClient(<ForwardsPage />)

    expect(await screen.findByText('web-public')).toBeInTheDocument()
    const forwardRows = await screen.findAllByRole('row')
    expect(forwardRows[1]).toHaveTextContent('admin-panel')
    expect(forwardRows[2]).toHaveTextContent('web-public')
    fireEvent.click(screen.getByRole('button', { name: /descending$/ }))
    expect(screen.getAllByRole('row')[1]).toHaveTextContent('web-public')
    expect(screen.getByText('admin-panel')).toBeInTheDocument()
    expect(await screen.findByText('edge.example.com:8443')).toBeInTheDocument()
    expect(await screen.findByText('edge.example.com:9443')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'admin' } })
    expect(screen.queryByText('web-public')).not.toBeInTheDocument()
    expect(screen.getByText('admin-panel')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '重置' }))
    fireEvent.change(screen.getByLabelText('隧道'), { target: { value: 'tun-1' } })
    expect(screen.getByText('web-public')).toBeInTheDocument()
    expect(screen.queryByText('admin-panel')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '重置' }))
    fireEvent.change(screen.getByLabelText('入口地址'), { target: { value: 'edge.example.com:9443' } })
    expect(screen.queryByText('web-public')).not.toBeInTheDocument()
    expect(screen.getByText('admin-panel')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '重置' }))
    fireEvent.change(screen.getByLabelText('目标地址'), { target: { value: '10.0.0.8' } })
    expect(screen.getByText('web-public')).toBeInTheDocument()
    expect(screen.queryByText('admin-panel')).not.toBeInTheDocument()
  })

  it('creates a tcp+udp forward and normalizes protocols', async () => {
    const fetchMock = installFetch([
      {
        path: '/api/forwards',
        method: 'GET',
        response: jsonResponse([]),
      },
      {
        path: '/api/nodes',
        method: 'GET',
        response: jsonResponse(nodes),
      },
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

    expect(await screen.findByRole('dialog', { name: '新建转发' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'cn-sg-hk' })).toBeInTheDocument()
    expect(screen.getByLabelText('协议')).toHaveValue('tcp_udp')
    fireEvent.change(screen.getByLabelText('名称'), { target: { value: 'web' } })
    fireEvent.change(screen.getByLabelText('隧道'), { target: { value: 'tun-1' } })
    fireEvent.change(screen.getByLabelText('监听端口'), { target: { value: '8443' } })
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
      tcp_strategy: 'failover',
      udp_strategy: 'failover',
      targets: [
        {
          id: '',
          address: '10.0.0.8:443',
          protocols: [],
          weight: 1,
          enabled: true,
          position: 0,
        },
      ],
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
