import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { api } from '../api'
import type { Dashboard } from '../types'
import { Banner, PageFrame, Panel, Stat } from '../components/ui'

export function DashboardPage() {
  const { data, error } = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => api<Dashboard>('/api/dashboard'),
  })

  return (
    <PageFrame
      title="概览"
      subtitle="控制面和数据面分离，节点主动连接，配置由控制器签名下发。"
      action={
        <Link to="/forwards/new" className="button-link">
          <Plus size={16} />
          新建转发
        </Link>
      }
    >
      {error && <Banner text={error instanceof Error ? error.message : '加载失败'} />}
      <div className="stats">
        <Stat label="在线节点" value={`${data?.online_nodes ?? 0}/${data?.nodes ?? 0}`} />
        <Stat label="活跃转发" value={String(data?.active_forwards ?? 0)} />
        <Stat label="隧道数量" value={String(data?.tunnels ?? 0)} />
        <Stat label="转发数量" value={String(data?.forwards ?? 0)} />
        <Stat label="配置版本" value={String(data?.revision ?? 0)} />
      </div>
      <Panel>
        <h2>运行边界</h2>
        <p>
          节点主动连接控制器，控制器不保存 SSH，不执行 shell。所有隧道和转发配置都必须经过签名后应用。
        </p>
      </Panel>
    </PageFrame>
  )
}
