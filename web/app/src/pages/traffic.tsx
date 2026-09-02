import { useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Layers, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { api } from '../api'
import type {
  ForwardInfo,
  TrafficKindSummary,
  TrafficMetricItem,
  TrafficSummary,
  TrafficTrendPoint,
  TunnelInfo,
} from '../types'
import { Banner, EmptyState, PageFrame, Panel, Stat, Table, formatBytes, formatTime } from '../components/ui'

type KindFilter = 'all' | 'forward' | 'tunnel'

const kindLabels: Record<string, string> = {
  forward: '转发',
  tunnel: '隧道',
}

export function TrafficPage() {
  const [kindFilter, setKindFilter] = useState<KindFilter>('all')
  const trafficQuery = useQuery({
    queryKey: ['traffic'],
    queryFn: () => api<TrafficSummary>('/api/traffic'),
  })
  const forwardsQuery = useQuery({
    queryKey: ['forwards'],
    queryFn: () => api<ForwardInfo[]>('/api/forwards'),
    staleTime: 60_000,
  })
  const tunnelsQuery = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api<TunnelInfo[]>('/api/tunnels'),
    staleTime: 60_000,
  })

  const data = trafficQuery.data
  const items = data?.items ?? []
  const totals = data?.totals
  const totalBytes = (totals?.bytes_in ?? 0) + (totals?.bytes_out ?? 0)
  const names = useMemo(() => {
    const result = new Map<string, string>()
    for (const forward of forwardsQuery.data ?? []) result.set(`forward:${forward.id}`, forward.name)
    for (const tunnel of tunnelsQuery.data ?? []) result.set(`tunnel:${tunnel.id}`, tunnel.name)
    return result
  }, [forwardsQuery.data, tunnelsQuery.data])
  const filteredItems = useMemo(
    () => items
      .filter((item) => kindFilter === 'all' || item.kind === kindFilter)
      .sort((left, right) => (right.bytes_in + right.bytes_out) - (left.bytes_in + left.bytes_out)),
    [items, kindFilter],
  )

  return (
    <PageFrame
      title="流量统计"
      subtitle="累计流量、最近 24 小时趋势与对象排行。"
      action={
        <button
          className="ghost icon-button"
          type="button"
          aria-label="刷新统计"
          title="刷新统计"
          onClick={() => trafficQuery.refetch()}
          disabled={trafficQuery.isFetching}
        >
          <RefreshCw size={17} className={trafficQuery.isFetching ? 'spin' : undefined} />
        </button>
      }
    >
      {trafficQuery.error && <Banner text={trafficQuery.error instanceof Error ? trafficQuery.error.message : '加载失败'} />}
      {trafficQuery.isLoading ? (
        <Panel className="traffic-loading">
          <div className="traffic-loading-mark" />
          <strong>正在加载统计</strong>
          <span>正在读取节点上报数据...</span>
        </Panel>
      ) : !data || items.length === 0 ? (
        <EmptyState
          title="还没有统计数据"
          text="启动节点并跑流量后，这里会显示总量、趋势和对象排行。"
        />
      ) : (
        <>
          <div className="stats traffic-kpis" aria-label="流量总览">
            <Stat label="总流量" value={formatBytes(totalBytes)} />
            <Stat label="入站" value={formatBytes(totals?.bytes_in ?? 0)} />
            <Stat label="出站" value={formatBytes(totals?.bytes_out ?? 0)} />
            <Stat label="连接次数" value={formatCount(totals?.connections ?? 0)} />
            <Stat label="统计对象" value={formatCount(totals?.objects ?? items.length)} />
          </div>

          <div className="traffic-main-grid">
            <Panel className="traffic-panel traffic-trend-panel">
              <div className="section-heading">
                <div>
                  <h2>最近 24 小时</h2>
                  <p>按小时汇总的入站与出站流量</p>
                </div>
                <span className="traffic-updated">最近上报 {formatTime(totals?.last_seen)}</span>
              </div>
              <TrafficTrendChart points={data.trend} />
            </Panel>

            <Panel className="traffic-panel traffic-kinds-panel">
              <div className="section-heading">
                <div>
                  <h2>按类型</h2>
                  <p>流量在转发和隧道之间的分布</p>
                </div>
                <Layers size={18} className="section-icon" aria-hidden="true" />
              </div>
              <TrafficKindList kinds={data.by_kind} totalBytes={totalBytes} />
            </Panel>
          </div>

          <div className="traffic-breakdown-grid">
            <TrafficKindChart
              kind="forward"
              summary={data.by_kind.find((item) => item.kind === 'forward')}
              items={items}
              names={names}
            />
            <TrafficKindChart
              kind="tunnel"
              summary={data.by_kind.find((item) => item.kind === 'tunnel')}
              items={items}
              names={names}
            />
          </div>

          <Panel className="traffic-panel traffic-objects-panel">
            <div className="section-heading traffic-objects-heading">
              <div>
                <h2>对象排行</h2>
                <p>按累计总流量排序，最近上报时间用于判断数据是否新鲜</p>
              </div>
              <div className="traffic-filter" role="group" aria-label="对象类型筛选">
                {([
                  ['all', '全部'],
                  ['forward', '转发'],
                  ['tunnel', '隧道'],
                ] as const).map(([value, label]) => (
                  <button
                    key={value}
                    type="button"
                    className={kindFilter === value ? 'active' : ''}
                    aria-pressed={kindFilter === value}
                    onClick={() => setKindFilter(value)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>
            <div className="list-summary">
              <span>显示 {filteredItems.length} / {items.length} 个对象</span>
              {forwardsQuery.error || tunnelsQuery.error ? <span>部分对象名称暂不可用</span> : null}
            </div>
            <TrafficTable items={filteredItems} names={names} />
          </Panel>
        </>
      )}
    </PageFrame>
  )
}

function TrafficTrendChart({ points }: { points: TrafficTrendPoint[] }) {
  const maxBytes = Math.max(...points.map((point) => point.bytes_in + point.bytes_out), 0)
  const chartMax = maxBytes || 1

  return (
    <div
      className="traffic-chart"
      role="img"
      aria-label={`最近 24 小时流量趋势，总峰值 ${formatBytes(maxBytes)}`}
    >
      <div className="traffic-chart-y-axis" aria-hidden="true">
        <span>{formatBytes(chartMax)}</span>
        <span>{formatBytes(chartMax / 2)}</span>
        <span>0 B</span>
      </div>
      <div className="traffic-chart-area">
        <div className="traffic-chart-grid" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <div className="traffic-chart-bars">
          {points.map((point, index) => {
            const total = point.bytes_in + point.bytes_out
            const height = total === 0 ? 0 : Math.max((total / chartMax) * 100, 2)
            const inboundHeight = total === 0 ? 0 : (point.bytes_in / total) * 100
            const label = formatHour(point.bucket)
            return (
              <div className="traffic-chart-column" key={point.bucket}>
                <div
                  className="traffic-chart-bar-stack"
                  style={{ height: `${height}%` }}
                  title={`${label} · 入站 ${formatBytes(point.bytes_in)} · 出站 ${formatBytes(point.bytes_out)} · 连接 ${formatCount(point.connections)}`}
                >
                  <span className="traffic-chart-bar-out" style={{ height: `${100 - inboundHeight}%` }} />
                  <span className="traffic-chart-bar-in" style={{ height: `${inboundHeight}%` }} />
                </div>
                <small>{index % 4 === 0 ? label : ''}</small>
              </div>
            )
          })}
        </div>
      </div>
      <div className="traffic-chart-legend">
        <span><i className="traffic-legend-in" />入站</span>
        <span><i className="traffic-legend-out" />出站</span>
      </div>
    </div>
  )
}

function TrafficKindList({ kinds, totalBytes }: { kinds: TrafficKindSummary[]; totalBytes: number }) {
  if (kinds.length === 0) {
    return <p className="traffic-muted">暂无类型分布</p>
  }

  return (
    <div className="traffic-kind-list">
      {kinds.map((kind) => {
        const bytes = kind.bytes_in + kind.bytes_out
        const percentage = totalBytes === 0 ? 0 : (bytes / totalBytes) * 100
        return (
          <div className="traffic-kind-row" key={kind.kind}>
            <div className="traffic-kind-title">
              <span className={`traffic-kind-dot ${kind.kind}`} aria-hidden="true" />
              <strong>{kindLabels[kind.kind] ?? kind.kind}</strong>
              <small>{formatCount(kind.objects)} 个对象</small>
              <b>{formatBytes(bytes)}</b>
            </div>
            <div className="traffic-progress" aria-hidden="true">
              <span className={kind.kind} style={{ width: `${Math.min(percentage, 100)}%` }} />
            </div>
            <div className="traffic-kind-meta">
              <span>入 {formatBytes(kind.bytes_in)}</span>
              <span>出 {formatBytes(kind.bytes_out)}</span>
              <span>{formatCount(kind.connections)} 次连接</span>
            </div>
          </div>
        )
      })}
    </div>
  )
}

function TrafficKindChart({
  kind,
  summary,
  items,
  names,
}: {
  kind: 'forward' | 'tunnel'
  summary?: TrafficKindSummary
  items: TrafficMetricItem[]
  names: Map<string, string>
}) {
  const rankedItems = items
    .filter((item) => item.kind === kind)
    .sort((left, right) => {
      const leftTotal = left.bytes_in + left.bytes_out
      const rightTotal = right.bytes_in + right.bytes_out
      return rightTotal - leftTotal
    })
    .slice(0, 6)
  const maxBytes = Math.max(...rankedItems.map((item) => item.bytes_in + item.bytes_out), 0)
  const kindTotal = summary ? summary.bytes_in + summary.bytes_out : rankedItems.reduce((total, item) => total + item.bytes_in + item.bytes_out, 0)

  return (
    <Panel className={`traffic-panel traffic-kind-chart-panel ${kind}`}>
      <div className="section-heading">
        <div>
          <h2>{kindLabels[kind]}流量</h2>
          <p>{formatCount(summary?.objects ?? items.filter((item) => item.kind === kind).length)} 个对象，按累计流量排序</p>
        </div>
        <strong className="traffic-chart-total">{formatBytes(kindTotal)}</strong>
      </div>
      {rankedItems.length === 0 ? (
        <p className="traffic-muted traffic-chart-empty">暂无{kindLabels[kind]}流量</p>
      ) : (
        <div className="traffic-breakdown-list">
          {rankedItems.map((item) => {
            const total = item.bytes_in + item.bytes_out
            const shortID = item.stat_id.replace(/^(forward|tunnel):/, '')
            const name = names.get(item.stat_id) ?? shortID
            return (
              <div className="traffic-breakdown-row" key={`${item.kind}:${item.stat_id}`}>
                <div className="traffic-breakdown-label">
                  <strong>{name}</strong>
                  <small>{name === shortID ? item.stat_id : shortID}</small>
                </div>
                <div
                  className="traffic-breakdown-track"
                  title={`${name} · 入站 ${formatBytes(item.bytes_in)} · 出站 ${formatBytes(item.bytes_out)}`}
                >
                  <span className="traffic-breakdown-in" style={{ width: `${maxBytes === 0 ? 0 : (item.bytes_in / maxBytes) * 100}%` }} />
                  <span className="traffic-breakdown-out" style={{ width: `${maxBytes === 0 ? 0 : (item.bytes_out / maxBytes) * 100}%` }} />
                </div>
                <strong className="traffic-breakdown-value">{formatBytes(total)}</strong>
              </div>
            )
          })}
          <div className="traffic-breakdown-legend">
            <span><i className="traffic-legend-in" />入站</span>
            <span><i className="traffic-legend-out" />出站</span>
            {rankedItems.length < (summary?.objects ?? rankedItems.length) && <small>仅显示流量最高的 6 个</small>}
          </div>
        </div>
      )}
    </Panel>
  )
}

function TrafficTable({ items, names }: { items: TrafficMetricItem[]; names: Map<string, string> }) {
  const maxBytes = Math.max(...items.map((item) => item.bytes_in + item.bytes_out), 0)

  if (items.length === 0) {
    return <p className="traffic-muted traffic-table-empty">当前筛选条件下没有对象</p>
  }

  return (
    <Table headers={['对象', '类型', '总流量', '入 / 出', '连接次数', '最近上报']}>
      {items.map((item) => {
        const total = item.bytes_in + item.bytes_out
        const shortID = item.stat_id.replace(/^(forward|tunnel):/, '')
        const name = names.get(item.stat_id) ?? shortID
        const percentage = maxBytes === 0 ? 0 : (total / maxBytes) * 100
        return (
          <tr key={`${item.kind}:${item.stat_id}`}>
            <td>
              <div className="traffic-object-name">
                <strong>{name}</strong>
                <small>{name === shortID ? item.stat_id : shortID}</small>
              </div>
            </td>
            <td><span className={`traffic-kind-badge ${item.kind}`}>{kindLabels[item.kind] ?? item.kind}</span></td>
            <td>
              <strong>{formatBytes(total)}</strong>
              <div className="traffic-rank-bar" aria-label={`占当前排行 ${percentage.toFixed(0)}%`}>
                <span style={{ width: `${Math.min(percentage, 100)}%` }} />
              </div>
            </td>
            <td>
              <div className="traffic-in-out">
                <span><ArrowDown size={13} />{formatBytes(item.bytes_in)}</span>
                <span><ArrowUp size={13} />{formatBytes(item.bytes_out)}</span>
              </div>
            </td>
            <td>{formatCount(item.connections)}</td>
            <td>{formatTime(item.last_seen)}</td>
          </tr>
        )
      })}
    </Table>
  )
}

function formatCount(value: number) {
  return new Intl.NumberFormat('zh-CN').format(value)
}

function formatHour(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
}
