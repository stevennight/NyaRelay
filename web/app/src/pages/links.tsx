import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, post } from '../api'
import type { LinkInfo, LinkType, NodeInfo } from '../types'
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

type LinkForm = {
  id: string
  name: string
  type: LinkType
  from_node: string
  to_node: string
  bind_addr: string
  public_addr: string
  server_name: string
  enabled: boolean
  settings: Record<string, string>
}

const emptyLinkForm = (): LinkForm => ({
  id: '',
  name: '',
  type: 'mtls',
  from_node: '',
  to_node: '',
  bind_addr: '0.0.0.0:9443',
  public_addr: '',
  server_name: '',
  enabled: true,
  settings: {},
})

export function LinksPage() {
  const query = useQuery({
    queryKey: ['links'],
    queryFn: () => api<LinkInfo[]>('/api/links'),
  })

  return (
    <PageFrame
      title="链路"
      subtitle="节点间链路可以是 direct、tls、mtls 或 ws-tls。"
      action={
        <Link to="/links/new" className="button-link">
          <Plus size={16} />
          新建链路
        </Link>
      }
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {(query.data ?? []).length === 0 ? (
        <EmptyState
          title="还没有链路"
          text="先创建一条节点间链路，再让路由把它串起来。"
          action={
            <Link to="/links/new" className="button-link">
              <Plus size={16} />
              新建链路
            </Link>
          }
        />
      ) : (
        <Table headers={['名称', '类型', '方向', '监听', '公网地址', '状态']}>
          {(query.data ?? []).map((link) => (
            <tr key={link.id}>
              <td>
                <strong>
                  <Link to="/links/$linkId" params={{ linkId: link.id }}>
                    {link.name}
                  </Link>
                </strong>
                <small>{link.id}</small>
              </td>
              <td>{link.type}</td>
              <td>{`${link.from_node} -> ${link.to_node}`}</td>
              <td>{link.bind_addr}</td>
              <td>{link.public_addr}</td>
              <td><StatusPill value={link.enabled ? 'online' : 'offline'} /></td>
            </tr>
          ))}
        </Table>
      )}
    </PageFrame>
  )
}

export function LinkNewPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return (
    <PageFrame
      title="新建链路"
      subtitle="填写来源节点、目标节点和传输类型。"
    >
      <LinkEditor
        initialLink={undefined}
        onSaved={async (saved) => {
          await queryClient.invalidateQueries({ queryKey: ['links'] })
          navigate({ to: '/links/$linkId', params: { linkId: saved.id }, replace: true })
        }}
      />
    </PageFrame>
  )
}

export function LinkDetailPage({ linkId }: { linkId: string }) {
  const queryClient = useQueryClient()
  const [message, setMessage] = useState('')
  const linkQuery = useQuery({
    queryKey: ['link', linkId],
    queryFn: () => api<LinkInfo>(`/api/links/${linkId}`),
  })

  return (
    <PageFrame
      title={linkQuery.data?.name || '链路详情'}
      subtitle="编辑链路时会保留 secret 和证书材料。"
    >
      {linkQuery.error && <Banner text={linkQuery.error instanceof Error ? linkQuery.error.message : '加载失败'} />}
      {linkQuery.data ? (
        <>
          <Panel>
            <DetailGrid
              items={[
                { label: 'ID', value: linkQuery.data.id },
                { label: '类型', value: linkQuery.data.type },
                { label: '方向', value: `${linkQuery.data.from_node} -> ${linkQuery.data.to_node}` },
                { label: '监听', value: linkQuery.data.bind_addr },
                { label: '公网地址', value: linkQuery.data.public_addr },
                { label: 'Server Name', value: linkQuery.data.server_name || '-' },
                { label: '创建时间', value: formatTime(linkQuery.data.created_at) },
                { label: '更新时间', value: formatTime(linkQuery.data.updated_at) },
                { label: '状态', value: <StatusPill value={linkQuery.data.enabled ? 'online' : 'offline'} /> },
              ]}
            />
          </Panel>
          <LinkEditor
            initialLink={linkQuery.data}
            onSaved={async () => {
              setMessage('已保存')
              await queryClient.invalidateQueries({ queryKey: ['links'] })
              await queryClient.invalidateQueries({ queryKey: ['link', linkId] })
            }}
          />
          {message && <Panel><p>{message}</p></Panel>}
        </>
      ) : null}
    </PageFrame>
  )
}

function LinkEditor({
  initialLink,
  onSaved,
}: {
  initialLink?: LinkInfo
  onSaved: (saved: LinkInfo) => void | Promise<void>
}) {
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const [form, setForm] = useState<LinkForm>(emptyLinkForm())
  const [error, setError] = useState('')

  useEffect(() => {
    if (!initialLink) {
      return
    }
    setForm({
      id: initialLink.id,
      name: initialLink.name,
      type: initialLink.type,
      from_node: initialLink.from_node,
      to_node: initialLink.to_node,
      bind_addr: initialLink.bind_addr,
      public_addr: initialLink.public_addr,
      server_name: initialLink.server_name ?? '',
      enabled: initialLink.enabled,
      settings: initialLink.settings ?? {},
    })
  }, [initialLink])

  const save = useMutation({
    mutationFn: async () => {
      const payload = {
        ...form,
        settings: form.settings,
      }
      return post<LinkInfo>('/api/links', payload)
    },
    onSuccess: async (saved) => {
      await onSaved(saved)
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : '保存失败')
    },
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
          <input
            value={form.name}
            onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
          />
        </Field>
        <Field label="类型">
          <select
            value={form.type}
            onChange={(event) => setForm((current) => ({ ...current, type: event.target.value as LinkType }))}
          >
            <option value="direct">直连</option>
            <option value="tls">TLS</option>
            <option value="mtls">mTLS</option>
            <option value="ws-tls">WebSocket TLS</option>
          </select>
        </Field>
        <Field label="来源节点">
          <select
            value={form.from_node}
            onChange={(event) => setForm((current) => ({ ...current, from_node: event.target.value }))}
          >
            <option value="">选择节点</option>
            {(nodesQuery.data ?? []).map((node) => (
              <option key={node.id} value={node.id}>
                {node.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="目标节点">
          <select
            value={form.to_node}
            onChange={(event) => setForm((current) => ({ ...current, to_node: event.target.value }))}
          >
            <option value="">选择节点</option>
            {(nodesQuery.data ?? []).map((node) => (
              <option key={node.id} value={node.id}>
                {node.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="目标节点监听">
          <input
            value={form.bind_addr}
            onChange={(event) => setForm((current) => ({ ...current, bind_addr: event.target.value }))}
          />
        </Field>
        <Field label="来源节点访问地址">
          <input
            value={form.public_addr}
            onChange={(event) => setForm((current) => ({ ...current, public_addr: event.target.value }))}
            placeholder="1.2.3.4:9443"
          />
        </Field>
        <Field label="Server Name" wide>
          <input
            value={form.server_name}
            onChange={(event) => setForm((current) => ({ ...current, server_name: event.target.value }))}
          />
        </Field>
        <Field label="启用" wide>
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))}
          />
        </Field>
      </FieldGrid>
      {error && <p className="error">{error}</p>}
      <button type="submit" disabled={save.isPending}>
        保存链路
      </button>
    </form>
  )
}
