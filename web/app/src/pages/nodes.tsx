import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ClipboardCopy, Download, Plus, RefreshCw, Settings, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, post } from '../api'
import type { ControllerInfo, NodeInfo, NodeInstallInfo, SignedNodeRelease } from '../types'
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
  Panel,
  StatusPill,
  SortableTable,
  formatTime,
  useConfirm,
  useSessionState,
} from '../components/ui'

type NodeForm = {
  name: string
  labels: string
  public_host: string
  port_min: string
  port_max: string
}

const emptyForm: NodeForm = {
  name: '',
  labels: '{}',
  public_host: '',
  port_min: '10000',
  port_max: '65535',
}

const nodeColumns = [
  { key: 'name', label: '名称', getValue: (node: NodeInfo) => node.name },
  { key: 'status', label: '状态', getValue: (node: NodeInfo) => node.revoked ? 'revoked' : node.status },
  { key: 'version', label: '版本', getValue: (node: NodeInfo) => node.version || '' },
  { key: 'system', label: '系统', getValue: (node: NodeInfo) => [node.system?.os, node.system?.arch].filter(Boolean).join('/') },
  { key: 'last_seen', label: '最近心跳', getValue: (node: NodeInfo) => node.last_seen ? Date.parse(node.last_seen) : null },
  { key: 'actions', label: '操作', sortable: false, getValue: () => null },
]

type NodeFilters = {
  query: string
  status: 'all' | 'online' | 'offline'
}

const emptyNodeFilters = (): NodeFilters => ({ query: '', status: 'all' })

function isNodeFilters(value: unknown): value is NodeFilters {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Record<string, unknown>
  return typeof candidate.query === 'string'
    && (candidate.status === 'all' || candidate.status === 'online' || candidate.status === 'offline')
}

export function NodesPage() {
  const navigate = useNavigate()
  return <NodesListView onCloseModal={() => navigate({ to: '/nodes', replace: true, resetScroll: false })} />
}

export function NodeNewPage() {
  const navigate = useNavigate()
  return (
    <NodesListView
      modal="new"
      onCloseModal={() => navigate({ to: '/nodes', replace: true, resetScroll: false })}
    />
  )
}

export function NodeDetailPage({ nodeId }: { nodeId: string }) {
  const navigate = useNavigate()
  return (
    <NodesListView
      modal="detail"
      nodeId={nodeId}
      onCloseModal={() => navigate({ to: '/nodes', replace: true, resetScroll: false })}
    />
  )
}

