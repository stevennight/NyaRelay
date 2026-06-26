import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import type { AuditEvent } from '../types'
import { Banner, EmptyState, PageFrame, Table, formatTime } from '../components/ui'

export function AuditPage() {
  const query = useQuery({
    queryKey: ['audit'],
    queryFn: () => api<AuditEvent[]>('/api/audit'),
  })

  return (
    <PageFrame
      title="审计"
      subtitle="所有配置变更、登录和节点操作都会留下记录。"
    >
      {query.error && <Banner text={query.error instanceof Error ? query.error.message : '加载失败'} />}
      {(query.data ?? []).length === 0 ? (
        <EmptyState title="还没有审计事件" text="完成一次登录或配置变更后，这里就会有记录。" />
      ) : (
        <Table headers={['时间', '操作者', '动作', '目标', '详情']}>
          {(query.data ?? []).map((event) => (
            <tr key={event.id}>
              <td>{formatTime(event.created_at)}</td>
              <td>{event.actor}</td>
              <td>{event.action}</td>
              <td>{event.target}</td>
              <td>
                <code>{event.detail}</code>
              </td>
            </tr>
          ))}
        </Table>
      )}
    </PageFrame>
  )
}
