import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ClipboardCopy, Download, Plus } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, post } from '../api'
import type { NodeInfo, NodeInstallInfo } from '../types'
import {
  Banner,
  DetailGrid,
  EmptyState,
  Field,
  FieldGrid,
  FormActions,
  PageFrame,
  Panel,
  StatusPill,
  Table,
  formatTime,
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

export function NodesPage() {
  const query = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const nodes = (query.data ?? []).filter((node) => !node.revoked)

  return (
    <PageFrame
      title="节点"
      subtitle="节点只接收结构化配置，不提供远程 shell。"
      action={
        <Link to="/nodes/new" className="button-link">
          <Plus size={16} />
          添加节点
        </Link>
      }
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {nodes.length === 0 ? (
        <EmptyState
          title="还没有节点"
          text="先添加节点，然后让 node 服务主动连到控制器。"
          action={
            <Link to="/nodes/new" className="button-link">
              <Plus size={16} />
              添加节点
            </Link>
          }
        />
      ) : (
        <Table headers={['名称', '状态', '版本', '系统', '最近心跳', '操作']}>
          {nodes.map((node) => (
            <tr key={node.id}>
              <td>
                <strong>
                  <Link to="/nodes/$nodeId" params={{ nodeId: node.id }}>
                    {node.name}
                  </Link>
                </strong>
                <small>{node.id}</small>
              </td>
              <td><StatusPill value={node.revoked ? 'revoked' : node.status} /></td>
              <td>{node.version || '-'}</td>
              <td>{[node.system?.os, node.system?.arch].filter(Boolean).join('/') || '-'}</td>
              <td>{formatTime(node.last_seen)}</td>
              <td>
                <Link to="/nodes/$nodeId" params={{ nodeId: node.id }} className="button-link ghost">
                  详情
                </Link>
              </td>
            </tr>
          ))}
        </Table>
      )}
    </PageFrame>
  )
}

export function NodeNewPage() {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<NodeForm>(emptyForm)
  const [error, setError] = useState('')
  const [result, setResult] = useState<NodeInstallInfo | null>(null)

  const create = useMutation({
    mutationFn: () =>
      post<NodeInstallInfo>('/api/nodes', {
        ...nodePayload(form),
      }),
    onSuccess: async (created) => {
      setResult(created)
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
    },
    onError: (err) => setError(err instanceof Error ? err.message : '创建失败'),
  })

  return (
    <PageFrame
      title="添加节点"
      subtitle="系统会生成节点凭据和控制器签名公钥。"
    >
      <form
        className="form"
        onSubmit={(event) => {
          event.preventDefault()
          setError('')
          create.mutate()
        }}
      >
        <FieldGrid>
          <Field label="节点名称">
            <input
              value={form.name}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
              placeholder="hk-1"
            />
          </Field>
          <Field label="标签 JSON" wide hint="只会保存字符串值，例如 {&quot;region&quot;:&quot;hk&quot;}。">
            <textarea
              className="text-area"
              value={form.labels}
              onChange={(event) => setForm((current) => ({ ...current, labels: event.target.value }))}
              rows={6}
            />
          </Field>
          <Field label="公开 IP / 域名" hint="留空时会优先使用节点上报的地址。">
            <input
              value={form.public_host}
              onChange={(event) => setForm((current) => ({ ...current, public_host: event.target.value }))}
              placeholder="hk.example.com"
            />
          </Field>
          <Field label="可用端口起始">
            <input
              type="number"
              min={10000}
              max={65535}
              value={form.port_min}
              onChange={(event) => setForm((current) => ({ ...current, port_min: event.target.value }))}
            />
          </Field>
          <Field label="可用端口结束">
            <input
              type="number"
              min={10000}
              max={65535}
              value={form.port_max}
              onChange={(event) => setForm((current) => ({ ...current, port_max: event.target.value }))}
            />
          </Field>
        </FieldGrid>
        {error && <p className="error">{error}</p>}
        <FormActions>
          <button type="submit" disabled={create.isPending}>
            生成节点凭据
          </button>
        </FormActions>
      </form>
      <NodeLaunchInfo
        result={result}
      />
    </PageFrame>
  )
}

export function NodeDetailPage({ nodeId }: { nodeId: string }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [installOpen, setInstallOpen] = useState(false)
  const [editForm, setEditForm] = useState<NodeForm>(emptyForm)
  const [message, setMessage] = useState('')
  const nodeQuery = useQuery({
    queryKey: ['node', nodeId],
    queryFn: () => api<NodeInfo>(`/api/nodes/${nodeId}`),
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
      navigate({ to: '/nodes', replace: true })
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
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
      await queryClient.invalidateQueries({ queryKey: ['node', nodeId] })
    },
    onError: (err) => setMessage(err instanceof Error ? err.message : '保存失败'),
  })

  const node = nodeQuery.data

  useEffect(() => {
    if (!node) return
    setEditForm({
      name: node.name,
      labels: JSON.stringify(node.labels ?? {}, null, 2),
      public_host: node.public_host ?? '',
      port_min: String(node.port_min ?? 10000),
      port_max: String(node.port_max ?? 65535),
    })
  }, [node])

  return (
    <PageFrame
      title={node?.name || '节点详情'}
      subtitle="查看节点状态、系统信息和吊销操作。"
      action={
        <div className="actions">
          <button
            className="ghost"
            type="button"
            onClick={async () => {
              setInstallOpen(true)
              await installQuery.refetch()
            }}
            disabled={installQuery.isFetching}
          >
            <Download size={16} />
            显示安装命令
          </button>
          <button className="ghost danger" type="button" onClick={() => revoke.mutate()} disabled={revoke.isPending}>
            吊销节点
          </button>
        </div>
      }
    >
      {nodeQuery.error && <Banner text={nodeQuery.error instanceof Error ? nodeQuery.error.message : '加载失败'} />}
      {node ? (
        <>
          {installOpen ? (
            <NodeInstallPanel
              result={installQuery.data ?? null}
              loading={installQuery.isFetching}
              error={installQuery.error instanceof Error ? installQuery.error.message : ''}
            />
          ) : null}
          <Panel>
            <DetailGrid
              items={[
                { label: 'ID', value: node.id },
                { label: '状态', value: <StatusPill value={node.revoked ? 'revoked' : node.status} /> },
                { label: '版本', value: node.version || '-' },
                { label: '公开入口', value: publicEndpoint(node) },
                { label: '可用端口范围', value: `${node.port_min ?? 10000}-${node.port_max ?? 65535}` },
                { label: '最近心跳', value: formatTime(node.last_seen) },
                { label: '创建时间', value: formatTime(node.created_at) },
                { label: '更新时间', value: formatTime(node.updated_at) },
              ]}
            />
          </Panel>
          <Panel>
            <h2>系统信息</h2>
            <DetailGrid
              items={[
                { label: '主机名', value: node.system?.hostname || '-' },
                { label: '系统', value: [node.system?.os, node.system?.arch].filter(Boolean).join('/') || '-' },
                { label: '地址', value: node.system?.ip || '-' },
                { label: '标签', value: renderLabels(node.labels) },
              ]}
            />
          </Panel>
          <Panel>
            <h2>节点设置</h2>
            <form
              className="form"
              onSubmit={(event) => {
                event.preventDefault()
                setMessage('')
                update.mutate()
              }}
            >
              <FieldGrid>
                <Field label="节点名称">
                  <input
                    value={editForm.name}
                    onChange={(event) => setEditForm((current) => ({ ...current, name: event.target.value }))}
                  />
                </Field>
                <Field label="公开 IP / 域名" hint="留空时会使用节点上报的地址。">
                  <input
                    value={editForm.public_host}
                    onChange={(event) => setEditForm((current) => ({ ...current, public_host: event.target.value }))}
                    placeholder={node.system?.ip || 'hk.example.com'}
                  />
                </Field>
                <Field label="可用端口起始">
                  <input
                    type="number"
                    min={10000}
                    max={65535}
                    value={editForm.port_min}
                    onChange={(event) => setEditForm((current) => ({ ...current, port_min: event.target.value }))}
                  />
                </Field>
                <Field label="可用端口结束">
                  <input
                    type="number"
                    min={10000}
                    max={65535}
                    value={editForm.port_max}
                    onChange={(event) => setEditForm((current) => ({ ...current, port_max: event.target.value }))}
                  />
                </Field>
                <Field label="标签 JSON" wide hint="保存前会验证 JSON 对象格式。">
                  <textarea
                    className="text-area"
                    rows={6}
                    value={editForm.labels}
                    onChange={(event) => setEditForm((current) => ({ ...current, labels: event.target.value }))}
                  />
                </Field>
              </FieldGrid>
              <FormActions>
                <button type="submit" disabled={update.isPending}>保存节点设置</button>
              </FormActions>
            </form>
            {message && <p>{message}</p>}
          </Panel>
        </>
      ) : null}
    </PageFrame>
  )
}

