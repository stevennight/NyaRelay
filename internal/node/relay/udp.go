package relay

import (
	"context"
	"fmt"
	"net"
	"time"

	"nyarelay/internal/shared/model"
	"nyarelay/internal/shared/protocol"
)

func (s *Service) listenUDPRoute(ctx context.Context, route model.Route) error {
	addr, err := net.ResolveUDPAddr("udp", route.Listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp route %s: %w", route.Name, err)
	}
	s.servers = append(s.servers, conn)
	s.log.Info("udp route listening", "route", route.Name, "listen", route.Listen)
	go s.udpLoop(ctx, route, conn)
	return nil
}

func (s *Service) udpLoop(ctx context.Context, route model.Route, inbound *net.UDPConn) {
	buf := make([]byte, 64*1024)
	for {
		_ = inbound.SetReadDeadline(time.Now().Add(time.Second))
		n, clientAddr, err := inbound.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		payload := append([]byte(nil), buf[:n]...)
		go s.handleUDPPacket(ctx, route, inbound, clientAddr, payload)
	}
}

func (s *Service) handleUDPPacket(ctx context.Context, route model.Route, inbound *net.UDPConn, clientAddr *net.UDPAddr, payload []byte) {
	counter := s.routes.Get(route.ID)
	counter.AddConnection()
	response, statID, err := s.forwardUDP(ctx, route, 0, payload)
	if err != nil {
		s.log.Debug("udp forward failed", "route", route.Name, "error", err)
		return
	}
	if statID != "" {
		linkCounter := s.links.Get(statID)
		linkCounter.AddIn(int64(len(payload)))
		linkCounter.AddOut(int64(len(response)))
	}
	counter.AddIn(int64(len(payload)))
	if len(response) > 0 {
		_, _ = inbound.WriteToUDP(response, clientAddr)
		counter.AddOut(int64(len(response)))
	}
}

func (s *Service) listenUDPLink(ctx context.Context, link model.Link) error {
	addr, err := net.ResolveUDPAddr("udp", link.BindAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp link %s: %w", link.Name, err)
	}
	s.servers = append(s.servers, conn)
	s.log.Info("udp link listening", "link", link.Name, "listen", link.BindAddr)
	go func() {
		buf := make([]byte, protocol.MaxUDPPacket)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			frame := append([]byte(nil), buf[:n]...)
			go s.handleUDPLinkPacket(ctx, link, conn, remote, frame)
		}
	}()
	return nil
}

func (s *Service) handleUDPLinkPacket(ctx context.Context, link model.Link, conn *net.UDPConn, remote *net.UDPAddr, frame []byte) {
	header, payload, err := protocol.DecodeUDPFrame(frame)
	if err != nil {
		s.log.Debug("invalid udp frame", "link", link.Name, "error", err)
		return
	}
	if link.Settings["secret"] != "" && header.Secret != link.Settings["secret"] {
		s.log.Warn("udp relay secret rejected", "link", link.Name, "route", header.RouteID)
		return
	}
	route, ok := s.findRoute(header.RouteID)
	if !ok || !route.Enabled {
		return
	}
	response, statID, err := s.forwardUDP(ctx, route, header.HopIndex+1, payload)
	if err != nil {
		s.log.Debug("udp next hop failed", "link", link.Name, "error", err)
		return
	}
	if statID != "" {
		linkCounter := s.links.Get(statID)
		linkCounter.AddIn(int64(len(payload)))
		linkCounter.AddOut(int64(len(response)))
	}
	out, err := protocol.EncodeUDPFrame(protocol.UDPHeader{
		RouteID:  route.ID,
		HopIndex: header.HopIndex,
		Secret:   header.Secret,
	}, response)
	if err != nil {
		return
	}
	_, _ = conn.WriteToUDP(out, remote)
}

func (s *Service) forwardUDP(ctx context.Context, route model.Route, hopIndex int, payload []byte) ([]byte, string, error) {
	if hopIndex >= len(route.Hops) {
		response, err := udpRoundTrip(ctx, route.Target, payload)
		return response, "target:" + route.ID, err
	}
	hop := route.Hops[hopIndex]
	link, ok := s.findLink(hop.LinkID)
	if !ok || !link.Enabled {
		return nil, "", fmt.Errorf("link %s is not available", hop.LinkID)
	}
	if link.Type != model.LinkDirect {
		return nil, link.ID, fmt.Errorf("udp hops currently require direct links, got %s", link.Type)
	}
	secret := ""
	if link.Settings != nil {
		secret = link.Settings["secret"]
	}
	frame, err := protocol.EncodeUDPFrame(protocol.UDPHeader{
		RouteID:  route.ID,
		HopIndex: hopIndex,
		Secret:   secret,
	}, payload)
	if err != nil {
		return nil, link.ID, err
	}
	responseFrame, err := udpRoundTrip(ctx, link.PublicAddr, frame)
	if err != nil {
		return nil, link.ID, err
	}
	_, response, err := protocol.DecodeUDPFrame(responseFrame)
	return response, link.ID, err
}

func udpRoundTrip(ctx context.Context, addr string, payload []byte) ([]byte, error) {
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	out, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = out.SetDeadline(deadline)
	_, err = out.Write(payload)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, protocol.MaxUDPPacket)
	n, err := out.Read(buf)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), buf[:n]...), nil
}
