import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Pause, Play, Plus, RotateCcw, Settings, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { api, post } from '../api'
import type { Dashboard, ForwardInfo, ForwardProtocol, NodeInfo, TunnelInfo } from '../types'
import {
  Banner,
  DetailGrid,
  EmptyState,
  Field,
  FieldGrid,
  FormActions,
  InlineActions,
  Modal,
  PageFrame,
  StatusPill,
  Table,
  ToggleField,
  formatTime,
} from '../components/ui'

type ProtocolMode = 'tcp' | 'udp' | 'tcp_udp'

type ForwardForm = {
  id: string
  name: string
  tunnel_id: string
  protocol_mode: ProtocolMode
  listen: string
  target: string
  enabled: boolean
}

type ForwardFilters = {
  name: string
  tunnelID: string
  entryAddress: string
}

const emptyForwardForm = (): ForwardForm => ({
  id: '',
  name: '',
  tunnel_id: '',
  protocol_mode: 'tcp_udp',
  listen: '',
  target: '',
  enabled: true,
})

const emptyForwardFilters = (): ForwardFilters => ({
  name: '',
  tunnelID: '',
  entryAddress: '',
})

export function ForwardsPage() {
  const navigate = useNavigate()
  return <ForwardsListView onCloseModal={() => navigate({ to: '/forwards', replace: true })} />
}

export function ForwardNewPage() {
  const navigate = useNavigate()
  return (
    <ForwardsListView
      modal="new"
      onCloseModal={() => navigate({ to: '/forwards', replace: true })}
    />
  )
}

export function ForwardDetailPage({ forwardId }: { forwardId: string }) {
  const navigate = useNavigate()
  return (
    <ForwardsListView
      modal="detail"
      forwardId={forwardId}
      onCloseModal={() => navigate({ to: '/forwards', replace: true })}
    />
  )
}

