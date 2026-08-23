package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"nyarelay/internal/shared/model"
	"nyarelay/internal/shared/protocol"
)

const (
	maxUDPActiveSessions      = 4096
	maxUDPSessionQueueEntries = 64
	maxUDPSessionQueueBytes   = 256 * 1024
	maxUDPTransportEvents     = 1
	udpSessionWriteTimeout    = 10 * time.Second
	udpSessionFirstFrameLimit = 10 * time.Second
)

type udpEntrySessions struct {
	mu         sync.Mutex
	maxEntries int
	generation uint64
	sessions   map[string]*udpEntrySession
}

func newUDPEntrySessions() *udpEntrySessions {
	return &udpEntrySessions{
		maxEntries: maxUDPActiveSessions,
		sessions:   make(map[string]*udpEntrySession),
	}
}

func (m *udpEntrySessions) getOrCreate(key string, create func() *udpEntrySession) (*udpEntrySession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.sessions[key]; ok {
		return existing, false
	}
	limit := m.maxEntries
	if limit <= 0 {
		limit = maxUDPActiveSessions
	}
	if len(m.sessions) >= limit {
		return nil, false
	}
	session := create()
	m.sessions[key] = session
	return session, true
}

func (m *udpEntrySessions) delete(key string, session *udpEntrySession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.sessions[key]; ok && current == session {
		delete(m.sessions, key)
	}
}

func (m *udpEntrySessions) closeAll() {
	m.mu.Lock()
	sessions := make([]*udpEntrySession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*udpEntrySession)
	m.mu.Unlock()

	for _, session := range sessions {
		session.close()
	}
}

func (m *udpEntrySessions) watch(ctx context.Context) {
	m.mu.Lock()
	m.generation++
	generation := m.generation
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.closeIfGeneration(generation)
	}()
}

func (m *udpEntrySessions) closeIfGeneration(generation uint64) {
	m.mu.Lock()
	if m.generation != generation {
		m.mu.Unlock()
		return
	}
	sessions := make([]*udpEntrySession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*udpEntrySession)
	m.mu.Unlock()

	for _, session := range sessions {
		session.close()
	}
}

type udpEntrySession struct {
	service   *Service
	key       string
	forward   model.ForwardRuntime
	tunnel    model.TunnelRuntime
	inbound   *net.UDPConn
	client    *net.UDPAddr
	sessionID string
	ctx       context.Context

	queue      chan []byte
	queueMu    sync.Mutex
	queuedByte int

	stateMu   sync.Mutex
	transport *udpSessionTransport
	closed    bool
	done      chan struct{}
}

func newUDPEntrySession(service *Service, key string, forward model.ForwardRuntime, tunnel model.TunnelRuntime, inbound *net.UDPConn, client *net.UDPAddr, ctx context.Context) *udpEntrySession {
	clientCopy := *client
	return &udpEntrySession{
		service:   service,
		key:       key,
		forward:   forward,
		tunnel:    tunnel,
		inbound:   inbound,
		client:    &clientCopy,
		sessionID: forward.ID + ":" + clientCopy.String(),
		ctx:       ctx,
		queue:     make(chan []byte, maxUDPSessionQueueEntries),
		done:      make(chan struct{}),
	}
}

func (s *udpEntrySession) enqueue(payload []byte) bool {
	if len(payload) > protocol.MaxUDPPacket {
		return false
	}
	queued := append([]byte(nil), payload...)
	s.stateMu.Lock()
	if s.closed || s.ctx.Err() != nil || (s.transport != nil && s.transport.isClosed()) {
		s.stateMu.Unlock()
		return false
	}
	s.queueMu.Lock()
	if s.queuedByte+len(queued) > maxUDPSessionQueueBytes {
		s.queueMu.Unlock()
		s.stateMu.Unlock()
		return false
	}
	select {
	case s.queue <- queued:
		s.queuedByte += len(queued)
		s.queueMu.Unlock()
		s.stateMu.Unlock()
		return true
	default:
		s.queueMu.Unlock()
		s.stateMu.Unlock()
		return false
	}
}

