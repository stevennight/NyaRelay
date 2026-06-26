package relay

import (
	"context"
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
)

type Service struct {
	log     *slog.Logger
	nodeID  string
	routes  *metrics.Counters
	links   *metrics.Counters
	mu      sync.Mutex
	cancel  context.CancelFunc
	config  model.RelayConfig
	servers []io.Closer
}

func New(log *slog.Logger, nodeID string) *Service {
	return &Service{
		log:    log,
		nodeID: nodeID,
		routes: metrics.New(),
		links:  metrics.New(),
	}
}

func (s *Service) Apply(ctx context.Context, cfg model.RelayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	for _, closer := range s.servers {
		_ = closer.Close()
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.servers = nil
	s.config = cfg

	for _, link := range cfg.Links {
		if !link.Enabled || link.ToNode != s.nodeID {
			continue
		}
		if err := s.listenLink(runCtx, link); err != nil {
			return err
		}
	}
	for _, route := range cfg.Routes {
		if !route.Enabled || route.EntryNode != s.nodeID {
			continue
		}
		if err := s.listenRoute(runCtx, route); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RouteStats() []model.TrafficStat {
	return s.routes.SnapshotAndReset()
}

func (s *Service) LinkStats() []model.TrafficStat {
	return s.links.SnapshotAndReset()
}

func (s *Service) listenRoute(ctx context.Context, route model.Route) error {
	if route.Protocol == model.ProtocolUDP {
		return s.listenUDPRoute(ctx, route)
	}
	ln, err := net.Listen("tcp", route.Listen)
	if err != nil {
		return fmt.Errorf("listen route %s: %w", route.Name, err)
	}
	s.servers = append(s.servers, ln)
	s.log.Info("route listening", "route", route.Name, "listen", route.Listen, "protocol", route.Protocol)
	go s.acceptLoop(ctx, ln, func(conn net.Conn) {
		s.handleTCPRoute(ctx, route, conn)
	})
	return nil
}

func (s *Service) listenLink(ctx context.Context, link model.Link) error {
	switch link.Type {
	case model.LinkWSTLS:
		tlsConfig, err := s.serverTLSConfig(link)
		if err != nil {
			return err
		}
		return s.listenWSLink(ctx, link, tlsConfig)
	case model.LinkTLS, model.LinkMTLS:
		tlsConfig, err := s.serverTLSConfig(link)
		if err != nil {
			return err
		}
		ln, err := tls.Listen("tcp", link.BindAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("listen link %s: %w", link.Name, err)
		}
		s.servers = append(s.servers, ln)
		s.log.Info("link listening", "link", link.Name, "listen", link.BindAddr, "type", link.Type)
		go s.acceptLoop(ctx, ln, func(conn net.Conn) {
			s.handleLinkConn(ctx, link, conn)
		})
		return nil
	default:
		ln, err := net.Listen("tcp", link.BindAddr)
		if err != nil {
			return fmt.Errorf("listen link %s: %w", link.Name, err)
		}
		s.servers = append(s.servers, ln)
		s.log.Info("link listening", "link", link.Name, "listen", link.BindAddr, "type", link.Type)
		go s.acceptLoop(ctx, ln, func(conn net.Conn) {
			s.handleLinkConn(ctx, link, conn)
		})
		if link.Type == model.LinkDirect {
			if err := s.listenUDPLink(ctx, link); err != nil {
				return err
			}
		}
		return nil
	}
}

func (s *Service) listenWSLink(ctx context.Context, link model.Link, tlsConfig *tls.Config) error {
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              link.BindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "nyarelay" {
			http.NotFound(w, r)
			return
		}
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
		s.handleLinkConn(ctx, link, conn)
	})
	ln, err := tls.Listen("tcp", link.BindAddr, tlsConfig)
	if err != nil {
		return fmt.Errorf("listen ws link %s: %w", link.Name, err)
	}
	s.servers = append(s.servers, closerFunc(func() error {
		_ = ln.Close()
		return server.Close()
	}))
	s.log.Info("ws link listening", "link", link.Name, "listen", link.BindAddr)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("ws link server failed", "link", link.Name, "error", err)
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
		go handle(conn)
	}
}

func (s *Service) handleTCPRoute(ctx context.Context, route model.Route, inbound net.Conn) {
	defer inbound.Close()
	counter := s.routes.Get(route.ID)
	counter.AddConnection()
	outbound, statID, err := s.dialRouteNext(ctx, route, 0)
	if err != nil {
		s.log.Warn("route dial failed", "route", route.Name, "error", err)
		return
	}
	defer outbound.Close()
	s.pipe(inbound, outbound, counter, s.links.Get(statID))
}

