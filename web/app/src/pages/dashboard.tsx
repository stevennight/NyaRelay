import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, GitCommit, Plus, Route, Server, Waypoints } from 'lucide-react'
import { api } from '../api'
import type { ControllerInfo, Dashboard } from '../types'
import { Banner, DetailGrid, LoadingState, PageFrame, Panel, Stat, formatTime } from '../components/ui'

export function DashboardPage() {
  const dashboardQuery = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => api<Dashboard>('/api/dashboard'),
  })
  const controllerQuery = useQuery({
    queryKey: ['controller-info'],
    queryFn: () => api<ControllerInfo>('/api/controller/info'),
  })
  const data = dashboardQuery.data
  const error = dashboardQuery.error
  const build = controllerQuery.data?.build
  const nodeRelease = controllerQuery.data?.node_release

  return (
    <PageFrame
      title="概览"
      subtitle="节点状态、转发运行情况与当前配置版本。"
      action={
        <Link to="/forwards/new" className="button-link">
          <Plus size={16} />
          新建转发
        </Link>
      }
    >
      {error && <Banner text={error instanceof Error ? error.message : '加载失败'} />}
      {controllerQuery.error && <Banner text={controllerQuery.error instanceof Error ? controllerQuery.error.message : '版本信息加载失败'} />}
      {dashboardQuery.isLoading ? (
        <LoadingState label="正在汇总运行状态" rows={3} />
      ) : (
        <div className="stats dashboard-stats">
          <Stat label="在线节点" value={`${data?.online_nodes ?? 0}/${data?.nodes ?? 0}`} icon={<Server size={18} />} tone="success" />
          <Stat label="活跃转发" value={String(data?.active_forwards ?? 0)} icon={<Activity size={18} />} tone="teal" />
          <Stat label="隧道数量" value={String(data?.tunnels ?? 0)} icon={<Waypoints size={18} />} tone="violet" />
          <Stat label="转发数量" value={String(data?.forwards ?? 0)} icon={<Route size={18} />} tone="amber" />
          <Stat label="配置版本" value={String(data?.revision ?? 0)} icon={<GitCommit size={18} />} />
        </div>
      )}
      <Panel className="dashboard-version-panel">
        <div className="section-heading">
          <div>
            <h2>版本信息</h2>
            <p>当前控制器构建和节点可用更新。</p>
          </div>
          <Link to="/settings/controller" className="button-link ghost">控制器设置</Link>
        </div>
        <DetailGrid
          items={[
            { label: '控制器版本', value: build?.version || '-' },
            { label: '控制器提交', value: build?.commit ? <code>{build.commit}</code> : '-' },
            { label: '构建时间', value: formatTime(build?.build_date) },
            { label: 'Node 可用版本', value: nodeRelease?.manifest.version || '-' },
            { label: 'Node 自动更新', value: nodeRelease?.update_enabled ? '已启用' : (nodeRelease?.disabled_reason || '未启用') },
          ]}
        />
      </Panel>
      <Panel>
        <h2>运行边界</h2>
        <p>
          节点主动连接控制器，控制器不保存 SSH，不执行 shell。所有隧道和转发配置都必须经过签名后应用。
        </p>
      </Panel>
    </PageFrame>
  )
}
