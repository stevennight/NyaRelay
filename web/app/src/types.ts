export type NodeStatus = 'offline' | 'online' | 'revoked'
export type TunnelType = 'direct' | 'chain'
export type TunnelTransport = 'direct' | 'tls' | 'mtls' | 'ws-tls'
export type TunnelStageRole = 'entry' | 'middle' | 'exit'
export type ForwardProtocol = 'tcp' | 'udp'

export interface NodeInfo {
  id: string
  name: string
  status: NodeStatus
  version: string
  labels?: Record<string, string>
  public_host?: string
  port_min?: number
  port_max?: number
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

export interface TunnelStageNode {
  id: string
  tunnel_id: string
  stage_id: string
  node_id: string
  protocols?: ForwardProtocol[]
  listen_addr?: string
  public_addr?: string
  connect_addr?: string
  weight?: number
  settings?: Record<string, string>
  created_at: string
  updated_at: string
}

export interface TunnelStage {
  id: string
  tunnel_id: string
  index: number
  role: TunnelStageRole
  strategy: string
  tcp_strategy?: string
  udp_strategy?: string
  nodes: TunnelStageNode[]
  created_at: string
  updated_at: string
}

export interface TunnelInfo {
  id: string
  name: string
  type: TunnelType
  transport: TunnelTransport
  entry_address?: string
  enabled: boolean
  settings?: Record<string, string>
  stages: TunnelStage[]
  created_at: string
  updated_at: string
}

export interface ForwardInfo {
  id: string
  name: string
  tunnel_id: string
  protocols: ForwardProtocol[]
  listen: string
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
  tunnels: number
  forwards: number
  active_forwards: number
  revision: number
}

export interface ControllerInfo {
  signing_key: string
  public_url: string
  revision: number
}

export interface NodeInstallInfo {
  node: NodeInfo
  token: string
  script_url: string
  binary_url: string
  command: string
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
