package validate

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode"
	"unicode/utf8"

	"nyarelay/internal/shared/model"
)

const (
	maxSelectionWeight   = 100
	MaxIDBytes           = 128
	MaxNameBytes         = 256
	MaxAddressBytes      = 512
	MaxStrategyBytes     = 32
	MaxTunnelStages      = 16
	MaxStageNodes        = 32
	MaxForwardTargets    = 128
	maxSettingsEntries   = 32
	maxSettingKeyBytes   = 64
	maxSettingValueBytes = 128 << 10
)

func Tunnel(tunnel model.Tunnel) error {
	if err := validateText("tunnel id", tunnel.ID, MaxIDBytes, false); err != nil {
		return err
	}
	if err := validateText("tunnel name", tunnel.Name, MaxNameBytes, false); err != nil {
		return err
	}
	if tunnel.EntryAddress != "" {
		if err := validateText("tunnel entry address", tunnel.EntryAddress, MaxAddressBytes, true); err != nil {
			return err
		}
	}
	if err := validateSettings("tunnel settings", tunnel.Settings); err != nil {
		return err
	}
	if len(tunnel.Stages) > MaxTunnelStages {
		return fmt.Errorf("tunnel has too many stages (maximum %d)", MaxTunnelStages)
	}
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
		if stage.ID != "" {
			if err := validateText(fmt.Sprintf("stage %d id", index), stage.ID, MaxIDBytes, true); err != nil {
				return err
			}
		}
		if err := validateText(fmt.Sprintf("stage %d strategy", index), stage.Strategy, MaxStrategyBytes, true); err != nil {
			return err
		}
		if err := validateText(fmt.Sprintf("stage %d tcp strategy", index), stage.TCPStrategy, MaxStrategyBytes, true); err != nil {
			return err
		}
		if err := validateText(fmt.Sprintf("stage %d udp strategy", index), stage.UDPStrategy, MaxStrategyBytes, true); err != nil {
			return err
		}
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
		if len(stage.Nodes) > MaxStageNodes {
			return fmt.Errorf("stage %d has too many nodes (maximum %d)", index, MaxStageNodes)
		}
		for _, node := range stage.Nodes {
			if err := validateText(fmt.Sprintf("stage %d node id", index), node.NodeID, MaxIDBytes, false); err != nil {
				return err
			}
			if err := validateText(fmt.Sprintf("stage %d node address", index), node.ListenAddr, MaxAddressBytes, true); err != nil {
				return err
			}
			if err := validateText(fmt.Sprintf("stage %d node public address", index), node.PublicAddr, MaxAddressBytes, true); err != nil {
				return err
			}
			if err := validateText(fmt.Sprintf("stage %d node connect address", index), node.ConnectAddr, MaxAddressBytes, true); err != nil {
				return err
			}
			if err := validateSettings(fmt.Sprintf("stage %d node settings", index), node.Settings); err != nil {
				return err
			}
			if strings.TrimSpace(node.NodeID) == "" {
				return fmt.Errorf("stage %d node id is required", index)
			}
			if node.Weight > maxSelectionWeight {
				return fmt.Errorf("stage %d node %s weight is too large", index, node.NodeID)
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
	if err := validateText(fmt.Sprintf("stage %d %s", index, field), strategy, MaxStrategyBytes, true); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "single", "round_robin", "random", "failover":
		return nil
	default:
		return fmt.Errorf("unsupported stage %d %s %q", index, field, strategy)
	}
}

func validateStageNodeProtocols(index int, node model.TunnelStageNode) error {
	if len(node.Protocols) > 2 {
		return fmt.Errorf("stage %d node %s has too many protocols", index, node.NodeID)
	}
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
	if err := validateText("forward id", forward.ID, MaxIDBytes, false); err != nil {
		return err
	}
	if err := validateText("forward name", forward.Name, MaxNameBytes, false); err != nil {
		return err
	}
	if err := validateText("forward tunnel id", forward.TunnelID, MaxIDBytes, false); err != nil {
		return err
	}
	if err := validateText("forward listen address", forward.Listen, MaxAddressBytes, false); err != nil {
		return err
	}
	if err := validateText("forward target", forward.Target, MaxAddressBytes, true); err != nil {
		return err
	}
	if err := validateText("forward strategy", forward.Strategy, MaxStrategyBytes, true); err != nil {
		return err
	}
	if err := validateText("forward tcp strategy", forward.TCPStrategy, MaxStrategyBytes, true); err != nil {
		return err
	}
	if err := validateText("forward udp strategy", forward.UDPStrategy, MaxStrategyBytes, true); err != nil {
		return err
	}
	if len(forward.Protocols) > 2 {
		return errors.New("forward has too many protocols")
	}
	if len(forward.Targets) > MaxForwardTargets {
		return fmt.Errorf("forward has too many targets (maximum %d)", MaxForwardTargets)
	}
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
		if err := validateText(fmt.Sprintf("forward target %d id", index+1), target.ID, MaxIDBytes, true); err != nil {
			return err
		}
		if err := validateText(fmt.Sprintf("forward target %d address", index+1), target.Address, MaxAddressBytes, false); err != nil {
			return err
		}
		if strings.TrimSpace(target.ID) != "" {
			if seenIDs[target.ID] {
				return fmt.Errorf("duplicate forward target id %q", target.ID)
			}
			seenIDs[target.ID] = true
		}
		if strings.TrimSpace(target.Address) == "" {
			return fmt.Errorf("forward target %d address is required", index+1)
		}
		if target.Weight > maxSelectionWeight {
			return fmt.Errorf("forward target %d weight is too large", index+1)
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
	if err := validateText("forward "+field, strategy, MaxStrategyBytes, true); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "single", "round_robin", "random", "failover":
		return nil
	default:
		return fmt.Errorf("unsupported forward %s %q", field, strategy)
	}
}

func validateForwardTargetProtocols(index int, target model.ForwardTarget) error {
	if len(target.Protocols) > 2 {
		return fmt.Errorf("forward target %d has too many protocols", index+1)
	}
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

func validateText(field, value string, maxBytes int, allowEmpty bool) error {
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is invalid UTF-8", field)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s is too long", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

func validateSettings(field string, settings map[string]string) error {
	if len(settings) > maxSettingsEntries {
		return fmt.Errorf("%s has too many entries (maximum %d)", field, maxSettingsEntries)
	}
	for key, value := range settings {
		if err := validateText(field+" key", key, maxSettingKeyBytes, false); err != nil {
			return err
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("%s value is invalid UTF-8", field)
		}
		if len(value) > maxSettingValueBytes {
			return fmt.Errorf("%s value is too long", field)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%s value contains a NUL character", field)
		}
	}
	return nil
}
