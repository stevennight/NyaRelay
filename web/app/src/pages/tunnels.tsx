import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Pause, Play, Plus, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { api, post } from '../api'
import type { NodeInfo, TunnelInfo, TunnelTransport, TunnelType } from '../types'
import {
  Banner,
  DetailGrid,
  EmptyState,
  Field,
  FieldGrid,
  InlineActions,
  PageFrame,
  Panel,
  StatusPill,
  Table,
  formatTime,
} from '../components/ui'

type TunnelForm = {
  id: string
  name: string
  type: TunnelType
  transport: TunnelTransport
  entry_node: string
  middle_nodes: string
  exit_node: string
  enabled: boolean
}

const emptyTunnelForm = (): TunnelForm => ({
  id: '',
  name: '',
  type: 'direct',
  transport: 'direct',
  entry_node: '',
  middle_nodes: '',
  exit_node: '',
  enabled: true,
})

export function TunnelsPage() {
  const query = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api<TunnelInfo[]>('/api/tunnels'),
  })

  return (
    <PageFrame
      title="隧道"
      subtitle="隧道定义入口、中间节点、出口和节点间传输。"
      action={
        <Link to="/tunnels/new" className="button-link">
          <Plus size={16} />
          新建隧道
        </Link>
      }
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {(query.data ?? []).length === 0 ? (
        <EmptyState
          title="还没有隧道"
          text="先选择入口节点；需要多跳时再添加中间节点和出口节点。"
          action={
            <Link to="/tunnels/new" className="button-link">
              <Plus size={16} />
              新建隧道
            </Link>
          }
        />
      ) : (
        <Table headers={['名称', '类型', '传输', '路径', '状态']}>
          {(query.data ?? []).map((tunnel) => (
            <tr key={tunnel.id}>
              <td>
                <strong>
                  <Link to="/tunnels/$tunnelId" params={{ tunnelId: tunnel.id }}>
                    {tunnel.name}
                  </Link>
                </strong>
                <small>{tunnel.id}</small>
              </td>
              <td>{tunnel.type}</td>
              <td>{tunnel.transport}</td>
              <td>{formatTunnelPath(tunnel)}</td>
              <td><StatusPill value={tunnel.enabled ? 'online' : 'offline'} /></td>
            </tr>
          ))}
        </Table>
      )}
    </PageFrame>
  )
}

export function TunnelNewPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return (
    <PageFrame title="新建隧道" subtitle="直入直出只需要入口节点；链式隧道需要入口和出口。">
      <TunnelEditor
        onSaved={async (saved) => {
          await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
          navigate({ to: '/tunnels/$tunnelId', params: { tunnelId: saved.id }, replace: true })
        }}
      />
    </PageFrame>
  )
}

export function TunnelDetailPage({ tunnelId }: { tunnelId: string }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [message, setMessage] = useState('')
  const dashboardQuery = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => api<{ revision: number }>('/api/dashboard'),
  })
  const query = useQuery({
    queryKey: ['tunnel', tunnelId],
    queryFn: () => api<TunnelInfo>(`/api/tunnels/${tunnelId}`),
  })
  const enable = useMutation({
    mutationFn: () => post(`/api/tunnels/${tunnelId}/enable`, {}),
    onSuccess: async () => {
      setMessage('已启用')
      await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
      await queryClient.invalidateQueries({ queryKey: ['tunnel', tunnelId] })
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
    },
  })
  const disable = useMutation({
    mutationFn: () => post(`/api/tunnels/${tunnelId}/disable`, {}),
    onSuccess: async () => {
      setMessage('已停用')
      await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
      await queryClient.invalidateQueries({ queryKey: ['tunnel', tunnelId] })
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => api<{ ok: boolean }>(`/api/tunnels/${tunnelId}`, { method: 'DELETE' }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      navigate({ to: '/tunnels', replace: true })
    },
  })

  return (
    <PageFrame
      title={query.data?.name || '隧道详情'}
      subtitle="保存后控制器会重新签名并推送相关节点配置。"
      action={query.data ? (
        <InlineActions>
          {query.data.enabled ? (
            <button className="ghost" type="button" onClick={() => disable.mutate()} disabled={disable.isPending}>
              <Pause size={16} />
              停用
            </button>
          ) : (
            <button className="ghost" type="button" onClick={() => enable.mutate()} disabled={enable.isPending}>
              <Play size={16} />
              启用
            </button>
          )}
          <button className="ghost danger" type="button" onClick={() => remove.mutate()} disabled={remove.isPending}>
            <Trash2 size={16} />
            删除
          </button>
        </InlineActions>
      ) : undefined}
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {enable.error && <Banner text={enable.error instanceof Error ? enable.error.message : '启用失败'} />}
      {disable.error && <Banner text={disable.error instanceof Error ? disable.error.message : '停用失败'} />}
      {remove.error && <Banner text={remove.error instanceof Error ? remove.error.message : '删除失败'} />}
      {query.data ? (
        <>
          <Panel>
            <DetailGrid
              items={[
                { label: 'ID', value: query.data.id },
                { label: '类型', value: query.data.type },
                { label: '传输', value: query.data.transport },
                { label: '路径', value: formatTunnelPath(query.data) },
                { label: '配置版本', value: String(dashboardQuery.data?.revision ?? '-') },
                { label: '创建时间', value: formatTime(query.data.created_at) },
                { label: '更新时间', value: formatTime(query.data.updated_at) },
                { label: '状态', value: <StatusPill value={query.data.enabled ? 'online' : 'offline'} /> },
              ]}
            />
          </Panel>
          <Panel>
            <h2>Stages</h2>
            <div className="hop-list">
              {query.data.stages.map((stage) => (
                <div className="hop" key={stage.id}>
                  <span>{stage.index}</span>
                  <strong>{stage.role}</strong>
                  <small>{stage.nodes.map((node) => node.node_id).join(', ')}</small>
                </div>
              ))}
            </div>
          </Panel>
          <TunnelEditor
            initialTunnel={query.data}
            onSaved={async () => {
              setMessage('已保存')
              await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
              await queryClient.invalidateQueries({ queryKey: ['tunnel', tunnelId] })
              await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
            }}
          />
          {message && <Panel><p>{message}</p></Panel>}
        </>
      ) : null}
    </PageFrame>
  )
}

