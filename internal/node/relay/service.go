package relay

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nyarelay/internal/node/metrics"
	"nyarelay/internal/shared/model"
	"nyarelay/internal/shared/protocol"
	sharedvalidate "nyarelay/internal/shared/validate"
)

type Service struct {
	log          *slog.Logger
	nodeID       string
	forwards     *metrics.Counters
	tunnels      *metrics.Counters
	selector     *candidateSelector
	targets      *targetSelector
	udp          *udpCandidateSessions
	udpTargets   *udpCandidateSessions
	udpEntries   *udpEntrySessions
	mu           sync.RWMutex
	cancel       context.CancelFunc
	expireCancel context.CancelFunc
	runtimeCtx   context.Context
	revision     int64
	config       model.RelayConfig
	servers      []io.Closer
	connSlots    chan struct{}
	udpSlots     chan struct{}
}

const (
	maxRelayConnections = 4096
	maxUDPPacketWorkers = 1024
	relayDialTimeout    = 10 * time.Second
	relayDialBudget     = 30 * time.Second
	relayHelloTimeout   = 10 * time.Second
	wsStageReadTimeout  = 15 * time.Second
)

func New(log *slog.Logger, nodeID string) *Service {
	return &Service{
		log:        log,
		nodeID:     nodeID,
		forwards:   metrics.New(),
		tunnels:    metrics.New(),
		selector:   newCandidateSelector(),
		targets:    newTargetSelector(),
		udp:        newUDPCandidateSessions(),
		udpTargets: newUDPCandidateSessions(),
		udpEntries: newUDPEntrySessions(),
		connSlots:  make(chan struct{}, maxRelayConnections),
		udpSlots:   make(chan struct{}, maxUDPPacketWorkers),
	}
}

func (s *Service) Apply(ctx context.Context, cfg model.RelayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !cfg.ExpiresAt.IsZero() && !time.Now().Before(cfg.ExpiresAt) {
		return errors.New("relay config has expired")
	}
	if cfg.Revision != 0 && cfg.Revision == s.revision && s.cancel != nil {
		if s.expireCancel != nil {
			s.expireCancel()
			s.expireCancel = nil
		}
		if !cfg.ExpiresAt.IsZero() {
			expireCtx, expireCancel := context.WithCancel(s.runtimeCtx)
			s.expireCancel = expireCancel
			go s.expireConfig(expireCtx, cfg.Revision, cfg.ExpiresAt)
		}
		s.config = cfg
		return nil
	}
	previousConfig := s.config
	previousRevision := s.revision
	s.stopRuntimeLocked()
	s.selector.setFailTimeout(failureCooldownFromConfig(cfg))
	s.targets.setFailTimeout(failureCooldownFromConfig(cfg))
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.runtimeCtx = runCtx
	s.servers = nil
	s.config = cfg
	s.revision = cfg.Revision
	if err := s.startConfigLocked(runCtx, cfg); err != nil {
		s.restorePreviousConfig(ctx, previousConfig, previousRevision)
		return err
	}
	return nil
}

func (s *Service) restorePreviousConfig(ctx context.Context, previousConfig model.RelayConfig, previousRevision int64) {
	s.stopRuntimeLocked()
	s.selector.setFailTimeout(failureCooldownFromConfig(previousConfig))
	s.targets.setFailTimeout(failureCooldownFromConfig(previousConfig))
	s.config = previousConfig
	s.revision = previousRevision
	if previousRevision == 0 && len(previousConfig.Forwards) == 0 && len(previousConfig.Tunnels) == 0 {
		return
	}
	restoreCtx, restoreCancel := context.WithCancel(ctx)
	s.cancel = restoreCancel
	s.runtimeCtx = restoreCtx
	s.servers = nil
	s.config = previousConfig
	s.revision = previousRevision
	if err := s.startConfigLocked(restoreCtx, previousConfig); err != nil {
		s.log.Error("restore previous config failed", "revision", previousRevision, "error", err)
		s.stopRuntimeLocked()
	}
}