func (s *udpEntrySession) isUnavailable() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.closed || s.ctx.Err() != nil || (s.transport != nil && s.transport.isClosed())
}

func (s *udpEntrySession) dequeue(payload []byte) {
	s.queueMu.Lock()
	s.queuedByte -= len(payload)
	if s.queuedByte < 0 {
		s.queuedByte = 0
	}
	s.queueMu.Unlock()
}

func (s *udpEntrySession) replaceTransport(transport *udpSessionTransport) bool {
	s.stateMu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.stateMu.Unlock()
		transport.close()
		return false
	}
	previous := s.transport
	s.transport = transport
	s.stateMu.Unlock()
	if previous != nil && previous != transport {
		previous.close()
	}
	return true
}

func (s *udpEntrySession) close() {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	transport := s.transport
	s.stateMu.Unlock()
	if transport != nil {
		transport.close()
	}
}

func (s *udpEntrySession) idleTimeout() time.Duration {
	timeout := s.service.udp.idleTimeout
	if timeout <= 0 {
		timeout = udpSessionIdleTimeout
	}
	return timeout
}

func (s *udpEntrySession) recordSent(transport *udpSessionTransport, payload []byte) {
	counter := s.service.forwards.Get("forward:" + s.forward.ID)
	counter.AddIn(int64(len(payload)))
	if transport.statID != "" {
		s.service.tunnels.Get(transport.statID).AddIn(int64(len(payload)))
	}
}

func (s *udpEntrySession) recordResponse(transport *udpSessionTransport, payload []byte) {
	counter := s.service.forwards.Get("forward:" + s.forward.ID)
	counter.AddOut(int64(len(payload)))
	if transport.statID != "" {
		s.service.tunnels.Get(transport.statID).AddOut(int64(len(payload)))
	}
	if _, err := s.inbound.WriteToUDP(payload, s.client); err != nil {
		s.service.log.Debug("udp client response write failed", "forward", s.forward.Name, "session", s.sessionID, "error", err)
	}
}

func (s *udpEntrySession) run() {
	defer func() {
		s.close()
		s.service.udpEntries.delete(s.key, s)
	}()

	transport, err := s.service.openUDPEntryTransport(s.ctx, s.forward, s.tunnel, s.sessionID)
	if err != nil {
		s.service.log.Debug("udp session open failed", "forward", s.forward.Name, "session", s.sessionID, "error", err)
		return
	}
	if !s.replaceTransport(transport) {
		return
	}
	s.service.forwards.Get("forward:" + s.forward.ID).AddConnection()

	maxAttempts := s.service.udpEntryAttemptLimit(s.forward, s.tunnel)
	attempts := 1
	hasSentPayload := false
	firstConfirmed := false
	timer := time.NewTimer(s.idleTimeout())
	defer timer.Stop()

	resetIdle := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(s.idleTimeout())
	}

	// A datagram is replayed only when the failed write made no progress. Once
	// any bytes reached the transport, the remote side may have received it.
	retryInitial := func(failure error, pending []byte, retryPending bool) bool {
		hadResponse := firstConfirmed || transport.hadResponse()
		s.service.log.Debug("udp session transport failed", "forward", s.forward.Name, "session", s.sessionID, "attempt", attempts, "max_attempts", maxAttempts, "had_response", hadResponse, "error", failure)
		if hadResponse {
			if !errors.Is(failure, io.EOF) {
				transport.markFailure(failure)
			}
			return false
		}
		if s.ctx.Err() == nil {
			transport.markFailureForRetry(failure)
		}
		if attempts >= maxAttempts {
			return false
		}
		transport.close()
		for attempts < maxAttempts {
			attempts++
			next, openErr := s.service.openUDPEntryTransport(s.ctx, s.forward, s.tunnel, s.sessionID)
			if openErr != nil {
				s.service.log.Debug("udp session replacement open failed", "forward", s.forward.Name, "session", s.sessionID, "attempt", attempts, "error", openErr)
				continue
			}
			if !s.replaceTransport(next) {
				return false
			}
			transport = next
			if retryPending {
				if _, sendErr := transport.send(pending); sendErr != nil {
					s.service.log.Debug("udp session replacement pending payload failed", "forward", s.forward.Name, "session", s.sessionID, "attempt", attempts, "error", sendErr)
					transport.markFailureForRetry(sendErr)
					transport.close()
					continue
				}
				hasSentPayload = true
				s.recordSent(transport, pending)
			}
			s.service.log.Debug("udp session replacement active", "forward", s.forward.Name, "session", s.sessionID, "attempt", attempts, "stat", transport.statID)
			resetIdle()
			return true
		}
		return false
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.done:
			return
		case <-timer.C:
			s.service.log.Debug("udp session idle timeout", "forward", s.forward.Name, "session", s.sessionID)
			return
		case payload := <-s.queue:
			s.dequeue(payload)
			wrote, sendErr := transport.send(payload)
			if sendErr != nil {
				if retryInitial(sendErr, payload, !wrote) {
					continue
				}
				return
			}
			hasSentPayload = true
			s.recordSent(transport, payload)
			resetIdle()
		case event, ok := <-transport.events:
			if !ok {
				failure := io.EOF
				if retryInitial(failure, nil, !hasSentPayload) {
					continue
				}
				return
			}
			if event.err != nil {
				if retryInitial(event.err, nil, !hasSentPayload) {
					continue
				}
				return
			}
			firstConfirmed = true
			s.recordResponse(transport, event.payload)
			resetIdle()
		}
	}
}

