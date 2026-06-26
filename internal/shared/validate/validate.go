package validate

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"nyarelay/internal/shared/model"
)

func Route(route model.Route) error {
	if strings.TrimSpace(route.ID) == "" {
		return errors.New("route id is required")
	}
	if strings.TrimSpace(route.Name) == "" {
		return errors.New("route name is required")
	}
	if route.Protocol != model.ProtocolTCP && route.Protocol != model.ProtocolUDP {
		return fmt.Errorf("unsupported route protocol %q", route.Protocol)
	}
	if strings.TrimSpace(route.EntryNode) == "" {
		return errors.New("entry node is required")
	}
	if _, _, err := net.SplitHostPort(route.Listen); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if _, _, err := net.SplitHostPort(route.Target); err != nil {
		return fmt.Errorf("invalid target address: %w", err)
	}
	return nil
}

func Link(link model.Link) error {
	if strings.TrimSpace(link.ID) == "" {
		return errors.New("link id is required")
	}
	if strings.TrimSpace(link.Name) == "" {
		return errors.New("link name is required")
	}
	switch link.Type {
	case model.LinkDirect, model.LinkTLS, model.LinkMTLS, model.LinkWSTLS:
	default:
		return fmt.Errorf("unsupported link type %q", link.Type)
	}
	if strings.TrimSpace(link.FromNode) == "" || strings.TrimSpace(link.ToNode) == "" {
		return errors.New("from_node and to_node are required")
	}
	if strings.TrimSpace(link.BindAddr) == "" {
		return errors.New("bind address is required")
	}
	if _, _, err := net.SplitHostPort(link.BindAddr); err != nil {
		return fmt.Errorf("invalid bind address: %w", err)
	}
	if _, _, err := net.SplitHostPort(link.PublicAddr); err != nil {
		return fmt.Errorf("invalid public address: %w", err)
	}
	return nil
}