function ForwardsListView({
  modal,
  forwardId,
  onCloseModal,
}: {
  modal?: 'new' | 'detail'
  forwardId?: string
  onCloseModal: () => void
}) {
  const forwardsQuery = useQuery({
    queryKey: ['forwards'],
    queryFn: () => api<ForwardInfo[]>('/api/forwards'),
  })
  const tunnelsQuery = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api<TunnelInfo[]>('/api/tunnels'),
  })
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const tunnelMap = useMemo(() => indexByID(tunnelsQuery.data ?? []), [tunnelsQuery.data])
  const nodeMap = useMemo(() => indexByID(nodesQuery.data ?? []), [nodesQuery.data])
  const [filters, setFilters] = useState<ForwardFilters>(emptyForwardFilters)
  const forwards = forwardsQuery.data ?? []
  const tunnelOptions = useMemo(() => {
    const options = (tunnelsQuery.data ?? []).map((tunnel) => ({
      id: tunnel.id,
      name: tunnel.name,
    }))
    const knownTunnelIDs = new Set(options.map((tunnel) => tunnel.id))
    forwards.forEach((forward) => {
      if (!knownTunnelIDs.has(forward.tunnel_id)) {
        options.push({ id: forward.tunnel_id, name: forward.tunnel_id })
        knownTunnelIDs.add(forward.tunnel_id)
      }
    })
    return options
  }, [forwards, tunnelsQuery.data])
  const filteredForwards = useMemo(() => {
    const nameNeedle = filters.name.trim().toLowerCase()
    const entryNeedle = filters.entryAddress.trim().toLowerCase()

    return forwards.filter((forward) => {
      const tunnel = tunnelMap.get(forward.tunnel_id)
      if (nameNeedle && !`${forward.name} ${forward.id}`.toLowerCase().includes(nameNeedle)) {
        return false
      }
      if (filters.tunnelID && forward.tunnel_id !== filters.tunnelID) {
        return false
      }
      if (entryNeedle) {
        const entryText = [
          formatForwardEndpoint(forward, tunnel, nodeMap),
          entryHostForTunnel(tunnel, nodeMap),
        ].join(' ').toLowerCase()
        if (!entryText.includes(entryNeedle)) {
          return false
        }
      }
      return true
    })
  }, [filters, forwards, nodeMap, tunnelMap])
  const hasActiveFilters = Boolean(filters.name.trim() || filters.tunnelID || filters.entryAddress.trim())

  return (
    <PageFrame
      title="转发"
      subtitle="转发选择一个隧道，设置入口端口和最终目标。"
      action={
        <Link to="/forwards/new" className="button-link">
          <Plus size={16} />
          新建转发
        </Link>
      }
    >
      {forwardsQuery.error && <Banner text={forwardsQuery.error instanceof Error ? forwardsQuery.error.message : '加载失败'} />}
      {forwards.length === 0 ? (
        <EmptyState
          title="还没有转发"
          text="先创建隧道，再为入口节点分配监听端口。"
          action={
            <Link to="/forwards/new" className="button-link">
              <Plus size={16} />
              新建转发
            </Link>
          }
        />
      ) : (
        <>
          <div className="list-filters" role="search" aria-label="转发筛选">
            <Field label="名称">
              <input
                value={filters.name}
                onChange={(event) => setFilters((current) => ({ ...current, name: event.target.value }))}
                placeholder="转发名称或 ID"
              />
            </Field>
            <Field label="隧道">
              <select
                value={filters.tunnelID}
                onChange={(event) => setFilters((current) => ({ ...current, tunnelID: event.target.value }))}
              >
                <option value="">全部隧道</option>
                {tunnelOptions.map((tunnel) => (
                  <option key={tunnel.id} value={tunnel.id}>{tunnel.name}</option>
                ))}
              </select>
            </Field>
            <Field label="入口地址">
              <input
                value={filters.entryAddress}
                onChange={(event) => setFilters((current) => ({ ...current, entryAddress: event.target.value }))}
                placeholder="入口节点域名或 IP"
              />
            </Field>
            <button
              className="ghost"
              type="button"
              onClick={() => setFilters(emptyForwardFilters())}
              disabled={!hasActiveFilters}
            >
              <RotateCcw size={16} />
              重置
            </button>
          </div>
          {filteredForwards.length === 0 ? (
            <EmptyState title="没有匹配的转发" text="调整名称、隧道或入口地址筛选后再查看。" />
          ) : (
            <Table headers={['名称', '协议', '隧道', '入口地址', '目标', '状态']}>
              {filteredForwards.map((forward) => {
                const tunnel = tunnelMap.get(forward.tunnel_id)
                return (
                  <tr key={forward.id}>
                    <td>
                      <strong>
                        <Link to="/forwards/$forwardId" params={{ forwardId: forward.id }}>
                          {forward.name}
                        </Link>
                      </strong>
                      <small>{forward.id}</small>
                    </td>
                    <td>{formatProtocols(forward.protocols)}</td>
                    <td>{tunnel?.name ?? forward.tunnel_id}</td>
                    <td>
                      {formatForwardEndpoint(forward, tunnel, nodeMap)}
                      <small>{listenPortSummary(forward.listen)}</small>
                    </td>
                    <td>{forward.target}</td>
                    <td><StatusPill value={forward.enabled ? 'enabled' : 'disabled'} /></td>
                  </tr>
                )
              })}
            </Table>
          )}
        </>
      )}
      {modal === 'new' && <ForwardCreateModal onClose={onCloseModal} />}
      {modal === 'detail' && forwardId && <ForwardDetailModal forwardId={forwardId} onClose={onCloseModal} />}
    </PageFrame>
  )
}

