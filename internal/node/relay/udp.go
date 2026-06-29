package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"nyarelay/internal/node/metrics"
	"nyarelay/internal/shared/model"
	"nyarelay/internal/shared/protocol"
)

func (s *Service) listenUDPForward(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime) error {
	addr, err := net.ResolveUDPAddr("udp", forward.Listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp forward %s: %w", forward.Name, err)
	}
	s.servers = append(s.servers, conn)
	s.log.Info("forward udp listening", "forward", forward.Name, "listen", forward.Listen)
	go s.udpLoop(ctx, forward, tunnel, conn)
	return nil
}

func (s *Service) udpLoop(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime, inbound *net.UDPConn) {
	buf := make([]byte, protocol.MaxUDPPacket)
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
		go s.handleUDPPacket(ctx, forward, tunnel, inbound, clientAddr, payload)
	}
}

func (s *Service) handleUDPPacket(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime, inbound *net.UDPConn, clientAddr *net.UDPAddr, payload []byte) {
	counter := s.forwards.Get("forward:" + forward.ID)
	counter.AddConnection()
	var response []byte
	var statID string
	var err error
	if tunnel.Type == model.TunnelDirect {
		response, err = udpRoundTrip(ctx, forward.Target, payload)
		statID = "target:" + forward.ID
	} else {
		sessionID := forward.ID + ":" + clientAddr.String()
		response, statID, err = s.forwardUDPOverTunnel(ctx, tunnel, forward, sessionID, payload)
	}
	if err != nil {
		s.log.Debug("udp forward failed", "forward", forward.Name, "error", err)
		return
	}
	if statID != "" {
		tunnelCounter := s.tunnels.Get(statID)
		tunnelCounter.AddIn(int64(len(payload)))
		tunnelCounter.AddOut(int64(len(response)))
	}
	counter.AddIn(int64(len(payload)))
	if len(response) > 0 {
		_, _ = inbound.WriteToUDP(response, clientAddr)
		counter.AddOut(int64(len(response)))
	}
}

func (s *Service) forwardUDPOverTunnel(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, sessionID string, payload []byte) ([]byte, string, error) {
	frame := protocol.UDPDatagramFrame{
		ForwardID: forward.ID,
		SessionID: sessionID,
		Payload:   payload,
	}
	response, statID, err := s.forwardUDPFrameWithRetry(ctx, tunnel, forward, 0, frame)
	return response.Payload, statID, err
}

func (s *Service) handleUDPStageTransit(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, stage model.TunnelRuntimeStage, inbound net.Conn, counter *metrics.Counter) {
	frame, err := protocol.ReadUDPDatagramFrame(inbound)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			s.log.Debug("udp transit read failed", "forward", forward.Name, "error", err)
		}
		return
	}
	if frame.ForwardID != forward.ID {
		return
	}
	response, statID, err := s.forwardUDPFrameWithRetry(ctx, tunnel, forward, stage.Index, frame)
	if err != nil {
		s.log.Warn("udp transit failed", "forward", forward.Name, "error", err)
		return
	}
	counter.AddIn(int64(len(frame.Payload)))
	counter.AddOut(int64(len(response.Payload)))
	tunnelCounter := s.tunnels.Get(statID)
	tunnelCounter.AddIn(int64(len(frame.Payload)))
	tunnelCounter.AddOut(int64(len(response.Payload)))
	if err := protocol.WriteUDPDatagramFrame(inbound, response); err != nil {
		s.markUDPStreamFailure(tunnel, forward, stage.Index, statID, frame.SessionID)
		return
	}
}

func (s *Service) forwardUDPFrameWithRetry(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, fromStageIndex int, frame protocol.UDPDatagramFrame) (protocol.UDPDatagramFrame, string, error) {
	attempts := s.udpCandidateAttemptLimit(tunnel, fromStageIndex)
	var lastErr error
	var lastStatID string
	for attempt := 0; attempt < attempts; attempt++ {
		stream, statID, err := s.dialForwardNext(ctx, tunnel, forward, fromStageIndex, "udp", frame.SessionID)
		lastStatID = statID
		if err != nil {
			lastErr = err
			continue
		}
		response, err := s.writeReadUDPFrame(stream, tunnel, forward, fromStageIndex, statID, frame)
		_ = stream.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return response, statID, nil
	}
	if lastErr == nil {
		lastErr = errors.New("udp candidate attempts exhausted")
	}
	return protocol.UDPDatagramFrame{}, lastStatID, lastErr
}

