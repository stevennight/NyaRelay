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
		if err := validateStageStrategy(index, "strategy", stage.Strategy); err != nil {
			return err
		}
		if err := validateStageStrategy(index, "tcp_strategy", stage.TCPStrategy); err != nil {
			return err
		}
		if err := validateStageStrategy(index, "udp_strategy", stage.UDPStrategy); err != nil {
			return err
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
			if err := validateStageNodeProtocols(index, node); err != nil {
				return err
			}
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

func validateStageStrategy(index int, field, strategy string) error {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "single", "round_robin", "random", "failover":
		return nil
	default:
		return fmt.Errorf("unsupported stage %d %s %q", index, field, strategy)
	}
}

func validateStageNodeProtocols(index int, node model.TunnelStageNode) error {
	seen := map[model.ForwardProtocol]bool{}
	for _, protocol := range node.Protocols {
		switch protocol {
		case model.ForwardProtocolTCP, model.ForwardProtocolUDP:
			if seen[protocol] {
				return fmt.Errorf("stage %d node %s has duplicate protocol %q", index, node.NodeID, protocol)
			}
			seen[protocol] = true
		default:
			return fmt.Errorf("unsupported stage %d node %s protocol %q", index, node.NodeID, protocol)
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
	if err := validateForwardStrategy("strategy", forward.Strategy); err != nil {
		return err
	}
	if err := validateForwardStrategy("tcp_strategy", forward.TCPStrategy); err != nil {
		return err
	}
	if err := validateForwardStrategy("udp_strategy", forward.UDPStrategy); err != nil {
		return err
	}
	targets := forward.Targets
	if len(targets) == 0 && strings.TrimSpace(forward.Target) != "" {
		targets = []model.ForwardTarget{{Address: forward.Target, Enabled: true}}
	}
	if len(targets) == 0 {
		return errors.New("forward target is required")
	}
	seenIDs := map[string]bool{}
	protocolTargets := map[model.ForwardProtocol]bool{}
	for index, target := range targets {
		if strings.TrimSpace(target.ID) != "" {
			if seenIDs[target.ID] {
				return fmt.Errorf("duplicate forward target id %q", target.ID)
			}
			seenIDs[target.ID] = true
		}
		if strings.TrimSpace(target.Address) == "" {
			return fmt.Errorf("forward target %d address is required", index+1)
		}
		if _, _, err := net.SplitHostPort(target.Address); err != nil {
			return fmt.Errorf("invalid target %d address: %w", index+1, err)
		}
		if err := validateForwardTargetProtocols(index, target); err != nil {
			return err
		}
		if target.Enabled {
			for _, protocol := range forward.Protocols {
				if targetSupportsProtocol(target.Protocols, protocol) {
					protocolTargets[protocol] = true
				}
			}
		}
	}
	for _, protocol := range forward.Protocols {
		if !protocolTargets[protocol] {
			return fmt.Errorf("forward has no enabled %s target", protocol)
		}
	}
	return nil
}

func validateForwardStrategy(field, strategy string) error {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "single", "round_robin", "random", "failover":
		return nil
	default:
		return fmt.Errorf("unsupported forward %s %q", field, strategy)
	}
}

func validateForwardTargetProtocols(index int, target model.ForwardTarget) error {
	seen := map[model.ForwardProtocol]bool{}
	for _, protocol := range target.Protocols {
		switch protocol {
		case model.ForwardProtocolTCP, model.ForwardProtocolUDP:
			if seen[protocol] {
				return fmt.Errorf("forward target %d has duplicate protocol %q", index+1, protocol)
			}
			seen[protocol] = true
		default:
			return fmt.Errorf("unsupported forward target %d protocol %q", index+1, protocol)
		}
	}
	return nil
}

func targetSupportsProtocol(protocols []model.ForwardProtocol, protocol model.ForwardProtocol) bool {
	if len(protocols) == 0 {
		return true
	}
	for _, candidate := range protocols {
		if candidate == protocol {
			return true
		}
	}
	return false
}