function ForwardCreateModal({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  return (
    <Modal
      title="新建转发"
      subtitle="选择隧道后，入口节点会按协议启动 TCP、UDP 或同端口双协议监听。"
      onClose={onClose}
      size="lg"
    >
      <ForwardEditor
        onSaved={async (saved) => {
          await queryClient.invalidateQueries({ queryKey: ['forwards'] })
          navigate({ to: '/forwards/$forwardId', params: { forwardId: saved.id }, replace: true })
        }}
      />
    </Modal>
  )
}

function ForwardDetailModal({ forwardId, onClose }: { forwardId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [mode, setMode] = useState<'details' | 'edit'>('details')
  const [message, setMessage] = useState('')
  const forwardQuery = useQuery({
    queryKey: ['forward', forwardId],
    queryFn: () => api<ForwardInfo>(`/api/forwards/${forwardId}`),
  })
  const tunnelsQuery = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api<TunnelInfo[]>('/api/tunnels'),
  })
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const dashboardQuery = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => api<Dashboard>('/api/dashboard'),
  })
  const tunnelMap = useMemo(() => indexByID(tunnelsQuery.data ?? []), [tunnelsQuery.data])
  const nodeMap = useMemo(() => indexByID(nodesQuery.data ?? []), [nodesQuery.data])
  const forward = forwardQuery.data
  const tunnel = forward ? tunnelMap.get(forward.tunnel_id) : undefined

  const pause = useMutation({
    mutationFn: () => post(`/api/forwards/${forwardId}/pause`, {}),
    onSuccess: async () => {
      setMessage('已暂停')
      await queryClient.invalidateQueries({ queryKey: ['forwards'] })
      await queryClient.invalidateQueries({ queryKey: ['forward', forwardId] })
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
    },
  })
  const resume = useMutation({
    mutationFn: () => post(`/api/forwards/${forwardId}/resume`, {}),
    onSuccess: async () => {
      setMessage('已恢复')
      await queryClient.invalidateQueries({ queryKey: ['forwards'] })
      await queryClient.invalidateQueries({ queryKey: ['forward', forwardId] })
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => api<{ ok: boolean }>(`/api/forwards/${forwardId}`, { method: 'DELETE' }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['forwards'] })
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      navigate({ to: '/forwards', replace: true })
    },
  })

  return (
    <Modal
      title={forward?.name || '转发详情'}
      subtitle="保存后控制器会重新签名并推送相关节点配置。"
      onClose={onClose}
      size="lg"
      action={forward ? (
        <InlineActions>
          <button className="ghost" type="button" onClick={() => setMode(mode === 'edit' ? 'details' : 'edit')}>
            <Settings size={16} />
            {mode === 'edit' ? '查看详情' : '编辑'}
          </button>
          {forward.enabled ? (
            <button className="ghost" type="button" onClick={() => pause.mutate()} disabled={pause.isPending}>
              <Pause size={16} />
              暂停
            </button>
          ) : (
            <button className="ghost" type="button" onClick={() => resume.mutate()} disabled={resume.isPending}>
              <Play size={16} />
              恢复
            </button>
          )}
          <button className="ghost danger" type="button" onClick={() => remove.mutate()} disabled={remove.isPending}>
            <Trash2 size={16} />
            删除
          </button>
        </InlineActions>
      ) : undefined}
    >
      {forwardQuery.error && <Banner text={forwardQuery.error instanceof Error ? forwardQuery.error.message : '加载失败'} />}
      {remove.error && <Banner text={remove.error instanceof Error ? remove.error.message : '删除失败'} />}
      {pause.error && <Banner text={pause.error instanceof Error ? pause.error.message : '暂停失败'} />}
      {resume.error && <Banner text={resume.error instanceof Error ? resume.error.message : '恢复失败'} />}
      {forward ? (
        <>
          {mode === 'edit' ? (
            <ForwardEditor
              initialForward={forward}
              onSaved={async () => {
                setMessage('已保存')
                setMode('details')
                await queryClient.invalidateQueries({ queryKey: ['forwards'] })
                await queryClient.invalidateQueries({ queryKey: ['forward', forwardId] })
                await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
              }}
            />
          ) : (
            <ForwardDetailsContent
              forward={forward}
              tunnel={tunnel}
              nodes={nodeMap}
              revision={dashboardQuery.data?.revision}
            />
          )}
          {message && <p className="modal-message">{message}</p>}
        </>
      ) : null}
    </Modal>
  )
}

