import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { TrafficPage } from './traffic'
import { installFetch, jsonResponse, renderWithClient } from '../test/helpers'

describe('traffic page', () => {
  it('renders trend and per-kind charts before the detail table', async () => {
    installFetch([
      {
        path: '/api/traffic',
        response: jsonResponse({
          totals: {
            bytes_in: 180,
            bytes_out: 60,
            connections: 12,
            objects: 2,
            last_seen: '2026-08-23T11:00:00Z',
          },
          by_kind: [
            { kind: 'forward', objects: 1, bytes_in: 120, bytes_out: 40, connections: 8, last_seen: '2026-08-23T11:00:00Z' },
            { kind: 'tunnel', objects: 1, bytes_in: 60, bytes_out: 20, connections: 4, last_seen: '2026-08-23T11:00:00Z' },
          ],
          trend: [
            { bucket: '2026-08-23T10:00:00Z', bytes_in: 180, bytes_out: 60, connections: 12 },
          ],
          items: [
            { stat_id: 'forward:fwd-1', kind: 'forward', bytes_in: 120, bytes_out: 40, connections: 8, last_seen: '2026-08-23T11:00:00Z' },
            { stat_id: 'tunnel:tun-1', kind: 'tunnel', bytes_in: 60, bytes_out: 20, connections: 4, last_seen: '2026-08-23T11:00:00Z' },
          ],
        }),
      },
      {
        path: '/api/forwards',
        response: jsonResponse([{ id: 'fwd-1', name: 'Web Gateway' }]),
      },
      {
        path: '/api/tunnels',
        response: jsonResponse([{ id: 'tun-1', name: 'Edge Tunnel' }]),
      },
    ])

    renderWithClient(<TrafficPage />)

    expect(await screen.findByRole('img', { name: /最近 24 小时流量趋势/ })).toBeInTheDocument()
    expect(screen.getByText('转发流量')).toBeInTheDocument()
    expect(screen.getByText('隧道流量')).toBeInTheDocument()
    expect((await screen.findAllByText('Web Gateway')).length).toBeGreaterThan(0)
    expect((await screen.findAllByText('Edge Tunnel')).length).toBeGreaterThan(0)

    fireEvent.click(screen.getByRole('button', { name: '转发' }))
    expect(screen.getByText('显示 1 / 2 个对象')).toBeInTheDocument()
  })
})
