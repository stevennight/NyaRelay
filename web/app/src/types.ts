export type NodeStatus = 'offline' | 'online' | 'revoked'
export type NodeUpdateStatus = '' | 'requested' | 'running' | 'succeeded' | 'failed'
export type TunnelType = 'direct' | 'chain'
export type TunnelTransport = 'direct' | 'tls' | 'mtls' | 'ws-tls'
export type TunnelStageRole = 'entry' | 'middle' | 'exit'
export type ForwardProtocol = 'tcp' | 'udp'

export interface NodeInfo {
  id: string
  name: string
  status: NodeStatus
  version: string
  desired_version?: string
  update_status?: NodeUpdateStatus
  update_error?: string
  update_requested_at?: string
  update_finished_at?: string
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
  target?: string
  targets: ForwardTargetInfo[]
  strategy?: string
  tcp_strategy?: string
  udp_strategy?: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface ForwardTargetInfo {
  id: string
  forward_id?: string
  address: string
  protocols?: ForwardProtocol[]
  weight?: number
  enabled: boolean
  position?: number
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
  build?: BuildInfo
  node_release?: SignedNodeRelease
}

export interface BuildInfo {
  version: string
  commit?: string
  build_date?: string
}

export interface NodeReleaseArtifact {
  os: string
  arch: string
  sha256: string
  size: number
}

export interface NodeReleaseManifest {
  version: string
  commit?: string
  build_date?: string
  artifacts: NodeReleaseArtifact[]
}

export interface SignedNodeRelease {
  manifest: NodeReleaseManifest
  signature?: string
  signing_key_id?: string
  update_enabled: boolean
  disabled_reason?: string
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
