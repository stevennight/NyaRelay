package model

import "time"

type TunnelType string

const (
	TunnelDirect TunnelType = "direct"
	TunnelChain  TunnelType = "chain"
)

type TunnelTransport string

const (
	TunnelTransportDirect TunnelTransport = "direct"
	TunnelTransportTLS    TunnelTransport = "tls"
	TunnelTransportMTLS   TunnelTransport = "mtls"
	TunnelTransportWSTLS  TunnelTransport = "ws-tls"
)

type TunnelStageRole string

const (
	TunnelStageEntry  TunnelStageRole = "entry"
	TunnelStageMiddle TunnelStageRole = "middle"
	TunnelStageExit   TunnelStageRole = "exit"
)

type ForwardProtocol string

const (
	ForwardProtocolTCP ForwardProtocol = "tcp"
	ForwardProtocolUDP ForwardProtocol = "udp"
)

type NodeStatus string

const (
	NodeOffline NodeStatus = "offline"
	NodeOnline  NodeStatus = "online"
	NodeRevoked NodeStatus = "revoked"
)

type NodeUpdateStatus string

const (
	NodeUpdateIdle      NodeUpdateStatus = ""
	NodeUpdateRequested NodeUpdateStatus = "requested"
	NodeUpdateRunning   NodeUpdateStatus = "running"
	NodeUpdateSucceeded NodeUpdateStatus = "succeeded"
	NodeUpdateFailed    NodeUpdateStatus = "failed"
)

type Node struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Status            NodeStatus        `json:"status"`
	Version           string            `json:"version"`
	DesiredVersion    string            `json:"desired_version,omitempty"`
	UpdateStatus      NodeUpdateStatus  `json:"update_status,omitempty"`
	UpdateError       string            `json:"update_error,omitempty"`
	UpdateRequestedAt time.Time         `json:"update_requested_at,omitempty"`
	UpdateFinishedAt  time.Time         `json:"update_finished_at,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	PublicHost        string            `json:"public_host,omitempty"`
	PortMin           int               `json:"port_min,omitempty"`
	PortMax           int               `json:"port_max,omitempty"`
	Approved          bool              `json:"approved"`
	Revoked           bool              `json:"revoked"`
	LastSeen          time.Time         `json:"last_seen,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	System            NodeSystem        `json:"system,omitempty"`
}

type NodeSystem struct {
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	IP       string `json:"ip,omitempty"`
}

