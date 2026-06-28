package controller

import (
	"testing"

	"nyarelay/internal/shared/model"
)

func TestScopeConfigForNodeLimitsTunnelSecrets(t *testing.T) {
	tunnel := model.Tunnel{
		ID:        "tun_1",
		Name:      "chain",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportMTLS,
		Enabled:   true,
		Stages: []model.TunnelStage{
			{
				ID:       "stage_entry",
				TunnelID: "tun_1",
				Index:    0,
				Role:     model.TunnelStageEntry,
				Strategy: "single",
				Nodes: []model.TunnelStageNode{{
					ID:       "stage_node_entry",
					TunnelID: "tun_1",
					StageID:  "stage_entry",
					NodeID:   "entry",
				}},
			},
			{
				ID:       "stage_exit",
				TunnelID: "tun_1",
				Index:    1,
				Role:     model.TunnelStageExit,
				Strategy: "single",
				Nodes: []model.TunnelStageNode{{
					ID:         "stage_node_exit",
					TunnelID:   "tun_1",
					StageID:    "stage_exit",
					NodeID:     "exit",
					ListenAddr: "127.0.0.1:9000",
					PublicAddr: "127.0.0.1:9000",
					Settings: map[string]string{
						"secret":      "relay-secret",
						"ca_cert":     "ca",
						"server_cert": "server-cert",
						"server_key":  "server-key",
						"client_cert": "client-cert",
						"client_key":  "client-key",
					},
				}},
			},
		},
	}
	forward := model.Forward{
		ID:        "fwd_1",
		Name:      "fwd",
		TunnelID:  "tun_1",
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    "127.0.0.1:8443",
		Target:    "127.0.0.1:443",
		Enabled:   true,
	}

	entryTunnels, entryForwards := scopeConfigForNode("entry", []model.Tunnel{tunnel}, []model.Forward{forward})
	if len(entryTunnels) != 1 || len(entryForwards) != 1 {
		t.Fatalf("expected entry tunnel and forward")
	}
	entryNext := entryTunnels[0].Stages[1].Nodes[0]
	if entryNext.Settings["server_key"] != "" {
		t.Fatal("dialing entry node must not receive server key")
	}
	if entryNext.Settings["client_key"] == "" {
		t.Fatal("dialing entry node must receive client key")
	}
	if entryForwards[0].Target != "" {
		t.Fatal("chain entry node must not receive target")
	}

	exitTunnels, exitForwards := scopeConfigForNode("exit", []model.Tunnel{tunnel}, []model.Forward{forward})
	if len(exitTunnels) != 1 || len(exitForwards) != 1 {
		t.Fatalf("expected exit tunnel and forward")
	}
	exitLocal := exitTunnels[0].Stages[1].Nodes[0]
	if exitLocal.Settings["client_key"] != "" {
		t.Fatal("listener exit node must not receive client key")
	}
	if exitLocal.Settings["server_key"] == "" {
		t.Fatal("listener exit node must receive server key")
	}
	if exitForwards[0].Target == "" {
		t.Fatal("exit node must receive target")
	}
}
