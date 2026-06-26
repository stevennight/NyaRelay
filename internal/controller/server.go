package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/nodehub"
	"nyarelay/internal/controller/store"
	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/ids"
	"nyarelay/internal/shared/logging"
	"nyarelay/internal/shared/model"
	sharedprotocol "nyarelay/internal/shared/protocol"
	"nyarelay/internal/shared/validate"
)

const (
	sessionCookieName = "nyarelay_session"
	signingKeySetting = "config_signing_private_key"
	signingPubSetting = "config_signing_public_key"
)

type Server struct {
	cfg      Config
	log      *slog.Logger
	store    *store.Store
	sessions *auth.Sessions
	limiter  *auth.LoginLimiter
	hub      *nodehub.Hub
	mux      *http.ServeMux
}

func Run(ctx context.Context, args []string) error {
	cfg := parseConfig(args)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	s := &Server{
		cfg:      cfg,
		log:      logging.New(cfg.LogLevel),
		store:    st,
		sessions: auth.NewSessions(cfg.SessionLifetime),
		limiter:  auth.NewLoginLimiter(),
		hub:      nodehub.New(),
		mux:      http.NewServeMux(),
	}
	if err := s.ensureSigningKey(ctx); err != nil {
		return err
	}
	rev, _ := st.CurrentRevision(ctx)
	s.hub.SetRevision(rev)
	s.routes()

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           secureHeaders(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("controller listening", "addr", cfg.ListenAddr, "data", cfg.DataDir)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/setup/status", s.handleSetupStatus)
	s.mux.HandleFunc("POST /api/setup", s.handleSetup)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.withAuth(s.handleLogout))
	s.mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))
	s.mux.HandleFunc("POST /api/settings/totp/setup", s.withAuth(s.handleTOTPSetup))
	s.mux.HandleFunc("POST /api/settings/totp/enable", s.withAuth(s.handleTOTPEnable))
	s.mux.HandleFunc("POST /api/settings/totp/disable", s.withAuth(s.handleTOTPDisable))
	s.mux.HandleFunc("GET /api/dashboard", s.withAuth(s.handleDashboard))
	s.mux.HandleFunc("GET /api/controller/info", s.withAuth(s.handleControllerInfo))
	s.mux.HandleFunc("GET /api/nodes", s.withAuth(s.handleListNodes))
	s.mux.HandleFunc("GET /api/nodes/{id}", s.withAuth(s.handleGetNode))
	s.mux.HandleFunc("POST /api/nodes", s.withAuth(s.handleCreateNode))
	s.mux.HandleFunc("POST /api/nodes/revoke", s.withAuth(s.handleRevokeNode))
	s.mux.HandleFunc("GET /api/links", s.withAuth(s.handleListLinks))
	s.mux.HandleFunc("GET /api/links/{id}", s.withAuth(s.handleGetLink))
	s.mux.HandleFunc("POST /api/links", s.withAuth(s.handleUpsertLink))
	s.mux.HandleFunc("GET /api/routes", s.withAuth(s.handleListRoutes))
	s.mux.HandleFunc("GET /api/routes/{id}", s.withAuth(s.handleGetRoute))
	s.mux.HandleFunc("POST /api/routes", s.withAuth(s.handleUpsertRoute))
	s.mux.HandleFunc("GET /api/traffic", s.withAuth(s.handleTraffic))
	s.mux.HandleFunc("GET /api/audit", s.withAuth(s.handleAudit))

	s.mux.HandleFunc("POST /api/node/heartbeat", s.withNode(s.handleNodeHeartbeat))
	s.mux.HandleFunc("GET /api/node/ws", s.withNode(s.handleNodeWS))
	s.mux.HandleFunc("GET /api/node/config", s.withNode(s.handleNodeConfig))
	s.mux.HandleFunc("GET /api/node/events", s.withNode(s.handleNodeEvents))
	s.mux.HandleFunc("POST /api/node/metrics", s.withNode(s.handleNodeMetrics))

	s.mux.HandleFunc("/", s.handleWeb)
}