func (s *Service) stopRuntimeLocked() {
	if s.expireCancel != nil {
		s.expireCancel()
		s.expireCancel = nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	for _, closer := range s.servers {
		_ = closer.Close()
	}
	s.udpEntries.closeAll()
	s.udp.clear()
	s.udpTargets.clear()
	s.selector.reset()
	s.targets.reset()
	s.cancel = nil
	s.runtimeCtx = nil
	s.servers = nil
}

func (s *Service) startConfigLocked(ctx context.Context, cfg model.RelayConfig) error {
	if !cfg.ExpiresAt.IsZero() {
		expireCtx, expireCancel := context.WithCancel(ctx)
		s.expireCancel = expireCancel
		go s.expireConfig(expireCtx, cfg.Revision, cfg.ExpiresAt)
	}
	go s.udp.gcLoop(ctx)
	go s.udpTargets.gcLoop(ctx)
	s.udpEntries.watch(ctx)
	for _, tunnel := range cfg.Tunnels {
		for _, stage := range tunnel.Stages {
			if stage.Role == model.TunnelStageEntry {
				continue
			}
			node, ok := stageNodeFor(stage, s.nodeID)
			if !ok {
				continue
			}
			if err := s.listenStage(ctx, tunnel, stage, node); err != nil {
				return err
			}
		}
	}
	for _, forward := range cfg.Forwards {
		if !forward.Enabled {
			continue
		}
		tunnel, ok := findTunnelInConfig(cfg, forward.TunnelID)
		if !ok || !s.isEntryNode(tunnel) {
			continue
		}
		for _, forwardProtocol := range forward.Protocols {
			switch forwardProtocol {
			case model.ForwardProtocolTCP:
				if err := s.listenTCPForward(ctx, forward, tunnel); err != nil {
					return err
				}
			case model.ForwardProtocolUDP:
				if err := s.listenUDPForward(ctx, forward, tunnel); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) expireConfig(ctx context.Context, revision int64, expiresAt time.Time) {
	timer := time.NewTimer(time.Until(expiresAt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != revision || s.cancel == nil {
		return
	}
	s.stopRuntimeLocked()
	s.config = model.RelayConfig{Revision: revision, NodeID: s.nodeID, ExpiresAt: expiresAt}
	s.log.Warn("relay config expired; stopped runtime", "revision", revision)
}

func (s *Service) ForwardStats() []model.TrafficStat {
	return s.forwards.SnapshotAndReset()
}

func (s *Service) TunnelStats() []model.TrafficStat {
	return s.tunnels.SnapshotAndReset()
}

func (s *Service) listenTCPForward(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime) error {
	ln, err := net.Listen("tcp", forward.Listen)
	if err != nil {
		return fmt.Errorf("listen forward %s: %w", forward.Name, err)
	}
	s.servers = append(s.servers, ln)
	s.log.Info("forward tcp listening", "forward", forward.Name, "listen", forward.Listen)
	go s.acceptLoop(ctx, ln, func(conn net.Conn) {
		s.handleTCPForward(ctx, forward, tunnel, conn)
	})
	return nil
}

func (s *Service) listenStage(ctx context.Context, tunnel model.TunnelRuntime, stage model.TunnelRuntimeStage, node model.TunnelRuntimeNode) error {
	switch tunnel.Transport {
	case model.TunnelTransportWSTLS:
		tlsConfig, err := s.serverTLSConfig(tunnel, node)
		if err != nil {
			return err
		}
		return s.listenWSStage(ctx, tunnel, stage, node, tlsConfig)
	case model.TunnelTransportTLS, model.TunnelTransportMTLS:
		tlsConfig, err := s.serverTLSConfig(tunnel, node)
		if err != nil {
			return err
		}
		ln, err := tls.Listen("tcp", node.ListenAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("listen tunnel %s stage %d: %w", tunnel.Name, stage.Index, err)
		}
		s.servers = append(s.servers, ln)
		s.log.Info("tunnel stage listening", "tunnel", tunnel.Name, "stage", stage.Index, "listen", node.ListenAddr, "transport", tunnel.Transport)
		go s.acceptLoop(ctx, ln, func(conn net.Conn) {
			s.handleStageConn(ctx, tunnel, stage, node, conn)
		})
		return nil
	default:
		ln, err := net.Listen("tcp", node.ListenAddr)
		if err != nil {
			return fmt.Errorf("listen tunnel %s stage %d: %w", tunnel.Name, stage.Index, err)
		}
		s.servers = append(s.servers, ln)
		s.log.Info("tunnel stage listening", "tunnel", tunnel.Name, "stage", stage.Index, "listen", node.ListenAddr, "transport", tunnel.Transport)
		go s.acceptLoop(ctx, ln, func(conn net.Conn) {
			s.handleStageConn(ctx, tunnel, stage, node, conn)
		})
		return nil
	}
}

func (s *Service) listenWSStage(ctx context.Context, tunnel model.TunnelRuntime, stage model.TunnelRuntimeStage, node model.TunnelRuntimeNode, tlsConfig *tls.Config) error {
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              node.ListenAddr,
		Handler:           mux,
		ReadTimeout:       wsStageReadTimeout,
		ReadHeaderTimeout: wsStageReadTimeout,
		WriteTimeout:      wsStageReadTimeout,
		IdleTimeout:       wsStageReadTimeout,
		MaxHeaderBytes:    64 * 1024,
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "nyarelay" {
			http.NotFound(w, r)
			return
		}
		if !s.tryAcquireConnection() {
			http.Error(w, "relay connection limit reached", http.StatusServiceUnavailable)
			return
		}
		defer s.releaseConnection()
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking is not supported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: nyarelay\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		s.handleStageConn(ctx, tunnel, stage, node, conn)
	})
	ln, err := tls.Listen("tcp", node.ListenAddr, tlsConfig)
	if err != nil {
		return fmt.Errorf("listen ws tunnel %s stage %d: %w", tunnel.Name, stage.Index, err)
	}
	s.servers = append(s.servers, closerFunc(func() error {
		_ = ln.Close()
		return server.Close()
	}))
	s.log.Info("ws tunnel stage listening", "tunnel", tunnel.Name, "stage", stage.Index, "listen", node.ListenAddr)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("ws tunnel stage server failed", "tunnel", tunnel.Name, "stage", stage.Index, "error", err)
		}
	}()
	return nil
}

func (s *Service) acceptLoop(ctx context.Context, ln net.Listener, handle func(net.Conn)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.log.Warn("accept failed", "error", err)
				continue
			}
		}
		if !s.tryAcquireConnection() {
			_ = conn.Close()
			continue
		}
		go func() {
			defer s.releaseConnection()
			handle(conn)
		}()
	}
}

