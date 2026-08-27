import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowDown, ArrowUp, Pause, Play, Plus, RotateCcw, Settings, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { api, post } from '../api'
import type { Dashboard, ForwardInfo, ForwardProtocol, ForwardTargetInfo, NodeInfo, TunnelInfo } from '../types'
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

type ProtocolMode = 'tcp' | 'udp' | 'tcp_udp'
type SelectionStrategy = 'single' | 'failover' | 'round_robin' | 'random'

type ForwardTargetForm = {
  clientID: string
  id: string
  address: string
  protocols: ForwardProtocol[]
  weight: number
  enabled: boolean
}

type ForwardForm = {
  id: string
  name: string
  tunnel_id: string
  protocol_mode: ProtocolMode
  listen: string
  tcp_strategy: SelectionStrategy
  udp_strategy: SelectionStrategy
  targets: ForwardTargetForm[]
  enabled: boolean
}

type ForwardFilters = {
  name: string
  tunnelID: string
  entryAddress: string
  targetAddress: string
}

function isForwardFilters(value: unknown): value is ForwardFilters {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Record<string, unknown>
  return ['name', 'tunnelID', 'entryAddress', 'targetAddress'].every((key) => typeof candidate[key] === 'string')
}

const emptyForwardForm = (): ForwardForm => ({
  id: '',
  name: '',
  tunnel_id: '',
  protocol_mode: 'tcp_udp',
  listen: '',
  tcp_strategy: 'single',
  udp_strategy: 'single',
  targets: [emptyTargetForm()],
  enabled: true,
})

function emptyTargetForm(): ForwardTargetForm {
  return {
    clientID: newFormClientID('target'),
    id: '',
    address: '',
    protocols: [],
    weight: 1,
    enabled: true,
  }
}

let nextFormClientID = 0

function newFormClientID(prefix: string) {
  nextFormClientID += 1
  return `${prefix}-${nextFormClientID}`
}

const emptyForwardFilters = (): ForwardFilters => ({
  name: '',
  tunnelID: '',
  entryAddress: '',
  targetAddress: '',
})

export function ForwardsPage() {
  const navigate = useNavigate()
  return <ForwardsListView onCloseModal={() => navigate({ to: '/forwards', replace: true, resetScroll: false })} />
}

export function ForwardNewPage() {
  const navigate = useNavigate()
  return (
    <ForwardsListView
      modal="new"
      onCloseModal={() => navigate({ to: '/forwards', replace: true, resetScroll: false })}
    />
  )
}