function TunnelEditor({
  initialTunnel,
  onSaved,
}: {
  initialTunnel?: TunnelInfo
  onSaved: (saved: TunnelInfo) => void | Promise<void>
}) {
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const nodes = useMemo(() => {
    const all = nodesQuery.data ?? []
    if (!initialTunnel) {
      return all.filter((node) => !node.revoked)
    }
    const selectedIDs = new Set(
      initialTunnel.stages.flatMap((stage) => stage.nodes.map((node) => node.node_id)),
    )
    return all.filter((node) => !node.revoked || selectedIDs.has(node.id))
  }, [initialTunnel, nodesQuery.data])
  const [form, setForm] = useState<TunnelForm>(emptyTunnelForm())
  const [error, setError] = useState('')

  useEffect(() => {
    if (!initialTunnel) return
    const entry = initialTunnel.stages.find((stage) => stage.role === 'entry')?.nodes[0]?.node_id ?? ''
    const middle = initialTunnel.stages
      .filter((stage) => stage.role === 'middle')
      .map((stage) => stage.nodes[0]?.node_id)
      .filter(Boolean)
      .join(',')
    const exit = initialTunnel.stages.find((stage) => stage.role === 'exit')?.nodes[0]?.node_id ?? ''
    setForm({
      id: initialTunnel.id,
      name: initialTunnel.name,
      type: initialTunnel.type,
      transport: initialTunnel.transport,
      entry_node: entry,
      middle_nodes: middle,
      exit_node: exit,
      enabled: initialTunnel.enabled,
    })
  }, [initialTunnel])

  const save = useMutation({
    mutationFn: () => {
      const payload = {
        id: form.id,
        name: form.name,
        type: form.type,
        transport: form.type === 'direct' ? 'direct' : form.transport,
        entry_node: form.entry_node,
        middle_nodes: form.middle_nodes.split(',').map((item) => item.trim()).filter(Boolean),
        exit_node: form.type === 'chain' ? form.exit_node : '',
        enabled: form.enabled,
      }
      if (initialTunnel) {
        return api<TunnelInfo>(`/api/tunnels/${initialTunnel.id}`, {
          method: 'PATCH',
          body: JSON.stringify(payload),
        })
      }
      return post<TunnelInfo>('/api/tunnels', payload)
    },
    onSuccess: async (saved) => {
      await onSaved(saved)
    },
    onError: (err) => setError(err instanceof Error ? err.message : '保存失败'),
  })

  return (
    <form
      className="form grid"
      onSubmit={(event) => {
        event.preventDefault()
        setError('')
        save.mutate()
      }}
    >
      <FieldGrid>
        <Field label="名称">
          <input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} />
        </Field>
        <Field label="类型">
          <select value={form.type} onChange={(event) => setForm((current) => ({ ...current, type: event.target.value as TunnelType }))}>
            <option value="direct">直入直出</option>
            <option value="chain">链式隧道</option>
          </select>
        </Field>
        <Field label="传输">
          <select
            value={form.type === 'direct' ? 'direct' : form.transport}
            disabled={form.type === 'direct'}
            onChange={(event) => setForm((current) => ({ ...current, transport: event.target.value as TunnelTransport }))}
          >
            <option value="direct">Direct stream</option>
            <option value="tls">TLS</option>
            <option value="mtls">mTLS</option>
            <option value="ws-tls">WS over TLS</option>
          </select>
        </Field>
        <Field label="入口节点">
          <select value={form.entry_node} onChange={(event) => setForm((current) => ({ ...current, entry_node: event.target.value }))}>
            <option value="">选择节点</option>
            {nodes.map((node) => <option key={node.id} value={node.id}>{node.name}</option>)}
          </select>
        </Field>
        {form.type === 'chain' ? (
          <>
            <Field label="中间节点">
              <input
                value={form.middle_nodes}
                onChange={(event) => setForm((current) => ({ ...current, middle_nodes: event.target.value }))}
                placeholder="多个节点 ID 用英文逗号分隔"
              />
            </Field>
            <Field label="出口节点">
              <select value={form.exit_node} onChange={(event) => setForm((current) => ({ ...current, exit_node: event.target.value }))}>
                <option value="">选择节点</option>
                {nodes.map((node) => <option key={node.id} value={node.id}>{node.name}</option>)}
              </select>
            </Field>
          </>
        ) : null}
        <Field label="启用">
          <input type="checkbox" checked={form.enabled} onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))} />
        </Field>
      </FieldGrid>
      {error && <p className="error">{error}</p>}
      <button type="submit" disabled={save.isPending}>保存隧道</button>
    </form>
  )
}

function formatTunnelPath(tunnel: TunnelInfo) {
  return tunnel.stages
    .slice()
    .sort((a, b) => a.index - b.index)
    .map((stage) => stage.nodes.map((node) => node.node_id).join('/'))
    .join(' -> ')
}
