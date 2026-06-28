package validate

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"nyarelay/internal/shared/model"
)

func Tunnel(tunnel model.Tunnel) error {
	if strings.TrimSpace(tunnel.ID) == "" {
		return errors.New("tunnel id is required")
	}
	if strings.TrimSpace(tunnel.Name) == "" {
		return errors.New("tunnel name is required")
	}
	switch tunnel.Type {
	case model.TunnelDirect, model.TunnelChain:
	default:
		return fmt.Errorf("unsupported tunnel type %q", tunnel.Type)
	}
	switch tunnel.Transport {
	case model.TunnelTransportDirect, model.TunnelTransportTLS, model.TunnelTransportMTLS, model.TunnelTransportWSTLS:
	default:
		return fmt.Errorf("unsupported tunnel transport %q", tunnel.Transport)
	}
	if tunnel.Type == model.TunnelDirect && tunnel.Transport != model.TunnelTransportDirect {
		return errors.New("direct tunnel transport must be direct")
	}
	if len(tunnel.Stages) == 0 {
		return errors.New("tunnel stages are required")
	}
	if tunnel.Type == model.TunnelDirect && len(tunnel.Stages) != 1 {
		return errors.New("direct tunnel must have exactly one entry stage")
	}
	if tunnel.Type == model.TunnelChain && len(tunnel.Stages) < 2 {
		return errors.New("chain tunnel must have entry and exit stages")
	}
	seenNodes := map[string]bool{}
	for index, stage := range tunnel.Stages {
		if stage.Index != index {
			return fmt.Errorf("stage index must be contiguous at %d", index)
		}
		expectedRole := model.TunnelStageMiddle
		if index == 0 {
			expectedRole = model.TunnelStageEntry
		} else if index == len(tunnel.Stages)-1 {
			expectedRole = model.TunnelStageExit
		}
		if tunnel.Type == model.TunnelDirect {
			expectedRole = model.TunnelStageEntry
		}
		if stage.Role != expectedRole {
			return fmt.Errorf("stage %d role must be %s", index, expectedRole)
		}
		switch strings.ToLower(strings.TrimSpace(stage.Strategy)) {
		case "", "single", "round_robin", "random", "failover":
		default:
			return fmt.Errorf("unsupported stage %d strategy %q", index, stage.Strategy)
		}
		if len(stage.Nodes) == 0 {
			return fmt.Errorf("stage %d must have at least one node", index)
		}
		for _, node := range stage.Nodes {
			if strings.TrimSpace(node.NodeID) == "" {
				return fmt.Errorf("stage %d node id is required", index)
			}
			if seenNodes[node.NodeID] {
				return fmt.Errorf("node %s appears more than once in tunnel", node.NodeID)
			}
			seenNodes[node.NodeID] = true
			if tunnel.Type == model.TunnelChain && stage.Role != model.TunnelStageEntry {
				if strings.TrimSpace(node.ListenAddr) == "" {
					return fmt.Errorf("stage %d listen address is required", index)
				}
				if _, _, err := net.SplitHostPort(node.ListenAddr); err != nil {
					return fmt.Errorf("invalid stage %d listen address: %w", index, err)
				}
				if strings.TrimSpace(node.PublicAddr) == "" && strings.TrimSpace(node.ConnectAddr) == "" {
					return fmt.Errorf("stage %d requires public_addr or connect_addr", index)
				}
				if node.PublicAddr != "" {
					if _, _, err := net.SplitHostPort(node.PublicAddr); err != nil {
						return fmt.Errorf("invalid stage %d public address: %w", index, err)
					}
				}
				if node.ConnectAddr != "" {
					if _, _, err := net.SplitHostPort(node.ConnectAddr); err != nil {
						return fmt.Errorf("invalid stage %d connect address: %w", index, err)
					}
				}
			}
		}
	}
	return nil
}

func Forward(forward model.Forward) error {
	if strings.TrimSpace(forward.ID) == "" {
		return errors.New("forward id is required")
	}
	if strings.TrimSpace(forward.Name) == "" {
		return errors.New("forward name is required")
	}
	if strings.TrimSpace(forward.TunnelID) == "" {
		return errors.New("tunnel id is required")
	}
	if len(forward.Protocols) == 0 {
		return errors.New("forward protocol is required")
	}
	for _, protocol := range forward.Protocols {
		if protocol != model.ForwardProtocolTCP && protocol != model.ForwardProtocolUDP {
			return fmt.Errorf("unsupported forward protocol %q", protocol)
		}
	}
	if _, _, err := net.SplitHostPort(forward.Listen); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if _, _, err := net.SplitHostPort(forward.Target); err != nil {
		return fmt.Errorf("invalid target address: %w", err)
	}
	return nil
}
