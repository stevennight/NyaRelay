import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Pause, Play, Plus, Settings, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { api, post } from '../api'
import type { NodeInfo, TunnelInfo, TunnelStageRole, TunnelTransport, TunnelType } from '../types'
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

type TunnelNodeForm = {
  id: string
  node_id: string
  weight: number
}

type TunnelStageForm = {
  id: string
  strategy: string
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
  const navigate = useNavigate()
  return <TunnelsListView onCloseModal={() => navigate({ to: '/tunnels', replace: true })} />
}

export function TunnelNewPage() {
  const navigate = useNavigate()
  return (
    <TunnelsListView
      modal="new"
      onCloseModal={() => navigate({ to: '/tunnels', replace: true })}
    />
  )
}

export function TunnelDetailPage({ tunnelId }: { tunnelId: string }) {
  const navigate = useNavigate()
  return (
    <TunnelsListView
      modal="detail"
      tunnelId={tunnelId}
      onCloseModal={() => navigate({ to: '/tunnels', replace: true })}
    />
  )
}

function TunnelsListView({
  modal,
  tunnelId,
  onCloseModal,
}: {
  modal?: 'new' | 'detail'
  tunnelId?: string
  onCloseModal: () => void
}) {
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
              <td>{formatTunnelPath(tunnel, nodeMap)}</td>
              <td><StatusPill value={tunnel.enabled ? 'online' : 'offline'} /></td>
            </tr>
          ))}
        </Table>
      )}
      {modal === 'new' && <TunnelCreateModal onClose={onCloseModal} />}
      {modal === 'detail' && tunnelId && <TunnelDetailModal tunnelId={tunnelId} onClose={onCloseModal} />}
    </PageFrame>
  )
}