function ForwardDetailsContent({
  forward,
  tunnel,
  nodes,
  revision,
}: {
  forward: ForwardInfo
  tunnel?: TunnelInfo
  nodes: Map<string, NodeInfo>
  revision?: number
}) {
  return (
    <section className="modal-section">
      <h3>基础信息</h3>
      <DetailGrid
        items={[
          { label: 'ID', value: forward.id },
          { label: '协议', value: formatProtocols(forward.protocols) },
          { label: '隧道', value: tunnel?.name ?? forward.tunnel_id },
          { label: '入口地址', value: formatForwardEndpoint(forward, tunnel, nodes) },
          { label: '监听端口', value: listenPortLabel(forward.listen) },
          { label: '目标地址', value: forward.target },
          { label: '配置版本', value: String(revision ?? '-') },
          { label: '创建时间', value: formatTime(forward.created_at) },
          { label: '更新时间', value: formatTime(forward.updated_at) },
          { label: '状态', value: <StatusPill value={forward.enabled ? 'enabled' : 'disabled'} /> },
        ]}
      />
    </section>
  )
}

function ForwardEditor({
  initialForward,
  onSaved,
}: {
  initialForward?: ForwardInfo
  onSaved: (saved: ForwardInfo) => void | Promise<void>
}) {
  const tunnelsQuery = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api<TunnelInfo[]>('/api/tunnels'),
  })
  const tunnels = useMemo(() => {
    const all = tunnelsQuery.data ?? []
    if (!initialForward?.tunnel_id) {
      return all.filter((tunnel) => tunnel.enabled)
    }
    const selected = all.find((tunnel) => tunnel.id === initialForward.tunnel_id)
    if (!selected || selected.enabled) {
      return all.filter((tunnel) => tunnel.enabled)
    }
    return [selected, ...all.filter((tunnel) => tunnel.enabled && tunnel.id !== selected.id)]
  }, [tunnelsQuery.data, initialForward?.tunnel_id])
  const [form, setForm] = useState<ForwardForm>(emptyForwardForm())
  const [error, setError] = useState('')

  useEffect(() => {
    if (!initialForward) return
    setForm({
      id: initialForward.id,
      name: initialForward.name,
      tunnel_id: initialForward.tunnel_id,
      protocol_mode: modeFromProtocols(initialForward.protocols),
      listen: listenInputValue(initialForward.listen),
      target: initialForward.target,
      enabled: initialForward.enabled,
    })
  }, [initialForward])

  const save = useMutation({
    mutationFn: () => {
      const payload = {
        id: form.id,
        name: form.name,
        tunnel_id: form.tunnel_id,
        protocols: protocolsFromMode(form.protocol_mode),
        listen: normalizeListenInput(form.listen),
        target: form.target,
        enabled: form.enabled,
      }
      if (initialForward) {
        return api<ForwardInfo>(`/api/forwards/${initialForward.id}`, {
          method: 'PATCH',
          body: JSON.stringify(payload),
        })
      }
      return post<ForwardInfo>('/api/forwards', payload)
    },
    onSuccess: async (saved) => {
      await onSaved(saved)
    },
    onError: (err) => setError(err instanceof Error ? err.message : '保存失败'),
  })

  return (
    <form
      className="form"
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
        <Field label="协议">
          <select value={form.protocol_mode} onChange={(event) => setForm((current) => ({ ...current, protocol_mode: event.target.value as ProtocolMode }))}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="tcp_udp">TCP+UDP</option>
          </select>
        </Field>
        <Field label="隧道">
          <select value={form.tunnel_id} onChange={(event) => setForm((current) => ({ ...current, tunnel_id: event.target.value }))}>
            <option value="">选择隧道</option>
            {tunnels.map((tunnel) => <option key={tunnel.id} value={tunnel.id}>{tunnel.name}</option>)}
          </select>
        </Field>
        <Field label="监听端口" hint="留空自动分配；填写 8443 即监听该端口。">
          <input
            value={form.listen}
            onChange={(event) => setForm((current) => ({ ...current, listen: event.target.value }))}
            placeholder="8443"
          />
        </Field>
        <Field label="目标地址" hint="最终目标地址，例如 10.0.0.8:443。">
          <input
            value={form.target}
            onChange={(event) => setForm((current) => ({ ...current, target: event.target.value }))}
            placeholder="10.0.0.8:443"
          />
        </Field>
        <ToggleField
          label="启用"
          description="保存后入口节点会按配置监听。"
          checked={form.enabled}
          onChange={(checked) => setForm((current) => ({ ...current, enabled: checked }))}
        />
      </FieldGrid>
      {error && <p className="error">{error}</p>}
      <FormActions>
        <button type="submit" disabled={save.isPending}>保存转发</button>
      </FormActions>
    </form>
  )
}