func (s *Service) tryAcquireConnection() bool {
	select {
	case s.connSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releaseConnection() {
	select {
	case <-s.connSlots:
	default:
	}
}

func (s *Service) tryAcquireUDPPacket() bool {
	select {
	case s.udpSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releaseUDPPacket() {
	select {
	case <-s.udpSlots:
	default:
	}
}

func (s *Service) handleTCPForward(ctx context.Context, forward model.ForwardRuntime, tunnel model.TunnelRuntime, inbound net.Conn) {
	defer s.closeConn(inbound, "tcp forward inbound")
	counter := s.forwards.Get("forward:" + forward.ID)
	counter.AddConnection()
	outbound, statID, err := s.dialForwardNext(ctx, tunnel, forward, 0, "tcp", "")
	if err != nil {
		s.log.Warn("forward dial failed", "forward", forward.Name, "error", err)
		return
	}
	defer s.closeConn(outbound, "tcp forward outbound")
	s.pipe(ctx, inbound, outbound, counter, s.tunnels.Get(statID))
}

func (s *Service) handleStageConn(ctx context.Context, tunnel model.TunnelRuntime, stage model.TunnelRuntimeStage, node model.TunnelRuntimeNode, inbound net.Conn) {
	defer s.closeConn(inbound, "stage inbound")
	_ = inbound.SetReadDeadline(time.Now().Add(relayHelloTimeout))
	hello, err := protocol.ReadHello(inbound)
	if err != nil {
		s.log.Warn("invalid relay hello", "tunnel", tunnel.Name, "stage", stage.Index, "error", err)
		return
	}
	_ = inbound.SetReadDeadline(time.Time{})
	if err := s.validateHello(tunnel, stage, node, hello); err != nil {
		s.log.Warn("relay hello rejected", "tunnel", tunnel.Name, "stage", stage.Index, "error", err)
		return
	}
	forward, ok := s.findForward(hello.ForwardID)
	if !ok || !forward.Enabled {
		s.log.Warn("forward not found for tunnel", "forward", hello.ForwardID)
		return
	}
	counter := s.forwards.Get("forward:" + forward.ID)
	counter.AddConnection()
	if stage.Role == model.TunnelStageExit {
		if hello.Network == "udp" {
			s.handleUDPStageExit(ctx, forward, inbound, counter)
			return
		}
		outbound, statID, _, err := s.dialForwardTarget(ctx, forward, "tcp", "")
		if err != nil {
			s.log.Warn("target dial failed", "forward", forward.Name, "error", err)
			return
		}
		defer s.closeConn(outbound, "stage target outbound")
		s.pipe(ctx, inbound, outbound, counter, s.tunnels.Get(statID))
		return
	}
	if hello.Network == "udp" {
		s.handleUDPStageTransit(ctx, tunnel, forward, stage, inbound, counter)
		return
	}
	next, statID, err := s.dialForwardNext(ctx, tunnel, forward, stage.Index, hello.Network, "")
	if err != nil {
		s.log.Warn("next stage dial failed", "forward", forward.Name, "error", err)
		return
	}
	defer s.closeConn(next, "stage next outbound")
	s.pipe(ctx, inbound, next, counter, s.tunnels.Get(statID))
}

func (s *Service) validateHello(tunnel model.TunnelRuntime, stage model.TunnelRuntimeStage, node model.TunnelRuntimeNode, hello protocol.RelayHello) error {
	if len(hello.TunnelID) > sharedvalidate.MaxIDBytes || len(hello.ForwardID) > sharedvalidate.MaxIDBytes {
		return errors.New("relay hello identifier is too large")
	}
	if len(hello.Secret) > 256 {
		return errors.New("relay hello secret is too large")
	}
	if stage.Index <= 0 || stage.Role == model.TunnelStageEntry {
		return errors.New("invalid relay listener stage")
	}
	if hello.TunnelID != tunnel.ID {
		return errors.New("tunnel id mismatch")
	}
	if hello.ToStageIndex != stage.Index {
		return errors.New("to stage index mismatch")
	}
	if hello.FromStageIndex != stage.Index-1 {
		return errors.New("from stage index mismatch")
	}
	if hello.Network != "tcp" && hello.Network != "udp" {
		return fmt.Errorf("unsupported network %q", hello.Network)
	}
	secret := node.Settings["secret"]
	if secret == "" {
		return errors.New("relay secret is missing")
	}
	wantSecret := sha256.Sum256([]byte(secret))
	gotSecret := sha256.Sum256([]byte(hello.Secret))
	if subtle.ConstantTimeCompare(wantSecret[:], gotSecret[:]) != 1 {
		return errors.New("secret mismatch")
	}
	forward, ok := s.findForward(hello.ForwardID)
	if !ok || !forward.Enabled {
		return errors.New("forward not found")
	}
	if forward.TunnelID != tunnel.ID {
		return errors.New("forward tunnel mismatch")
	}
	if !forwardSupports(forward, model.ForwardProtocol(hello.Network)) {
		return errors.New("forward protocol mismatch")
	}
	if !runtimeNodeSupportsProtocol(node, model.ForwardProtocol(hello.Network)) {
		return errors.New("stage node protocol mismatch")
	}
	return nil
}

func (s *Service) dialForwardNext(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, fromStageIndex int, network, sessionID string) (net.Conn, string, error) {
	dialCtx, cancel := context.WithTimeout(ctx, relayDialBudget)
	defer cancel()
	ctx = dialCtx
	if tunnel.Type == model.TunnelDirect {
		conn, statID, _, err := s.dialForwardTarget(ctx, forward, network, sessionID)
		return conn, statID, err
	}
	nextIndex := fromStageIndex + 1
	if nextIndex >= len(tunnel.Stages) {
		conn, statID, _, err := s.dialForwardTarget(ctx, forward, network, sessionID)
		return conn, statID, err
	}
	nextStage := tunnel.Stages[nextIndex]
	if len(nextStage.Nodes) == 0 {
		return nil, "", fmt.Errorf("tunnel stage %d has no node", nextIndex)
	}
	if network == "udp" && sessionID != "" {
		return s.dialForwardNextUDP(ctx, tunnel, forward, fromStageIndex, nextStage, sessionID)
	}
	order := s.selector.order(tunnel.ID, nextStage, network)
	if len(order) == 0 {
		return nil, stageStatID(tunnel.ID, nextIndex), fmt.Errorf("no available candidate in stage %d", nextIndex)
	}
	var lastErr error
	for _, idx := range order {
		conn, statID, err := s.dialStageCandidate(ctx, tunnel, forward, fromStageIndex, nextIndex, idx, network)
		if err == nil {
			return conn, statID, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available candidate in stage %d", nextIndex)
	}
	return nil, stageStatID(tunnel.ID, nextIndex), lastErr
}

func (s *Service) dialForwardTarget(ctx context.Context, forward model.ForwardRuntime, network, sessionID string) (net.Conn, string, string, error) {
	dialCtx, cancel := context.WithTimeout(ctx, relayDialBudget)
	defer cancel()
	ctx = dialCtx
	var sessionKey string
	if network == "udp" && sessionID != "" {
		sessionKey = udpTargetSessionKey(forward.ID, sessionID)
		if session, ok := s.udpTargets.get(sessionKey, time.Now()); ok {
			if target, ok := forwardTargetAt(forward, session.candidateIndex); ok {
				if target.ID == session.candidateNodeID && target.Enabled && targetSupportsProtocol(target.Protocols, model.ForwardProtocolUDP) {
					conn, err := dialRelayContext(ctx, network, target.Address)
					if err == nil {
						s.targets.recordSuccess(forward.ID, target.ID, network)
						return conn, targetStatID(forward.ID, target.ID), target.ID, nil
					}
					s.targets.recordFailure(forward.ID, target.ID, network)
					s.udpTargets.delete(sessionKey)
				} else {
					s.udpTargets.delete(sessionKey)
				}
			} else {
				s.udpTargets.delete(sessionKey)
			}
		}
	}
	order := s.targets.order(forward, network)
	if len(order) == 0 {
		return nil, "target:" + forward.ID, "", fmt.Errorf("no available %s target for forward %s", network, forward.Name)
	}
	var lastErr error
	for _, idx := range order {
		target, ok := forwardTargetAt(forward, idx)
		if !ok {
			continue
		}
		conn, err := dialRelayContext(ctx, network, target.Address)
		if err != nil {
			s.targets.recordFailure(forward.ID, target.ID, network)
			lastErr = err
			continue
		}
		s.targets.recordSuccess(forward.ID, target.ID, network)
		if sessionKey != "" {
			s.udpTargets.bind(sessionKey, idx, target.ID, time.Now())
		}
		return conn, targetStatID(forward.ID, target.ID), target.ID, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available %s target for forward %s", network, forward.Name)
	}
	return nil, "target:" + forward.ID, "", lastErr
}

func udpTargetSessionKey(forwardID, sessionID string) string {
	return "target:" + forwardID + ":udp:" + sessionID
}

func targetStatID(forwardID, targetID string) string {
	if targetID == "" {
		return "target:" + forwardID
	}
	return "target:" + forwardID + ":" + targetID
}

func (s *Service) dialStageCandidate(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, fromStageIndex, nextIndex, candidateIndex int, network string) (net.Conn, string, error) {
	if nextIndex < 0 || nextIndex >= len(tunnel.Stages) {
		return nil, "", fmt.Errorf("invalid tunnel stage %d", nextIndex)
	}
	nextStage := tunnel.Stages[nextIndex]
	if candidateIndex < 0 || candidateIndex >= len(nextStage.Nodes) {
		return nil, stageStatID(tunnel.ID, nextIndex), fmt.Errorf("invalid candidate %d in stage %d", candidateIndex, nextIndex)
	}
	nextNode := nextStage.Nodes[candidateIndex]
	conn, err := s.dialStage(ctx, tunnel, nextNode)
	if err != nil {
		s.selector.recordFailure(tunnel.ID, nextIndex, nextNode.NodeID, network)
		return nil, stageCandidateStatID(tunnel.ID, nextIndex, nextNode.NodeID), err
	}
	if err := protocol.WriteHello(conn, protocol.RelayHello{
		TunnelID:       tunnel.ID,
		ForwardID:      forward.ID,
		FromStageIndex: fromStageIndex,
		ToStageIndex:   nextIndex,
		Network:        network,
		Secret:         nextNode.Settings["secret"],
	}); err != nil {
		_ = conn.Close()
		s.selector.recordFailure(tunnel.ID, nextIndex, nextNode.NodeID, network)
		return nil, stageCandidateStatID(tunnel.ID, nextIndex, nextNode.NodeID), err
	}
	s.selector.recordSuccess(tunnel.ID, nextIndex, nextNode.NodeID, network)
	return conn, stageCandidateStatID(tunnel.ID, nextIndex, nextNode.NodeID), nil
}

func (s *Service) dialForwardNextUDP(ctx context.Context, tunnel model.TunnelRuntime, forward model.ForwardRuntime, fromStageIndex int, nextStage model.TunnelRuntimeStage, sessionID string) (net.Conn, string, error) {
	nextIndex := fromStageIndex + 1
	key := udpSessionKey(tunnel.ID, forward.ID, fromStageIndex, nextIndex, sessionID)
	if session, ok := s.udp.get(key, time.Now()); ok {
		if session.candidateIndex >= 0 && session.candidateIndex < len(nextStage.Nodes) {
			node := nextStage.Nodes[session.candidateIndex]
			if node.NodeID == session.candidateNodeID && runtimeNodeSupportsProtocol(node, model.ForwardProtocolUDP) {
				conn, statID, err := s.dialStageCandidate(ctx, tunnel, forward, fromStageIndex, nextIndex, session.candidateIndex, "udp")
				if err == nil {
					return conn, statID, nil
				}
				s.udp.delete(key)
			}
		} else {
			s.udp.delete(key)
		}
	}
	order := s.selector.order(tunnel.ID, nextStage, "udp")
	if len(order) == 0 {
		return nil, stageStatID(tunnel.ID, nextIndex), fmt.Errorf("no available udp candidate in stage %d", nextIndex)
	}
	var lastErr error
	for _, idx := range order {
		conn, statID, err := s.dialStageCandidate(ctx, tunnel, forward, fromStageIndex, nextIndex, idx, "udp")
		if err != nil {
			lastErr = err
			continue
		}
		s.udp.bind(key, idx, nextStage.Nodes[idx].NodeID, time.Now())
		return conn, statID, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available udp candidate in stage %d", nextIndex)
	}
	return nil, stageStatID(tunnel.ID, nextIndex), lastErr
}

func udpSessionKey(tunnelID, forwardID string, fromStageIndex, toStageIndex int, sessionID string) string {
	return fmt.Sprintf("%s:%s:%d:%d:udp:%s", tunnelID, forwardID, fromStageIndex, toStageIndex, sessionID)
}

func (s *Service) dialStage(ctx context.Context, tunnel model.TunnelRuntime, node model.TunnelRuntimeNode) (net.Conn, error) {
	addr := node.ConnectAddr
	if addr == "" {
		addr = node.PublicAddr
	}
	if addr == "" {
		return nil, errors.New("stage has no connect address")
	}
	dialCtx, cancel := context.WithTimeout(ctx, relayDialTimeout)
	defer cancel()
	dialer := &net.Dialer{Timeout: relayDialTimeout, KeepAlive: 30 * time.Second}
	switch tunnel.Transport {
	case model.TunnelTransportTLS, model.TunnelTransportMTLS:
		raw, err := dialer.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		serverName := node.Settings["server_name"]
		if serverName == "" {
			serverName = hostOnly(addr)
		}
		clientTLS, err := s.clientTLSConfig(tunnel, node, serverName)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		tlsConn := tls.Client(raw, clientTLS)
		_ = tlsConn.SetDeadline(time.Now().Add(relayDialTimeout))
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			if closeErr := tlsConn.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
			return nil, err
		}
		_ = tlsConn.SetDeadline(time.Time{})
		return tlsConn, nil
	case model.TunnelTransportWSTLS:
		raw, err := dialer.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		host := node.Settings["server_name"]
		if host == "" {
			host = hostOnly(addr)
		}
		if strings.ContainsAny(host, "\r\n\t ") {
			_ = raw.Close()
			return nil, errors.New("invalid websocket host")
		}
		clientTLS, err := s.clientTLSConfig(tunnel, node, host)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		tlsConn := tls.Client(raw, clientTLS)
		_ = tlsConn.SetDeadline(time.Now().Add(relayDialTimeout))
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			if closeErr := tlsConn.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
			return nil, err
		}
		req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: nyarelay\r\n\r\n", host)
		if _, err := tlsConn.Write([]byte(req)); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		response, err := http.ReadResponse(bufio.NewReader(tlsConn), &http.Request{Method: http.MethodGet})
		if err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSwitchingProtocols ||
			!strings.EqualFold(response.Header.Get("Upgrade"), "nyarelay") ||
			!headerContainsToken(response.Header.Get("Connection"), "upgrade") {
			_ = tlsConn.Close()
			return nil, errors.New("websocket-style upgrade rejected")
		}
		_ = tlsConn.SetDeadline(time.Time{})
		return tlsConn, nil
	default:
		return dialer.DialContext(dialCtx, "tcp", addr)
	}
}

func dialRelayContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, relayDialTimeout)
	defer cancel()
	return (&net.Dialer{Timeout: relayDialTimeout, KeepAlive: 30 * time.Second}).DialContext(dialCtx, network, address)
}

func (s *Service) pipe(ctx context.Context, a, b net.Conn, forwardCounter *metrics.Counter, tunnelCounter *metrics.Counter) {
	done := make(chan struct{}, 2)
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = a.Close()
			_ = b.Close()
		case <-stop:
		}
	}()
	defer close(stop)
	copySide := func(dst, src net.Conn, addForward func(int64), addTunnel func(int64)) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				written, werr := dst.Write(buf[:n])
				if written > 0 {
					addForward(int64(written))
					if addTunnel != nil {
						addTunnel(int64(written))
					}
				}
				if werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		_ = dst.Close()
		_ = src.Close()
		done <- struct{}{}
	}
	var tunnelIn, tunnelOut func(int64)
	if tunnelCounter != nil {
		tunnelIn = tunnelCounter.AddIn
		tunnelOut = tunnelCounter.AddOut
	}
	go copySide(b, a, forwardCounter.AddIn, tunnelIn)
	go copySide(a, b, forwardCounter.AddOut, tunnelOut)
	<-done
}

