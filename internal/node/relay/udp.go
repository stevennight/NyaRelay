package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
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
			if ctx.Err() != nil {
				return
			}
			continue
		}
		payload := append([]byte(nil), buf[:n]...)
		s.submitUDPEntryPacket(ctx, forward, tunnel, inbound, clientAddr, payload)
	}
}

func watchConnContext(ctx context.Context, conn net.Conn) func() {
	stop := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	return func() {
		stopOnce.Do(func() { close(stop) })
	}
}

func (s *Service) readInitialUDPFrame(ctx context.Context, conn net.Conn) (protocol.UDPDatagramFrame, error) {
	stopWatch := watchConnContext(ctx, conn)
	defer stopWatch()
	deadline := time.Now().Add(udpSessionFirstFrameLimit)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetReadDeadline(deadline)
	frame, err := protocol.ReadUDPDatagramFrame(conn)
	_ = conn.SetReadDeadline(time.Time{})
	return frame, err
}

func validateInitialUDPFrame(frame protocol.UDPDatagramFrame, forwardID string) error {
	if frame.ForwardID != forwardID {
		return errors.New("udp frame forward mismatch")
	}
	if frame.SessionID == "" {
		return errors.New("udp frame session is empty")
	}
	return nil
}

func writeUDPFrameWithDeadline(conn net.Conn, frame protocol.UDPDatagramFrame) error {
	_ = conn.SetWriteDeadline(time.Now().Add(udpSessionWriteTimeout))
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	return protocol.WriteUDPDatagramFrame(conn, frame)
}

func writeUDPConnPayload(conn net.Conn, payload []byte) error {
	_ = conn.SetWriteDeadline(time.Now().Add(udpSessionWriteTimeout))
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	n, err := conn.Write(payload)
	if err != nil {
		return err
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

type udpPumpSide uint8

const (
	udpPumpUpstream udpPumpSide = iota
	udpPumpDownstream
)

type udpPumpResult struct {
	side        udpPumpSide
	err         error
	writeFailed bool
}

type udpSessionEndReason uint8

const (
	udpSessionEndedByPump udpSessionEndReason = iota
	udpSessionEndedByContext
	udpSessionEndedByIdle
)

type udpPersistentSessionResult struct {
	reason udpSessionEndReason
	pump   udpPumpResult
}

func runUDPPersistentSession(ctx context.Context, idleTimeout time.Duration, closeConnections func(), pumps ...func(context.Context, func()) udpPumpResult) udpPersistentSessionResult {
	if idleTimeout <= 0 {
		idleTimeout = udpSessionIdleTimeout
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	activity := make(chan struct{}, 1)
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	results := make(chan udpPumpResult, len(pumps))
	var wait sync.WaitGroup
	for _, pump := range pumps {
		pump := pump
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- pump(sessionCtx, touch)
		}()
	}

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	resetIdle := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idleTimeout)
	}

	var result udpPersistentSessionResult
selectLoop:
	for {
		select {
		case pumpResult := <-results:
			result.reason = udpSessionEndedByPump
			result.pump = pumpResult
			break selectLoop
		case <-ctx.Done():
			result.reason = udpSessionEndedByContext
			break selectLoop
		case <-timer.C:
			result.reason = udpSessionEndedByIdle
			break selectLoop
		case <-activity:
			resetIdle()
		}
	}

	cancel()
	closeConnections()
	wait.Wait()
	return result
}

