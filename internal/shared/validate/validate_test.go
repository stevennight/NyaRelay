package validate

import (
	"testing"

	"nyarelay/internal/shared/model"
)

func TestTunnelAllowsSingleNodeDirect(t *testing.T) {
	err := Tunnel(model.Tunnel{
		ID:        "tun_1",
		Name:      "direct",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Enabled:   true,
		Stages: []model.TunnelStage{{
			ID:       "stage_1",
			TunnelID: "tun_1",
			Index:    0,
			Role:     model.TunnelStageEntry,
			Strategy: "single",
			Nodes: []model.TunnelStageNode{{
				ID:       "stage_node_1",
				TunnelID: "tun_1",
				StageID:  "stage_1",
				NodeID:   "node_1",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTunnelRejectsUnknownTransport(t *testing.T) {
	err := Tunnel(model.Tunnel{
		ID:        "tun_1",
		Name:      "bad",
		Type:      model.TunnelChain,
		Transport: "shell",
		Enabled:   true,
	})
	if err == nil {
		t.Fatal("expected unknown tunnel transport to fail")
	}
}

func TestForwardAllowsTCPUDP(t *testing.T) {
	err := Forward(model.Forward{
		ID:        "fwd_1",
		Name:      "game",
		TunnelID:  "tun_1",
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
		Listen:    "127.0.0.1:8443",
		Target:    "127.0.0.1:443",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTunnelAllowsPerProtocolStrategiesAndProtocols(t *testing.T) {
	err := Tunnel(model.Tunnel{
		ID:        "tun_1",
		Name:      "direct",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Enabled:   true,
		Stages: []model.TunnelStage{{
			ID:          "stage_1",
			TunnelID:    "tun_1",
			Index:       0,
			Role:        model.TunnelStageEntry,
			Strategy:    "failover",
			TCPStrategy: "round_robin",
			UDPStrategy: "random",
			Nodes: []model.TunnelStageNode{{
				ID:        "stage_node_1",
				TunnelID:  "tun_1",
				StageID:   "stage_1",
				NodeID:    "node_1",
				Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTunnelRejectsUnsupportedCandidateProtocol(t *testing.T) {
	err := Tunnel(model.Tunnel{
		ID:        "tun_1",
		Name:      "direct",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Enabled:   true,
		Stages: []model.TunnelStage{{
			ID:       "stage_1",
			TunnelID: "tun_1",
			Index:    0,
			Role:     model.TunnelStageEntry,
			Strategy: "single",
			Nodes: []model.TunnelStageNode{{
				ID:        "stage_node_1",
				TunnelID:  "tun_1",
				StageID:   "stage_1",
				NodeID:    "node_1",
				Protocols: []model.ForwardProtocol{"icmp"},
			}},
		}},
	})
	if err == nil {
		t.Fatal("expected unsupported candidate protocol to fail")
	}
}
