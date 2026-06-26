package model

import "time"

type LinkType string

const (
	LinkDirect LinkType = "direct"
	LinkTLS    LinkType = "tls"
	LinkMTLS   LinkType = "mtls"
	LinkWSTLS  LinkType = "ws-tls"
)

type RouteProtocol string

const (
	ProtocolTCP RouteProtocol = "tcp"
	ProtocolUDP RouteProtocol = "udp"
)

type NodeStatus string

const (
	NodeOffline NodeStatus = "offline"
	NodeOnline  NodeStatus = "online"
	NodeRevoked NodeStatus = "revoked"
)

type Node struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     NodeStatus        `json:"status"`
	Version    string            `json:"version"`
	Labels     map[string]string `json:"labels,omitempty"`
	PublicHost string            `json:"public_host,omitempty"`
	PortMin    int               `json:"port_min,omitempty"`
	PortMax    int               `json:"port_max,omitempty"`
	Approved   bool              `json:"approved"`
	Revoked    bool              `json:"revoked"`
	LastSeen   time.Time         `json:"last_seen,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	System     NodeSystem        `json:"system,omitempty"`
}

type NodeSystem struct {
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	IP       string `json:"ip,omitempty"`
}

type Link struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       LinkType          `json:"type"`
	FromNode   string            `json:"from_node"`
	ToNode     string            `json:"to_node"`
	BindAddr   string            `json:"bind_addr"`
	PublicAddr string            `json:"public_addr"`
	ServerName string            `json:"server_name,omitempty"`
	Enabled    bool              `json:"enabled"`
	Settings   map[string]string `json:"settings,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

type RouteHop struct {
	LinkID string `json:"link_id"`
}

type Route struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Protocol  RouteProtocol `json:"protocol"`
	EntryNode string        `json:"entry_node"`
	Listen    string        `json:"listen"`
	Hops      []RouteHop    `json:"hops"`
	Target    string        `json:"target"`
	Enabled   bool          `json:"enabled"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type RelayConfig struct {
	Revision  int64     `json:"revision"`
	IssuedAt  time.Time `json:"issued_at"`
	NodeID    string    `json:"node_id"`
	Nodes     []Node    `json:"nodes"`
	Links     []Link    `json:"links"`
	Routes    []Route   `json:"routes"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SignedConfig struct {
	Config    RelayConfig `json:"config"`
	Signature string      `json:"signature"`
	KeyID     string      `json:"key_id"`
}

type MetricsReport struct {
	NodeID      string        `json:"node_id"`
	ObservedAt  time.Time     `json:"observed_at"`
	RouteStats  []TrafficStat `json:"route_stats,omitempty"`
	LinkStats   []TrafficStat `json:"link_stats,omitempty"`
	Runtime     RuntimeStat   `json:"runtime"`
	AgentErrors []AgentError  `json:"agent_errors,omitempty"`
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

type AuditEvent struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}