type udpTransportMode uint8

const (
	udpTransportDatagram udpTransportMode = iota
	udpTransportFrame
)

type udpTransportEvent struct {
	payload []byte
	err     error
}

type udpSessionTransport struct {
	conn      net.Conn
	mode      udpTransportMode
	forwardID string
	sessionID string
	statID    string
	failure   func(error)

	events      chan udpTransportEvent
	done        chan struct{}
	closeOnce   sync.Once
	eventsOnce  sync.Once
	failureOnce sync.Once
	seen        atomic.Bool
}

func newUDPSessionTransport(conn net.Conn, mode udpTransportMode, forwardID, sessionID, statID string, failure func(error)) *udpSessionTransport {
	transport := &udpSessionTransport{
		conn:      conn,
		mode:      mode,
		forwardID: forwardID,
		sessionID: sessionID,
		statID:    statID,
		failure:   failure,
		events:    make(chan udpTransportEvent, maxUDPTransportEvents),
		done:      make(chan struct{}),
	}
	go transport.readLoop()
	return transport
}

func (t *udpSessionTransport) isClosed() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func (t *udpSessionTransport) hadResponse() bool {
	return t.seen.Load()
}

type udpWriteTracker struct {
	writer io.Writer
	wrote  bool
}

func (w *udpWriteTracker) Write(payload []byte) (int, error) {
	n, err := w.writer.Write(payload)
	if n > 0 {
		w.wrote = true
	}
	return n, err
}

func (t *udpSessionTransport) send(payload []byte) (bool, error) {
	select {
	case <-t.done:
		return false, net.ErrClosed
	default:
	}
	_ = t.conn.SetWriteDeadline(time.Now().Add(udpSessionWriteTimeout))
	defer func() { _ = t.conn.SetWriteDeadline(time.Time{}) }()
	if t.mode == udpTransportFrame {
		writer := &udpWriteTracker{writer: t.conn}
		err := protocol.WriteUDPDatagramFrame(writer, protocol.UDPDatagramFrame{
			ForwardID: t.forwardID,
			SessionID: t.sessionID,
			Payload:   payload,
		})
		return writer.wrote, err
	}
	n, err := t.conn.Write(payload)
	if err != nil {
		return n > 0, err
	}
	if n != len(payload) {
		return n > 0, io.ErrShortWrite
	}
	return true, nil
}

