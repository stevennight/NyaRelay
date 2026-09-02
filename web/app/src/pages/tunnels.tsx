import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Outlet, useNavigate } from '@tanstack/react-router'
import { ArrowDown, ArrowUp, ChevronRight, GripVertical, LogIn, LogOut, Milestone, Pause, Play, Plus, RefreshCw, Server, Settings, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, post } from '../api'
import type { ForwardProtocol, NodeInfo, TunnelInfo, TunnelStageRole, TunnelTransport, TunnelType } from '../types'
import {
  Banner,
  DetailGrid,
  EmptyState,
  Field,
  FieldGrid,
  FormActions,
  InlineActions,
  LoadingState,
  Modal,
  PageFrame,
  StatusPill,
  SortableTable,
  ToggleField,
  formatTime,
  useConfirm,
  useSessionState,
} from '../components/ui'

type TunnelNodeForm = {
  clientID: string
  id: string
  node_id: string
  weight: number
  protocols: ForwardProtocol[]
}

type TunnelStageForm = {
  clientID: string
  id: string
  strategy: string
  tcp_strategy: string
  udp_strategy: string
  nodes: TunnelNodeForm[]
}

type TunnelForm = {
  id: string
  name: string
  type: TunnelType
  transport: TunnelTransport
  entry_address: string
  enabled: boolean
  stages: TunnelStageForm[]
}

type TunnelFilters = {
  query: string
  type: 'all' | TunnelType
  transport: 'all' | TunnelTransport
}

const emptyTunnelFilters = (): TunnelFilters => ({ query: '', type: 'all', transport: 'all' })

function isTunnelFilters(value: unknown): value is TunnelFilters {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Record<string, unknown>
  return typeof candidate.query === 'string'
    && (candidate.type === 'all' || candidate.type === 'direct' || candidate.type === 'chain')
    && (candidate.transport === 'all'
      || candidate.transport === 'direct'
      || candidate.transport === 'tls'
      || candidate.transport === 'mtls'
      || candidate.transport === 'ws-tls')
}

const emptyTunnelForm = (): TunnelForm => ({
  id: '',
  name: '',
  type: 'direct',
  transport: 'direct',
  entry_address: '',
  enabled: true,
  stages: [emptyStageForm()],
})

export function TunnelsPage() {
  return (
    <>
      <TunnelsListView />
      <Outlet />
    </>
  )
}

export function TunnelNewPage() {
  const navigate = useNavigate()
  return <TunnelCreateModal onClose={() => navigate({ to: '/tunnels', replace: true, resetScroll: false })} />
}

export function TunnelDetailPage({ tunnelId }: { tunnelId: string }) {
  const navigate = useNavigate()
  return (
    <TunnelDetailModal
      tunnelId={tunnelId}
      onClose={() => navigate({ to: '/tunnels', replace: true, resetScroll: false })}
    />
  )
}

