import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import type { TrafficSummary } from '../types'
import { Banner, EmptyState, PageFrame, Panel, Table, formatBytes, formatTime } from '../components/ui'

export function TrafficPage() {
  const query = useQuery({
    queryKey: ['traffic'],
    queryFn: () => api<TrafficSummary[]>('/api/traffic'),
  })

  return (
    <PageFrame
      title="流量"
      subtitle="节点会主动上报 route 和 link 的流量统计。"
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {(query.data ?? []).length === 0 ? (
        <EmptyState
          title="还没有统计数据"
          text="启动节点并跑流量后，这里会显示上报结果。"
        />
      ) : (
        <Table headers={['对象', '类型', '入站', '出站', '连接数', '最近上报']}>
          {(query.data ?? []).map((item) => (
            <tr key={`${item.kind}:${item.stat_id}`}>
              <td>{item.stat_id}</td>
              <td>{item.kind}</td>
              <td>{formatBytes(item.bytes_in)}</td>
              <td>{formatBytes(item.bytes_out)}</td>
              <td>{item.connections}</td>
              <td>{formatTime(item.last_seen)}</td>
            </tr>
          ))}
        </Table>
      )}
    </PageFrame>
  )
}