func (t *udpSessionTransport) readLoop() {
	defer t.eventsOnce.Do(func() { close(t.events) })
	for {
		var payload []byte
		var err error
		if t.mode == udpTransportFrame {
			var frame protocol.UDPDatagramFrame
			frame, err = protocol.ReadUDPDatagramFrame(t.conn)
			if err == nil {
				if frame.ForwardID != t.forwardID || frame.SessionID != t.sessionID {
					err = fmt.Errorf("udp response frame mismatch")
				} else {
					payload = frame.Payload
				}
			}
		} else {
			buf := make([]byte, protocol.MaxUDPPacket)
			var n int
			n, err = t.conn.Read(buf)
			if err == nil {
				payload = append([]byte(nil), buf[:n]...)
			}
		}
		if err != nil {
			if !t.isClosed() {
				select {
				case t.events <- udpTransportEvent{err: err}:
				case <-t.done:
				}
			}
			t.close()
			return
		}
		t.seen.Store(true)
		select {
		case t.events <- udpTransportEvent{payload: payload}:
		case <-t.done:
			return
		}
	}
}

func (t *udpSessionTransport) markFailure(err error) {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return
	}
	t.failureOnce.Do(func() {
		if t.failure != nil {
			t.failure(err)
		}
	})
}

func (t *udpSessionTransport) markFailureForRetry(err error) {
	if errors.Is(err, net.ErrClosed) {
		if !t.isClosed() {
			return
		}
		t.failureOnce.Do(func() {
			if t.failure != nil {
				t.failure(err)
			}
		})
		return
	}
	t.markFailure(err)
}

func (t *udpSessionTransport) close() {
	t.closeOnce.Do(func() {
		close(t.done)
		_ = t.conn.Close()
	})
}

func (s *Service) submitUDPEntryPacket(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime, inbound *net.UDPConn, clientAddr *net.UDPAddr, payload []byte) {
	clientCopy := *clientAddr
	key := forward.ID + ":udp:" + clientCopy.String()
	for attempt := 0; attempt < 2; attempt++ {
		session, created := s.udpEntries.getOrCreate(key, func() *udpEntrySession {
			return newUDPEntrySession(s, key, forward, tunnel, inbound, &clientCopy, ctx)
		})
		if session == nil {
			s.log.Debug("udp session limit reached", "forward", forward.Name, "client", clientCopy.String())
			return
		}
		if created {
			go session.run()
		}
		if session.enqueue(payload) {
			return
		}
		if !session.isUnavailable() {
			s.log.Debug("udp session queue full", "forward", forward.Name, "client", clientCopy.String())
			return
		}
		s.udpEntries.delete(key, session)
		session.close()
	}
	s.log.Debug("udp packet dropped after session close", "forward", forward.Name, "client", clientCopy.String())
}

func (s *Service) openUDPEntryTransport(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime, sessionID string) (*udpSessionTransport, error) {
	if tunnel.Type == model.TunnelDirect {
		conn, statID, targetID, err := s.dialForwardTarget(ctx, forward, "udp", sessionID)
		if err != nil {
			return nil, err
		}
		return newUDPSessionTransport(conn, udpTransportDatagram, forward.ID, sessionID, statID, func(failure error) {
			s.markUDPTargetFailure(forward.ID, sessionID, targetID)
		}), nil
	}
	conn, statID, err := s.dialForwardNext(ctx, tunnel, forward, 0, "udp", sessionID)
	if err != nil {
		return nil, err
	}
	return newUDPSessionTransport(conn, udpTransportFrame, forward.ID, sessionID, statID, func(failure error) {
		s.markUDPStreamFailure(tunnel, forward, 0, statID, sessionID)
	}), nil
}

func (s *Service) udpEntryAttemptLimit(forward model.ForwardRuntime, tunnel model.TunnelRuntime) int {
	if tunnel.Type == model.TunnelDirect {
		if len(forward.Targets) > 0 {
			return len(forward.Targets)
		}
		return 1
	}
	if len(tunnel.Stages) <= 1 {
		return 1
	}
	count := 0
	for _, node := range tunnel.Stages[1].Nodes {
		if runtimeNodeSupportsProtocol(node, model.ForwardProtocolUDP) {
			count++
		}
	}
	if count <= 0 {
		return 1
	}
	return count
}