function NodesListView({
  modal,
  nodeId,
  onCloseModal,
}: {
  modal?: 'new' | 'detail'
  nodeId?: string
  onCloseModal: () => void
}) {
  const query = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const controllerInfo = useQuery({
    queryKey: ['controller-info'],
    queryFn: () => api<ControllerInfo>('/api/controller/info'),
  })
  const queryClient = useQueryClient()
  const updateNode = useMutation({
    mutationFn: (id: string) => post<NodeInfo>(`/api/nodes/${id}/update`, {}),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
    },
  })
  const updateAll = useMutation({
    mutationFn: () => post('/api/nodes/update', {}),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
    },
  })
  const nodes = useMemo(() => (query.data ?? []).filter((node) => !node.revoked), [query.data])
  const [filters, setFilters] = useSessionState<NodeFilters>('nodes.filters', emptyNodeFilters, isNodeFilters)
  const filteredNodes = useMemo(() => {
    const needle = filters.query.trim().toLowerCase()
    return nodes.filter((node) => {
      if (filters.status !== 'all' && node.status !== filters.status) return false
      if (!needle) return true
      const searchable = [
        node.name,
        node.id,
        node.version,
        node.public_host,
        node.system?.hostname,
        node.system?.ip,
        ...Object.entries(node.labels ?? {}).flat(),
      ].filter(Boolean).join(' ').toLowerCase()
      return searchable.includes(needle)
    })
  }, [filters, nodes])
  const hasActiveFilters = Boolean(filters.query.trim() || filters.status !== 'all')
  const release = controllerInfo.data?.node_release

  return (
    <PageFrame
      title="节点"
      subtitle="节点只接收结构化配置，不提供远程 shell。"
      action={
        <InlineActions>
          <button
            className="ghost"
            type="button"
            onClick={() => updateAll.mutate()}
            disabled={!release?.update_enabled || updateAll.isPending}
          >
            <RefreshCw size={16} />
            批量更新
          </button>
          <Link to="/nodes/new" className="button-link" resetScroll={false}>
            <Plus size={16} />
            添加节点
          </Link>
        </InlineActions>
      }
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {updateNode.error && <Banner text={updateNode.error instanceof Error ? updateNode.error.message : '更新下发失败'} />}
      {updateAll.error && <Banner text={updateAll.error instanceof Error ? updateAll.error.message : '批量更新失败'} />}
      {query.isLoading ? (
        <LoadingState label="正在加载节点" />
      ) : query.error ? null : nodes.length === 0 ? (
        <EmptyState
          title="还没有节点"
          text="先添加节点，然后让 node 服务主动连到控制器。"
          action={
            <Link to="/nodes/new" className="button-link" resetScroll={false}>
              <Plus size={16} />
              添加节点
            </Link>
          }
        />
      ) : (
        <>
          <div className="list-filters list-filters-compact" role="search" aria-label="节点筛选">
            <Field label="搜索节点">
              <input
                value={filters.query}
                onChange={(event) => setFilters((current) => ({ ...current, query: event.target.value }))}
                placeholder="名称、ID、地址或标签"
              />
            </Field>
            <Field label="状态">
              <select
                value={filters.status}
                onChange={(event) => setFilters((current) => ({ ...current, status: event.target.value as NodeFilters['status'] }))}
              >
                <option value="all">全部状态</option>
                <option value="online">在线</option>
                <option value="offline">离线</option>
              </select>
            </Field>
            <button className="ghost" type="button" onClick={() => setFilters(emptyNodeFilters())} disabled={!hasActiveFilters}>
              <RefreshCw size={16} />
              重置
            </button>
          </div>
          <div className="list-summary" aria-live="polite">
            <span>{hasActiveFilters ? `显示 ${filteredNodes.length} / ${nodes.length} 个节点` : `共 ${nodes.length} 个节点`}</span>
            {hasActiveFilters && (
              <button className="text-button" type="button" onClick={() => setFilters(emptyNodeFilters())}>
                清除筛选
              </button>
            )}
          </div>
          {filteredNodes.length === 0 ? (
            <EmptyState title="没有匹配的节点" text="调整搜索词或状态筛选后再查看。" />
          ) : (
            <SortableTable
              items={filteredNodes}
              columns={nodeColumns}
              getRowKey={(node) => node.id}
              defaultSortKey="name"
              storageKey="nodes"
            >
              {(node) => (
                <tr key={node.id}>
                  <td>
                    <strong>
                      <Link to="/nodes/$nodeId" params={{ nodeId: node.id }} resetScroll={false}>
                        {node.name}
                      </Link>
                    </strong>
                    <small>{node.id}</small>
                  </td>
                  <td><StatusPill value={node.revoked ? 'revoked' : node.status} /></td>
                  <td>
                    {node.version || '-'}
                    {nodeUpdateSummary(node)}
                    {release?.update_enabled && canUpdateNode(node, release) ? <small>可更新到 {release.manifest.version}</small> : null}
                  </td>
                  <td>{[node.system?.os, node.system?.arch].filter(Boolean).join('/') || '-'}</td>
                  <td>{formatTime(node.last_seen)}</td>
                  <td>
                    <InlineActions>
                      {release?.update_enabled && canUpdateNode(node, release) ? (
                        <button
                          className="ghost"
                          type="button"
                          onClick={() => updateNode.mutate(node.id)}
                          disabled={updateNode.isPending}
                        >
                          <RefreshCw size={16} />
                          更新
                        </button>
                      ) : null}
                      <Link to="/nodes/$nodeId" params={{ nodeId: node.id }} className="button-link ghost" resetScroll={false}>
                        详情
                      </Link>
                    </InlineActions>
                  </td>
                </tr>
              )}
            </SortableTable>
          )}
        </>
      )}
      {modal === 'new' && <NodeCreateModal onClose={onCloseModal} />}
      {modal === 'detail' && nodeId && <NodeDetailModal nodeId={nodeId} onClose={onCloseModal} />}
    </PageFrame>
  )
}