export function ForwardDetailPage({ forwardId }: { forwardId: string }) {
  const navigate = useNavigate()
  return (
    <ForwardsListView
      modal="detail"
      forwardId={forwardId}
      onCloseModal={() => navigate({ to: '/forwards', replace: true, resetScroll: false })}
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
  const [filters, setFilters] = useSessionState<ForwardFilters>('forwards.filters', emptyForwardFilters, isForwardFilters)
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
    const targetNeedle = filters.targetAddress.trim().toLowerCase()

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
      if (targetNeedle && !forwardTargets(forward).some((target) => target.address.toLowerCase().includes(targetNeedle))) {
        return false
      }
      return true
    })
  }, [filters, forwards, nodeMap, tunnelMap])
  const forwardColumns = [
    { key: 'name', label: '名称', getValue: (forward: ForwardInfo) => forward.name },
    { key: 'protocols', label: '协议', getValue: (forward: ForwardInfo) => formatProtocols(forward.protocols) },
    { key: 'tunnel', label: '隧道', getValue: (forward: ForwardInfo) => tunnelMap.get(forward.tunnel_id)?.name ?? forward.tunnel_id },
    {
      key: 'entry',
      label: '入口地址',
      getValue: (forward: ForwardInfo) => {
        const tunnel = tunnelMap.get(forward.tunnel_id)
        return formatForwardEndpoint(forward, tunnel, nodeMap) + ' ' + listenPortSummary(forward.listen)
      },
    },
    { key: 'target', label: '目标', getValue: (forward: ForwardInfo) => formatTargetSummary(forward) },
    { key: 'status', label: '状态', getValue: (forward: ForwardInfo) => forward.enabled ? 'enabled' : 'disabled' },
  ]
  const hasActiveFilters = Boolean(
    filters.name.trim() || filters.tunnelID || filters.entryAddress.trim() || filters.targetAddress.trim(),
  )

  return (
    <PageFrame
      title="转发"
      subtitle="转发选择一个隧道，设置入口端口、目标池和选择策略。"
      action={
        <Link to="/forwards/new" className="button-link" resetScroll={false}>
          <Plus size={16} />
          新建转发
        </Link>
      }
    >
      {forwardsQuery.error && <Banner text={forwardsQuery.error instanceof Error ? forwardsQuery.error.message : '加载失败'} />}
      {forwardsQuery.isLoading ? (
        <LoadingState label="正在加载转发" />
      ) : forwardsQuery.error ? null : forwards.length === 0 ? (
        <EmptyState
          title="还没有转发"
          text="先创建隧道，再为入口节点分配监听端口。"
          action={
            <Link to="/forwards/new" className="button-link" resetScroll={false}>
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
            <Field label="目标地址">
              <input
                value={filters.targetAddress}
                onChange={(event) => setFilters((current) => ({ ...current, targetAddress: event.target.value }))}
                placeholder="最终目标地址或端口"
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
          <div className="list-summary" aria-live="polite">
            <span>{hasActiveFilters ? `显示 ${filteredForwards.length} / ${forwards.length} 条转发` : `共 ${forwards.length} 条转发`}</span>
            {hasActiveFilters && (
              <button className="text-button" type="button" onClick={() => setFilters(emptyForwardFilters())}>
                清除筛选
              </button>
            )}
          </div>
          {filteredForwards.length === 0 ? (
            <EmptyState title="没有匹配的转发" text="调整名称、隧道、入口地址或目标地址筛选后再查看。" />
          ) : (
            <SortableTable
              items={filteredForwards}
              columns={forwardColumns}
              getRowKey={(forward) => forward.id}
              defaultSortKey="name"
              storageKey="forwards"
            >
              {(forward) => {
                const tunnel = tunnelMap.get(forward.tunnel_id)
                return (
                  <tr key={forward.id}>
                    <td>
                      <strong>
                        <Link to="/forwards/$forwardId" params={{ forwardId: forward.id }} resetScroll={false}>
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
                    <td>{formatTargetSummary(forward)}</td>
                    <td><StatusPill value={forward.enabled ? 'enabled' : 'disabled'} /></td>
                  </tr>
                )
              }}
            </SortableTable>
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
  const confirmAction = useConfirm()
  const [editorDirty, setEditorDirty] = useState(false)
  const handleClose = () => {
    if (!editorDirty) {
      onClose()
      return
    }
    void confirmAction({
      title: '放弃转发修改？',
      description: '当前转发还有未保存的修改，关闭后这些内容不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) onClose()
    })
  }

  return (
    <Modal
      title="新建转发"
      subtitle="选择隧道后，入口节点会按协议启动 TCP、UDP 或同端口双协议监听。"
      onClose={handleClose}
      size="lg"
    >
      <ForwardEditor
        onDirtyChange={setEditorDirty}
        onSaved={async (saved) => {
          setEditorDirty(false)
          await queryClient.invalidateQueries({ queryKey: ['forwards'] })
          navigate({ to: '/forwards/$forwardId', params: { forwardId: saved.id }, replace: true, resetScroll: false })
        }}
      />
    </Modal>
  )
}

function ForwardDetailModal({ forwardId, onClose }: { forwardId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const confirmAction = useConfirm()
  const [mode, setMode] = useState<'details' | 'edit'>('details')
  const [editorDirty, setEditorDirty] = useState(false)
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
      navigate({ to: '/forwards', replace: true, resetScroll: false })
    },
  })
  const handleClose = () => {
    if (mode !== 'edit' || !editorDirty) {
      onClose()
      return
    }
    void confirmAction({
      title: '放弃转发修改？',
      description: '当前转发还有未保存的修改，关闭后这些内容不会保留。',
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
      title: '放弃转发修改？',
      description: '返回详情后，当前未保存的修改不会保留。',
      confirmLabel: '放弃修改',
      tone: 'danger',
    }).then((confirmed) => {
      if (confirmed) setMode('details')
    })
  }

  return (
    <Modal
      title={forward?.name || '转发详情'}
      subtitle="保存后控制器会重新签名并推送相关节点配置。"
      onClose={handleClose}
      size="lg"
      action={forward ? (
        <InlineActions>
          <button className="ghost" type="button" onClick={toggleMode}>
            <Settings size={16} />
            {mode === 'edit' ? '查看详情' : '编辑'}
          </button>
          {forward.enabled ? (
            <button className="ghost" type="button" onClick={() => pause.mutate()} disabled={pause.isPending || editorDirty}>
              <Pause size={16} />
              暂停
            </button>
          ) : (
            <button className="ghost" type="button" onClick={() => resume.mutate()} disabled={resume.isPending || editorDirty}>
              <Play size={16} />
              恢复
            </button>
          )}
          <button
            className="ghost danger"
            type="button"
            onClick={() => void confirmAction({
              title: '删除转发？',
              description: `转发“${forward.name}”将被永久删除，入口监听会停止。`,
              confirmLabel: '删除转发',
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
      {forwardQuery.error && <Banner text={forwardQuery.error instanceof Error ? forwardQuery.error.message : '加载失败'} />}
      {remove.error && <Banner text={remove.error instanceof Error ? remove.error.message : '删除失败'} />}
      {pause.error && <Banner text={pause.error instanceof Error ? pause.error.message : '暂停失败'} />}
      {resume.error && <Banner text={resume.error instanceof Error ? resume.error.message : '恢复失败'} />}
      {forward ? (
        <>
          {mode === 'edit' ? (
            <ForwardEditor
              initialForward={forward}
              onDirtyChange={setEditorDirty}
              onSaved={async () => {
                setMessage('已保存')
                setEditorDirty(false)
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
          { label: '目标地址', value: <TargetDetails targets={forwardTargets(forward)} /> },
          { label: '目标策略', value: formatStrategy(forward.tcp_strategy, forward.udp_strategy, forward.strategy) },
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
  onDirtyChange,
}: {
  initialForward?: ForwardInfo
  onSaved: (saved: ForwardInfo) => void | Promise<void>
  onDirtyChange?: (dirty: boolean) => void
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
  const [dirty, setDirty] = useState(false)
  const updateForm: typeof setForm = (updater) => {
    setDirty(true)
    setForm(updater)
  }

  useEffect(() => {
    if (!initialForward || dirty) return
    setForm({
      id: initialForward.id,
      name: initialForward.name,
      tunnel_id: initialForward.tunnel_id,
      protocol_mode: modeFromProtocols(initialForward.protocols),
      listen: listenInputValue(initialForward.listen),
      tcp_strategy: strategyValue(initialForward.tcp_strategy, initialForward.strategy),
      udp_strategy: strategyValue(initialForward.udp_strategy, initialForward.strategy),
      targets: forwardTargets(initialForward).map((target) => ({
        id: target.id,
        clientID: target.id || newFormClientID('target'),
        address: target.address,
        protocols: target.protocols ?? [],
        weight: Math.max(1, target.weight ?? 1),
        enabled: target.enabled,
      })),
      enabled: initialForward.enabled,
    })
    setDirty(false)
  }, [dirty, initialForward])

  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])

  const save = useMutation({
    mutationFn: () => {
      const payload = {
        id: form.id,
        name: form.name,
        tunnel_id: form.tunnel_id,
        protocols: protocolsFromMode(form.protocol_mode),
        listen: normalizeListenInput(form.listen),
        tcp_strategy: form.tcp_strategy,
        udp_strategy: form.udp_strategy,
        targets: form.targets.map((target, position) => ({
          id: target.id,
          address: target.address,
          protocols: target.protocols,
          weight: target.weight,
          enabled: target.enabled,
          position,
        })),
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
      setDirty(false)
      await onSaved(saved)
    },
    onError: (err) => setError(err instanceof Error ? err.message : '保存失败'),
  })

  const previousTargetCount = useRef(form.targets.length)
  const targetRefs = useRef<Record<string, HTMLDivElement | null>>({})

  useEffect(() => {
    if (form.targets.length > previousTargetCount.current) {
      const newTarget = form.targets[form.targets.length - 1]
      if (newTarget) {
        const scrollToTarget = () => targetRefs.current[newTarget.clientID]?.scrollIntoView?.({ block: 'nearest' })
        if (typeof window.requestAnimationFrame === 'function') window.requestAnimationFrame(scrollToTarget)
        else window.setTimeout(scrollToTarget, 0)
      }
    }
    previousTargetCount.current = form.targets.length
  }, [form.targets.length])

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
        <Field label="协议">
          <select value={form.protocol_mode} onChange={(event) => updateForm((current) => ({ ...current, protocol_mode: event.target.value as ProtocolMode }))}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="tcp_udp">TCP+UDP</option>
          </select>
        </Field>
        <Field label="隧道">
          <select value={form.tunnel_id} onChange={(event) => updateForm((current) => ({ ...current, tunnel_id: event.target.value }))}>
            <option value="">选择隧道</option>
            {tunnels.map((tunnel) => <option key={tunnel.id} value={tunnel.id}>{tunnel.name}</option>)}
          </select>
        </Field>
        <Field label="监听端口" hint="留空自动分配；填写 8443 即监听该端口。">
          <input
            value={form.listen}
            onChange={(event) => updateForm((current) => ({ ...current, listen: event.target.value }))}
            placeholder="8443"
          />
        </Field>
        <Field label="TCP 策略">
          <select
            value={form.tcp_strategy}
            onChange={(event) => updateForm((current) => ({ ...current, tcp_strategy: event.target.value as SelectionStrategy }))}
          >
            {strategyOptions()}
          </select>
        </Field>
        <Field label="UDP 策略">
          <select
            value={form.udp_strategy}
            onChange={(event) => updateForm((current) => ({ ...current, udp_strategy: event.target.value as SelectionStrategy }))}
          >
            {strategyOptions()}
          </select>
        </Field>
        <ToggleField
          label="启用"
          ariaLabel="启用转发"
          description="保存后入口节点会按配置监听。"
          checked={form.enabled}
          onChange={(checked) => updateForm((current) => ({ ...current, enabled: checked }))}
        />
      </FieldGrid>
      <section className="form-section">
        <div className="section-heading">
          <div>
            <h3>目标池</h3>
            <p>TCP 按连接选择，UDP 按会话保持目标粘滞。</p>
          </div>
          <button
            type="button"
            className="ghost"
            onClick={() => updateForm((current) => ({ ...current, targets: [...current.targets, emptyTargetForm()] }))}
          >
            <Plus size={16} />
            添加目标
          </button>
        </div>
        <div className="forward-target-list">
          {form.targets.map((target, index) => (
            <div
              className="hop forward-target-row"
              key={target.clientID}
              ref={(element) => { targetRefs.current[target.clientID] = element }}
            >
              <span>{index + 1}</span>
              <input
                aria-label={index === 0 ? '目标地址' : `目标地址 ${index + 1}`}
                value={target.address}
                onChange={(event) => updateTarget(updateForm, index, { address: event.target.value })}
                placeholder="10.0.0.8:443"
              />
              <label className="candidate-weight">
                <span>权重</span>
                <input
                  aria-label={`目标权重 ${index + 1}`}
                  type="number"
                  min={1}
                  value={String(target.weight)}
                  onChange={(event) => updateTarget(updateForm, index, { weight: Math.max(1, Number(event.target.value) || 1) })}
                />
              </label>
              <div className="candidate-protocols">
                {(['tcp', 'udp'] as const).map((protocol) => (
                  <label key={protocol}>
                    <input
                      type="checkbox"
                      checked={target.protocols.length === 0 || target.protocols.includes(protocol)}
                      onChange={(event) => updateTarget(updateForm, index, {
                        protocols: toggleTargetProtocol(target.protocols, protocol, event.target.checked),
                      })}
                    />
                    {protocol.toUpperCase()}
                  </label>
                ))}
              </div>
              <ToggleField
                label="启用"
                ariaLabel={`启用目标 ${index + 1}`}
                checked={target.enabled}
                onChange={(checked) => updateTarget(updateForm, index, { enabled: checked })}
              />
              <InlineActions>
                <button
                  type="button"
                  className="ghost icon-button"
                  aria-label={`上移目标 ${index + 1}`}
                  title="上移目标"
                  disabled={index === 0}
                  onClick={() => moveTarget(updateForm, index, index - 1)}
                >
                  <ArrowUp size={16} />
                </button>
                <button
                  type="button"
                  className="ghost icon-button"
                  aria-label={`下移目标 ${index + 1}`}
                  title="下移目标"
                  disabled={index === form.targets.length - 1}
                  onClick={() => moveTarget(updateForm, index, index + 1)}
                >
                  <ArrowDown size={16} />
                </button>
                <button
                  type="button"
                  className="ghost danger"
                  disabled={form.targets.length === 1}
                  onClick={() => updateForm((current) => ({
                    ...current,
                    targets: current.targets.filter((_, targetIndex) => targetIndex !== index),
                  }))}
                >
                  <Trash2 size={16} />
                  移除
                </button>
              </InlineActions>
            </div>
          ))}
        </div>
      </section>
      {error && <p className="error">{error}</p>}
      <FormActions>
        <button type="submit" disabled={save.isPending}>保存转发</button>
      </FormActions>
    </form>
  )
}

function strategyOptions() {
  return [
    <option key="single" value="single">单候选</option>,
    <option key="failover" value="failover">故障切换</option>,
    <option key="round_robin" value="round_robin">轮询</option>,
    <option key="random" value="random">随机</option>,
  ]
}

function strategyValue(strategy: string | undefined, fallback?: string): SelectionStrategy {
  switch (strategy?.trim().toLowerCase()) {
    case 'failover':
      return 'failover'
    case 'round_robin':
      return 'round_robin'
    case 'random':
      return 'random'
    case 'single':
      return 'single'
    default:
      return fallback ? strategyValue(fallback) : 'failover'
  }
}

function forwardTargets(forward: ForwardInfo): ForwardTargetInfo[] {
  if (forward.targets?.length) return forward.targets
  if (forward.target) {
    return [{
      id: `legacy:${forward.id}`,
      address: forward.target,
      enabled: true,
      weight: 1,
    }]
  }
  return []
}

function formatTargetSummary(forward: ForwardInfo) {
  const targets = forwardTargets(forward)
  if (targets.length === 0) return '-'
  if (targets.length === 1) return targets[0].address
  return `${targets[0].address} 等 ${targets.length} 个目标`
}

function formatStrategy(tcpStrategy?: string, udpStrategy?: string, legacyStrategy?: string) {
  const fallback = strategyValue(legacyStrategy)
  const tcp = strategyValue(tcpStrategy, fallback)
  const udp = strategyValue(udpStrategy, fallback)
  return `TCP ${tcp} / UDP ${udp}`
}

function TargetDetails({ targets }: { targets: ForwardTargetInfo[] }) {
  if (targets.length === 0) return <span>-</span>
  return (
    <div className="detail-target-list">
      {targets.map((target) => (
        <div key={target.id || target.address}>
          <strong>{target.address}</strong>
          <small>
            {target.enabled ? '启用' : '停用'} · 权重 {target.weight ?? 1}
          </small>
        </div>
      ))}
    </div>
  )
}

function updateTarget(
  setForm: Dispatch<SetStateAction<ForwardForm>>,
  index: number,
  patch: Partial<ForwardTargetForm>,
) {
  setForm((current) => ({
    ...current,
    targets: current.targets.map((target, targetIndex) => (
      targetIndex === index ? { ...target, ...patch } : target
    )),
  }))
}

function moveTarget(
  setForm: Dispatch<SetStateAction<ForwardForm>>,
  from: number,
  to: number,
) {
  setForm((current) => {
    if (to < 0 || to >= current.targets.length) return current
    const targets = [...current.targets]
    const [target] = targets.splice(from, 1)
    targets.splice(to, 0, target)
    return { ...current, targets }
  })
}

function toggleTargetProtocol(protocols: ForwardProtocol[], protocol: ForwardProtocol, enabled: boolean) {
  const next = new Set<ForwardProtocol>(protocols.length === 0 ? ['tcp', 'udp'] : protocols)
  if (enabled) next.add(protocol)
  else next.delete(protocol)
  if (next.size === 2) return []
  return Array.from(next).filter((item): item is ForwardProtocol => item === 'tcp' || item === 'udp')
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