function TunnelsListView() {
  const query = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api<TunnelInfo[]>('/api/tunnels'),
  })
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
    enabled: (query.data ?? []).length > 0,
  })
  const nodeMap = useMemo(() => indexByID(nodesQuery.data ?? []), [nodesQuery.data])
  const tunnels = query.data ?? []
  const [filters, setFilters] = useSessionState<TunnelFilters>('tunnels.filters', emptyTunnelFilters, isTunnelFilters)
  const filteredTunnels = useMemo(() => {
    const needle = filters.query.trim().toLowerCase()
    return tunnels.filter((tunnel) => {
      if (filters.type !== 'all' && tunnel.type !== filters.type) return false
      if (filters.transport !== 'all' && tunnel.transport !== filters.transport) return false
      if (!needle) return true
      const searchable = [
        tunnel.name,
        tunnel.id,
        tunnel.entry_address,
        formatTunnelPath(tunnel, nodeMap),
      ].filter(Boolean).join(' ').toLowerCase()
      return searchable.includes(needle)
    })
  }, [filters, nodeMap, tunnels])
  const hasActiveFilters = Boolean(filters.query.trim() || filters.type !== 'all' || filters.transport !== 'all')
  const tunnelColumns = [
    { key: 'name', label: '名称', getValue: (tunnel: TunnelInfo) => tunnel.name },
    { key: 'type', label: '类型', getValue: (tunnel: TunnelInfo) => tunnel.type },
    { key: 'transport', label: '传输', getValue: (tunnel: TunnelInfo) => tunnel.transport },
    { key: 'path', label: '路径', getValue: (tunnel: TunnelInfo) => formatTunnelPath(tunnel, nodeMap) },
    { key: 'status', label: '状态', getValue: (tunnel: TunnelInfo) => tunnel.enabled ? 'online' : 'offline' },
  ]

  return (
    <PageFrame
      title="隧道"
      subtitle="隧道定义入口、中间节点、出口和节点间传输。"
      action={
        <Link to="/tunnels/new" className="button-link" resetScroll={false}>
          <Plus size={16} />
          新建隧道
        </Link>
      }
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {query.isLoading ? (
        <LoadingState label="正在加载隧道" />
      ) : query.error ? null : tunnels.length === 0 ? (
        <EmptyState
          title="还没有隧道"
          text="先选择入口节点；需要多跳时再添加中间节点和出口节点。"
          action={
            <Link to="/tunnels/new" className="button-link" resetScroll={false}>
              <Plus size={16} />
              新建隧道
            </Link>
          }
        />
      ) : (
        <>
          <div className="list-filters list-filters-tunnels" role="search" aria-label="隧道筛选">
            <Field label="搜索隧道">
              <input
                value={filters.query}
                onChange={(event) => setFilters((current) => ({ ...current, query: event.target.value }))}
                placeholder="名称、ID、入口或路径"
              />
            </Field>
            <Field label="类型">
              <select
                value={filters.type}
                onChange={(event) => setFilters((current) => ({ ...current, type: event.target.value as TunnelFilters['type'] }))}
              >
                <option value="all">全部类型</option>
                <option value="direct">直入直出</option>
                <option value="chain">链式隧道</option>
              </select>
            </Field>
            <Field label="传输">
              <select
                value={filters.transport}
                onChange={(event) => setFilters((current) => ({ ...current, transport: event.target.value as TunnelFilters['transport'] }))}
              >
                <option value="all">全部传输</option>
                <option value="direct">Direct stream</option>
                <option value="tls">TLS</option>
                <option value="mtls">mTLS</option>
                <option value="ws-tls">WS over TLS</option>
              </select>
            </Field>
            <button className="ghost" type="button" onClick={() => setFilters(emptyTunnelFilters())} disabled={!hasActiveFilters}>
              <RefreshCw size={16} />
              重置
            </button>
          </div>
          <div className="list-summary" aria-live="polite">
            <span>{hasActiveFilters ? `显示 ${filteredTunnels.length} / ${tunnels.length} 条隧道` : `共 ${tunnels.length} 条隧道`}</span>
            {hasActiveFilters && (
              <button className="text-button" type="button" onClick={() => setFilters(emptyTunnelFilters())}>
                清除筛选
              </button>
            )}
          </div>
          {filteredTunnels.length === 0 ? (
            <EmptyState title="没有匹配的隧道" text="调整搜索词、类型或传输筛选后再查看。" />
          ) : (
            <SortableTable
              items={filteredTunnels}
              columns={tunnelColumns}
              getRowKey={(tunnel) => tunnel.id}
              defaultSortKey="name"
              storageKey="tunnels"
            >
              {(tunnel) => (
                <tr key={tunnel.id}>
                  <td>
                    <strong>
                      <Link to="/tunnels/$tunnelId" params={{ tunnelId: tunnel.id }} resetScroll={false}>
                        {tunnel.name}
                      </Link>
                    </strong>
                    <small>{tunnel.id}</small>
                  </td>
                  <td>{tunnel.type}</td>
                  <td>{tunnel.transport}</td>
                  <td>{formatTunnelPath(tunnel, nodeMap)}</td>
                  <td><StatusPill value={tunnel.enabled ? 'online' : 'offline'} /></td>
                </tr>
              )}
            </SortableTable>
          )}
        </>
      )}
    </PageFrame>
  )
}