func (s *Server) ensureSigningKey(ctx context.Context) error {
	if _, ok, err := s.store.GetSetting(ctx, signingKeySetting); err != nil || ok {
		return err
	}
	pub, priv, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, signingKeySetting, priv); err != nil {
		return err
	}
	return s.store.SetSetting(ctx, signingPubSetting, pub)
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.UserCount(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"needs_setup": count == 0,
		"public_url":  s.cfg.PublicURL,
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.UserCount(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	if count > 0 {
		writeError(w, errors.New("setup is already complete"), http.StatusConflict)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	user, err := s.store.CreateUser(r.Context(), strings.TrimSpace(req.Username), hash)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	_ = s.store.AddAudit(r.Context(), user.Username, "setup.complete", "controller", map[string]string{"username": user.Username})
	session, err := s.sessions.Create(user.ID, user.Username)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, session)
	writeJSON(w, map[string]any{"user": user})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	limitKey := r.RemoteAddr + ":" + req.Username
	if !s.limiter.Allow(limitKey) {
		writeError(w, errors.New("too many failed login attempts"), http.StatusTooManyRequests)
		return
	}
	user, err := s.store.FindUserByUsername(r.Context(), req.Username)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, req.Password) {
		s.limiter.Fail(limitKey)
		writeError(w, errors.New("invalid username or password"), http.StatusUnauthorized)
		return
	}
	if user.TOTPEnabled {
		secret, _, err := s.store.TOTPSecret(r.Context(), user.ID)
		if err != nil || !auth.VerifyTOTP(secret, req.TOTPCode, time.Now()) {
			s.limiter.Fail(limitKey)
			writeError(w, errors.New("invalid totp code"), http.StatusUnauthorized)
			return
		}
	}
	session, err := s.sessions.Create(user.ID, user.Username)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	s.limiter.Success(limitKey)
	setSessionCookie(w, session)
	_ = s.store.AddAudit(r.Context(), user.Username, "auth.login", "controller", map[string]any{"ip": r.RemoteAddr})
	writeJSON(w, map[string]any{"user": user})
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request, session auth.Session) {
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	if err := s.store.SetTOTPSecret(r.Context(), session.UserID, secret, false); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"secret": secret,
		"url":    auth.TOTPURL("NyaRelay", session.Username, secret),
	})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	secret, _, err := s.store.TOTPSecret(r.Context(), session.UserID)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	if secret == "" || !auth.VerifyTOTP(secret, req.Code, time.Now()) {
		writeError(w, errors.New("invalid totp code"), http.StatusBadRequest)
		return
	}
	if err := s.store.SetTOTPSecret(r.Context(), session.UserID, secret, true); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	_ = s.store.AddAudit(r.Context(), session.Username, "totp.enable", "user", map[string]any{"user_id": session.UserID})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request, session auth.Session) {
	if err := s.store.SetTOTPSecret(r.Context(), session.UserID, "", false); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	_ = s.store.AddAudit(r.Context(), session.Username, "totp.disable", "user", map[string]any{"user_id": session.UserID})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, session auth.Session) {
	s.sessions.Delete(session.ID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, session auth.Session) {
	writeJSON(w, map[string]any{
		"user": map[string]any{
			"id":       session.UserID,
			"username": session.Username,
		},
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, session auth.Session) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	links, err := s.store.ListLinks(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	routes, err := s.store.ListRoutes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	var online, activeRoutes int
	for _, node := range nodes {
		if node.Status == model.NodeOnline && !node.Revoked {
			online++
		}
	}
	for _, route := range routes {
		if route.Enabled {
			activeRoutes++
		}
	}
	writeJSON(w, map[string]any{
		"nodes":         len(nodes),
		"online_nodes":  online,
		"links":         len(links),
		"routes":        len(routes),
		"active_routes": activeRoutes,
		"revision":      s.hub.Revision(),
	})
}

func (s *Server) handleControllerInfo(w http.ResponseWriter, r *http.Request, session auth.Session) {
	pub, _, err := s.store.GetSetting(r.Context(), signingPubSetting)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"signing_key": pub,
		"public_url":  s.cfg.PublicURL,
		"revision":    s.hub.Revision(),
	})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request, session auth.Session) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, nodes)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request, session auth.Session) {
	node, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, node)
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, errors.New("node name is required"), http.StatusBadRequest)
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	node := model.Node{
		ID:        ids.New("node"),
		Name:      strings.TrimSpace(req.Name),
		Status:    model.NodeOffline,
		Labels:    req.Labels,
		Approved:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.UpsertNode(r.Context(), node, token); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, _ := s.store.BumpRevision(r.Context())
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "node.create", node.ID, map[string]string{"name": node.Name})
	writeJSON(w, map[string]any{"node": node, "token": token})
}