func (s *Service) handleUDPStageTransit(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, stage model.TunnelRuntimeStage, inbound net.Conn, counter *metrics.Counter) {
	first, err := s.readInitialUDPFrame(ctx, inbound)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			s.log.Debug("udp transit first frame read failed", "forward", forward.Name, "error", err)
		}
		return
	}
	if err := validateInitialUDPFrame(first, forward.ID); err != nil {
		s.log.Debug("udp transit first frame rejected", "forward", forward.Name, "error", err)
		return
	}
	next, statID, err := s.dialForwardNext(ctx, tunnel, forward, stage.Index, "udp", first.SessionID)
	if err != nil {
		s.log.Warn("udp transit next stage dial failed", "forward", forward.Name, "error", err)
		return
	}
	defer s.closeConn(next, "udp transit next outbound")
	if err := writeUDPFrameWithDeadline(next, first); err != nil {
		s.markUDPStreamFailure(tunnel, forward, stage.Index, statID, first.SessionID)
		s.log.Debug("udp transit first frame write failed", "forward", forward.Name, "error", err)
		return
	}
	counter.AddIn(int64(len(first.Payload)))
	if statID != "" {
		s.tunnels.Get(statID).AddIn(int64(len(first.Payload)))
	}

	responseSeen := false
	result := runUDPPersistentSession(ctx, s.udpIdleTimeout(), func() {
		_ = inbound.Close()
		_ = next.Close()
	},
		func(sessionCtx context.Context, touch func()) udpPumpResult {
			return s.relayUDPFramePump(sessionCtx, inbound, next, forward.ID, first.SessionID, udpPumpUpstream, counter, statID, nil, touch)
		},
		func(sessionCtx context.Context, touch func()) udpPumpResult {
			return s.relayUDPFramePump(sessionCtx, next, inbound, forward.ID, first.SessionID, udpPumpDownstream, counter, statID, &responseSeen, touch)
		},
	)
	if result.reason == udpSessionEndedByPump && result.pump.side == udpPumpDownstream && !result.pump.writeFailed {
		if !errors.Is(result.pump.err, io.EOF) || !responseSeen {
			s.markUDPStreamFailure(tunnel, forward, stage.Index, statID, first.SessionID)
		}
	}
	if result.reason == udpSessionEndedByPump && result.pump.side == udpPumpUpstream && result.pump.writeFailed {
		s.markUDPStreamFailure(tunnel, forward, stage.Index, statID, first.SessionID)
	}
	if result.reason == udpSessionEndedByPump && result.pump.err != nil && !errors.Is(result.pump.err, io.EOF) && !errors.Is(result.pump.err, net.ErrClosed) {
		s.log.Debug("udp transit session ended", "forward", forward.Name, "error", result.pump.err)
	}
}

func (s *Service) relayUDPFramePump(ctx context.Context, src, dst net.Conn, forwardID, sessionID string, side udpPumpSide, counter *metrics.Counter, statID string, responseSeen *bool, touch func()) udpPumpResult {
	for {
		frame, err := protocol.ReadUDPDatagramFrame(src)
		if err != nil {
			return udpPumpResult{side: side, err: err}
		}
		if frame.ForwardID != forwardID || frame.SessionID != sessionID {
			return udpPumpResult{side: side, err: errors.New("udp frame session mismatch")}
		}
		if err := writeUDPFrameWithDeadline(dst, frame); err != nil {
			return udpPumpResult{side: side, err: err, writeFailed: true}
		}
		if side == udpPumpDownstream && responseSeen != nil {
			*responseSeen = true
		}
		touch()
		if side == udpPumpUpstream {
			counter.AddIn(int64(len(frame.Payload)))
			if statID != "" {
				s.tunnels.Get(statID).AddIn(int64(len(frame.Payload)))
			}
		} else {
			counter.AddOut(int64(len(frame.Payload)))
			if statID != "" {
				s.tunnels.Get(statID).AddOut(int64(len(frame.Payload)))
			}
		}
	}
}

func (s *Service) handleUDPStageExit(ctx context.Context, forward model.ForwardRuntime, inbound net.Conn, counter *metrics.Counter) {
	first, err := s.readInitialUDPFrame(ctx, inbound)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			s.log.Debug("udp exit first frame read failed", "forward", forward.Name, "error", err)
		}
		return
	}
	if err := validateInitialUDPFrame(first, forward.ID); err != nil {
		s.log.Debug("udp exit first frame rejected", "forward", forward.Name, "error", err)
		return
	}
	out, statID, targetID, err := s.openUDPStageTarget(ctx, forward, first.SessionID, first.Payload)
	if err != nil {
		s.log.Debug("udp exit target open failed", "forward", forward.Name, "error", err)
		return
	}
	defer s.closeConn(out, "udp exit target outbound")
	counter.AddIn(int64(len(first.Payload)))
	if statID != "" {
		s.tunnels.Get(statID).AddIn(int64(len(first.Payload)))
	}

	responseSeen := false
	result := runUDPPersistentSession(ctx, s.udpIdleTimeout(), func() {
		_ = inbound.Close()
		_ = out.Close()
	},
		func(sessionCtx context.Context, touch func()) udpPumpResult {
			return s.relayUDPFramesToTarget(sessionCtx, inbound, out, forward.ID, first.SessionID, counter, statID, touch)
		},
		func(sessionCtx context.Context, touch func()) udpPumpResult {
			return s.relayUDPTargetToFrames(sessionCtx, out, inbound, forward.ID, first.SessionID, counter, statID, &responseSeen, touch)
		},
	)
	if result.reason == udpSessionEndedByPump && result.pump.side == udpPumpDownstream && !result.pump.writeFailed {
		if !errors.Is(result.pump.err, io.EOF) || !responseSeen {
			s.markUDPTargetFailure(forward.ID, first.SessionID, targetID)
		}
	}
	if result.reason == udpSessionEndedByPump && result.pump.side == udpPumpUpstream && result.pump.writeFailed {
		s.markUDPTargetFailure(forward.ID, first.SessionID, targetID)
	}
	if result.reason == udpSessionEndedByPump && result.pump.err != nil && !errors.Is(result.pump.err, io.EOF) && !errors.Is(result.pump.err, net.ErrClosed) {
		s.log.Debug("udp exit session ended", "forward", forward.Name, "error", result.pump.err)
	}
}