func (s *Service) handleLinkConn(ctx context.Context, link model.Link, inbound net.Conn) {
	defer inbound.Close()
	hello, err := protocol.ReadHello(inbound)
	if err != nil {
		s.log.Warn("invalid relay hello", "link", link.Name, "error", err)
		return
	}
	if link.Settings["secret"] != "" && hello.Secret != link.Settings["secret"] {
		s.log.Warn("relay secret rejected", "link", link.Name, "route", hello.RouteID)
		return
	}
	route, ok := s.findRoute(hello.RouteID)
	if !ok || !route.Enabled {
		s.log.Warn("route not found for link", "route", hello.RouteID)
		return
	}
	counter := s.routes.Get(route.ID)
	counter.AddConnection()
	next, statID, err := s.dialRouteNext(ctx, route, hello.HopIndex+1)
	if err != nil {
		s.log.Warn("next hop dial failed", "route", route.Name, "error", err)
		return
	}
	defer next.Close()
	s.pipe(inbound, next, counter, s.links.Get(statID))
}

func (s *Service) dialRouteNext(ctx context.Context, route model.Route, hopIndex int) (net.Conn, string, error) {
	if hopIndex >= len(route.Hops) {
		conn, err := (&net.Dialer{}).DialContext(ctx, string(route.Protocol), route.Target)
		return conn, "target:" + route.ID, err
	}
	hop := route.Hops[hopIndex]
	link, ok := s.findLink(hop.LinkID)
	if !ok || !link.Enabled {
		return nil, "", fmt.Errorf("link %s is not available", hop.LinkID)
	}
	conn, err := s.dialLink(ctx, link)
	if err != nil {
		return nil, link.ID, err
	}
	secret := ""
	if link.Settings != nil {
		secret = link.Settings["secret"]
	}
	if err := protocol.WriteHello(conn, protocol.RelayHello{
		RouteID:  route.ID,
		HopIndex: hopIndex,
		Network:  string(route.Protocol),
		Secret:   secret,
	}); err != nil {
		_ = conn.Close()
		return nil, link.ID, err
	}
	return conn, link.ID, nil
}

func (s *Service) dialLink(ctx context.Context, link model.Link) (net.Conn, error) {
	dialer := &net.Dialer{}
	switch link.Type {
	case model.LinkTLS, model.LinkMTLS:
		raw, err := dialer.DialContext(ctx, "tcp", link.PublicAddr)
		if err != nil {
			return nil, err
		}
		serverName := link.ServerName
		if serverName == "" {
			serverName = strings.Split(link.PublicAddr, ":")[0]
		}
		clientTLS, err := s.clientTLSConfig(link, serverName)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		tlsConn := tls.Client(raw, clientTLS)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		return tlsConn, nil
	case model.LinkWSTLS:
		raw, err := dialer.DialContext(ctx, "tcp", link.PublicAddr)
		if err != nil {
			return nil, err
		}
		host := link.ServerName
		if host == "" {
			host = strings.Split(link.PublicAddr, ":")[0]
		}
		clientTLS, err := s.clientTLSConfig(link, host)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		tlsConn := tls.Client(raw, clientTLS)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: nyarelay\r\n\r\n", host)
		if _, err := tlsConn.Write([]byte(req)); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		buf := make([]byte, 1024)
		n, err := tlsConn.Read(buf)
		if err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		if !strings.Contains(string(buf[:n]), "101") {
			_ = tlsConn.Close()
			return nil, errors.New("websocket-style upgrade rejected")
		}
		return tlsConn, nil
	default:
		return dialer.DialContext(ctx, "tcp", link.PublicAddr)
	}
}

func (s *Service) pipe(a, b net.Conn, routeCounter *metrics.Counter, linkCounter *metrics.Counter) {
	done := make(chan struct{}, 2)
	copySide := func(dst, src net.Conn, addRoute func(int64), addLink func(int64)) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				written, werr := dst.Write(buf[:n])
				if written > 0 {
					addRoute(int64(written))
					if linkCounter != nil {
						addLink(int64(written))
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
	go copySide(b, a, routeCounter.AddIn, linkCounter.AddIn)
	go copySide(a, b, routeCounter.AddOut, linkCounter.AddOut)
	<-done
}

func (s *Service) findRoute(id string) (model.Route, bool) {
	for _, route := range s.config.Routes {
		if route.ID == id {
			return route, true
		}
	}
	return model.Route{}, false
}

func (s *Service) findLink(id string) (model.Link, bool) {
	for _, link := range s.config.Links {
		if link.ID == id {
			return link, true
		}
	}
	return model.Link{}, false
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}