func (s *Service) closeConn(conn net.Conn, name string) {
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		s.log.Debug("connection close failed", "conn", name, "error", err)
	}
}

func (s *Service) findTunnel(id string) (model.TunnelRuntime, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return findTunnelInConfig(s.config, id)
}

func findTunnelInConfig(config model.RelayConfig, id string) (model.TunnelRuntime, bool) {
	for _, tunnel := range config.Tunnels {
		if tunnel.ID == id {
			return tunnel, true
		}
	}
	return model.TunnelRuntime{}, false
}

func (s *Service) findForward(id string) (model.ForwardRuntime, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, forward := range s.config.Forwards {
		if forward.ID == id {
			return forward, true
		}
	}
	return model.ForwardRuntime{}, false
}

func (s *Service) isEntryNode(tunnel model.TunnelRuntime) bool {
	if len(tunnel.Stages) == 0 || len(tunnel.Stages[0].Nodes) == 0 {
		return false
	}
	return stageHasNode(tunnel.Stages[0], s.nodeID)
}

func stageNodeFor(stage model.TunnelRuntimeStage, nodeID string) (model.TunnelRuntimeNode, bool) {
	for _, node := range stage.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return model.TunnelRuntimeNode{}, false
}

func forwardSupports(forward model.ForwardRuntime, protocolValue model.ForwardProtocol) bool {
	for _, forwardProtocol := range forward.Protocols {
		if forwardProtocol == protocolValue {
			return true
		}
	}
	return false
}

func stageStatID(tunnelID string, stageIndex int) string {
	return fmt.Sprintf("tunnel:%s:stage:%d", tunnelID, stageIndex)
}

func stageCandidateStatID(tunnelID string, stageIndex int, nodeID string) string {
	return fmt.Sprintf("tunnel:%s:stage:%d:candidate:%s", tunnelID, stageIndex, nodeID)
}

func stageHasNode(stage model.TunnelRuntimeStage, nodeID string) bool {
	for _, node := range stage.Nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

func hostOnly(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return trimEnclosingBrackets(host)
	}
	trimmed := trimEnclosingBrackets(value)
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String()
	}
	return trimmed
}

func trimEnclosingBrackets(value string) string {
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") && len(value) >= 2 {
		return value[1 : len(value)-1]
	}
	return value
}

func headerContainsToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}