function NodeCreateModal({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const confirmAction = useConfirm()
  const [form, setForm] = useState<NodeForm>(emptyForm)
  const [editorDirty, setEditorDirty] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<NodeInstallInfo | null>(null)
  const handleClose = () => {
    if (!editorDirty) {
      onClose()
      return
    }
    void confirmAction({
      title: '放弃节点修改？',
      description: '当前节点还有未保存的修改，关闭后这些内容不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) onClose()
    })
  }

  const create = useMutation({
    mutationFn: () =>
      post<NodeInstallInfo>('/api/nodes', {
        ...nodePayload(form),
      }),
    onSuccess: async (created) => {
      setEditorDirty(false)
      setResult(created)
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
    },
    onError: (err) => setError(err instanceof Error ? err.message : '创建失败'),
  })

  return (
    <Modal
      title="添加节点"
      subtitle={result ? '节点凭据已生成，请在目标机器执行安装命令。' : '填写基础信息后生成节点凭据和安装命令。'}
      onClose={handleClose}
      size="lg"
    >
      {result ? (
        <NodeInstallContent result={result} />
      ) : (
        <NodeForm
          form={form}
          error={error}
          submitLabel="生成节点凭据"
          pending={create.isPending}
          onChange={(next) => {
            setEditorDirty(true)
            setForm(next)
          }}
          onSubmit={() => {
            setError('')
            create.mutate()
          }}
        />
      )}
    </Modal>
  )
}

function NodeDetailModal({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const confirmAction = useConfirm()
  const [mode, setMode] = useState<'details' | 'edit'>('details')
  const [editorDirty, setEditorDirty] = useState(false)
  const [installOpen, setInstallOpen] = useState(false)
  const [editForm, setEditForm] = useState<NodeForm>(emptyForm)
  const [message, setMessage] = useState('')
  const nodeQuery = useQuery({
    queryKey: ['node', nodeId],
    queryFn: () => api<NodeInfo>(`/api/nodes/${nodeId}`),
  })
  const controllerInfo = useQuery({
    queryKey: ['controller-info'],
    queryFn: () => api<ControllerInfo>('/api/controller/info'),
  })
  const installQuery = useQuery({
    queryKey: ['node-install', nodeId],
    queryFn: () => api<NodeInstallInfo>(`/api/nodes/${nodeId}/install`),
    enabled: false,
  })
  const revoke = useMutation({
    mutationFn: () => post('/api/nodes/revoke', { id: nodeId }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
      await queryClient.invalidateQueries({ queryKey: ['node', nodeId] })
      await queryClient.invalidateQueries({ queryKey: ['node-install', nodeId] })
      navigate({ to: '/nodes', replace: true, resetScroll: false })
    },
  })
  const update = useMutation({
    mutationFn: () =>
      api<NodeInfo>(`/api/nodes/${nodeId}`, {
        method: 'PATCH',
        body: JSON.stringify(nodePayload(editForm)),
      }),
    onSuccess: async () => {
      setMessage('已保存')
      setEditorDirty(false)
      setMode('details')
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
      await queryClient.invalidateQueries({ queryKey: ['node', nodeId] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '保存失败'),
  })
  const updateBinary = useMutation({
    mutationFn: () => post<NodeInfo>(`/api/nodes/${nodeId}/update`, {}),
    onSuccess: async () => {
      setMessage('已下发更新请求')
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
      await queryClient.invalidateQueries({ queryKey: ['node', nodeId] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '更新下发失败'),
  })

  const node = nodeQuery.data
  const release = controllerInfo.data?.node_release

  useEffect(() => {
    if (!node || editorDirty) return
    setEditForm({
      name: node.name,
      labels: JSON.stringify(node.labels ?? {}, null, 2),
      public_host: node.public_host ?? '',
      port_min: String(node.port_min ?? 10000),
      port_max: String(node.port_max ?? 65535),
    })
    setEditorDirty(false)
  }, [editorDirty, node])

  const handleClose = () => {
    if (mode !== 'edit' || !editorDirty) {
      onClose()
      return
    }
    void confirmAction({
      title: '放弃节点修改？',
      description: '当前节点还有未保存的修改，关闭后这些内容不会保留。',
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
      title: '放弃节点修改？',
      description: '返回详情后，当前未保存的修改不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) setMode('details')
    })
  }

  return (
    <Modal
      title={node?.name || '节点详情'}
      subtitle="查看节点状态、系统信息和节点配置。"
      onClose={handleClose}
      size="lg"
      action={node ? (
        <InlineActions>
          <button className="ghost" type="button" onClick={toggleMode}>
            <Settings size={16} />
            {mode === 'edit' ? '查看详情' : '编辑'}
          </button>
          <button
            className="ghost"
            type="button"
            onClick={async () => {
              setInstallOpen(true)
              await installQuery.refetch()
            }}
            disabled={installQuery.isFetching || editorDirty}
          >
            <Download size={16} />
            安装命令
          </button>
          {release?.update_enabled && canUpdateNode(node, release) ? (
            <button className="ghost" type="button" onClick={() => updateBinary.mutate()} disabled={updateBinary.isPending || editorDirty}>
              <RefreshCw size={16} />
              更新版本
            </button>
          ) : null}
          <button
            className="ghost danger"
            type="button"
            onClick={() => void confirmAction({
              title: '吊销节点？',
              description: `节点“${node.name}”将无法继续连接控制器。`,
              confirmLabel: '吊销节点',
              tone: 'danger',
            }).then((confirmed) => {
              if (confirmed) revoke.mutate()
            })}
            disabled={revoke.isPending || editorDirty}
          >
            <Trash2 size={16} />
            吊销
          </button>
        </InlineActions>
      ) : undefined}
    >
      {nodeQuery.error && <Banner text={nodeQuery.error instanceof Error ? nodeQuery.error.message : '加载失败'} />}
      {node ? (
        <>
          {mode === 'edit' ? (
            <NodeForm
              form={editForm}
              error={update.error instanceof Error ? update.error.message : ''}
              submitLabel="保存节点设置"
              pending={update.isPending}
              onChange={(next) => {
                setEditorDirty(true)
                setEditForm(next)
              }}
              onSubmit={() => {
                setMessage('')
                update.mutate()
              }}
              publicHostPlaceholder={node.system?.ip || 'hk.example.com'}
            />
          ) : (
            <NodeDetailsContent node={node} />
          )}
          {message && <p className="modal-message">{message}</p>}
          {installOpen ? (
            <section className="modal-section">
              <div className="modal-section-header">
                <h3>节点安装命令</h3>
                <button className="ghost" type="button" onClick={() => setInstallOpen(false)}>收起</button>
              </div>
              <NodeInstallContent
                result={installQuery.data ?? null}
                loading={installQuery.isFetching}
                error={installQuery.error instanceof Error ? installQuery.error.message : ''}
              />
            </section>
          ) : null}
        </>
      ) : null}
    </Modal>
  )
}

function NodeForm({
  form,
  error,
  submitLabel,
  pending,
  onChange,
  onSubmit,
  publicHostPlaceholder = 'hk.example.com',
}: {
  form: NodeForm
  error?: string
  submitLabel: string
  pending: boolean
  onChange: (form: NodeForm) => void
  onSubmit: () => void
  publicHostPlaceholder?: string
}) {
  return (
    <form
      className="form"
      onSubmit={(event) => {
        event.preventDefault()
        onSubmit()
      }}
    >
      <NodeFormFields form={form} onChange={onChange} publicHostPlaceholder={publicHostPlaceholder} />
      {error && <p className="error">{error}</p>}
      <FormActions>
        <button type="submit" disabled={pending}>{submitLabel}</button>
      </FormActions>
    </form>
  )
}

function NodeFormFields({
  form,
  onChange,
  publicHostPlaceholder,
}: {
  form: NodeForm
  onChange: (form: NodeForm) => void
  publicHostPlaceholder: string
}) {
  return (
    <FieldGrid>
      <Field label="节点名称">
        <input
          value={form.name}
          onChange={(event) => onChange({ ...form, name: event.target.value })}
          placeholder="hk-1"
        />
      </Field>
      <Field label="节点 IP / 域名" hint="留空时会优先使用节点上报的地址。">
        <input
          value={form.public_host}
          onChange={(event) => onChange({ ...form, public_host: event.target.value })}
          placeholder={publicHostPlaceholder}
        />
      </Field>
      <Field label="可用端口起始">
        <input
          type="number"
          min={1}
          max={65535}
          value={form.port_min}
          onChange={(event) => onChange({ ...form, port_min: event.target.value })}
        />
      </Field>
      <Field label="可用端口结束">
        <input
          type="number"
          min={1}
          max={65535}
          value={form.port_max}
          onChange={(event) => onChange({ ...form, port_max: event.target.value })}
        />
      </Field>
      <Field label="标签 JSON" wide hint="只会保存字符串值，保存前会验证 JSON 对象格式。">
        <textarea
          className="text-area"
          rows={6}
          value={form.labels}
          onChange={(event) => onChange({ ...form, labels: event.target.value })}
        />
      </Field>
    </FieldGrid>
  )
}

function NodeDetailsContent({ node }: { node: NodeInfo }) {
  return (
    <>
      <section className="modal-section">
        <h3>基础信息</h3>
        <DetailGrid
          items={[
            { label: 'ID', value: node.id },
            { label: '状态', value: <StatusPill value={node.revoked ? 'revoked' : node.status} /> },
            { label: '版本', value: node.version || '-' },
            { label: '目标版本', value: node.desired_version || '-' },
            { label: '更新状态', value: updateStatusLabel(node) },
            { label: '更新错误', value: node.update_error || '-' },
            { label: '节点入口', value: publicEndpoint(node) },
            { label: '可用端口范围', value: `${node.port_min ?? 10000}-${node.port_max ?? 65535}` },
            { label: '最近心跳', value: formatTime(node.last_seen) },
            { label: '创建时间', value: formatTime(node.created_at) },
            { label: '更新时间', value: formatTime(node.updated_at) },
          ]}
        />
      </section>
      <section className="modal-section">
        <h3>系统信息</h3>
        <DetailGrid
          items={[
            { label: '主机名', value: node.system?.hostname || '-' },
            { label: '系统', value: [node.system?.os, node.system?.arch].filter(Boolean).join('/') || '-' },
            { label: '地址', value: node.system?.ip || '-' },
            { label: '标签', value: renderLabels(node.labels) },
          ]}
        />
      </section>
    </>
  )
}

function NodeInstallContent({
  result,
  loading = false,
  error = '',
}: {
  result: NodeInstallInfo | null
  loading?: boolean
  error?: string
}) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'error'>('idle')
  const copyTimer = useRef<number | undefined>(undefined)

  useEffect(() => () => {
    if (copyTimer.current !== undefined) window.clearTimeout(copyTimer.current)
  }, [])

  if (!result) {
    return loading ? (
      <Panel>
        <p>正在加载安装命令。</p>
      </Panel>
    ) : error ? (
      <Panel>
        <p className="error">{error}</p>
      </Panel>
    ) : null
  }
  const command = result.command || ''
  const copyCommand = async () => {
    try {
      await copyText(command)
      setCopyState('copied')
      if (copyTimer.current !== undefined) window.clearTimeout(copyTimer.current)
      copyTimer.current = window.setTimeout(() => setCopyState('idle'), 1800)
    } catch {
      setCopyState('error')
    }
  }
  return (
    <Panel>
      <h3>节点安装命令</h3>
      <pre>{command}</pre>
      <DetailGrid
        items={[
          { label: '安装脚本', value: <code>{result.script_url}</code> },
          { label: '节点二进制', value: <code>{result.binary_url}</code> },
        ]}
      />
      <div className="actions">
        <a className="button-link ghost" href={result.script_url} target="_blank" rel="noreferrer">
          <Download size={16} />
          下载脚本
        </a>
        <button className="ghost" type="button" onClick={() => { void copyCommand() }}>
          <ClipboardCopy size={16} />
          {copyState === 'copied' ? '已复制' : copyState === 'error' ? '复制失败' : '复制命令'}
        </button>
        <Link to="/nodes/$nodeId" params={{ nodeId: result.node.id }} className="button-link" resetScroll={false}>
          节点详情
        </Link>
      </div>
    </Panel>
  )
}

async function copyText(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  document.execCommand('copy')
  document.body.removeChild(textarea)
}

function renderLabels(labels?: Record<string, string>) {
  const entries = Object.entries(labels ?? {})
  if (entries.length === 0) return '-'
  return (
    <div className="chip-list">
      {entries.map(([key, value]) => (
        <span className="chip" key={key}>
          {key}={value}
        </span>
      ))}
    </div>
  )
}

function parseLabels(raw: string) {
  const trimmed = raw.trim()
  if (!trimmed) return {}
  let value: unknown
  try {
    value = JSON.parse(trimmed) as unknown
  } catch {
    throw new Error('标签 JSON 无效')
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('标签必须是对象')
  }
  const out: Record<string, string> = {}
  for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
    if (typeof entry === 'string') {
      out[key] = entry
    }
  }
  return out
}

function nodePayload(form: NodeForm) {
  return {
    name: form.name,
    labels: parseLabels(form.labels),
    public_host: form.public_host.trim(),
    port_min: parsePort(form.port_min, 10000),
    port_max: parsePort(form.port_max, 65535),
  }
}

function parsePort(value: string, fallback: number) {
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) ? parsed : fallback
}

function canUpdateNode(node: NodeInfo, release?: SignedNodeRelease) {
  return Boolean(release?.update_enabled && versionCompare(node.version || '', release.manifest.version) < 0)
}

function nodeUpdateSummary(node: NodeInfo) {
  const label = updateStatusLabel(node)
  return label === '-' ? null : <small>{label}</small>
}

function updateStatusLabel(node: NodeInfo) {
  switch (node.update_status) {
    case 'requested':
      return `等待更新到 ${node.desired_version || '-'}`
    case 'running':
      return `正在更新到 ${node.desired_version || '-'}`
    case 'succeeded':
      return `已更新到 ${node.desired_version || node.version || '-'}`
    case 'failed':
      return node.update_error ? `更新失败：${node.update_error}` : '更新失败'
    default:
      return '-'
  }
}

function versionCompare(left: string, right: string) {
  const a = versionParts(left)
  const b = versionParts(right)
  for (let index = 0; index < Math.max(a.length, b.length); index += 1) {
    const av = a[index] ?? 0
    const bv = b[index] ?? 0
    if (av < bv) return -1
    if (av > bv) return 1
  }
  return 0
}

function versionParts(value: string) {
  const core = value
    .trim()
    .replace(/^v/, '')
    .split(/[+-]/)[0]
  return core
    .split('.')
    .map((part) => Number.parseInt(part, 10) || 0)
}

function publicEndpoint(node: NodeInfo) {
  const host = node.public_host || node.system?.ip || '-'
  return host === '-' ? '-' : `${host}:${node.port_min ?? 10000}-${node.port_max ?? 65535}`
}
