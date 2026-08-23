package protocol

import "nyarelay/internal/shared/model"

const SupportsLongConfigLeaseHeader = "X-NyaRelay-Supports-Long-Config-Lease"

type ControlMessage struct {
	Type                    string                   `json:"type"`
	NodeID                  string                   `json:"node_id,omitempty"`
	Version                 string                   `json:"version,omitempty"`
	SupportsLongConfigLease bool                     `json:"supports_long_config_lease,omitempty"`
	System                  model.NodeSystem         `json:"system,omitempty"`
	Revision                int64                    `json:"revision,omitempty"`
	Config                  *model.SignedConfig      `json:"config,omitempty"`
	Update                  *model.NodeUpdateCommand `json:"update,omitempty"`
	UpdateReport            *model.NodeUpdateReport  `json:"update_report,omitempty"`
	Error                   string                   `json:"error,omitempty"`
}