function TunnelCreateModal({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  return (
    <Modal
      title="新建隧道"
      subtitle="隧道由一层或多层 stage 组成，每层可放多个候选节点。"
      onClose={onClose}
      size="lg"
    >
      <TunnelEditor
        onSaved={async (saved) => {
          await queryClient.invalidateQueries({ queryKey: ['tunnels'] })
          navigate({ to: '/tunnels/$tunnelId', params: { tunnelId: saved.id }, replace: true })
        }}
      />
    </Modal>
  )
}

function TunnelDetailModal({ tunnelId, onClose }: { tunnelId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [mode, setMode] = useState<'details' | 'edit'>('details')
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
      navigate({ to: '/tunnels', replace: true })
    },
  })

  const tunnel = query.data

  return (
    <Modal
      title={tunnel?.name || '隧道详情'}
      subtitle="保存后控制器会重新签名并推送相关节点配置。"
      onClose={onClose}
      size="lg"
      action={tunnel ? (
        <InlineActions>
          <button className="ghost" type="button" onClick={() => setMode(mode === 'edit' ? 'details' : 'edit')}>
            <Settings size={16} />
            {mode === 'edit' ? '查看详情' : '编辑'}
          </button>
          {tunnel.enabled ? (
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
      {tunnel ? (
        <>
          {mode === 'edit' ? (
            <TunnelEditor
              initialTunnel={tunnel}
              onSaved={async () => {
                setMessage('已保存')
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
        <h3>Stages</h3>
        <div className="hop-list">
          {tunnel.stages.map((stage) => (
            <div className="hop" key={stage.id}>
              <span>{stage.index + 1}</span>
              <strong>{stage.role}</strong>
              <small>{stage.strategy}</small>
              <small>{stage.nodes.map((node) => nodeDisplayName(node.node_id, nodes)).join(' / ')}</small>
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
}: {
  initialTunnel?: TunnelInfo
  onSaved: (saved: TunnelInfo) => void | Promise<void>
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

  useEffect(() => {
    if (!initialTunnel) return
    setForm({
      id: initialTunnel.id,
      name: initialTunnel.name,
      type: initialTunnel.type,
      transport: initialTunnel.transport,
      entry_address: initialTunnel.entry_address ?? '',
      enabled: initialTunnel.enabled,
      stages: initialTunnel.stages.length > 0
        ? initialTunnel.stages.map((stage) => ({
          id: stage.id,
          strategy: stage.strategy || 'single',
          nodes: stage.nodes.length > 0
            ? stage.nodes.map((node) => ({
              id: node.id,
              node_id: node.node_id,
              weight: node.weight || 1,
            }))
            : [emptyNodeForm()],
        }))
        : defaultStagesForType(initialTunnel.type),
    })
  }, [initialTunnel])

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
          nodes: stage.nodes.map((node) => ({
            id: node.id,
            node_id: node.node_id,
            weight: node.weight || 1,
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
        <Field label="类型">
          <select
            value={form.type}
            onChange={(event) => setForm((current) => {
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
            onChange={(event) => setForm((current) => ({ ...current, transport: event.target.value as TunnelTransport }))}
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
            onChange={(event) => setForm((current) => ({ ...current, entry_address: event.target.value }))}
            placeholder="edge.example.com"
          />
        </Field>
        <ToggleField
          label="启用"
          description="保存后立即推送到相关节点。"
          checked={form.enabled}
          onChange={(checked) => setForm((current) => ({ ...current, enabled: checked }))}
        />
      </FieldGrid>
      <section className="form-section">
        <div className="form-section-header">
          <h2>Stages</h2>
          {form.type === 'chain' ? (
            <button
              type="button"
              className="ghost"
              onClick={() => setForm((current) => ({
                ...current,
                stages: insertMiddleStage(current.stages),
              }))}
            >
              <Plus size={16} />
              添加中间层
            </button>
          ) : null}
        </div>
        <div className="hop-list">
          {form.stages.map((stage, stageIndex) => {
            const label = stageLabel(form.type, stageIndex, form.stages.length)
            return (
              <div key={stage.id || `${stageIndex}`} className="stage-editor">
                <div className="stage-header">
                  <div className="stage-title">
                    <strong>{label}</strong>
                    <small>{stageRoleFor(form.type, stageIndex, form.stages.length)} · {stage.nodes.length} 个候选</small>
                  </div>
                  <InlineActions>
                    <button
                      type="button"
                      className="ghost"
                      onClick={() => setForm((current) => ({
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
                      <button
                        type="button"
                        className="ghost danger"
                        onClick={() => setForm((current) => ({
                          ...current,
                          stages: current.stages.filter((_, idx) => idx !== stageIndex),
                        }))}
                      >
                        <Trash2 size={16} />
                        删除层
                      </button>
                    ) : null}
                  </InlineActions>
                </div>
                <FieldGrid>
                  <Field label="策略">
                    <select
                      value={stage.strategy}
                      onChange={(event) => setForm((current) => ({
                        ...current,
                        stages: current.stages.map((item, idx) => (
                          idx === stageIndex ? { ...item, strategy: event.target.value } : item
                        )),
                      }))}
                    >
                      <option value="single">单候选</option>
                      <option value="failover">故障切换</option>
                      <option value="round_robin">轮询</option>
                      <option value="random">随机</option>
                    </select>
                  </Field>
                  <Field label="候选数量">
                    <input value={String(stage.nodes.length)} readOnly />
                  </Field>
                </FieldGrid>
                <div className="hop-list">
                  {stage.nodes.map((nodeForm, nodeIndex) => (
                    <div className="hop candidate-row" key={nodeForm.id || `${stageIndex}-${nodeIndex}`}>
                      <span>{nodeIndex + 1}</span>
                      <select
                        aria-label={`候选节点 ${stageIndex + 1}-${nodeIndex + 1}`}
                        value={nodeForm.node_id}
                        onChange={(event) => setForm((current) => ({
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
                          <option key={node.id} value={node.id}>
                            {node.name}
                          </option>
                        ))}
                      </select>
                      <label className="candidate-weight">
                        <span>权重</span>
                        <input
                          aria-label={`候选权重 ${stageIndex + 1}-${nodeIndex + 1}`}
                          type="number"
                          min={1}
                          value={String(nodeForm.weight)}
                          onChange={(event) => setForm((current) => ({
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
                      <button
                        type="button"
                        className="ghost danger"
                        disabled={stage.nodes.length === 1}
                        onClick={() => setForm((current) => ({
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
                        移除
                      </button>
                    </div>
                  ))}
                </div>
              </div>
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
    id: '',
    node_id: '',
    weight: 1,
  }
}

function emptyStageForm(): TunnelStageForm {
  return {
    id: '',
    strategy: 'single',
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
    id: stage.id,
    strategy: stage.strategy || 'single',
    nodes: nodes.map((node) => ({
      id: node.id,
      node_id: node.node_id,
      weight: node.weight > 0 ? node.weight : 1,
    })),
  }
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

function insertMiddleStage(stages: TunnelStageForm[]): TunnelStageForm[] {
  if (stages.length <= 1) {
    return [...stages, emptyStageForm()]
  }
  return [...stages.slice(0, stages.length - 1), emptyStageForm(), stages[stages.length - 1]]
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