func (s *Server) handleRevokeNode(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := s.store.RevokeNode(r.Context(), req.ID); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, _ := s.store.BumpRevision(r.Context())
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "node.revoke", req.ID, map[string]string{"id": req.ID})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request, session auth.Session) {
	links, err := s.store.ListLinks(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, links)
}

func (s *Server) handleGetLink(w http.ResponseWriter, r *http.Request, session auth.Session) {
	link, err := s.store.GetLink(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, link)
}

func (s *Server) handleUpsertLink(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		ID         string            `json:"id"`
		Name       string            `json:"name"`
		Type       model.LinkType    `json:"type"`
		FromNode   string            `json:"from_node"`
		ToNode     string            `json:"to_node"`
		BindAddr   string            `json:"bind_addr"`
		PublicAddr string            `json:"public_addr"`
		ServerName string            `json:"server_name"`
		Enabled    *bool             `json:"enabled"`
		Settings   map[string]string `json:"settings"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	link := model.Link{
		ID:         req.ID,
		Name:       req.Name,
		Type:       req.Type,
		FromNode:   req.FromNode,
		ToNode:     req.ToNode,
		BindAddr:   req.BindAddr,
		PublicAddr: req.PublicAddr,
		ServerName: req.ServerName,
		Enabled:    enabled,
		Settings:   req.Settings,
	}
	if link.ID == "" {
		link.ID = ids.New("link")
	}
	if link.Settings == nil {
		link.Settings = map[string]string{}
	}
	if link.Settings["secret"] == "" {
		secret, err := randomToken()
		if err != nil {
			writeError(w, err, http.StatusInternalServerError)
			return
		}
		link.Settings["secret"] = secret
	}
	if link.Type == model.LinkTLS || link.Type == model.LinkMTLS || link.Type == model.LinkWSTLS {
		if link.Settings["ca_cert"] == "" || link.Settings["server_cert"] == "" || link.Settings["server_key"] == "" {
			certName := link.ID
			if certName == "" {
				certName = link.Name
			}
			serverName := link.ServerName
			if serverName == "" {
				serverName = strings.Split(link.PublicAddr, ":")[0]
			}
			certs, err := sharedcrypto.GenerateLinkCertificates(certName, serverName)
			if err != nil {
				writeError(w, err, http.StatusInternalServerError)
				return
			}
			link.Settings["ca_cert"] = certs.CACert
			link.Settings["server_cert"] = certs.ServerCert
			link.Settings["server_key"] = certs.ServerKey
			link.Settings["client_cert"] = certs.ClientCert
			link.Settings["client_key"] = certs.ClientKey
		}
	}
	if err := validate.Link(link); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := s.store.UpsertLink(r.Context(), link); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, _ := s.store.BumpRevision(r.Context())
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "link.upsert", link.ID, link)
	writeJSON(w, link)
}

func (s *Server) handleListRoutes(w http.ResponseWriter, r *http.Request, session auth.Session) {
	routes, err := s.store.ListRoutes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, routes)
}

func (s *Server) handleGetRoute(w http.ResponseWriter, r *http.Request, session auth.Session) {
	route, err := s.store.GetRoute(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, route)
}

func (s *Server) handleUpsertRoute(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var route model.Route
	if err := readJSON(r, &route); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if route.ID == "" {
		route.ID = ids.New("route")
	}
	if err := validate.Route(route); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := s.store.UpsertRoute(r.Context(), route); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, _ := s.store.BumpRevision(r.Context())
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "route.upsert", route.ID, route)
	writeJSON(w, route)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, session auth.Session) {
	events, err := s.store.ListAudit(r.Context(), 100)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, events)
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request, session auth.Session) {
	summary, err := s.store.MetricSummary(r.Context(), 200)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, summary)
}

func (s *Server) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request, node model.Node) {
	var req struct {
		Version string           `json:"version"`
		System  model.NodeSystem `json:"system"`
	}
	_ = readJSON(r, &req)
	if err := s.store.MarkNodeSeen(r.Context(), node.ID, req.System, req.Version); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"revision": s.hub.Revision()})
}

func (s *Server) handleNodeWS(w http.ResponseWriter, r *http.Request, node model.Node) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	s.hub.RegisterSocket(node.ID, conn)
	defer s.hub.UnregisterSocket(node.ID, conn)

	var hello sharedprotocol.ControlMessage
	if err := wsjson.Read(r.Context(), conn, &hello); err != nil {
		_ = s.store.MarkNodeOffline(r.Context(), node.ID)
		return
	}
	if hello.Type != "hello" {
		_ = wsjson.Write(r.Context(), conn, sharedprotocol.ControlMessage{Type: "error", Error: "expected hello"})
		_ = s.store.MarkNodeOffline(r.Context(), node.ID)
		return
	}
	if err := s.store.MarkNodeSeen(r.Context(), node.ID, hello.System, hello.Version); err != nil {
		_ = s.store.MarkNodeOffline(r.Context(), node.ID)
		return
	}
	if err := s.sendNodeConfig(r.Context(), node.ID, conn); err != nil {
		_ = s.store.MarkNodeOffline(r.Context(), node.ID)
		return
	}

	for {
		var msg sharedprotocol.ControlMessage
		if err := wsjson.Read(r.Context(), conn, &msg); err != nil {
			_ = s.store.MarkNodeOffline(context.Background(), node.ID)
			return
		}
		switch msg.Type {
		case "heartbeat":
			_ = s.store.MarkNodeSeen(r.Context(), node.ID, msg.System, msg.Version)
		case "pull_config":
			_ = s.sendNodeConfig(r.Context(), node.ID, conn)
		}
	}
}

func (s *Server) handleNodeConfig(w http.ResponseWriter, r *http.Request, node model.Node) {
	signed, err := s.compileConfig(r.Context(), node.ID)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, signed)
}

func (s *Server) handleNodeEvents(w http.ResponseWriter, r *http.Request, node model.Node) {
	known, _ := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	revision := s.hub.Wait(r.Context(), node.ID, known, 25*time.Second)
	writeJSON(w, map[string]any{"revision": revision})
}

func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request, node model.Node) {
	var report model.MetricsReport
	if err := readJSON(r, &report); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	report.NodeID = node.ID
	if report.ObservedAt.IsZero() {
		report.ObservedAt = time.Now().UTC()
	}
	if err := s.store.InsertMetrics(r.Context(), report); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) sendNodeConfig(ctx context.Context, nodeID string, conn *websocket.Conn) error {
	signed, err := s.compileConfig(ctx, nodeID)
	if err != nil {
		return err
	}
	return wsjson.Write(ctx, conn, sharedprotocol.ControlMessage{
		Type:   "config",
		Config: &signed,
	})
}

func (s *Server) pushConfigs(ctx context.Context) {
	for _, nodeID := range s.hub.NodeIDs() {
		if err := s.pushConfig(ctx, nodeID); err != nil {
			s.log.Debug("push config failed", "node", nodeID, "error", err)
		}
	}
}

func (s *Server) pushConfig(ctx context.Context, nodeID string) error {
	signed, err := s.compileConfig(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.hub.Send(nodeID, sharedprotocol.ControlMessage{Type: "config", Config: &signed})
}

func (s *Server) compileConfig(ctx context.Context, nodeID string) (model.SignedConfig, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	links, err := s.store.ListLinks(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	routes, err := s.store.ListRoutes(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	rev, err := s.store.CurrentRevision(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	routes, links = scopeConfigForNode(nodeID, routes, links)
	cfg := model.RelayConfig{
		Revision:  rev,
		IssuedAt:  time.Now().UTC(),
		NodeID:    nodeID,
		Nodes:     nodes,
		Links:     links,
		Routes:    routes,
		ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour),
	}
	priv, _, err := s.store.GetSetting(ctx, signingKeySetting)
	if err != nil {
		return model.SignedConfig{}, err
	}
	pub, _, err := s.store.GetSetting(ctx, signingPubSetting)
	if err != nil {
		return model.SignedConfig{}, err
	}
	sig, err := sharedcrypto.SignJSON(priv, cfg)
	if err != nil {
		return model.SignedConfig{}, err
	}
	return model.SignedConfig{Config: cfg, Signature: sig, KeyID: pub}, nil
}

func scopeConfigForNode(nodeID string, routes []model.Route, links []model.Link) ([]model.Route, []model.Link) {
	linkByID := make(map[string]model.Link, len(links))
	for _, link := range links {
		linkByID[link.ID] = link
	}
	relevantLinks := make(map[string]bool)
	scopedRoutes := make([]model.Route, 0, len(routes))
	for _, route := range routes {
		relevant := route.EntryNode == nodeID
		for _, hop := range route.Hops {
			link, ok := linkByID[hop.LinkID]
			if !ok {
				continue
			}
			if link.FromNode == nodeID || link.ToNode == nodeID {
				relevant = true
				relevantLinks[link.ID] = true
			}
		}
		if relevant {
			scopedRoutes = append(scopedRoutes, route)
			for _, hop := range route.Hops {
				link, ok := linkByID[hop.LinkID]
				if !ok {
					continue
				}
				if link.FromNode == nodeID || link.ToNode == nodeID {
					relevantLinks[link.ID] = true
				}
			}
		}
	}
	scopedLinks := make([]model.Link, 0, len(relevantLinks))
	for _, link := range links {
		if !relevantLinks[link.ID] {
			continue
		}
		link.Settings = scopeLinkSettings(nodeID, link)
		scopedLinks = append(scopedLinks, link)
	}
	return scopedRoutes, scopedLinks
}

func scopeLinkSettings(nodeID string, link model.Link) map[string]string {
	settings := map[string]string{}
	copyKey := func(key string) {
		if link.Settings != nil && link.Settings[key] != "" {
			settings[key] = link.Settings[key]
		}
	}
	copyKey("secret")
	copyKey("skip_verify")
	copyKey("ca_cert")
	if nodeID == link.ToNode {
		copyKey("server_cert")
		copyKey("server_key")
	}
	if nodeID == link.FromNode && link.Type == model.LinkMTLS {
		copyKey("client_cert")
		copyKey("client_key")
	}
	return settings
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, errors.New("authentication required"), http.StatusUnauthorized)
			return
		}
		session, ok := s.sessions.Get(cookie.Value)
		if !ok {
			writeError(w, errors.New("authentication required"), http.StatusUnauthorized)
			return
		}
		next(w, r, session)
	}
}

func (s *Server) withNode(next func(http.ResponseWriter, *http.Request, model.Node)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-NyaRelay-Node-ID")
		token := r.Header.Get("X-NyaRelay-Node-Token")
		if id == "" || token == "" {
			writeError(w, errors.New("node credentials are required"), http.StatusUnauthorized)
			return
		}
		node, err := s.store.AuthenticateNode(r.Context(), id, token)
		if err != nil {
			writeError(w, err, http.StatusUnauthorized)
			return
		}
		next(w, r, node)
	}
}

func setSessionCookie(w http.ResponseWriter, session auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		Expires:  session.ExpiresAt,
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dest)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, err error, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (s *Server) handleFallbackWeb(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, fmt.Errorf("not found: %s", r.URL.Path), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>NyaRelay</title>
  <style>
    body { margin: 0; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f7f9; color: #171a1f; }
    main { max-width: 880px; margin: 12vh auto; padding: 0 24px; }
    h1 { font-size: 42px; margin: 0 0 12px; letter-spacing: 0; }
    p { color: #5b6472; font-size: 17px; line-height: 1.6; }
    code { background: #e9edf3; border-radius: 6px; padding: 2px 6px; }
  </style>
</head>
<body>
  <main>
    <h1>NyaRelay controller</h1>
    <p>Backend API is running. The React management panel will be served here after the web build is added.</p>
    <p>Check <code>/api/setup/status</code> to initialize the controller.</p>
  </main>
</body>
</html>`))
}
