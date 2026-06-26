export type NodeStatus = 'offline' | 'online' | 'revoked'
export type LinkType = 'direct' | 'tls' | 'mtls' | 'ws-tls'
export type RouteProtocol = 'tcp' | 'udp'

export interface NodeInfo {
  id: string
  name: string
  status: NodeStatus
  version: string
  labels?: Record<string, string>
  approved: boolean
  revoked: boolean
  last_seen?: string
  created_at: string
  updated_at: string
  system?: {
    hostname?: string
    os?: string
    arch?: string
    ip?: string
  }
}

export interface LinkInfo {
  id: string
  name: string
  type: LinkType
  from_node: string
  to_node: string
  bind_addr: string
  public_addr: string
  server_name?: string
  enabled: boolean
  settings?: Record<string, string>
  created_at: string
  updated_at: string
}

export interface RouteInfo {
  id: string
  name: string
  protocol: RouteProtocol
  entry_node: string
  listen: string
  hops: Array<{ link_id: string }>
  target: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface AuditEvent {
  id: number
  actor: string
  action: string
  target: string
  detail: string
  created_at: string
}

export interface Dashboard {
  nodes: number
  online_nodes: number
  links: number
  routes: number
  active_routes: number
  revision: number
}

export interface ControllerInfo {
  signing_key: string
  public_url: string
  revision: number
}

export interface TrafficSummary {
  stat_id: string
  kind: string
  bytes_in: number
  bytes_out: number
  connections: number
  last_seen: string
}

export interface SetupStatus {
  needs_setup: boolean
  public_url: string
}

export interface MeResponse {
  user: {
    id: number
    username: string
  }
}
