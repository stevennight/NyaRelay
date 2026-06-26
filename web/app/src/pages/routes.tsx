import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { api, post } from '../api'
import type { LinkInfo, NodeInfo, RouteInfo, RouteProtocol } from '../types'
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

type RouteForm = {
  id: string
  name: string
  protocol: RouteProtocol
  entry_node: string
  listen: string
  target: string
  enabled: boolean
}

const emptyRouteForm = (): RouteForm => ({
  id: '',
  name: '',
  protocol: 'tcp',
  entry_node: '',
  listen: '0.0.0.0:8443',
  target: '',
  enabled: true,
})

export function RoutesPage() {
  const query = useQuery({
    queryKey: ['routes'],
    queryFn: () => api<RouteInfo[]>('/api/routes'),
  })

  return (
    <PageFrame
      title="路由"
      subtitle="单节点直入直出和多跳链路都从这里编排。"
      action={
        <Link to="/routes/new" className="button-link">
          <Plus size={16} />
          新建路由
        </Link>
      }
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {(query.data ?? []).length === 0 ? (
        <EmptyState
          title="还没有路由"
          text="先选入口节点和目标地址，再决定要不要加 hop。"
          action={
            <Link to="/routes/new" className="button-link">
              <Plus size={16} />
              新建路由
            </Link>
          }
        />
      ) : (
        <Table headers={['名称', '协议', '入口', '监听', '跳数', '目标', '状态']}>
          {(query.data ?? []).map((route) => (
            <tr key={route.id}>
              <td>
                <strong>
                  <Link to="/routes/$routeId" params={{ routeId: route.id }}>
                    {route.name}
                  </Link>
                </strong>
                <small>{route.id}</small>
              </td>
              <td>{route.protocol.toUpperCase()}</td>
              <td>{route.entry_node}</td>
              <td>{route.listen}</td>
              <td>{route.hops?.length ?? 0}</td>
              <td>{route.target}</td>
              <td><StatusPill value={route.enabled ? 'online' : 'offline'} /></td>
            </tr>
          ))}
        </Table>
      )}
    </PageFrame>
  )
}

export function RouteNewPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return (
    <PageFrame title="新建路由" subtitle="留空 hops 时就是单节点直入直出。">
      <RouteEditor
        initialRoute={undefined}
        onSaved={async (saved) => {
          await queryClient.invalidateQueries({ queryKey: ['routes'] })
          navigate({ to: '/routes/$routeId', params: { routeId: saved.id }, replace: true })
        }}
      />
    </PageFrame>
  )
}

export function RouteDetailPage({ routeId }: { routeId: string }) {
  const queryClient = useQueryClient()
  const [message, setMessage] = useState('')
  const routeQuery = useQuery({
    queryKey: ['route', routeId],
    queryFn: () => api<RouteInfo>(`/api/routes/${routeId}`),
  })
  const linksQuery = useQuery({
    queryKey: ['links'],
    queryFn: () => api<LinkInfo[]>('/api/links'),
  })

  const hopNames = useMemo(() => {
    const map = new Map((linksQuery.data ?? []).map((link) => [link.id, link]))
    return (routeQuery.data?.hops ?? []).map((hop) => map.get(hop.link_id)?.name ?? hop.link_id)
  }, [linksQuery.data, routeQuery.data])

  return (
    <PageFrame
      title={routeQuery.data?.name || '路由详情'}
      subtitle="编辑保存后，控制器会重新签名并推送配置。"
    >
      {routeQuery.error && <Banner text={routeQuery.error instanceof Error ? routeQuery.error.message : '加载失败'} />}
      {routeQuery.data ? (
        <>
          <Panel>
            <DetailGrid
              items={[
                { label: 'ID', value: routeQuery.data.id },
                { label: '协议', value: routeQuery.data.protocol.toUpperCase() },
                { label: '入口节点', value: routeQuery.data.entry_node },
                { label: '监听', value: routeQuery.data.listen },
                { label: '目标', value: routeQuery.data.target },
                { label: '创建时间', value: formatTime(routeQuery.data.created_at) },
                { label: '更新时间', value: formatTime(routeQuery.data.updated_at) },
                { label: '状态', value: <StatusPill value={routeQuery.data.enabled ? 'online' : 'offline'} /> },
              ]}
            />
          </Panel>
          <Panel>
            <h2>Hop 链</h2>
            {hopNames.length === 0 ? (
              <p>没有 hop，这就是单节点直入直出。</p>
            ) : (
              <div className="hop-list">
                {hopNames.map((name, index) => (
                  <div className="hop" key={`${name}-${index}`}>
                    <span>{index + 1}</span>
                    <strong>{name}</strong>
                  </div>
                ))}
              </div>
            )}
          </Panel>
          <RouteEditor
            initialRoute={routeQuery.data}
            onSaved={async () => {
              setMessage('已保存')
              await queryClient.invalidateQueries({ queryKey: ['routes'] })
              await queryClient.invalidateQueries({ queryKey: ['route', routeId] })
            }}
          />
          {message && <Panel><p>{message}</p></Panel>}
        </>
      ) : null}
    </PageFrame>
  )
}