function NodeLaunchInfo({
  result,
}: {
  result?: NodeInstallInfo | null
}) {
  return result ? (
    <NodeInstallPanel result={result} />
  ) : (
    <Panel>
      <h2>部署提示</h2>
      <p>添加节点后，控制器会返回一次性的安装命令和下载脚本入口。</p>
    </Panel>
  )
}

function NodeInstallPanel({
  result,
  loading = false,
  error = '',
}: {
  result: NodeInstallInfo | null
  loading?: boolean
  error?: string
}) {
  if (!result) {
    return loading ? (
      <Panel>
        <h2>节点安装命令</h2>
        <p>正在加载安装命令。</p>
      </Panel>
    ) : error ? (
      <Panel>
        <h2>节点安装命令</h2>
        <p className="error">{error}</p>
      </Panel>
    ) : null
  }
  const command = result.command || ''
  return (
    <Panel>
      <h2>节点安装命令</h2>
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
        <button className="ghost" type="button" onClick={() => { void copyText(command) }}>
          <ClipboardCopy size={16} />
          复制命令
        </button>
        <Link to="/nodes/$nodeId" params={{ nodeId: result.node.id }} className="button-link">
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
  try {
    const value = JSON.parse(trimmed) as unknown
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
  } catch (err) {
    throw new Error(err instanceof Error ? err.message : '标签 JSON 无效')
  }
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

function publicEndpoint(node: NodeInfo) {
  const host = node.public_host || node.system?.ip || '-'
  return host === '-' ? '-' : `${host}:${node.port_min ?? 10000}-${node.port_max ?? 65535}`
}