type Tunnel struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         TunnelType        `json:"type"`
	Transport    TunnelTransport   `json:"transport"`
	EntryAddress string            `json:"entry_address,omitempty"`
	Enabled      bool              `json:"enabled"`
	Settings     map[string]string `json:"settings,omitempty"`
	Stages       []TunnelStage     `json:"stages"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type TunnelStage struct {
	ID          string            `json:"id"`
	TunnelID    string            `json:"tunnel_id"`
	Index       int               `json:"index"`
	Role        TunnelStageRole   `json:"role"`
	Strategy    string            `json:"strategy"`
	TCPStrategy string            `json:"tcp_strategy,omitempty"`
	UDPStrategy string            `json:"udp_strategy,omitempty"`
	Nodes       []TunnelStageNode `json:"nodes"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type TunnelStageNode struct {
	ID          string            `json:"id"`
	TunnelID    string            `json:"tunnel_id"`
	StageID     string            `json:"stage_id"`
	NodeID      string            `json:"node_id"`
	Protocols   []ForwardProtocol `json:"protocols,omitempty"`
	ListenAddr  string            `json:"listen_addr,omitempty"`
	PublicAddr  string            `json:"public_addr,omitempty"`
	ConnectAddr string            `json:"connect_addr,omitempty"`
	Weight      int               `json:"weight,omitempty"`
	Settings    map[string]string `json:"settings,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type Forward struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	TunnelID    string            `json:"tunnel_id"`
	Protocols   []ForwardProtocol `json:"protocols"`
	Listen      string            `json:"listen"`
	Target      string            `json:"target,omitempty"`
	Targets     []ForwardTarget   `json:"targets"`
	Strategy    string            `json:"strategy,omitempty"`
	TCPStrategy string            `json:"tcp_strategy,omitempty"`
	UDPStrategy string            `json:"udp_strategy,omitempty"`
	Enabled     bool              `json:"enabled"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type PortAllocation struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	OwnerKind string    `json:"owner_kind"`
	OwnerID   string    `json:"owner_id"`
	Protocol  string    `json:"protocol"`
	Port      int       `json:"port"`
	BindAddr  string    `json:"bind_addr"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RelayConfig struct {
	Revision  int64            `json:"revision"`
	IssuedAt  time.Time        `json:"issued_at"`
	NodeID    string           `json:"node_id"`
	Nodes     []Node           `json:"nodes"`
	Tunnels   []TunnelRuntime  `json:"tunnels"`
	Forwards  []ForwardRuntime `json:"forwards"`
	ExpiresAt time.Time        `json:"expires_at"`
}

type TunnelRuntime struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Type      TunnelType           `json:"type"`
	Transport TunnelTransport      `json:"transport"`
	Stages    []TunnelRuntimeStage `json:"stages"`
	Settings  map[string]string    `json:"settings,omitempty"`
}

type TunnelRuntimeStage struct {
	Index       int                 `json:"index"`
	Role        TunnelStageRole     `json:"role"`
	Strategy    string              `json:"strategy"`
	TCPStrategy string              `json:"tcp_strategy,omitempty"`
	UDPStrategy string              `json:"udp_strategy,omitempty"`
	Nodes       []TunnelRuntimeNode `json:"nodes"`
}

type TunnelRuntimeNode struct {
	NodeID      string            `json:"node_id"`
	Protocols   []ForwardProtocol `json:"protocols,omitempty"`
	ListenAddr  string            `json:"listen_addr,omitempty"`
	PublicAddr  string            `json:"public_addr,omitempty"`
	ConnectAddr string            `json:"connect_addr,omitempty"`
	Weight      int               `json:"weight,omitempty"`
	Settings    map[string]string `json:"settings,omitempty"`
}

type ForwardTarget struct {
	ID        string            `json:"id"`
	ForwardID string            `json:"forward_id,omitempty"`
	Address   string            `json:"address"`
	Protocols []ForwardProtocol `json:"protocols,omitempty"`
	Weight    int               `json:"weight,omitempty"`
	Enabled   bool              `json:"enabled"`
	Position  int               `json:"position,omitempty"`
}

type ForwardRuntime struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	TunnelID    string            `json:"tunnel_id"`
	Protocols   []ForwardProtocol `json:"protocols"`
	Listen      string            `json:"listen,omitempty"`
	Target      string            `json:"target,omitempty"`
	Targets     []ForwardTarget   `json:"targets,omitempty"`
	Strategy    string            `json:"strategy,omitempty"`
	TCPStrategy string            `json:"tcp_strategy,omitempty"`
	UDPStrategy string            `json:"udp_strategy,omitempty"`
	Enabled     bool              `json:"enabled"`
}

type SignedConfig struct {
	Config    RelayConfig `json:"config"`
	Signature string      `json:"signature"`
	KeyID     string      `json:"key_id"`
}

type MetricsReport struct {
	NodeID       string        `json:"node_id"`
	ObservedAt   time.Time     `json:"observed_at"`
	ForwardStats []TrafficStat `json:"forward_stats,omitempty"`
	TunnelStats  []TrafficStat `json:"tunnel_stats,omitempty"`
	Runtime      RuntimeStat   `json:"runtime"`
	AgentErrors  []AgentError  `json:"agent_errors,omitempty"`
}

type TrafficStat struct {
	ID          string `json:"id"`
	BytesIn     int64  `json:"bytes_in"`
	BytesOut    int64  `json:"bytes_out"`
	Connections int64  `json:"connections"`
}

type RuntimeStat struct {
	UptimeSeconds int64 `json:"uptime_seconds"`
	Goroutines    int   `json:"goroutines"`
}

type AgentError struct {
	At      time.Time `json:"at"`
	Scope   string    `json:"scope"`
	Message string    `json:"message"`
}

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
}

type NodeReleaseManifest struct {
	Version   string                `json:"version"`
	Commit    string                `json:"commit,omitempty"`
	BuildDate string                `json:"build_date,omitempty"`
	Artifacts []NodeReleaseArtifact `json:"artifacts"`
}

type NodeReleaseArtifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type SignedNodeRelease struct {
	Manifest       NodeReleaseManifest `json:"manifest"`
	Signature      string              `json:"signature,omitempty"`
	SigningKeyID   string              `json:"signing_key_id,omitempty"`
	UpdateEnabled  bool                `json:"update_enabled"`
	DisabledReason string              `json:"disabled_reason,omitempty"`
}

type NodeUpdateCommand struct {
	Version      string              `json:"version"`
	Manifest     NodeReleaseManifest `json:"manifest"`
	Signature    string              `json:"signature"`
	SigningKeyID string              `json:"signing_key_id"`
}

type NodeUpdateReport struct {
	Status      NodeUpdateStatus `json:"status"`
	Version     string           `json:"version,omitempty"`
	Error       string           `json:"error,omitempty"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
}

type NodeUpdateRequest struct {
	ControllerURL string    `json:"controller_url"`
	NodeID        string    `json:"node_id"`
	NodeToken     string    `json:"node_token"`
	TargetVersion string    `json:"target_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	SHA256        string    `json:"sha256"`
	RequestedAt   time.Time `json:"requested_at"`
}

type AuditEvent struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}