function TunnelCreateModal({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const confirmAction = useConfirm()
  const [editorDirty, setEditorDirty] = useState(false)
  const handleClose = () => {
    if (!editorDirty) {
      onClose()
      return
    }
    void confirmAction({
      title: '放弃隧道修改？',
      description: '当前隧道还有未保存的修改，关闭后这些内容不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) onClose()
    })
  }

  return (
    <Modal
      title="新建隧道"
      subtitle="隧道由一层或多层路由层组成，每层可放多个候选节点。"
      onClose={handleClose}
      size="lg"
    >
      <TunnelEditor
        onDirtyChange={setEditorDirty}
        onSaved={async (saved) => {
          setEditorDirty(false)
          await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
          navigate({ to: '/tunnels/$tunnelId', params: { tunnelId: saved.id }, replace: true, resetScroll: false })
        }}
      />
    </Modal>
  )
}

function TunnelDetailModal({ tunnelId, onClose }: { tunnelId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const confirmAction = useConfirm()
  const [mode, setMode] = useState<'details' | 'edit'>('details')
  const [editorDirty, setEditorDirty] = useState(false)
  const [message, setMessage] = useState('')
  const dashboardQuery = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => api<{ revision: number }>('/api/dashboard'),
  })
  const query = useQuery({
    queryKey: ['tunnel', tunnelId],
    queryFn: () => api<TunnelInfo>(`/api/tunnels/${tunnelId}`),
  })
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
    enabled: Boolean(query.data),
  })
  const nodeMap = useMemo(() => indexByID(nodesQuery.data ?? []), [nodesQuery.data])
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
      navigate({ to: '/tunnels', replace: true, resetScroll: false })
    },
  })

  const tunnel = query.data
  const handleClose = () => {
    if (mode !== 'edit' || !editorDirty) {
      onClose()
      return
    }
    void confirmAction({
      title: '放弃隧道修改？',
      description: '当前隧道还有未保存的修改，关闭后这些内容不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) onClose()
    })
  }
  const toggleMode = () => {
    if (mode !== 'edit' || !editorDirty) {
      setMode(mode === 'edit' ? 'details' : 'edit')
      return
    }
    void confirmAction({
      title: '放弃隧道修改？',
      description: '返回详情后，当前未保存的修改不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) setMode('details')
    })
  }

  return (
    <Modal
      title={tunnel?.name || '隧道详情'}
      subtitle="保存后控制器会重新签名并推送相关节点配置。"
      onClose={handleClose}
      size="lg"
      action={tunnel ? (
        <InlineActions>
          <button className="ghost" type="button" onClick={toggleMode}>
            <Settings size={16} />
            {mode === 'edit' ? '查看详情' : '编辑'}
          </button>
          {tunnel.enabled ? (
            <button className="ghost" type="button" onClick={() => disable.mutate()} disabled={disable.isPending || editorDirty}>
              <Pause size={16} />
              停用
            </button>
          ) : (
            <button className="ghost" type="button" onClick={() => enable.mutate()} disabled={enable.isPending || editorDirty}>
              <Play size={16} />
              启用
            </button>
          )}
          <button
            className="ghost danger"
            type="button"
            onClick={() => void confirmAction({
              title: '删除隧道？',
              description: `隧道“${tunnel.name}”将被永久删除，相关节点配置会重新下发。`,
              confirmLabel: '删除隧道',
              tone: 'danger',
            }).then((confirmed) => {
              if (confirmed) remove.mutate()
            })}
            disabled={remove.isPending || editorDirty}
          >
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
      {tunnel ? (
        <>
          {mode === 'edit' ? (
            <TunnelEditor
              initialTunnel={tunnel}
              onDirtyChange={setEditorDirty}
              onSaved={async () => {
                setMessage('已保存')
                setEditorDirty(false)
                setMode('details')
                await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
                await queryClient.invalidateQueries({ queryKey: ['tunnel', tunnelId] })
                await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
              }}
            />
          ) : (
            <TunnelDetailsContent tunnel={tunnel} nodes={nodeMap} revision={dashboardQuery.data?.revision} />
          )}
          {message && <p className="modal-message">{message}</p>}
        </>
      ) : null}
    </Modal>
  )
}

function TunnelDetailsContent({
  tunnel,
  nodes,
  revision,
}: {
  tunnel: TunnelInfo
  nodes: Map<string, NodeInfo>
  revision?: number
}) {
  return (
    <>
      <section className="modal-section">
        <h3>基础信息</h3>
        <DetailGrid
          items={[
            { label: 'ID', value: tunnel.id },
            { label: '类型', value: tunnel.type },
            { label: '传输', value: tunnel.transport },
            { label: '入口地址', value: tunnel.entry_address || defaultTunnelEntryAddress(tunnel, nodes) || '-' },
            { label: '路径', value: formatTunnelPath(tunnel, nodes) },
            { label: '配置版本', value: String(revision ?? '-') },
            { label: '创建时间', value: formatTime(tunnel.created_at) },
            { label: '更新时间', value: formatTime(tunnel.updated_at) },
            { label: '状态', value: <StatusPill value={tunnel.enabled ? 'online' : 'offline'} /> },
          ]}
        />
      </section>
      <section className="modal-section">
        <h3>路由层</h3>
        <div className="hop-list">
          {tunnel.stages.map((stage, stageIndex) => (
            <div className="hop" key={stage.id}>
              <span>{stage.index + 1}</span>
              <strong>{stageLabel(tunnel.type, stageIndex, tunnel.stages.length)}</strong>
              <small>TCP {strategyLabel(stage.tcp_strategy || stage.strategy)} · UDP {strategyLabel(stage.udp_strategy || stage.strategy)}</small>
              <small>{stage.nodes.map((node) => `${nodeDisplayName(node.node_id, nodes)} (${formatProtocols(normalizeNodeProtocols(node.protocols))})`).join(' / ')}</small>
            </div>
          ))}
        </div>
      </section>
    </>
  )
}

function TunnelEditor({
  initialTunnel,
  onSaved,
  onDirtyChange,
}: {
  initialTunnel?: TunnelInfo
  onSaved: (saved: TunnelInfo) => void | Promise<void>
  onDirtyChange?: (dirty: boolean) => void
}) {
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const allNodes = nodesQuery.data ?? []
  const nodes = useMemo(() => {
    if (!initialTunnel) {
      return allNodes.filter((node) => !node.revoked)
    }
    const selectedIDs = new Set(
      initialTunnel.stages.flatMap((stage) => stage.nodes.map((node) => node.node_id)),
    )
    return allNodes.filter((node) => !node.revoked || selectedIDs.has(node.id))
  }, [allNodes, initialTunnel])
  const [form, setForm] = useState<TunnelForm>(emptyTunnelForm())
  const [error, setError] = useState('')
  const [dirty, setDirty] = useState(false)
  const updateForm: typeof setForm = (updater) => {
    setDirty(true)
    setForm(updater)
  }
  const [draggingCandidate, setDraggingCandidate] = useState<{ stageIndex: number; nodeIndex: number } | null>(null)
  const candidateRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const previousCandidateCounts = useRef<Record<string, number>>({})

  useEffect(() => {
    if (!initialTunnel || dirty) return
    setForm({
      id: initialTunnel.id,
      name: initialTunnel.name,
      type: initialTunnel.type,
      transport: initialTunnel.transport,
      entry_address: initialTunnel.entry_address ?? '',
      enabled: initialTunnel.enabled,
      stages: initialTunnel.stages.length > 0
        ? initialTunnel.stages.map((stage) => ({
          clientID: stage.id || newFormClientID('stage'),
          id: stage.id,
          strategy: stage.strategy || 'single',
          tcp_strategy: stage.tcp_strategy || '',
          udp_strategy: stage.udp_strategy || '',
          nodes: stage.nodes.length > 0
            ? stage.nodes.map((node) => ({
              clientID: node.id || newFormClientID('candidate'),
              id: node.id,
              node_id: node.node_id,
              weight: node.weight || 1,
              protocols: normalizeNodeProtocols(node.protocols),
            }))
            : [emptyNodeForm()],
        }))
        : defaultStagesForType(initialTunnel.type),
    })
    setDirty(false)
  }, [dirty, initialTunnel])

  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])

  useEffect(() => {
    form.stages.forEach((stage) => {
      const previousCount = previousCandidateCounts.current[stage.clientID]
      if (previousCount !== undefined && stage.nodes.length > previousCount) {
        const newCandidate = stage.nodes[stage.nodes.length - 1]
        if (newCandidate) {
          const scrollToCandidate = () => candidateRefs.current[newCandidate.clientID]?.scrollIntoView?.({ block: 'nearest' })
          if (typeof window.requestAnimationFrame === 'function') window.requestAnimationFrame(scrollToCandidate)
          else window.setTimeout(scrollToCandidate, 0)
        }
      }
      previousCandidateCounts.current[stage.clientID] = stage.nodes.length
    })
  }, [form.stages])

  const save = useMutation({
    mutationFn: () => {
      const stages = normalizeStagesForType(form.type, form.stages)
      const payload = {
        id: form.id,
        name: form.name,
        type: form.type,
        transport: form.type === 'direct' ? 'direct' : form.transport,
        entry_address: form.entry_address.trim(),
        enabled: form.enabled,
        stages: stages.map((stage, index) => ({
          id: stage.id,
          index,
          role: stageRoleFor(form.type, index, stages.length),
          strategy: stage.strategy || 'single',
          tcp_strategy: stage.tcp_strategy,
          udp_strategy: stage.udp_strategy,
          nodes: stage.nodes.map((node) => ({
            id: node.id,
            node_id: node.node_id,
            weight: node.weight || 1,
            protocols: normalizeNodeProtocols(node.protocols),
          })),
        })),
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
      setDirty(false)
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
          <input value={form.name} onChange={(event) => updateForm((current) => ({ ...current, name: event.target.value }))} />
        </Field>
        <Field label="类型">
          <select
            value={form.type}
            onChange={(event) => updateForm((current) => {
              const type = event.target.value as TunnelType
              return {
                ...current,
                type,
                transport: type === 'direct' ? 'direct' : current.transport,
                stages: normalizeStagesForType(type, current.stages),
              }
            })}
          >
            <option value="direct">直入直出</option>
            <option value="chain">链式隧道</option>
          </select>
        </Field>
        <Field label="传输">
          <select
            value={form.type === 'direct' ? 'direct' : form.transport}
            disabled={form.type === 'direct'}
            onChange={(event) => updateForm((current) => ({ ...current, transport: event.target.value as TunnelTransport }))}
          >
            <option value="direct">Direct stream</option>
            <option value="tls">TLS</option>
            <option value="mtls">mTLS</option>
            <option value="ws-tls">WS over TLS</option>
          </select>
        </Field>
        <Field label="入口地址" hint="可填域名或 IP；留空时使用入口层第一个节点的节点 IP / 域名。">
          <input
            value={form.entry_address}
            onChange={(event) => updateForm((current) => ({ ...current, entry_address: event.target.value }))}
            placeholder="edge.example.com"
          />
        </Field>
        <ToggleField
          label="启用"
          ariaLabel="启用隧道"
          description="保存后立即推送到相关节点。"
          checked={form.enabled}
          onChange={(checked) => updateForm((current) => ({ ...current, enabled: checked }))}
        />
      </FieldGrid>
      <section className="form-section route-editor">
        <div className="form-section-header multi-editor-heading">
          <div>
            <h2>路由路径</h2>
            <p>按入口到出口的顺序管理每层节点和选择策略。</p>
          </div>
          {form.type === 'chain' ? (
            <button
              type="button"
              className="ghost"
              onClick={() => updateForm((current) => ({
                ...current,
                stages: insertMiddleStage(current.stages),
              }))}
            >
              <Plus size={16} />
              添加中间层
            </button>
          ) : null}
        </div>
        <div className="route-stage-map" aria-label="隧道路由路径">
          {form.stages.map((stage, stageIndex) => {
            const role = stageRoleFor(form.type, stageIndex, form.stages.length)
            const RoleIcon = stageRoleIcon(role)
            return (
              <div className={`route-stage-map-item role-${role}`} key={stage.clientID}>
                {stageIndex > 0 && <ChevronRight className="route-stage-connector" size={16} aria-hidden="true" />}
                <span><RoleIcon size={16} /></span>
                <strong>{stageLabel(form.type, stageIndex, form.stages.length)}</strong>
                <small>{stage.nodes.length} 个节点</small>
              </div>
            )
          })}
        </div>
        {nodesQuery.isSuccess && nodes.length === 0 && (
          <div className="form-notice" role="status">
            <Server size={17} />
            <span>当前没有可用于隧道的节点。</span>
          </div>
        )}
        <div className="hop-list">
          {form.stages.map((stage, stageIndex) => {
            const label = stageLabel(form.type, stageIndex, form.stages.length)
            const role = stageRoleFor(form.type, stageIndex, form.stages.length)
            const RoleIcon = stageRoleIcon(role)
            return (
              <section key={stage.clientID} className={`stage-editor role-${role}`}>
                <div className="stage-header">
                  <div className="stage-ident">
                    <span className="stage-role-icon"><RoleIcon size={18} /></span>
                    <div className="stage-title">
                      <strong>{label}</strong>
                      <small>{stageRoleLabel(role)}</small>
                    </div>
                    <span className="stage-count">{stage.nodes.length} 个候选</span>
                  </div>
                  <InlineActions>
                    <button
                      type="button"
                      className="ghost"
                      onClick={() => updateForm((current) => ({
                        ...current,
                        stages: current.stages.map((item, idx) => (
                          idx === stageIndex
                            ? { ...item, nodes: [...item.nodes, emptyNodeForm()] }
                            : item
                        )),
                      }))}
                    >
                      <Plus size={16} />
                      添加候选
                    </button>
                    {form.type === 'chain' && stageIndex > 0 && stageIndex < form.stages.length - 1 ? (
                      <>
                        <button
                          type="button"
                          className="ghost icon-button"
                          aria-label={`上移${label}`}
                          title="上移中间层"
                          disabled={stageIndex === 1}
                          onClick={() => updateForm((current) => ({
                            ...current,
                            stages: moveArrayItem(current.stages, stageIndex, stageIndex - 1),
                          }))}
                        >
                          <ArrowUp size={16} />
                        </button>
                        <button
                          type="button"
                          className="ghost icon-button"
                          aria-label={`下移${label}`}
                          title="下移中间层"
                          disabled={stageIndex === form.stages.length - 2}
                          onClick={() => updateForm((current) => ({
                            ...current,
                            stages: moveArrayItem(current.stages, stageIndex, stageIndex + 1),
                          }))}
                        >
                          <ArrowDown size={16} />
                        </button>
                        <button
                          type="button"
                          className="ghost danger icon-button"
                          aria-label={`删除${label}`}
                          title="删除中间层"
                          onClick={() => updateForm((current) => ({
                            ...current,
                            stages: current.stages.filter((_, idx) => idx !== stageIndex),
                          }))}
                        >
                          <Trash2 size={16} />
                        </button>
                      </>
                    ) : null}
                  </InlineActions>
                </div>
                <FieldGrid className="stage-strategy-grid">
                  <Field label="TCP 策略">
                    <select
                      value={stage.tcp_strategy || stage.strategy || 'single'}
                      onChange={(event) => updateForm((current) => ({
                        ...current,
                        stages: current.stages.map((item, idx) => (
                          idx === stageIndex ? { ...item, tcp_strategy: event.target.value } : item
                        )),
                      }))}
                    >
                      <option value="single">单候选</option>
                      <option value="failover">故障切换</option>
                      <option value="round_robin">轮询</option>
                      <option value="random">随机</option>
                    </select>
                  </Field>
                  <Field label="UDP 策略">
                    <select
                      value={stage.udp_strategy || stage.strategy || 'single'}
                      onChange={(event) => updateForm((current) => ({
                        ...current,
                        stages: current.stages.map((item, idx) => (
                          idx === stageIndex ? { ...item, udp_strategy: event.target.value } : item
                        )),
                      }))}
                    >
                      <option value="single">单候选</option>
                      <option value="failover">故障切换</option>
                      <option value="round_robin">轮询</option>
                      <option value="random">随机</option>
                    </select>
                  </Field>
                </FieldGrid>
                <div className="candidate-list-heading">
                  <strong>候选节点</strong>
                  <span>顺序用于故障切换，也可用方向按钮调整。</span>
                </div>
                <div className="hop-list">
                  {stage.nodes.map((nodeForm, nodeIndex) => {
                    const selectedNode = nodes.find((node) => node.id === nodeForm.node_id)
                    return (
                    <div
                      className={`hop candidate-row${draggingCandidate?.stageIndex === stageIndex && draggingCandidate.nodeIndex === nodeIndex ? ' dragging' : ''}`}
                      key={nodeForm.clientID}
                      ref={(element) => { candidateRefs.current[nodeForm.clientID] = element }}
                      onDragOver={(event) => {
                        if (!draggingCandidate || draggingCandidate.stageIndex !== stageIndex) return
                        event.preventDefault()
                        event.dataTransfer.dropEffect = 'move'
                      }}
                      onDrop={(event) => {
                        event.preventDefault()
                        const source = parseCandidateDragData(event.dataTransfer.getData('text/plain'))
                        if (!source || source.stageIndex !== stageIndex || source.nodeIndex === nodeIndex) {
                          setDraggingCandidate(null)
                          return
                        }
                        updateForm((current) => ({
                          ...current,
                          stages: current.stages.map((item, idx) => (
                            idx === stageIndex
                              ? { ...item, nodes: moveArrayItem(item.nodes, source.nodeIndex, nodeIndex) }
                              : item
                          )),
                        }))
                        setDraggingCandidate(null)
                      }}
                    >
                      <div className="candidate-card-header">
                        <div className="candidate-card-ident">
                          <span className="item-index">{nodeIndex + 1}</span>
                          <div>
                            <strong>{selectedNode?.name || `候选 ${nodeIndex + 1}`}</strong>
                            <small>{selectedNode ? nodeAddressLabel(selectedNode) : '尚未选择节点'}</small>
                          </div>
                        </div>
                        <InlineActions>
                          <button
                            type="button"
                            className="ghost icon-button candidate-drag-handle"
                            aria-label={`拖拽排序候选 ${stageIndex + 1}-${nodeIndex + 1}`}
                            title="拖拽排序"
                            disabled={stage.nodes.length === 1}
                            draggable={stage.nodes.length > 1}
                            onDragStart={(event) => {
                              if (stage.nodes.length === 1) return
                              event.dataTransfer.effectAllowed = 'move'
                              event.dataTransfer.setData('text/plain', `${stageIndex}:${nodeIndex}`)
                              setDraggingCandidate({ stageIndex, nodeIndex })
                            }}
                            onDragEnd={() => setDraggingCandidate(null)}
                          >
                            <GripVertical size={16} />
                          </button>
                          <button
                            type="button"
                            className="ghost icon-button"
                            aria-label={`上移候选 ${stageIndex + 1}-${nodeIndex + 1}`}
                            title="上移候选"
                            disabled={nodeIndex === 0}
                            onClick={() => moveCandidate(updateForm, stageIndex, nodeIndex, nodeIndex - 1)}
                          >
                            <ArrowUp size={16} />
                          </button>
                          <button
                            type="button"
                            className="ghost icon-button"
                            aria-label={`下移候选 ${stageIndex + 1}-${nodeIndex + 1}`}
                            title="下移候选"
                            disabled={nodeIndex === stage.nodes.length - 1}
                            onClick={() => moveCandidate(updateForm, stageIndex, nodeIndex, nodeIndex + 1)}
                          >
                            <ArrowDown size={16} />
                          </button>
                          <button
                            type="button"
                            className="ghost danger icon-button"
                            aria-label={`移除候选 ${stageIndex + 1}-${nodeIndex + 1}`}
                            title="移除候选"
                            disabled={stage.nodes.length === 1}
                            onClick={() => updateForm((current) => ({
                              ...current,
                              stages: current.stages.map((item, idx) => (
                                idx === stageIndex
                                  ? {
                                    ...item,
                                    nodes: item.nodes.filter((_, cIndex) => cIndex !== nodeIndex),
                                  }
                                  : item
                              )),
                            }))}
                          >
                            <Trash2 size={16} />
                          </button>
                        </InlineActions>
                      </div>
                      <div className="candidate-card-fields">
                        <label className="candidate-field candidate-node-field">
                          <span>节点</span>
                          <select
                            aria-label={`候选节点 ${stageIndex + 1}-${nodeIndex + 1}`}
                            value={nodeForm.node_id}
                            onChange={(event) => updateForm((current) => ({
                              ...current,
                              stages: current.stages.map((item, idx) => (
                                idx === stageIndex
                                  ? {
                                    ...item,
                                    nodes: item.nodes.map((candidate, cIndex) => (
                                      cIndex === nodeIndex ? { ...candidate, node_id: event.target.value } : candidate
                                    )),
                                  }
                                  : item
                              )),
                            }))}
                          >
                            <option value="">选择节点</option>
                            {nodes.map((node) => (
                              <option
                                key={node.id}
                                value={node.id}
                                disabled={node.id !== nodeForm.node_id && stage.nodes.some((candidate) => candidate.node_id === node.id)}
                              >
                                {node.name}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label className="candidate-field candidate-weight">
                          <span>权重</span>
                          <input
                            aria-label={`候选权重 ${stageIndex + 1}-${nodeIndex + 1}`}
                            type="number"
                            min={1}
                            value={String(nodeForm.weight)}
                            onChange={(event) => updateForm((current) => ({
                              ...current,
                              stages: current.stages.map((item, idx) => (
                                idx === stageIndex
                                  ? {
                                    ...item,
                                    nodes: item.nodes.map((candidate, cIndex) => (
                                      cIndex === nodeIndex
                                        ? {
                                          ...candidate,
                                          weight: Math.max(1, Number(event.target.value) || 1),
                                        }
                                        : candidate
                                    )),
                                  }
                                  : item
                              )),
                            }))}
                          />
                        </label>
                        <div className="candidate-field candidate-protocol-field">
                          <span>协议</span>
                          <div className="candidate-protocols">
                            {(['tcp', 'udp'] as const).map((protocol) => (
                              <label key={protocol}>
                                <input
                                  aria-label={protocol.toUpperCase()}
                                  type="checkbox"
                                  checked={nodeForm.protocols.includes(protocol)}
                                  onChange={(event) => updateForm((current) => ({
                                    ...current,
                                    stages: current.stages.map((item, idx) => (
                                      idx === stageIndex
                                        ? {
                                          ...item,
                                          nodes: item.nodes.map((candidate, cIndex) => (
                                            cIndex === nodeIndex
                                              ? {
                                                ...candidate,
                                                protocols: toggleNodeProtocol(candidate.protocols, protocol, event.target.checked),
                                              }
                                              : candidate
                                          )),
                                        }
                                        : item
                                    )),
                                  }))}
                                />
                                {protocol.toUpperCase()}
                              </label>
                            ))}
                          </div>
                        </div>
                      </div>
                    </div>
                    )
                  })}
                </div>
              </section>
            )
          })}
        </div>
      </section>
      {error && <p className="error">{error}</p>}
      <FormActions>
        <button type="submit" disabled={save.isPending}>保存隧道</button>
      </FormActions>
    </form>
  )
}

function emptyNodeForm(): TunnelNodeForm {
  return {
    clientID: newFormClientID('candidate'),
    id: '',
    node_id: '',
    weight: 1,
    protocols: ['tcp', 'udp'],
  }
}

function emptyStageForm(): TunnelStageForm {
  return {
    clientID: newFormClientID('stage'),
    id: '',
    strategy: 'single',
    tcp_strategy: 'single',
    udp_strategy: 'single',
    nodes: [emptyNodeForm()],
  }
}

function defaultStagesForType(type: TunnelType): TunnelStageForm[] {
  if (type === 'chain') {
    return [emptyStageForm(), emptyStageForm()]
  }
  return [emptyStageForm()]
}

function normalizeStagesForType(type: TunnelType, stages: TunnelStageForm[]): TunnelStageForm[] {
  const normalized = stages.length > 0 ? stages : defaultStagesForType(type)
  if (type === 'direct') {
    return [normalizeStage(normalized[0])]
  }
  if (normalized.length === 1) {
    return [normalizeStage(normalized[0]), emptyStageForm()]
  }
  return normalized.map(normalizeStage)
}

function normalizeStage(stage: TunnelStageForm): TunnelStageForm {
  const nodes = stage.nodes.length > 0 ? stage.nodes : [emptyNodeForm()]
  return {
    clientID: stage.clientID,
    id: stage.id,
    strategy: stage.strategy || 'single',
    tcp_strategy: stage.tcp_strategy || stage.strategy || 'single',
    udp_strategy: stage.udp_strategy || stage.strategy || 'single',
    nodes: nodes.map((node) => ({
      clientID: node.clientID,
      id: node.id,
      node_id: node.node_id,
      weight: node.weight > 0 ? node.weight : 1,
      protocols: normalizeNodeProtocols(node.protocols),
    })),
  }
}

let nextFormClientID = 0

function newFormClientID(prefix: string) {
  nextFormClientID += 1
  return `${prefix}-${nextFormClientID}`
}

function stageLabel(type: TunnelType, index: number, stageCount: number): string {
  if (type === 'direct') {
    return '入口层'
  }
  if (index === 0) {
    return '入口层'
  }
  if (index === stageCount - 1) {
    return '出口层'
  }
  return `中间层 ${index}`
}

function stageRoleFor(type: TunnelType, index: number, stageCount: number): TunnelStageRole {
  if (type === 'direct' || index === 0) {
    return 'entry'
  }
  if (index === stageCount - 1) {
    return 'exit'
  }
  return 'middle'
}

function stageRoleIcon(role: TunnelStageRole) {
  if (role === 'entry') return LogIn
  if (role === 'exit') return LogOut
  return Milestone
}

function stageRoleLabel(role: TunnelStageRole): string {
  if (role === 'entry') return '接收外部流量'
  if (role === 'exit') return '连接最终目标'
  return '中继并转发流量'
}

function strategyLabel(strategy?: string): string {
  if (strategy === 'failover') return '故障切换'
  if (strategy === 'round_robin') return '轮询'
  if (strategy === 'random') return '随机'
  return '单候选'
}

function nodeAddressLabel(node: NodeInfo): string {
  return node.public_host || node.system?.ip || node.system?.hostname || '未配置公开地址'
}

function insertMiddleStage(stages: TunnelStageForm[]): TunnelStageForm[] {
  if (stages.length <= 1) {
    return [...stages, emptyStageForm()]
  }
  return [...stages.slice(0, stages.length - 1), emptyStageForm(), stages[stages.length - 1]]
}

function parseCandidateDragData(value: string): { stageIndex: number; nodeIndex: number } | null {
  const [stageIndex, nodeIndex] = value.split(':').map((part) => Number(part))
  if (!Number.isInteger(stageIndex) || !Number.isInteger(nodeIndex)) {
    return null
  }
  return { stageIndex, nodeIndex }
}

function moveArrayItem<T>(items: T[], fromIndex: number, toIndex: number): T[] {
  if (
    fromIndex === toIndex ||
    fromIndex < 0 ||
    toIndex < 0 ||
    fromIndex >= items.length ||
    toIndex >= items.length
  ) {
    return items
  }
  const next = [...items]
  const [moved] = next.splice(fromIndex, 1)
  next.splice(toIndex, 0, moved)
  return next
}

function moveCandidate(
  setForm: React.Dispatch<React.SetStateAction<TunnelForm>>,
  stageIndex: number,
  fromIndex: number,
  toIndex: number,
) {
  setForm((current) => ({
    ...current,
    stages: current.stages.map((stage, index) => (
      index === stageIndex
        ? { ...stage, nodes: moveArrayItem(stage.nodes, fromIndex, toIndex) }
        : stage
    )),
  }))
}

function normalizeNodeProtocols(protocols?: ForwardProtocol[]): ForwardProtocol[] {
  const enabled = new Set(protocols && protocols.length > 0 ? protocols : ['tcp', 'udp'])
  const out: ForwardProtocol[] = []
  if (enabled.has('tcp')) out.push('tcp')
  if (enabled.has('udp')) out.push('udp')
  return out.length > 0 ? out : ['tcp', 'udp']
}

function toggleNodeProtocol(protocols: ForwardProtocol[], protocol: ForwardProtocol, enabled: boolean): ForwardProtocol[] {
  const next = new Set(normalizeNodeProtocols(protocols))
  if (enabled) {
    next.add(protocol)
  } else {
    next.delete(protocol)
  }
  return normalizeNodeProtocols(Array.from(next) as ForwardProtocol[])
}

function formatProtocols(protocols: ForwardProtocol[]) {
  return normalizeNodeProtocols(protocols).map((protocol) => protocol.toUpperCase()).join('+')
}

function indexByID<T extends { id: string }>(items: T[]) {
  return new Map(items.map((item) => [item.id, item]))
}

function formatTunnelPath(tunnel: TunnelInfo, nodes?: Map<string, NodeInfo>) {
  return tunnel.stages
    .slice()
    .sort((a, b) => a.index - b.index)
    .map((stage) => stage.nodes.map((node) => nodeDisplayName(node.node_id, nodes)).join('/'))
    .join(' -> ')
}

function defaultTunnelEntryAddress(tunnel: TunnelInfo, nodes?: Map<string, NodeInfo>) {
  const entryNodeID = tunnel.stages.find((stage) => stage.role === 'entry')?.nodes[0]?.node_id
  if (!entryNodeID) return ''
  const entryNode = nodes?.get(entryNodeID)
  return entryNode?.public_host || entryNode?.system?.ip || ''
}

function nodeDisplayName(nodeID: string, nodes?: Map<string, NodeInfo>) {
  return nodes?.get(nodeID)?.name || nodeID
}
