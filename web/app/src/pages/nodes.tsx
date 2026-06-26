import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useState } from 'react'
import { api, post } from '../api'
import type { ControllerInfo, NodeInfo } from '../types'
import {
  Banner,
  DetailGrid,
  EmptyState,
  Field,
  FieldGrid,
  PageFrame,
  Panel,
  StatusPill,
  Table,
  formatTime,
} from '../components/ui'

type NodeForm = {
  name: string
  labels: string
}

const emptyForm: NodeForm = { name: '', labels: '{}' }

export function NodesPage() {
  const query = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })

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
      {(query.data ?? []).length === 0 ? (
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
          {(query.data ?? []).map((node) => (
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
  const controller = useQuery({
    queryKey: ['controller-info'],
    queryFn: () => api<ControllerInfo>('/api/controller/info'),
  })
  const [form, setForm] = useState<NodeForm>(emptyForm)
  const [error, setError] = useState('')
  const [result, setResult] = useState<{ node: NodeInfo; token: string } | null>(null)

  const create = useMutation({
    mutationFn: () =>
      post<{ node: NodeInfo; token: string }>('/api/nodes', {
        name: form.name,
        labels: parseLabels(form.labels),
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
          <Field label="标签 JSON" wide>
            <textarea
              className="text-area"
              value={form.labels}
              onChange={(event) => setForm((current) => ({ ...current, labels: event.target.value }))}
              rows={6}
            />
          </Field>
        </FieldGrid>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={create.isPending}>
          生成节点凭据
        </button>
      </form>
      <NodeLaunchInfo
        controllerUrl={controller.data?.public_url}
        signingKey={controller.data?.signing_key}
        result={result}
      />
    </PageFrame>
  )
}

export function NodeDetailPage({ nodeId }: { nodeId: string }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const nodeQuery = useQuery({
    queryKey: ['node', nodeId],
    queryFn: () => api<NodeInfo>(`/api/nodes/${nodeId}`),
  })
  const revoke = useMutation({
    mutationFn: () => post('/api/nodes/revoke', { id: nodeId }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['nodes'] })
      await queryClient.invalidateQueries({ queryKey: ['node', nodeId] })
      navigate({ to: '/nodes', replace: true })
    },
  })

  const node = nodeQuery.data

  return (
    <PageFrame
      title={node?.name || '节点详情'}
      subtitle="查看节点状态、系统信息和吊销操作。"
      action={
        <button className="ghost danger" onClick={() => revoke.mutate()} disabled={revoke.isPending}>
          吊销节点
        </button>
      }
    >
      {nodeQuery.error && <Banner text={nodeQuery.error instanceof Error ? nodeQuery.error.message : '加载失败'} />}
      {node ? (
        <>
          <Panel>
            <DetailGrid
              items={[
                { label: 'ID', value: node.id },
                { label: '状态', value: <StatusPill value={node.revoked ? 'revoked' : node.status} /> },
                { label: '版本', value: node.version || '-' },
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
        </>
      ) : null}
    </PageFrame>
  )
}

function NodeLaunchInfo({
  controllerUrl,
  signingKey,
  result,
}: {
  controllerUrl?: string
  signingKey?: string
  result?: { node: NodeInfo; token: string } | null
}) {
  return result ? (
    <Panel>
      <h2>systemd 节点启动参数</h2>
      <pre>{`nyarelay-node --controller ${controllerUrl || 'https://your-domain.example'} --id ${result.node.id} --token ${result.token} --signing-key ${signingKey || '<controller-public-key>'}`}</pre>
      <div className="actions">
        <Link to="/nodes/$nodeId" params={{ nodeId: result.node.id }} className="button-link">
          节点详情
        </Link>
        <Link to="/nodes" className="button-link ghost">
          回到节点列表
        </Link>
      </div>
    </Panel>
  ) : (
    <Panel>
      <h2>部署提示</h2>
      <p>添加节点后，控制器会返回一次性的节点 token 和启动命令。</p>
    </Panel>
  )
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