function indexByID<T extends { id: string }>(items: T[]) {
  return new Map(items.map((item) => [item.id, item]))
}

function modeFromProtocols(protocols: ForwardProtocol[]): ProtocolMode {
  const set = new Set(protocols)
  if (set.has('tcp') && set.has('udp')) return 'tcp_udp'
  if (set.has('udp')) return 'udp'
  return 'tcp'
}

function protocolsFromMode(mode: ProtocolMode): ForwardProtocol[] {
  if (mode === 'tcp_udp') return ['tcp', 'udp']
  return [mode]
}

function formatProtocols(protocols: ForwardProtocol[]) {
  return protocols.map((protocol) => protocol.toUpperCase()).join('+')
}

function formatForwardEndpoint(forward: ForwardInfo, tunnel?: TunnelInfo, nodes?: Map<string, NodeInfo>) {
  const port = portFromListen(forward.listen)
  const entryHost = entryHostForTunnel(tunnel, nodes)
  if (entryHost) {
    const host = hostForEndpoint(entryHost)
    return port ? `${host}:${port}` : host
  }
  return forward.listen || '自动分配'
}

function entryHostForTunnel(tunnel?: TunnelInfo, nodes?: Map<string, NodeInfo>) {
  const explicitHost = tunnel?.entry_address?.trim()
  if (explicitHost) return explicitHost

  const entryNodeID = tunnel?.stages.find((stage) => stage.role === 'entry')?.nodes[0]?.node_id
  if (!entryNodeID) return ''
  const entryNode = nodes?.get(entryNodeID)
  return entryNode?.public_host || entryNode?.system?.ip || ''
}

function listenInputValue(listen: string) {
  return portFromListen(listen) || listen
}

function normalizeListenInput(listen: string) {
  const trimmed = listen.trim()
  if (!trimmed) return ''
  if (/^\d+$/.test(trimmed)) return `:${trimmed}`
  return trimmed
}

function listenPortSummary(listen: string) {
  const label = listenPortLabel(listen)
  return label === '自动分配' ? label : `监听端口：${label}`
}

function listenPortLabel(listen: string) {
  return portFromListen(listen) || listen || '自动分配'
}

function portFromListen(listen: string) {
  const trimmed = listen.trim()
  if (/^\d+$/.test(trimmed)) return trimmed
  const match = trimmed.match(/:(\d+)$/)
  return match?.[1] ?? ''
}

function hostForEndpoint(host: string) {
  if (host.includes(':') && !host.startsWith('[')) {
    return `[${host}]`
  }
  return host
}