func (s *Service) openUDPStageTarget(ctx context.Context, forward model.ForwardRuntime, sessionID string, firstPayload []byte) (net.Conn, string, string, error) {
	attempts := len(forward.Targets)
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		out, statID, targetID, err := s.dialForwardTarget(ctx, forward, "udp", sessionID)
		if err != nil {
			lastErr = err
			continue
		}
		if err := writeUDPConnPayload(out, firstPayload); err != nil {
			s.markUDPTargetFailure(forward.ID, sessionID, targetID)
			_ = out.Close()
			lastErr = err
			continue
		}
		return out, statID, targetID, nil
	}
	if lastErr == nil {
		lastErr = errors.New("udp target attempts exhausted")
	}
	return nil, "target:" + forward.ID, "", lastErr
}

func (s *Service) relayUDPFramesToTarget(ctx context.Context, inbound, target net.Conn, forwardID, sessionID string, counter *metrics.Counter, statID string, touch func()) udpPumpResult {
	for {
		frame, err := protocol.ReadUDPDatagramFrame(inbound)
		if err != nil {
			return udpPumpResult{side: udpPumpUpstream, err: err}
		}
		if frame.ForwardID != forwardID || frame.SessionID != sessionID {
			return udpPumpResult{side: udpPumpUpstream, err: errors.New("udp frame session mismatch")}
		}
		if err := writeUDPConnPayload(target, frame.Payload); err != nil {
			return udpPumpResult{side: udpPumpUpstream, err: err, writeFailed: true}
		}
		touch()
		counter.AddIn(int64(len(frame.Payload)))
		if statID != "" {
			s.tunnels.Get(statID).AddIn(int64(len(frame.Payload)))
		}
	}
}

func (s *Service) relayUDPTargetToFrames(ctx context.Context, target, inbound net.Conn, forwardID, sessionID string, counter *metrics.Counter, statID string, responseSeen *bool, touch func()) udpPumpResult {
	buf := make([]byte, protocol.MaxUDPPacket)
	for {
		n, err := target.Read(buf)
		if err != nil {
			return udpPumpResult{side: udpPumpDownstream, err: err}
		}
		if err := writeUDPFrameWithDeadline(inbound, protocol.UDPDatagramFrame{
			ForwardID: forwardID,
			SessionID: sessionID,
			Payload:   append([]byte(nil), buf[:n]...),
		}); err != nil {
			return udpPumpResult{side: udpPumpDownstream, err: err, writeFailed: true}
		}
		*responseSeen = true
		touch()
		counter.AddOut(int64(n))
		if statID != "" {
			s.tunnels.Get(statID).AddOut(int64(n))
		}
	}
}

func (s *Service) udpIdleTimeout() time.Duration {
	timeout := s.udp.idleTimeout
	if timeout <= 0 {
		timeout = udpSessionIdleTimeout
	}
	return timeout
}

func (s *Service) markUDPTargetFailure(forwardID, sessionID, targetID string) {
	if targetID == "" {
		return
	}
	s.targets.recordFailure(forwardID, targetID, "udp")
	if sessionID != "" {
		s.udpTargets.delete(udpTargetSessionKey(forwardID, sessionID))
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

func udpRoundTrip(ctx context.Context, addr string, payload []byte) (response []byte, err error) {
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	out, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
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