func (s *Service) writeReadUDPFrame(stream net.Conn, tunnel model.TunnelRuntime, forward model.ForwardRuntime, fromStageIndex int, statID string, frame protocol.UDPDatagramFrame) (protocol.UDPDatagramFrame, error) {
	if err := protocol.WriteUDPDatagramFrame(stream, frame); err != nil {
		s.markUDPStreamFailure(tunnel, forward, fromStageIndex, statID, frame.SessionID)
		return protocol.UDPDatagramFrame{}, err
	}
	response, err := protocol.ReadUDPDatagramFrame(stream)
	if err != nil {
		s.markUDPStreamFailure(tunnel, forward, fromStageIndex, statID, frame.SessionID)
		return protocol.UDPDatagramFrame{}, err
	}
	if response.ForwardID != forward.ID || response.SessionID != frame.SessionID {
		s.markUDPStreamFailure(tunnel, forward, fromStageIndex, statID, frame.SessionID)
		return protocol.UDPDatagramFrame{}, errors.New("udp response frame mismatch")
	}
	return response, nil
}

func (s *Service) udpCandidateAttemptLimit(tunnel model.TunnelRuntime, fromStageIndex int) int {
	nextIndex := fromStageIndex + 1
	if tunnel.Type == model.TunnelDirect || nextIndex >= len(tunnel.Stages) {
		return 1
	}
	count := 0
	for _, node := range tunnel.Stages[nextIndex].Nodes {
		if runtimeNodeSupportsProtocol(node, model.ForwardProtocolUDP) {
			count++
		}
	}
	if count <= 0 {
		return 1
	}
	return count
}

func (s *Service) handleUDPStageExit(ctx context.Context, forward model.ForwardRuntime, inbound net.Conn, counter *metrics.Counter) {
	for {
		frame, err := protocol.ReadUDPDatagramFrame(inbound)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Debug("udp stream read failed", "forward", forward.Name, "error", err)
			}
			return
		}
		if frame.ForwardID != forward.ID {
			return
		}
		response, err := udpRoundTrip(ctx, forward.Target, frame.Payload)
		if err != nil {
			s.log.Debug("udp target round trip failed", "forward", forward.Name, "error", err)
			return
		}
		counter.AddIn(int64(len(frame.Payload)))
		counter.AddOut(int64(len(response)))
		if err := protocol.WriteUDPDatagramFrame(inbound, protocol.UDPDatagramFrame{
			ForwardID: forward.ID,
			SessionID: frame.SessionID,
			Payload:   response,
		}); err != nil {
			return
		}
	}
}

func (s *Service) markUDPStreamFailure(tunnel model.TunnelRuntime, forward model.ForwardRuntime, fromStageIndex int, statID, sessionID string) {
	nextIndex := fromStageIndex + 1
	if nextIndex >= len(tunnel.Stages) {
		return
	}
	candidateNodeID, ok := candidateNodeIDFromStatID(statID)
	if !ok {
		return
	}
	s.selector.recordFailure(tunnel.ID, nextIndex, candidateNodeID, "udp")
	s.udp.delete(udpSessionKey(tunnel.ID, forward.ID, fromStageIndex, nextIndex, sessionID))
}

func candidateNodeIDFromStatID(statID string) (string, bool) {
	const marker = ":candidate:"
	idx := strings.LastIndex(statID, marker)
	if idx < 0 {
		return "", false
	}
	nodeID := statID[idx+len(marker):]
	return nodeID, nodeID != ""
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
	if _, err := out.Write(payload); err != nil {
		return nil, err
	}
	buf := make([]byte, protocol.MaxUDPPacket)
	n, err := out.Read(buf)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), buf[:n]...), nil
}