function RouteEditor({
  initialRoute,
  onSaved,
}: {
  initialRoute?: RouteInfo
  onSaved: (saved: RouteInfo) => void | Promise<void>
}) {
  const nodesQuery = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api<NodeInfo[]>('/api/nodes'),
  })
  const linksQuery = useQuery({
    queryKey: ['links'],
    queryFn: () => api<LinkInfo[]>('/api/links'),
  })
  const [form, setForm] = useState<RouteForm>(emptyRouteForm())
  const [hopIds, setHopIds] = useState<string[]>([])
  const [selectedHop, setSelectedHop] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    if (!initialRoute) {
      return
    }
    setForm({
      id: initialRoute.id,
      name: initialRoute.name,
      protocol: initialRoute.protocol,
      entry_node: initialRoute.entry_node,
      listen: initialRoute.listen,
      target: initialRoute.target,
      enabled: initialRoute.enabled,
    })
    setHopIds(initialRoute.hops.map((hop) => hop.link_id))
  }, [initialRoute])

  const save = useMutation({
    mutationFn: async () => {
      const payload = {
        ...form,
        hops: hopIds.map((link_id) => ({ link_id })),
      }
      return post<RouteInfo>('/api/routes', payload)
    },
    onSuccess: async (saved) => {
      await onSaved(saved)
    },
    onError: (err) => setError(err instanceof Error ? err.message : '保存失败'),
  })

  const selectedLinks = useMemo(
    () => hopIds.map((id) => (linksQuery.data ?? []).find((link) => link.id === id)).filter(Boolean) as LinkInfo[],
    [hopIds, linksQuery.data],
  )

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
        <Field label="协议">
          <select
            value={form.protocol}
            onChange={(event) => setForm((current) => ({ ...current, protocol: event.target.value as RouteProtocol }))}
          >
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </Field>
        <Field label="入口节点">
          <select
            value={form.entry_node}
            onChange={(event) => setForm((current) => ({ ...current, entry_node: event.target.value }))}
          >
            <option value="">选择节点</option>
            {(nodesQuery.data ?? []).map((node) => (
              <option key={node.id} value={node.id}>
                {node.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="监听地址">
          <input
            value={form.listen}
            onChange={(event) => setForm((current) => ({ ...current, listen: event.target.value }))}
          />
        </Field>
        <Field label="目标地址">
          <input
            value={form.target}
            onChange={(event) => setForm((current) => ({ ...current, target: event.target.value }))}
            placeholder="landing.example.com:443"
          />
        </Field>
        <Field label="启用">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))}
          />
        </Field>
        <Field label="添加链路" wide>
          <div className="inline-form">
            <select value={selectedHop} onChange={(event) => setSelectedHop(event.target.value)}>
              <option value="">留空即单节点直入直出</option>
              {(linksQuery.data ?? []).map((link) => (
                <option key={link.id} value={link.id}>
                  {`${link.name} / ${link.type} / ${link.from_node} -> ${link.to_node}`}
                </option>
              ))}
            </select>
            <button
              type="button"
              className="ghost"
              onClick={() => {
                if (selectedHop && !hopIds.includes(selectedHop)) {
                  setHopIds([...hopIds, selectedHop])
                }
              }}
            >
              追加
            </button>
          </div>
        </Field>
      </FieldGrid>
      <Panel>
        <h2>当前跳点</h2>
        {selectedLinks.length === 0 ? (
          <p>没有选择链路，保存后就是单节点直入直出。</p>
        ) : (
          <div className="hop-list">
            {selectedLinks.map((link, index) => (
              <div className="hop" key={link.id}>
                <span>{index + 1}</span>
                <strong>{link.name}</strong>
                <small>{`${link.type} / ${link.from_node} -> ${link.to_node}`}</small>
                <button
                  className="ghost danger"
                  type="button"
                  onClick={() => setHopIds(hopIds.filter((id) => id !== link.id))}
                >
                  移除
                </button>
              </div>
            ))}
          </div>
        )}
      </Panel>
      {error && <p className="error">{error}</p>}
      <button type="submit" disabled={save.isPending}>
        保存路由
      </button>
    </form>
  )
}
