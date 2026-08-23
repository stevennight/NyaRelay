package controller

import (
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
	sharedversion "nyarelay/internal/shared/version"
)

const (
	sessionCookieName              = "nyarelay_session"
	signingKeySetting              = "config_signing_private_key"
	signingPubSetting              = "config_signing_public_key"
	controllerPublicURLSetting     = "controller_public_url"
	historyMetricsRetentionSetting = "history_cleanup_metrics_retention"
	historyAuditRetentionSetting   = "history_cleanup_audit_retention"
	historyCleanupIntervalSetting  = "history_cleanup_interval"
	failureCooldownSetting         = "relay_failure_cooldown"
	maxUsernameBytes               = 64
	controlWebSocketReadLimit      = 256 * 1024
	controlWebSocketHelloTimeout   = 10 * time.Second
	controlWebSocketIdleTimeout    = 90 * time.Second
	nodeConfigLease                = 15 * time.Minute
	maxNodeCredentialBytes         = 256
	maxNodeNameBytes               = 256
	maxNodeLabelEntries            = 64
	maxNodeLabelKeyBytes           = 128
	maxNodeLabelValueBytes         = 1024
	maxNodeMetadataBytes           = 256
	maxNodeVersionBytes            = 128
	maxNodeUpdateErrorBytes        = 2048
	maxMetricStats                 = 512
	maxMetricIDBytes               = 256
	maxAgentErrors                 = 64
	maxAgentErrorScopeBytes        = 128
	maxAgentErrorMessageBytes      = 2048
	maxMetricValue                 = int64(1 << 60)
	maxMetricGoroutines            = 1 << 20
	maxMetricTimeSkew              = 24 * time.Hour
	minNodeMetricsInterval         = 5 * time.Second
	maxNodeMetricsRateEntries      = 10000
	nodeMetricsRateEntryTTL        = 15 * time.Minute
	maxProxyHeaderBytes            = 256
	dummyPasswordHash              = "pbkdf2-sha256$210000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type historyCleanupConfig struct {
	MetricsRetention time.Duration
	AuditRetention   time.Duration
	CleanupInterval  time.Duration
}

type Server struct {
	cfg             Config
	log             *slog.Logger
	store           *store.Store
	sessions        *auth.Sessions
	limiter         *auth.LoginLimiter
	nodeLimiter     *auth.LoginLimiter
	hub             *nodehub.Hub
	mux             *http.ServeMux
	cleanupMu       sync.RWMutex
	cleanupConfig   historyCleanupConfig
	cleanupWake     chan struct{}
	failureMu       sync.RWMutex
	failureCooldown time.Duration
	nodeSocketMu    sync.Mutex
	releaseMu       sync.Mutex
	releaseCache    model.SignedNodeRelease
	releaseCachedAt time.Time
	metricsMu       sync.Mutex
	metricsLast     map[string]time.Time
}

func Run(ctx context.Context, args []string) error {
	if sharedversion.IsVersionCommand(args) {
		fmt.Println(sharedversion.Print("nyarelay-controller"))
		return nil
	}
	cfg := parseConfig(args)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return err
	}
	secretKey, err := loadControllerSecretsKey(cfg)
	if err != nil {
		return err
	}
	st, err := store.OpenWithSecretKey(ctx, cfg.DBPath, secretKey)
	if err != nil {
		return err
	}
	defer closeStore(st)

	s := &Server{
		cfg:             cfg,
		log:             logging.New(cfg.LogLevel),
		store:           st,
		sessions:        auth.NewSessions(cfg.SessionLifetime),
		limiter:         auth.NewLoginLimiter(),
		nodeLimiter:     auth.NewLoginLimiter(),
		hub:             nodehub.New(),
		mux:             http.NewServeMux(),
		cleanupConfig:   historyCleanupConfigFromConfig(cfg),
		cleanupWake:     make(chan struct{}, 1),
		failureCooldown: cfg.FailureCooldown,
	}
	if err := s.ensureSigningKey(ctx); err != nil {
		return err
	}
	if err := s.loadHistoryCleanupConfig(ctx); err != nil {
		return err
	}
	if err := s.loadFailureCooldown(ctx); err != nil {
		return err
	}
	rev, _ := st.CurrentRevision(ctx)
	s.hub.SetRevision(rev)
	s.routes()

	go s.historyCleanupLoop(ctx)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           secureHeaders(s.mux),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
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
	s.mux.HandleFunc("POST /api/controller/info", s.withAuth(s.handleUpdateControllerInfo))
	s.mux.HandleFunc("GET /api/nodes", s.withAuth(s.handleListNodes))
	s.mux.HandleFunc("GET /api/nodes/{id}", s.withAuth(s.handleGetNode))
	s.mux.HandleFunc("GET /api/nodes/{id}/install", s.withAuth(s.handleGetNodeInstall))
	s.mux.HandleFunc("POST /api/nodes", s.withAuth(s.handleCreateNode))
	s.mux.HandleFunc("PATCH /api/nodes/{id}", s.withAuth(s.handleUpdateNode))
	s.mux.HandleFunc("POST /api/nodes/{id}/update", s.withAuth(s.handleUpdateNodeBinary))
	s.mux.HandleFunc("POST /api/nodes/update", s.withAuth(s.handleUpdateAllNodeBinaries))
	s.mux.HandleFunc("POST /api/nodes/revoke", s.withAuth(s.handleRevokeNode))
	s.mux.HandleFunc("GET /api/tunnels", s.withAuth(s.handleListTunnels))
	s.mux.HandleFunc("GET /api/tunnels/{id}", s.withAuth(s.handleGetTunnel))
	s.mux.HandleFunc("POST /api/tunnels", s.withAuth(s.handleUpsertTunnel))
	s.mux.HandleFunc("PATCH /api/tunnels/{id}", s.withAuth(s.handleUpsertTunnel))
	s.mux.HandleFunc("DELETE /api/tunnels/{id}", s.withAuth(s.handleDeleteTunnel))
	s.mux.HandleFunc("POST /api/tunnels/{id}/enable", s.withAuth(s.handleEnableTunnel))
	s.mux.HandleFunc("POST /api/tunnels/{id}/disable", s.withAuth(s.handleDisableTunnel))
	s.mux.HandleFunc("GET /api/forwards", s.withAuth(s.handleListForwards))
	s.mux.HandleFunc("GET /api/forwards/{id}", s.withAuth(s.handleGetForward))
	s.mux.HandleFunc("POST /api/forwards", s.withAuth(s.handleUpsertForward))
	s.mux.HandleFunc("PATCH /api/forwards/{id}", s.withAuth(s.handleUpsertForward))
	s.mux.HandleFunc("DELETE /api/forwards/{id}", s.withAuth(s.handleDeleteForward))
	s.mux.HandleFunc("POST /api/forwards/{id}/pause", s.withAuth(s.handlePauseForward))
	s.mux.HandleFunc("POST /api/forwards/{id}/resume", s.withAuth(s.handleResumeForward))
	s.mux.HandleFunc("GET /api/traffic", s.withAuth(s.handleTraffic))
	s.mux.HandleFunc("GET /api/audit", s.withAuth(s.handleAudit))

	s.mux.HandleFunc("POST /api/node/heartbeat", s.withNode(s.handleNodeHeartbeat))
	s.mux.HandleFunc("GET /api/node/ws", s.withNode(s.handleNodeWS))
	s.mux.HandleFunc("GET /api/node/config", s.withNode(s.handleNodeConfig))
	s.mux.HandleFunc("GET /api/node/events", s.withNode(s.handleNodeEvents))
	s.mux.HandleFunc("POST /api/node/metrics", s.withNode(s.handleNodeMetrics))

	s.mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	s.mux.HandleFunc("GET /downloads/nyarelay-node/manifest", s.handleDownloadNodeReleaseManifest)
	s.mux.HandleFunc("GET /downloads/nyarelay-node/signature", s.handleDownloadNodeBinarySignature)
	s.mux.HandleFunc("GET /downloads/nyarelay-node", s.handleDownloadNodeBinary)
	s.mux.HandleFunc("/", s.handleWeb)
}

func (s *Server) ensureSigningKey(ctx context.Context) error {
	privateValue, privateOK, err := s.store.GetSetting(ctx, signingKeySetting)
	if err != nil {
		return err
	}
	publicValue, publicOK, err := s.store.GetSetting(ctx, signingPubSetting)
	if err != nil {
		return err
	}
	if privateOK && !publicOK {
		privateKey, err := sharedcrypto.DecodePrivateKey(privateValue)
		if err != nil {
			return fmt.Errorf("invalid persisted signing private key: %w", err)
		}
		publicKey, ok := privateKey.Public().(ed25519.PublicKey)
		if !ok {
			return errors.New("invalid persisted signing private key")
		}
		return s.store.SetSetting(ctx, signingPubSetting, sharedcrypto.EncodeKey(publicKey))
	}
	if !privateOK && publicOK {
		return errors.New("persisted signing key pair is incomplete")
	}
	if privateOK && publicOK {
		privateKey, err := sharedcrypto.DecodePrivateKey(privateValue)
		if err != nil {
			return fmt.Errorf("invalid persisted signing private key: %w", err)
		}
		publicKey, err := sharedcrypto.DecodePublicKey(publicValue)
		if err != nil {
			return fmt.Errorf("invalid persisted signing public key: %w", err)
		}
		derived, ok := privateKey.Public().(ed25519.PublicKey)
		if !ok || string(derived) != string(publicKey) {
			return errors.New("persisted signing key pair does not match")
		}
		return nil
	}

	pub, priv, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		return err
	}
	return s.store.SetSettings(ctx, map[string]string{
		signingKeySetting: priv,
		signingPubSetting: pub,
	})
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.UserCount(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"needs_setup": count == 0,
		"public_url":  s.controllerPublicURL(r.Context()),
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.requireSameOrigin(w, r) {
		return
	}
	count, err := s.store.UserCount(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	if count > 0 {
		writeError(w, store.ErrSetupComplete, http.StatusConflict)
		return
	}
	setupKey := "setup:" + s.loginIPLimitKey(r)
	if !s.limiter.Allow(setupKey) {
		writeError(w, errors.New("too many setup attempts"), http.StatusTooManyRequests)
		return
	}
	// Count the request before password hashing so unauthenticated setup cannot
	// be used as an unbounded PBKDF2 workload. A successful setup clears it.
	s.limiter.Fail(setupKey)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)
	if err := validateUsername(username); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	user, err := s.store.CreateInitialUser(r.Context(), username, hash)
	if err != nil {
		if errors.Is(err, store.ErrSetupComplete) {
			writeError(w, err, http.StatusConflict)
			return
		}
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.limiter.Success(setupKey)
	_ = s.store.AddAudit(r.Context(), user.Username, "setup.complete", "controller", map[string]string{"username": user.Username})
	session, err := s.sessions.Create(user.ID, user.Username)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, r, session)
	writeJSON(w, map[string]any{"user": user})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireSameOrigin(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)
	limitKeys := s.loginLimitKeys(r, username)
	if !s.allowLogin(limitKeys) {
		writeError(w, errors.New("too many failed login attempts"), http.StatusTooManyRequests)
		return
	}
	failLogin := func() {
		for _, key := range limitKeys {
			s.limiter.Fail(key)
		}
	}
	successLogin := func() {
		for _, key := range limitKeys {
			s.limiter.Success(key)
		}
	}
	if err := validateUsername(username); err != nil {
		failLogin()
		writeError(w, errors.New("invalid username or password"), http.StatusUnauthorized)
		return
	}
	user, err := s.store.FindUserByUsername(r.Context(), username)
	passwordHash := dummyPasswordHash
	if err == nil {
		passwordHash = user.PasswordHash
	}
	if err != nil || !auth.VerifyPassword(passwordHash, req.Password) {
		failLogin()
		writeError(w, errors.New("invalid username or password"), http.StatusUnauthorized)
		return
	}
	if user.TOTPEnabled {
		secret, _, err := s.store.TOTPSecret(r.Context(), user.ID)
		if err != nil || !auth.VerifyTOTP(secret, req.TOTPCode, time.Now()) {
			failLogin()
			writeError(w, errors.New("invalid username or password"), http.StatusUnauthorized)
			return
		}
	}
	session, err := s.sessions.Create(user.ID, user.Username)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	successLogin()
	s.setSessionCookie(w, r, session)
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
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request, session auth.Session) {
	writeJSON(w, map[string]any{
		"user": map[string]any{
			"id":       session.UserID,
			"username": session.Username,
		},
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	tunnels, err := s.store.ListTunnels(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	forwards, err := s.store.ListForwards(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	nodes = activeNodes(nodes)
	var online, activeForwards int
	for _, node := range nodes {
		if node.Status == model.NodeOnline {
			online++
		}
	}
	for _, forward := range forwards {
		if forward.Enabled {
			activeForwards++
		}
	}
	writeJSON(w, map[string]any{
		"nodes":           len(nodes),
		"online_nodes":    online,
		"tunnels":         len(tunnels),
		"forwards":        len(forwards),
		"active_forwards": activeForwards,
		"revision":        s.hub.Revision(),
	})
}

func (s *Server) handleControllerInfo(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	pub, _, err := s.store.GetSetting(r.Context(), signingPubSetting)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"signing_key":      pub,
		"public_url":       s.controllerPublicURL(r.Context()),
		"revision":         s.hub.Revision(),
		"build":            sharedversion.Info(),
		"node_release":     s.nodeRelease(),
		"history_cleanup":  s.historyCleanupConfigResponse(),
		"failure_cooldown": formatNonNegativeDuration(s.currentFailureCooldown()),
	})
}

func (s *Server) handleUpdateControllerInfo(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		PublicURL        *string `json:"public_url"`
		MetricsRetention *string `json:"metrics_retention"`
		AuditRetention   *string `json:"audit_retention"`
		CleanupInterval  *string `json:"cleanup_interval"`
		FailureCooldown  *string `json:"failure_cooldown"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}

	next := s.currentHistoryCleanupConfig()
	nextFailureCooldown := s.currentFailureCooldown()
	failureCooldownChanged := false
	settings := make(map[string]string)
	changes := make(map[string]string)
	if req.PublicURL != nil {
		value, err := normalizeControllerURL(*req.PublicURL)
		if err != nil {
			writeError(w, fmt.Errorf("public_url: %w", err), http.StatusBadRequest)
			return
		}
		settings[controllerPublicURLSetting] = value
		changes["public_url"] = value
	}
	if req.MetricsRetention != nil {
		parsed, err := parseNonNegativeDuration(*req.MetricsRetention)
		if err != nil {
			writeError(w, fmt.Errorf("metrics_retention: %w", err), http.StatusBadRequest)
			return
		}
		next.MetricsRetention = parsed
		settings[historyMetricsRetentionSetting] = formatNonNegativeDuration(parsed)
		changes["metrics_retention"] = formatNonNegativeDuration(parsed)
	}
	if req.AuditRetention != nil {
		parsed, err := parseNonNegativeDuration(*req.AuditRetention)
		if err != nil {
			writeError(w, fmt.Errorf("audit_retention: %w", err), http.StatusBadRequest)
			return
		}
		next.AuditRetention = parsed
		settings[historyAuditRetentionSetting] = formatNonNegativeDuration(parsed)
		changes["audit_retention"] = formatNonNegativeDuration(parsed)
	}
	if req.CleanupInterval != nil {
		parsed, err := parseCleanupInterval(*req.CleanupInterval)
		if err != nil {
			writeError(w, fmt.Errorf("cleanup_interval: %w", err), http.StatusBadRequest)
			return
		}
		next.CleanupInterval = parsed
		settings[historyCleanupIntervalSetting] = formatNonNegativeDuration(parsed)
		changes["cleanup_interval"] = formatNonNegativeDuration(parsed)
	}
	if req.FailureCooldown != nil {
		parsed, err := parsePositiveDuration(*req.FailureCooldown)
		if err != nil {
			writeError(w, fmt.Errorf("failure_cooldown: %w", err), http.StatusBadRequest)
			return
		}
		nextFailureCooldown = parsed
		failureCooldownChanged = true
		settings[failureCooldownSetting] = formatNonNegativeDuration(parsed)
		changes["failure_cooldown"] = formatNonNegativeDuration(parsed)
	}

	if len(settings) == 0 {
		writeError(w, errors.New("no controller settings supplied"), http.StatusBadRequest)
		return
	}
	if err := s.store.SetSettings(r.Context(), settings); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	if req.PublicURL != nil {
		_ = s.store.AddAudit(r.Context(), session.Username, "controller.public_url.update", "controller", map[string]string{"public_url": changes["public_url"]})
	}
	if req.MetricsRetention != nil || req.AuditRetention != nil || req.CleanupInterval != nil {
		s.setHistoryCleanupConfig(next)
		_ = s.store.AddAudit(r.Context(), session.Username, "controller.history_cleanup.update", "controller", changes)
	}
	if failureCooldownChanged {
		s.setFailureCooldown(nextFailureCooldown)
		rev, err := s.store.BumpRevision(r.Context())
		if err != nil {
			writeError(w, err, http.StatusInternalServerError)
			return
		}
		s.hub.SetRevision(rev)
		s.pushConfigs(r.Context())
		_ = s.store.AddAudit(r.Context(), session.Username, "controller.failure_cooldown.update", "controller", map[string]string{
			"failure_cooldown": formatNonNegativeDuration(nextFailureCooldown),
		})
	}
	writeJSON(w, map[string]any{
		"public_url":       s.controllerPublicURL(r.Context()),
		"history_cleanup":  s.historyCleanupConfigResponse(),
		"failure_cooldown": formatNonNegativeDuration(s.currentFailureCooldown()),
	})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	nodes = activeNodes(nodes)
	writeJSON(w, nodes)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	node, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil || node.Revoked {
		writeError(w, errors.New("node not found"), http.StatusNotFound)
		return
	}
	writeJSON(w, node)
}

func (s *Server) handleGetNodeInstall(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	node, token, err := s.store.GetNodeWithToken(r.Context(), r.PathValue("id"))
	if err != nil || node.Revoked {
		writeError(w, errors.New("node not found"), http.StatusNotFound)
		return
	}
	controllerURL := controllerBaseURL(s.controllerPublicURL(r.Context()), r)
	pub, _, err := s.store.GetSetting(r.Context(), signingPubSetting)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	updateSigningKey, err := installUpdateSigningKey(controllerURL)
	if err != nil {
		writeError(w, err, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, NodeInstallInfo{
		Node:      node,
		Token:     token,
		ScriptURL: installScriptURL(controllerURL),
		BinaryURL: nodeBinaryURL(controllerURL),
		Command:   installCommand(controllerURL, node.ID, token, pub, updateSigningKey),
	})
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		Name       string            `json:"name"`
		Labels     map[string]string `json:"labels"`
		PublicHost string            `json:"public_host"`
		PortMin    int               `json:"port_min"`
		PortMax    int               `json:"port_max"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validateNodeInput(req.Name, req.Labels, req.PublicHost); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validateNodePortRange(req.PortMin, req.PortMax); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	controllerURL := controllerBaseURL(s.controllerPublicURL(r.Context()), r)
	pub, _, err := s.store.GetSetting(r.Context(), signingPubSetting)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	updateSigningKey, err := installUpdateSigningKey(controllerURL)
	if err != nil {
		writeError(w, err, http.StatusServiceUnavailable)
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	node := model.Node{
		ID:         ids.New("node"),
		Name:       strings.TrimSpace(req.Name),
		Status:     model.NodeOffline,
		Labels:     req.Labels,
		PublicHost: strings.TrimSpace(req.PublicHost),
		PortMin:    req.PortMin,
		PortMax:    req.PortMax,
		Approved:   true,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.store.UpsertNode(r.Context(), node, token); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, _ := s.store.BumpRevision(r.Context())
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "node.create", node.ID, map[string]string{"name": node.Name})
	writeJSON(w, buildNodeInstallInfo(controllerURL, pub, updateSigningKey, node, token))
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request, session auth.Session) {
	node, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil || node.Revoked {
		writeError(w, errors.New("node not found"), http.StatusNotFound)
		return
	}
	var req struct {
		Name       string            `json:"name"`
		Labels     map[string]string `json:"labels"`
		PublicHost string            `json:"public_host"`
		PortMin    int               `json:"port_min"`
		PortMax    int               `json:"port_max"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validateNodeInput(req.Name, req.Labels, req.PublicHost); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validateNodePortRange(req.PortMin, req.PortMax); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	node.Name = strings.TrimSpace(req.Name)
	node.Labels = req.Labels
	node.PublicHost = strings.TrimSpace(req.PublicHost)
	node.PortMin = req.PortMin
	node.PortMax = req.PortMax
	if err := s.store.UpdateNode(r.Context(), node); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, _ := s.store.BumpRevision(r.Context())
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "node.update", node.ID, node)
	writeJSON(w, node)
}

func (s *Server) handleRevokeNode(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validateNodeText("node id", req.ID, validate.MaxIDBytes, false); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.nodeSocketMu.Lock()
	revokeErr := s.store.RevokeNode(r.Context(), req.ID)
	var revisionErr error
	if revokeErr == nil {
		var revision int64
		revision, revisionErr = s.store.BumpRevision(r.Context())
		if revisionErr == nil {
			s.hub.SetRevision(revision)
			// Push a higher-revision empty config before closing the control
			// socket so an online node stops its data plane immediately.
			if err := s.pushConfig(r.Context(), req.ID); err != nil && !errors.Is(err, nodehub.ErrNotConnected) {
				s.log.Warn("send revoked node empty config failed", "node", req.ID, "error", err)
			}
		}
		s.hub.Close(req.ID, websocket.StatusPolicyViolation, "node revoked")
	}
	s.nodeSocketMu.Unlock()
	if revokeErr != nil {
		writeError(w, revokeErr, http.StatusBadRequest)
		return
	}
	if revisionErr != nil {
		writeError(w, revisionErr, http.StatusInternalServerError)
		return
	}
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "node.revoke", req.ID, map[string]string{"id": req.ID})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateNodeBinary(w http.ResponseWriter, r *http.Request, session auth.Session) {
	release := s.nodeRelease()
	if !release.UpdateEnabled {
		writeError(w, errors.New(release.DisabledReason), http.StatusConflict)
		return
	}
	node, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil || node.Revoked {
		writeError(w, errors.New("node not found"), http.StatusNotFound)
		return
	}
	if !nodeNeedsUpdate(node.Version, release.Manifest.Version) {
		writeJSON(w, node)
		return
	}
	updated, err := s.store.RequestNodeUpdate(r.Context(), node.ID, release.Manifest.Version)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	_ = s.store.AddAudit(r.Context(), session.Username, "node.update.request", node.ID, map[string]string{"version": release.Manifest.Version})
	if err := s.pushNodeUpdate(r.Context(), node.ID); err != nil {
		s.log.Warn("push node update failed", "node", node.ID, "error", err)
	}
	writeJSON(w, updated)
}

func (s *Server) handleUpdateAllNodeBinaries(w http.ResponseWriter, r *http.Request, session auth.Session) {
	release := s.nodeRelease()
	if !release.UpdateEnabled {
		writeError(w, errors.New(release.DisabledReason), http.StatusConflict)
		return
	}
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	var requested int
	for _, node := range nodes {
		if node.Revoked || !nodeNeedsUpdate(node.Version, release.Manifest.Version) {
			continue
		}
		if _, err := s.store.RequestNodeUpdate(r.Context(), node.ID, release.Manifest.Version); err != nil {
			writeError(w, err, http.StatusBadRequest)
			return
		}
		requested++
		if err := s.pushNodeUpdate(r.Context(), node.ID); err != nil {
			s.log.Warn("push node update failed", "node", node.ID, "error", err)
		}
	}
	_ = s.store.AddAudit(r.Context(), session.Username, "node.update.request_all", "nodes", map[string]any{"version": release.Manifest.Version, "count": requested})
	writeJSON(w, map[string]any{"version": release.Manifest.Version, "requested": requested})
}

func (s *Server) handleListTunnels(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	tunnels, err := s.store.ListTunnels(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	for i := range tunnels {
		tunnels[i] = redactTunnel(tunnels[i])
	}
	writeJSON(w, tunnels)
}

func (s *Server) handleGetTunnel(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	tunnel, err := s.store.GetTunnel(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, redactTunnel(tunnel))
}

func (s *Server) handleUpsertTunnel(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req tunnelRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if pathID := r.PathValue("id"); pathID != "" {
		req.ID = pathID
	}
	tunnel, allocations, err := s.prepareTunnel(r.Context(), req)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validate.Tunnel(tunnel); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, err := s.store.SaveTunnel(r.Context(), tunnel, allocations)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "tunnel.upsert", tunnel.ID, redactTunnel(tunnel))
	saved, err := s.store.GetTunnel(r.Context(), tunnel.ID)
	if err != nil {
		writeJSON(w, redactTunnel(tunnel))
		return
	}
	writeJSON(w, redactTunnel(saved))
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request, session auth.Session) {
	force := r.URL.Query().Get("force") == "true"
	rev, err := s.store.DeleteTunnel(r.Context(), r.PathValue("id"), force)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "tunnel.delete", r.PathValue("id"), map[string]bool{"force": force})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleEnableTunnel(w http.ResponseWriter, r *http.Request, session auth.Session) {
	s.setTunnelEnabled(w, r, session, true)
}

func (s *Server) handleDisableTunnel(w http.ResponseWriter, r *http.Request, session auth.Session) {
	s.setTunnelEnabled(w, r, session, false)
}

func (s *Server) setTunnelEnabled(w http.ResponseWriter, r *http.Request, session auth.Session, enabled bool) {
	rev, err := s.store.SetTunnelEnabled(r.Context(), r.PathValue("id"), enabled)
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	action := "tunnel.disable"
	if enabled {
		action = "tunnel.enable"
	}
	_ = s.store.AddAudit(r.Context(), session.Username, action, r.PathValue("id"), map[string]bool{"enabled": enabled})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleListForwards(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	forwards, err := s.store.ListForwards(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, forwards)
}

func (s *Server) handleGetForward(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	forward, err := s.store.GetForward(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, forward)
}

func (s *Server) handleUpsertForward(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var req forwardRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if pathID := r.PathValue("id"); pathID != "" {
		req.ID = pathID
	}
	prepared, allocations, err := s.prepareForward(r.Context(), req)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validate.Forward(prepared); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	rev, err := s.store.SaveForward(r.Context(), prepared, allocations)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "forward.upsert", prepared.ID, prepared)
	writeJSON(w, prepared)
}

func (s *Server) handleDeleteForward(w http.ResponseWriter, r *http.Request, session auth.Session) {
	rev, err := s.store.DeleteForward(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	_ = s.store.AddAudit(r.Context(), session.Username, "forward.delete", r.PathValue("id"), map[string]string{"id": r.PathValue("id")})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePauseForward(w http.ResponseWriter, r *http.Request, session auth.Session) {
	s.setForwardEnabled(w, r, session, false)
}

func (s *Server) handleResumeForward(w http.ResponseWriter, r *http.Request, session auth.Session) {
	s.setForwardEnabled(w, r, session, true)
}

func (s *Server) setForwardEnabled(w http.ResponseWriter, r *http.Request, session auth.Session, enabled bool) {
	rev, err := s.store.SetForwardEnabled(r.Context(), r.PathValue("id"), enabled)
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	s.hub.SetRevision(rev)
	s.pushConfigs(r.Context())
	action := "forward.pause"
	if enabled {
		action = "forward.resume"
	}
	_ = s.store.AddAudit(r.Context(), session.Username, action, r.PathValue("id"), map[string]bool{"enabled": enabled})
	writeJSON(w, map[string]bool{"ok": true})
}

type tunnelRequest struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Type         model.TunnelType      `json:"type"`
	Transport    model.TunnelTransport `json:"transport"`
	EntryAddress *string               `json:"entry_address"`
	Enabled      *bool                 `json:"enabled"`
	Settings     map[string]string     `json:"settings"`
	EntryNode    string                `json:"entry_node"`
	MiddleNodes  []string              `json:"middle_nodes"`
	ExitNode     string                `json:"exit_node"`
	Stages       []model.TunnelStage   `json:"stages"`
}

type forwardRequest struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	TunnelID    string                  `json:"tunnel_id"`
	Protocols   []model.ForwardProtocol `json:"protocols"`
	Listen      string                  `json:"listen"`
	Target      string                  `json:"target"`
	Targets     []model.ForwardTarget   `json:"targets"`
	Strategy    string                  `json:"strategy"`
	TCPStrategy string                  `json:"tcp_strategy"`
	UDPStrategy string                  `json:"udp_strategy"`
	Enabled     *bool                   `json:"enabled"`
}

func (s *Server) prepareTunnel(ctx context.Context, req tunnelRequest) (model.Tunnel, []model.PortAllocation, error) {
	if len(req.Stages) > validate.MaxTunnelStages {
		return model.Tunnel{}, nil, fmt.Errorf("tunnel has too many stages (maximum %d)", validate.MaxTunnelStages)
	}
	if req.Type == model.TunnelChain && len(req.MiddleNodes)+2 > validate.MaxTunnelStages {
		return model.Tunnel{}, nil, fmt.Errorf("tunnel has too many stages (maximum %d)", validate.MaxTunnelStages)
	}
	for index, stage := range req.Stages {
		if len(stage.Nodes) > validate.MaxStageNodes {
			return model.Tunnel{}, nil, fmt.Errorf("stage %d has too many nodes (maximum %d)", index, validate.MaxStageNodes)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		req.ID = ids.New("tun")
	}
	if req.Type == "" {
		req.Type = model.TunnelDirect
	}
	if req.Type == model.TunnelDirect || req.Transport == "" {
		req.Transport = model.TunnelTransportDirect
	}
	enabled := true
	var existing model.Tunnel
	if current, err := s.store.GetTunnel(ctx, req.ID); err == nil {
		existing = current
		enabled = current.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	entryAddress := ""
	if req.EntryAddress == nil {
		entryAddress = existing.EntryAddress
	} else {
		entryAddress = hostOnly(*req.EntryAddress)
	}
	tunnel := model.Tunnel{
		ID:           strings.TrimSpace(req.ID),
		Name:         strings.TrimSpace(req.Name),
		Type:         req.Type,
		Transport:    req.Transport,
		EntryAddress: entryAddress,
		Enabled:      enabled,
		Settings:     emptyStringMap(req.Settings),
		Stages:       req.Stages,
	}
	if tunnel.Settings == nil {
		tunnel.Settings = emptyStringMap(existing.Settings)
	}
	if len(tunnel.Stages) == 0 {
		tunnel.Stages = buildRequestStages(tunnel.ID, req)
	}
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return model.Tunnel{}, nil, err
	}
	nodeByID := make(map[string]model.Node, len(nodes))
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	excludedOwners := map[string]bool{}
	for _, stage := range existing.Stages {
		for _, node := range stage.Nodes {
			excludedOwners[node.ID] = true
		}
	}
	allocations, err := s.store.ListPortAllocations(ctx)
	if err != nil {
		return model.Tunnel{}, nil, err
	}
	used := usedPortSet(allocations, excludedOwners)
	var outAllocations []model.PortAllocation
	seenNodes := map[string]bool{}
	requestedStageIDs := make(map[string]bool, len(tunnel.Stages))
	for _, stage := range tunnel.Stages {
		if stageID := strings.TrimSpace(stage.ID); stageID != "" {
			requestedStageIDs[stageID] = true
		}
	}
	usedStageIDs := make(map[string]bool, len(tunnel.Stages))
	usedStageNodeIDs := make(map[string]bool)
	for i := range tunnel.Stages {
		stage := &tunnel.Stages[i]
		stage.TunnelID = tunnel.ID
		stage.Index = i
		stage.Role = roleForStage(tunnel.Type, i, len(tunnel.Stages))
		stage.ID = strings.TrimSpace(stage.ID)
		if stage.ID == "" || usedStageIDs[stage.ID] {
			if existingStage := findExistingStage(existing, i); existingStage.ID != "" &&
				!usedStageIDs[existingStage.ID] && !requestedStageIDs[existingStage.ID] {
				stage.ID = existingStage.ID
			} else {
				stage.ID = ids.New("stage")
			}
		}
		usedStageIDs[stage.ID] = true
		if stage.Strategy == "" {
			if len(stage.Nodes) > 1 {
				stage.Strategy = "failover"
			} else {
				stage.Strategy = "single"
			}
		} else {
			stage.Strategy = normalizeStageStrategyValue(stage.Strategy)
		}
		stage.TCPStrategy = normalizeStageStrategyValue(stage.TCPStrategy)
		stage.UDPStrategy = normalizeStageStrategyValue(stage.UDPStrategy)
		if len(stage.Nodes) == 0 {
			return model.Tunnel{}, nil, fmt.Errorf("stage %d must have at least one node", i)
		}
		for j := range stage.Nodes {
			node := &stage.Nodes[j]
			node.TunnelID = tunnel.ID
			node.StageID = stage.ID
			node.ID = strings.TrimSpace(node.ID)
			node.NodeID = strings.TrimSpace(node.NodeID)
			if node.NodeID == "" {
				return model.Tunnel{}, nil, fmt.Errorf("stage %d node id is required", i)
			}
			if seenNodes[node.NodeID] {
				return model.Tunnel{}, nil, fmt.Errorf("node %s appears more than once in tunnel", node.NodeID)
			}
			seenNodes[node.NodeID] = true
			realNode, ok := nodeByID[node.NodeID]
			if !ok || realNode.Revoked {
				return model.Tunnel{}, nil, fmt.Errorf("node %s not found", node.NodeID)
			}
			existingNode := findExistingStageNode(existing, i, node.NodeID)
			if node.ID == "" || usedStageNodeIDs[node.ID] {
				if existingNode.ID != "" && !usedStageNodeIDs[existingNode.ID] {
					node.ID = existingNode.ID
				} else {
					node.ID = ids.New("stage_node")
				}
			}
			usedStageNodeIDs[node.ID] = true
			if node.Weight <= 0 {
				node.Weight = 1
			}
			node.Protocols, err = normalizeStageNodeProtocols(node.Protocols)
			if err != nil {
				return model.Tunnel{}, nil, fmt.Errorf("stage %d node %s: %w", i, node.NodeID, err)
			}
			node.Settings = mergeSettings(existingNode.Settings, node.Settings)
			if tunnel.Type == model.TunnelDirect || stage.Role == model.TunnelStageEntry {
				node.ListenAddr = ""
				node.PublicAddr = ""
				node.ConnectAddr = strings.TrimSpace(node.ConnectAddr)
				node.Settings = map[string]string{}
				continue
			}
			if node.ListenAddr == "" {
				node.ListenAddr = existingNode.ListenAddr
			}
			if node.PublicAddr == "" {
				node.PublicAddr = existingNode.PublicAddr
			}
			if node.ConnectAddr == "" {
				node.ConnectAddr = existingNode.ConnectAddr
			}
			port := 0
			if node.ListenAddr == "" {
				port, err = allocatePort(realNode, "tcp", used)
				if err != nil {
					return model.Tunnel{}, nil, err
				}
				node.ListenAddr = net.JoinHostPort("", strconv.Itoa(port))
			} else {
				port, err = portFromAddr(node.ListenAddr)
				if err != nil {
					return model.Tunnel{}, nil, err
				}
				if err := ensurePortInRange(realNode, port); err != nil {
					return model.Tunnel{}, nil, err
				}
				markPortUsed(used, realNode.ID, "tcp", port)
			}
			if node.PublicAddr == "" {
				if host := defaultNodePublicHost(realNode); host != "" {
					node.PublicAddr = net.JoinHostPort(host, strconv.Itoa(port))
				}
			}
			if node.PublicAddr == "" && node.ConnectAddr == "" {
				return model.Tunnel{}, nil, fmt.Errorf("node %s requires public_host, reported system.ip, or connect_addr for chain stage", realNode.ID)
			}
			if node.Settings == nil {
				node.Settings = map[string]string{}
			}
			if node.Settings["secret"] == "" {
				secret, err := randomToken()
				if err != nil {
					return model.Tunnel{}, nil, err
				}
				node.Settings["secret"] = secret
			}
			if err := ensureTunnelCertificates(tunnel, node); err != nil {
				return model.Tunnel{}, nil, err
			}
			outAllocations = append(outAllocations, model.PortAllocation{
				ID:        ids.New("alloc"),
				NodeID:    realNode.ID,
				OwnerKind: "tunnel_stage_node",
				OwnerID:   node.ID,
				Protocol:  "tcp",
				Port:      port,
				BindAddr:  node.ListenAddr,
			})
		}
	}
	if tunnel.EntryAddress == "" {
		entryNodeID, err := tunnelEntryNodeID(tunnel)
		if err != nil {
			return model.Tunnel{}, nil, err
		}
		entryNode := nodeByID[entryNodeID]
		tunnel.EntryAddress = defaultTunnelEntryAddress(entryNode)
	}
	return tunnel, outAllocations, nil
}

func (s *Server) prepareForward(ctx context.Context, req forwardRequest) (model.Forward, []model.PortAllocation, error) {
	if strings.TrimSpace(req.ID) == "" {
		req.ID = ids.New("fwd")
	}
	enabled := true
	if existing, err := s.store.GetForward(ctx, req.ID); err == nil {
		enabled = existing.Enabled
		if req.Name == "" {
			req.Name = existing.Name
		}
		if req.TunnelID == "" {
			req.TunnelID = existing.TunnelID
		}
		if len(req.Protocols) == 0 {
			req.Protocols = existing.Protocols
		}
		if req.Listen == "" {
			req.Listen = existing.Listen
		}
		if req.Targets == nil && req.Target == "" {
			req.Targets = existing.Targets
			if req.Targets == nil {
				req.Target = existing.Target
			}
		}
		if req.Strategy == "" {
			req.Strategy = existing.Strategy
		}
		if req.TCPStrategy == "" {
			req.TCPStrategy = existing.TCPStrategy
		}
		if req.UDPStrategy == "" {
			req.UDPStrategy = existing.UDPStrategy
		}
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	protocols, err := normalizeForwardProtocols(req.Protocols)
	if err != nil {
		return model.Forward{}, nil, err
	}
	tunnel, err := s.store.GetTunnel(ctx, req.TunnelID)
	if err != nil {
		return model.Forward{}, nil, errors.New("tunnel not found")
	}
	if !tunnel.Enabled {
		return model.Forward{}, nil, errors.New("tunnel is disabled")
	}
	if err := validateTunnelEffectiveCandidates(tunnel, protocols); err != nil {
		return model.Forward{}, nil, err
	}
	targets, err := normalizeForwardTargets(req.ID, req.Target, req.Targets)
	if err != nil {
		return model.Forward{}, nil, err
	}
	strategy := normalizeStageStrategyValue(req.Strategy)
	if strategy == "" {
		if len(targets) > 1 {
			strategy = "failover"
		} else {
			strategy = "single"
		}
	}
	tcpStrategy := normalizeStageStrategyValue(req.TCPStrategy)
	udpStrategy := normalizeStageStrategyValue(req.UDPStrategy)
	entryCandidates, err := forwardEntryCandidates(ctx, s.store, tunnel, protocols)
	if err != nil {
		return model.Forward{}, nil, err
	}
	forward := model.Forward{
		ID:          strings.TrimSpace(req.ID),
		Name:        strings.TrimSpace(req.Name),
		TunnelID:    strings.TrimSpace(req.TunnelID),
		Protocols:   protocols,
		Listen:      strings.TrimSpace(req.Listen),
		Target:      targets[0].Address,
		Targets:     targets,
		Strategy:    strategy,
		TCPStrategy: tcpStrategy,
		UDPStrategy: udpStrategy,
		Enabled:     enabled,
	}
	excludedOwners := map[string]bool{forward.ID: true}
	allocations, err := s.store.ListPortAllocations(ctx)
	if err != nil {
		return model.Forward{}, nil, err
	}
	used := usedPortSet(allocations, excludedOwners)
	port := 0
	if forward.Listen == "" {
		port, err = allocateSharedProtocolPortAcrossEntryCandidates(entryCandidates, used)
		if err != nil {
			return model.Forward{}, nil, err
		}
		forward.Listen = net.JoinHostPort("", strconv.Itoa(port))
	} else {
		port, err = portFromAddr(forward.Listen)
		if err != nil {
			return model.Forward{}, nil, err
		}
		if err := ensurePortAvailableAcrossEntryCandidates(entryCandidates, port, used); err != nil {
			return model.Forward{}, nil, err
		}
	}
	var outAllocations []model.PortAllocation
	for _, entryCandidate := range entryCandidates {
		for _, protocol := range entryCandidate.Protocols {
			outAllocations = append(outAllocations, model.PortAllocation{
				ID:        ids.New("alloc"),
				NodeID:    entryCandidate.Node.ID,
				OwnerKind: "forward",
				OwnerID:   forward.ID,
				Protocol:  string(protocol),
				Port:      port,
				BindAddr:  forward.Listen,
			})
		}
	}
	return forward, outAllocations, nil
}

func buildRequestStages(tunnelID string, req tunnelRequest) []model.TunnelStage {
	nodeIDs := []string{strings.TrimSpace(req.EntryNode)}
	if req.Type == model.TunnelChain {
		nodeIDs = append(nodeIDs, req.MiddleNodes...)
		nodeIDs = append(nodeIDs, strings.TrimSpace(req.ExitNode))
	}
	stages := make([]model.TunnelStage, 0, len(nodeIDs))
	for i, nodeID := range nodeIDs {
		if strings.TrimSpace(nodeID) == "" {
			continue
		}
		stageID := ids.New("stage")
		stages = append(stages, model.TunnelStage{
			ID:       stageID,
			TunnelID: tunnelID,
			Index:    i,
			Role:     roleForStage(req.Type, i, len(nodeIDs)),
			Strategy: "single",
			Nodes: []model.TunnelStageNode{{
				ID:       ids.New("stage_node"),
				TunnelID: tunnelID,
				StageID:  stageID,
				NodeID:   strings.TrimSpace(nodeID),
				Weight:   1,
			}},
		})
	}
	return stages
}

func roleForStage(tunnelType model.TunnelType, index, count int) model.TunnelStageRole {
	if tunnelType == model.TunnelDirect || index == 0 {
		return model.TunnelStageEntry
	}
	if index == count-1 {
		return model.TunnelStageExit
	}
	return model.TunnelStageMiddle
}

func findExistingStage(tunnel model.Tunnel, index int) model.TunnelStage {
	for _, stage := range tunnel.Stages {
		if stage.Index == index {
			return stage
		}
	}
	return model.TunnelStage{}
}

func findExistingStageNode(tunnel model.Tunnel, index int, nodeID string) model.TunnelStageNode {
	stage := findExistingStage(tunnel, index)
	for _, node := range stage.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	return model.TunnelStageNode{}
}

func ensureTunnelCertificates(tunnel model.Tunnel, node *model.TunnelStageNode) error {
	if tunnel.Transport != model.TunnelTransportTLS && tunnel.Transport != model.TunnelTransportMTLS && tunnel.Transport != model.TunnelTransportWSTLS {
		return nil
	}
	if node.Settings["ca_cert"] != "" && node.Settings["server_cert"] != "" && node.Settings["server_key"] != "" {
		return nil
	}
	serverName := node.Settings["server_name"]
	if serverName == "" {
		serverName = firstHost(node.PublicAddr, node.ConnectAddr)
	}
	certs, err := sharedcrypto.GenerateTunnelCertificates(node.ID, serverName)
	if err != nil {
		return err
	}
	node.Settings["ca_cert"] = certs.CACert
	node.Settings["server_cert"] = certs.ServerCert
	node.Settings["server_key"] = certs.ServerKey
	node.Settings["client_cert"] = certs.ClientCert
	node.Settings["client_key"] = certs.ClientKey
	return nil
}

func firstHost(addrs ...string) string {
	for _, addr := range addrs {
		if host := hostOnly(addr); host != "" {
			return host
		}
	}
	return ""
}

func normalizeForwardProtocols(protocols []model.ForwardProtocol) ([]model.ForwardProtocol, error) {
	seen := map[model.ForwardProtocol]bool{}
	for _, protocol := range protocols {
		switch protocol {
		case model.ForwardProtocolTCP, model.ForwardProtocolUDP:
			seen[protocol] = true
		default:
			return nil, fmt.Errorf("unsupported forward protocol %q", protocol)
		}
	}
	var out []model.ForwardProtocol
	if seen[model.ForwardProtocolTCP] {
		out = append(out, model.ForwardProtocolTCP)
	}
	if seen[model.ForwardProtocolUDP] {
		out = append(out, model.ForwardProtocolUDP)
	}
	if len(out) == 0 {
		return nil, errors.New("forward protocol is required")
	}
	return out, nil
}

func normalizeForwardTargets(forwardID, legacyTarget string, requested []model.ForwardTarget) ([]model.ForwardTarget, error) {
	if requested != nil && strings.TrimSpace(legacyTarget) != "" {
		return nil, errors.New("target and targets cannot both be set")
	}
	targets := requested
	if targets == nil {
		if strings.TrimSpace(legacyTarget) == "" {
			return nil, errors.New("forward target is required")
		}
		targets = []model.ForwardTarget{{
			ID:      "legacy:" + strings.TrimSpace(forwardID),
			Address: strings.TrimSpace(legacyTarget),
			Weight:  1,
			Enabled: true,
		}}
	}
	if len(targets) == 0 {
		return nil, errors.New("forward target is required")
	}
	if len(targets) > validate.MaxForwardTargets {
		return nil, fmt.Errorf("forward has too many targets (maximum %d)", validate.MaxForwardTargets)
	}
	out := make([]model.ForwardTarget, 0, len(targets))
	seenIDs := map[string]bool{}
	for index, target := range targets {
		target.ForwardID = strings.TrimSpace(forwardID)
		target.ID = strings.TrimSpace(target.ID)
		if target.ID == "" {
			target.ID = ids.New("target")
		}
		if seenIDs[target.ID] {
			return nil, fmt.Errorf("duplicate forward target id %q", target.ID)
		}
		seenIDs[target.ID] = true
		target.Address = strings.TrimSpace(target.Address)
		target.Position = index
		if target.Weight <= 0 {
			target.Weight = 1
		}
		normalizedProtocols, err := normalizeStageNodeProtocols(target.Protocols)
		if err != nil {
			return nil, fmt.Errorf("forward target %d: %w", index+1, err)
		}
		target.Protocols = normalizedProtocols
		out = append(out, target)
	}
	return out, nil
}

func normalizeStageStrategyValue(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "":
		return ""
	case "single":
		return "single"
	case "round_robin":
		return "round_robin"
	case "random":
		return "random"
	case "failover":
		return "failover"
	default:
		return strings.TrimSpace(strategy)
	}
}

func normalizeStageNodeProtocols(protocols []model.ForwardProtocol) ([]model.ForwardProtocol, error) {
	if len(protocols) == 0 {
		return nil, nil
	}
	seen := map[model.ForwardProtocol]bool{}
	for _, protocol := range protocols {
		switch protocol {
		case model.ForwardProtocolTCP, model.ForwardProtocolUDP:
			seen[protocol] = true
		default:
			return nil, fmt.Errorf("unsupported protocol %q", protocol)
		}
	}
	out := make([]model.ForwardProtocol, 0, 2)
	if seen[model.ForwardProtocolTCP] {
		out = append(out, model.ForwardProtocolTCP)
	}
	if seen[model.ForwardProtocolUDP] {
		out = append(out, model.ForwardProtocolUDP)
	}
	return out, nil
}

func stageNodeSupportsProtocol(node model.TunnelStageNode, protocol model.ForwardProtocol) bool {
	if len(node.Protocols) == 0 {
		return protocol == model.ForwardProtocolTCP || protocol == model.ForwardProtocolUDP
	}
	for _, candidateProtocol := range node.Protocols {
		if candidateProtocol == protocol {
			return true
		}
	}
	return false
}

func validateTunnelEffectiveCandidates(tunnel model.Tunnel, protocols []model.ForwardProtocol) error {
	for _, stage := range tunnel.Stages {
		for _, protocol := range protocols {
			if !stageHasEffectiveCandidate(stage, protocol) {
				return fmt.Errorf("stage %d has no %s candidate", stage.Index, protocol)
			}
		}
	}
	return nil
}

func stageHasEffectiveCandidate(stage model.TunnelStage, protocol model.ForwardProtocol) bool {
	for _, node := range stage.Nodes {
		if strings.TrimSpace(node.NodeID) != "" && stageNodeSupportsProtocol(node, protocol) {
			return true
		}
	}
	return false
}

type forwardEntryCandidate struct {
	NodeID    string
	Node      model.Node
	Protocols []model.ForwardProtocol
}

type nodeStore interface {
	GetNode(ctx context.Context, id string) (model.Node, error)
}

func forwardEntryCandidates(ctx context.Context, store nodeStore, tunnel model.Tunnel, protocols []model.ForwardProtocol) ([]forwardEntryCandidate, error) {
	if len(tunnel.Stages) == 0 || len(tunnel.Stages[0].Nodes) == 0 {
		return nil, errors.New("tunnel has no entry node")
	}
	out := make([]forwardEntryCandidate, 0, len(tunnel.Stages[0].Nodes))
	for _, node := range tunnel.Stages[0].Nodes {
		nodeID := strings.TrimSpace(node.NodeID)
		if nodeID == "" {
			continue
		}
		candidateProtocols := filterForwardProtocols(protocols, node.Protocols)
		if len(candidateProtocols) == 0 {
			continue
		}
		entryNode, err := store.GetNode(ctx, nodeID)
		if err != nil || entryNode.Revoked {
			return nil, errors.New("entry node not found")
		}
		out = append(out, forwardEntryCandidate{
			NodeID:    nodeID,
			Node:      entryNode,
			Protocols: candidateProtocols,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("tunnel has no entry node for forward protocols")
	}
	return out, nil
}

func filterForwardProtocols(protocols []model.ForwardProtocol, candidateProtocols []model.ForwardProtocol) []model.ForwardProtocol {
	out := make([]model.ForwardProtocol, 0, len(protocols))
	for _, protocol := range protocols {
		if candidateSupportsProtocol(candidateProtocols, protocol) {
			out = append(out, protocol)
		}
	}
	return out
}

func candidateSupportsProtocol(candidateProtocols []model.ForwardProtocol, protocol model.ForwardProtocol) bool {
	if len(candidateProtocols) == 0 {
		return protocol == model.ForwardProtocolTCP || protocol == model.ForwardProtocolUDP
	}
	for _, candidateProtocol := range candidateProtocols {
		if candidateProtocol == protocol {
			return true
		}
	}
	return false
}

func tunnelEntryNodeIDs(tunnel model.Tunnel) ([]string, error) {
	if len(tunnel.Stages) == 0 || len(tunnel.Stages[0].Nodes) == 0 {
		return nil, errors.New("tunnel has no entry node")
	}
	out := make([]string, 0, len(tunnel.Stages[0].Nodes))
	for _, node := range tunnel.Stages[0].Nodes {
		if strings.TrimSpace(node.NodeID) != "" {
			out = append(out, node.NodeID)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("tunnel has no entry node")
	}
	return out, nil
}

func tunnelEntryNodeID(tunnel model.Tunnel) (string, error) {
	entryNodeIDs, err := tunnelEntryNodeIDs(tunnel)
	if err != nil {
		return "", err
	}
	return entryNodeIDs[0], nil
}

func defaultTunnelEntryAddress(node model.Node) string {
	return defaultNodePublicHost(node)
}

func defaultNodePublicHost(node model.Node) string {
	if host := hostOnly(node.PublicHost); host != "" {
		return host
	}
	return hostOnly(node.System.IP)
}

type usedPorts map[string]map[string]map[int]bool

func usedPortSet(allocations []model.PortAllocation, excludedOwners map[string]bool) usedPorts {
	used := usedPorts{}
	for _, allocation := range allocations {
		if excludedOwners[allocation.OwnerID] {
			continue
		}
		markPortUsed(used, allocation.NodeID, allocation.Protocol, allocation.Port)
	}
	return used
}

func allocateSharedProtocolPortAcrossEntryCandidates(candidates []forwardEntryCandidate, used usedPorts) (int, error) {
	if len(candidates) == 0 {
		return 0, errors.New("no entry nodes available")
	}
	portMin, portMax := normalizedPortRange(candidates[0].Node)
	for _, candidate := range candidates[1:] {
		nodeMin, nodeMax := normalizedPortRange(candidate.Node)
		if nodeMin > portMin {
			portMin = nodeMin
		}
		if nodeMax < portMax {
			portMax = nodeMax
		}
	}
	for port := portMin; port <= portMax; port++ {
		if !portAvailableAcrossEntryCandidates(candidates, port, used) {
			continue
		}
		for _, candidate := range candidates {
			for _, protocol := range candidate.Protocols {
				markPortUsed(used, candidate.Node.ID, string(protocol), port)
			}
		}
		return port, nil
	}
	return 0, errors.New("no common forward port available across entry nodes")
}

func ensurePortAvailableAcrossEntryCandidates(candidates []forwardEntryCandidate, port int, used usedPorts) error {
	if len(candidates) == 0 {
		return errors.New("no entry nodes available")
	}
	for _, candidate := range candidates {
		if err := ensurePortInRange(candidate.Node, port); err != nil {
			return err
		}
		for _, protocol := range candidate.Protocols {
			if isPortUsed(used, candidate.Node.ID, string(protocol), port) {
				return fmt.Errorf("listen port %d/%s is already in use on node %s", port, protocol, candidate.Node.ID)
			}
		}
	}
	for _, candidate := range candidates {
		for _, protocol := range candidate.Protocols {
			markPortUsed(used, candidate.Node.ID, string(protocol), port)
		}
	}
	return nil
}

func portAvailableAcrossEntryCandidates(candidates []forwardEntryCandidate, port int, used usedPorts) bool {
	for _, candidate := range candidates {
		if err := ensurePortInRange(candidate.Node, port); err != nil {
			return false
		}
		for _, protocol := range candidate.Protocols {
			if isPortUsed(used, candidate.Node.ID, string(protocol), port) {
				return false
			}
		}
	}
	return true
}

func allocatePort(node model.Node, protocol string, used usedPorts) (int, error) {
	portMin, portMax := normalizedPortRange(node)
	for port := portMin; port <= portMax; port++ {
		if isPortUsed(used, node.ID, protocol, port) {
			continue
		}
		markPortUsed(used, node.ID, protocol, port)
		return port, nil
	}
	return 0, fmt.Errorf("no free %s port available on node %s", protocol, node.ID)
}

func normalizedPortRange(node model.Node) (int, int) {
	portMin, portMax := node.PortMin, node.PortMax
	if portMin <= 0 {
		portMin = 10000
	}
	if portMax <= 0 {
		portMax = 65535
	}
	return portMin, portMax
}

func ensurePortInRange(node model.Node, port int) error {
	portMin, portMax := normalizedPortRange(node)
	if port < portMin || port > portMax {
		return fmt.Errorf("port %d is outside node %s range %d-%d", port, node.ID, portMin, portMax)
	}
	return nil
}

func portFromAddr(addr string) (int, error) {
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", portText)
	}
	return port, nil
}

func isPortUsed(used usedPorts, nodeID, protocol string, port int) bool {
	if used[nodeID] == nil || used[nodeID][protocol] == nil {
		return false
	}
	return used[nodeID][protocol][port]
}

func markPortUsed(used usedPorts, nodeID, protocol string, port int) {
	if used[nodeID] == nil {
		used[nodeID] = map[string]map[int]bool{}
	}
	if used[nodeID][protocol] == nil {
		used[nodeID][protocol] = map[int]bool{}
	}
	used[nodeID][protocol][port] = true
}

func mergeSettings(base map[string]string, override map[string]string) map[string]string {
	out := emptyStringMap(base)
	for key, value := range override {
		out[key] = value
	}
	return out
}

func emptyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func redactTunnel(tunnel model.Tunnel) model.Tunnel {
	tunnel.Settings = redactSettings(tunnel.Settings)
	for i := range tunnel.Stages {
		for j := range tunnel.Stages[i].Nodes {
			tunnel.Stages[i].Nodes[j].Settings = redactSettings(tunnel.Stages[i].Nodes[j].Settings)
		}
	}
	return tunnel
}

func redactSettings(settings map[string]string) map[string]string {
	if len(settings) == 0 {
		return settings
	}
	out := make(map[string]string, len(settings))
	for key, value := range settings {
		switch key {
		case "secret", "server_key", "client_key", "server_cert", "client_cert", "ca_cert":
			out[key] = ""
		default:
			out[key] = value
		}
	}
	return out
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	events, err := s.store.ListAudit(r.Context(), 100)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, events)
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request, _ auth.Session) {
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
	if err := readJSON(r, &req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := validateNodeHeartbeat(req.Version, req.System); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
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
	defer closeWebSocket(conn, websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(controlWebSocketReadLimit)

	var hello sharedprotocol.ControlMessage
	helloCtx, cancelHello := context.WithTimeout(r.Context(), controlWebSocketHelloTimeout)
	err = wsjson.Read(helloCtx, conn, &hello)
	cancelHello()
	if err != nil {
		logNodeWebSocketReadFailure(s.log, r.Context(), err, "node", node.ID, "phase", "hello")
		return
	}
	if hello.Type != "hello" {
		s.log.Warn("node websocket hello rejected", "node", node.ID, "type", hello.Type)
		return
	}
	if err := validateNodeHello(node.ID, hello); err != nil {
		s.log.Warn("node websocket hello rejected", "node", node.ID, "error", err)
		return
	}
	s.nodeSocketMu.Lock()
	current, err := s.store.GetNode(r.Context(), node.ID)
	if err != nil || current.Revoked || !current.Approved {
		s.nodeSocketMu.Unlock()
		return
	}
	if err := s.store.MarkNodeSeen(r.Context(), node.ID, hello.System, hello.Version); err != nil {
		s.nodeSocketMu.Unlock()
		s.log.Error("mark node seen failed", "node", node.ID, "error", err)
		return
	}
	s.log.Info("node online", "node", node.ID, "version", hello.Version)
	s.hub.RegisterSocket(node.ID, conn)
	s.nodeSocketMu.Unlock()
	defer func() {
		if s.hub.UnregisterSocket(node.ID, conn) {
			if err := s.store.MarkNodeOffline(context.Background(), node.ID); err != nil {
				s.log.Error("mark node offline failed", "node", node.ID, "error", err)
			} else {
				s.log.Info("node offline", "node", node.ID)
			}
		}
	}()
	if err := s.pushConfig(r.Context(), node.ID); err != nil {
		s.log.Warn("send node config failed", "node", node.ID, "error", err)
		return
	}
	if err := s.sendNodeUpdateIfNeeded(r.Context(), node.ID, func(msg sharedprotocol.ControlMessage) error {
		return s.hub.SendContext(r.Context(), node.ID, msg)
	}); err != nil {
		logNodeWebSocketFailure(s.log, r.Context(), "send node update failed", err, "node", node.ID)
	}

	for {
		var msg sharedprotocol.ControlMessage
		readCtx, cancelRead := context.WithTimeout(r.Context(), controlWebSocketIdleTimeout)
		err := wsjson.Read(readCtx, conn, &msg)
		cancelRead()
		if err != nil {
			logNodeWebSocketReadFailure(s.log, r.Context(), err, "node", node.ID)
			return
		}
		if err := validateNodeControlMessage(node.ID, msg); err != nil {
			s.log.Warn("node websocket message rejected", "node", node.ID, "error", err)
			return
		}
		switch msg.Type {
		case "heartbeat":
			if err := s.store.MarkNodeSeen(r.Context(), node.ID, msg.System, msg.Version); err != nil {
				s.log.Error("mark node heartbeat failed", "node", node.ID, "error", err)
			}
			if msg.UpdateReport != nil {
				if err := s.store.UpdateNodeReport(r.Context(), node.ID, *msg.UpdateReport); err != nil {
					s.log.Warn("update node report failed", "node", node.ID, "error", err)
				}
			}
			if err := s.pushNodeUpdate(r.Context(), node.ID); err != nil {
				logNodeWebSocketFailure(s.log, r.Context(), "push node update failed", err, "node", node.ID)
			}
		case "pull_config":
			if err := s.pushConfig(r.Context(), node.ID); err != nil {
				s.log.Warn("send node config failed", "node", node.ID, "error", err)
			}
		case "update_status":
			if msg.UpdateReport != nil {
				if err := s.store.UpdateNodeReport(r.Context(), node.ID, *msg.UpdateReport); err != nil {
					s.log.Warn("update node report failed", "node", node.ID, "error", err)
				}
			}
		}
	}
}

func logNodeWebSocketFailure(log *slog.Logger, ctx context.Context, message string, err error, args ...any) {
	args = append(args, "error", err)
	if ctx.Err() != nil {
		log.Debug(message, args...)
		return
	}
	log.Warn(message, args...)
}

func logNodeWebSocketReadFailure(log *slog.Logger, ctx context.Context, err error, args ...any) {
	status := websocket.CloseStatus(err)
	if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
		args = append(args, "error", err)
		log.Debug("node websocket read failed", args...)
		return
	}
	logNodeWebSocketFailure(log, ctx, "node websocket read failed", err, args...)
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
	if !s.allowNodeMetrics(node.ID) {
		writeError(w, errors.New("metrics reports are too frequent"), http.StatusTooManyRequests)
		return
	}
	var report model.MetricsReport
	if err := readJSON(r, &report); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	report.NodeID = node.ID
	if report.ObservedAt.IsZero() {
		report.ObservedAt = time.Now().UTC()
	}
	if err := validateMetricsReport(report); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if err := s.store.InsertMetrics(r.Context(), report); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) allowNodeMetrics(nodeID string) bool {
	now := time.Now()
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metricsLast == nil {
		s.metricsLast = make(map[string]time.Time)
	}
	if last, ok := s.metricsLast[nodeID]; ok && now.Sub(last) < minNodeMetricsInterval {
		return false
	}
	for id, last := range s.metricsLast {
		if now.Sub(last) > nodeMetricsRateEntryTTL {
			delete(s.metricsLast, id)
		}
	}
	if _, exists := s.metricsLast[nodeID]; !exists && len(s.metricsLast) >= maxNodeMetricsRateEntries {
		var oldestID string
		var oldest time.Time
		for id, last := range s.metricsLast {
			if oldestID == "" || last.Before(oldest) {
				oldestID = id
				oldest = last
			}
		}
		if oldestID != "" {
			delete(s.metricsLast, oldestID)
		}
	}
	s.metricsLast[nodeID] = now
	return true
}

func (s *Server) pushConfigs(ctx context.Context) {
	for _, nodeID := range s.hub.NodeIDs() {
		if err := s.pushConfig(ctx, nodeID); err != nil {
			s.log.Warn("push config failed", "node", nodeID, "error", err)
		}
	}
}

func (s *Server) pushConfig(ctx context.Context, nodeID string) error {
	signed, err := s.compileConfig(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.hub.SendContext(ctx, nodeID, sharedprotocol.ControlMessage{Type: "config", Config: &signed})
}

func (s *Server) pushNodeUpdate(ctx context.Context, nodeID string) error {
	return s.sendNodeUpdateIfNeeded(ctx, nodeID, func(msg sharedprotocol.ControlMessage) error {
		return s.hub.SendContext(ctx, nodeID, msg)
	})
}

func (s *Server) sendNodeUpdateIfNeeded(ctx context.Context, nodeID string, send func(sharedprotocol.ControlMessage) error) error {
	node, err := s.store.GetNode(ctx, nodeID)
	if err != nil || node.Revoked {
		return err
	}
	if node.UpdateStatus != model.NodeUpdateRequested || node.DesiredVersion == "" {
		return nil
	}
	if !nodeNeedsUpdate(node.Version, node.DesiredVersion) {
		return nil
	}
	release := s.nodeRelease()
	if !release.UpdateEnabled {
		return errors.New(release.DisabledReason)
	}
	if release.Manifest.Version != node.DesiredVersion {
		return nil
	}
	return send(sharedprotocol.ControlMessage{
		Type: "update",
		Update: &model.NodeUpdateCommand{
			Version:      release.Manifest.Version,
			Manifest:     release.Manifest,
			Signature:    release.Signature,
			SigningKeyID: release.SigningKeyID,
		},
	})
}

func (s *Server) compileConfig(ctx context.Context, nodeID string) (model.SignedConfig, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	activeNodeIDs := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if !node.Revoked {
			activeNodeIDs[node.ID] = true
		}
	}
	tunnels, err := s.store.ListTunnels(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	forwards, err := s.store.ListForwards(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	rev, err := s.store.CurrentRevision(ctx)
	if err != nil {
		return model.SignedConfig{}, err
	}
	scopedTunnels, scopedForwards := scopeConfigForNodeWithActiveNodes(nodeID, activeNodeIDs, tunnels, forwards)
	cfg := model.RelayConfig{
		Revision:               rev,
		IssuedAt:               time.Now().UTC(),
		NodeID:                 nodeID,
		Tunnels:                scopedTunnels,
		Forwards:               scopedForwards,
		FailureCooldownSeconds: int64(s.currentFailureCooldown() / time.Second),
	}
	cfg.ExpiresAt = cfg.IssuedAt.Add(nodeConfigLease)
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

func activeNodes(nodes []model.Node) []model.Node {
	out := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.Revoked {
			continue
		}
		out = append(out, node)
	}
	return out
}

func scopeConfigForNode(nodeID string, tunnels []model.Tunnel, forwards []model.Forward) ([]model.TunnelRuntime, []model.ForwardRuntime) {
	return scopeConfigForNodeWithActiveNodes(nodeID, nil, tunnels, forwards)
}

func scopeConfigForNodeWithActiveNodes(nodeID string, activeNodeIDs map[string]bool, tunnels []model.Tunnel, forwards []model.Forward) ([]model.TunnelRuntime, []model.ForwardRuntime) {
	forwardsByTunnel := make(map[string][]model.Forward)
	for _, forward := range forwards {
		forwardsByTunnel[forward.TunnelID] = append(forwardsByTunnel[forward.TunnelID], forward)
	}
	var scopedTunnels []model.TunnelRuntime
	var scopedForwards []model.ForwardRuntime
	for _, tunnel := range tunnels {
		if activeNodeIDs != nil {
			var ok bool
			tunnel, ok = filterActiveTunnelNodes(tunnel, activeNodeIDs)
			if !ok {
				continue
			}
		}
		if !tunnel.Enabled || !nodeParticipatesInTunnel(nodeID, tunnel) {
			continue
		}
		scopedTunnels = append(scopedTunnels, scopeTunnelRuntime(nodeID, tunnel))
		role, ok := nodeTunnelRole(nodeID, tunnel)
		if !ok {
			continue
		}
		for _, forward := range forwardsByTunnel[tunnel.ID] {
			if scopedForward, ok := scopeForwardRuntime(nodeID, forward, tunnel, role); ok {
				scopedForwards = append(scopedForwards, scopedForward)
			}
		}
	}
	return scopedTunnels, scopedForwards
}

func filterActiveTunnelNodes(tunnel model.Tunnel, activeNodeIDs map[string]bool) (model.Tunnel, bool) {
	filtered := tunnel
	filtered.Stages = make([]model.TunnelStage, 0, len(tunnel.Stages))
	for _, stage := range tunnel.Stages {
		originalNodes := stage.Nodes
		stage.Nodes = make([]model.TunnelStageNode, 0, len(stage.Nodes))
		for _, node := range originalNodes {
			if activeNodeIDs[node.NodeID] {
				stage.Nodes = append(stage.Nodes, node)
			}
		}
		if len(stage.Nodes) == 0 {
			return model.Tunnel{}, false
		}
		filtered.Stages = append(filtered.Stages, stage)
	}
	return filtered, len(filtered.Stages) > 0
}

func scopeTunnelRuntime(nodeID string, tunnel model.Tunnel) model.TunnelRuntime {
	runtime := model.TunnelRuntime{
		ID:        tunnel.ID,
		Name:      tunnel.Name,
		Type:      tunnel.Type,
		Transport: tunnel.Transport,
	}
	for stageIndex, stage := range tunnel.Stages {
		runtimeStage := model.TunnelRuntimeStage{
			Index:       stage.Index,
			Role:        stage.Role,
			Strategy:    stage.Strategy,
			TCPStrategy: stage.TCPStrategy,
			UDPStrategy: stage.UDPStrategy,
		}
		for _, node := range stage.Nodes {
			runtimeNode := model.TunnelRuntimeNode{
				NodeID:      node.NodeID,
				Protocols:   append([]model.ForwardProtocol(nil), node.Protocols...),
				ListenAddr:  node.ListenAddr,
				PublicAddr:  node.PublicAddr,
				ConnectAddr: node.ConnectAddr,
				Weight:      node.Weight,
			}
			switch {
			case node.NodeID == nodeID:
				runtimeNode.Settings = listenerSettings(tunnel.Transport, node.Settings)
			case stageIndex > 0 && previousStageNodeHasNode(tunnel, stageIndex, nodeID):
				runtimeNode.Settings = dialerSettings(tunnel.Transport, node.Settings)
			}
			runtimeStage.Nodes = append(runtimeStage.Nodes, runtimeNode)
		}
		runtime.Stages = append(runtime.Stages, runtimeStage)
	}
	return runtime
}

func scopeForwardRuntime(nodeID string, forward model.Forward, tunnel model.Tunnel, role model.TunnelStageRole) (model.ForwardRuntime, bool) {
	protocols := append([]model.ForwardProtocol(nil), forward.Protocols...)
	if candidate, ok := tunnelStageNodeForNode(tunnel, nodeID); ok {
		protocols = filterForwardProtocols(protocols, candidate.Protocols)
	}
	if len(protocols) == 0 {
		return model.ForwardRuntime{}, false
	}
	runtime := model.ForwardRuntime{
		ID:          forward.ID,
		Name:        forward.Name,
		TunnelID:    forward.TunnelID,
		Protocols:   protocols,
		Strategy:    forward.Strategy,
		TCPStrategy: forward.TCPStrategy,
		UDPStrategy: forward.UDPStrategy,
		Enabled:     forward.Enabled,
	}
	if role == model.TunnelStageEntry {
		runtime.Listen = forward.Listen
		if tunnel.Type == model.TunnelDirect {
			runtime.Target = forward.Target
			runtime.Targets = copyForwardTargets(forward.Targets, forward.Target)
		}
	}
	if role == model.TunnelStageExit {
		runtime.Target = forward.Target
		runtime.Targets = copyForwardTargets(forward.Targets, forward.Target)
	}
	return runtime, true
}

func copyForwardTargets(targets []model.ForwardTarget, legacyTarget string) []model.ForwardTarget {
	if len(targets) == 0 && strings.TrimSpace(legacyTarget) != "" {
		targets = []model.ForwardTarget{{
			ID:      "legacy-target",
			Address: strings.TrimSpace(legacyTarget),
			Weight:  1,
			Enabled: true,
		}}
	}
	out := make([]model.ForwardTarget, len(targets))
	copy(out, targets)
	for i := range out {
		out[i].Protocols = append([]model.ForwardProtocol(nil), out[i].Protocols...)
		out[i].Position = i
	}
	return out
}

func nodeParticipatesInTunnel(nodeID string, tunnel model.Tunnel) bool {
	_, ok := nodeTunnelRole(nodeID, tunnel)
	return ok
}

func nodeTunnelRole(nodeID string, tunnel model.Tunnel) (model.TunnelStageRole, bool) {
	for _, stage := range tunnel.Stages {
		for _, node := range stage.Nodes {
			if node.NodeID == nodeID {
				return stage.Role, true
			}
		}
	}
	return "", false
}

func tunnelStageNodeForNode(tunnel model.Tunnel, nodeID string) (model.TunnelStageNode, bool) {
	for _, stage := range tunnel.Stages {
		for _, node := range stage.Nodes {
			if node.NodeID == nodeID {
				return node, true
			}
		}
	}
	return model.TunnelStageNode{}, false
}

func previousStageNodeHasNode(tunnel model.Tunnel, stageIndex int, nodeID string) bool {
	if stageIndex <= 0 || stageIndex > len(tunnel.Stages)-1 {
		return false
	}
	prev := tunnel.Stages[stageIndex-1]
	for _, node := range prev.Nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

func listenerSettings(transport model.TunnelTransport, settings map[string]string) map[string]string {
	out := map[string]string{}
	copySetting(out, settings, "secret")
	if transport == model.TunnelTransportTLS || transport == model.TunnelTransportMTLS || transport == model.TunnelTransportWSTLS {
		copySetting(out, settings, "server_cert")
		copySetting(out, settings, "server_key")
	}
	if transport == model.TunnelTransportMTLS {
		copySetting(out, settings, "ca_cert")
	}
	return out
}

func dialerSettings(transport model.TunnelTransport, settings map[string]string) map[string]string {
	out := map[string]string{}
	copySetting(out, settings, "secret")
	copySetting(out, settings, "server_name")
	copySetting(out, settings, "skip_verify")
	if transport == model.TunnelTransportTLS || transport == model.TunnelTransportMTLS || transport == model.TunnelTransportWSTLS {
		copySetting(out, settings, "ca_cert")
	}
	if transport == model.TunnelTransportMTLS {
		copySetting(out, settings, "client_cert")
		copySetting(out, settings, "client_key")
	}
	return out
}

func copySetting(dst map[string]string, src map[string]string, key string) {
	if src != nil && src[key] != "" {
		dst[key] = src[key]
	}
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if stateChangingMethod(r.Method) && !s.requireSameOrigin(w, r) {
			return
		}
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
		limiter := s.nodeAuthLimiter()
		limitKeys := s.nodeAuthLimitKeys(r, id)
		if !allowLimiterKeys(limiter, limitKeys) {
			writeError(w, errors.New("too many node authentication attempts"), http.StatusTooManyRequests)
			return
		}
		failAuth := func() {
			failLimiterKeys(limiter, limitKeys)
		}
		succeedAuth := func() {
			successLimiterKeys(limiter, limitKeys)
		}
		if id == "" || token == "" {
			failAuth()
			writeError(w, errors.New("node credentials are required"), http.StatusUnauthorized)
			return
		}
		if len(id) > validate.MaxIDBytes || len(token) > maxNodeCredentialBytes {
			failAuth()
			writeError(w, errors.New("node credentials are invalid"), http.StatusUnauthorized)
			return
		}
		if err := validateNodeText("node id", id, validate.MaxIDBytes, false); err != nil {
			failAuth()
			writeError(w, errors.New("node credentials are invalid"), http.StatusUnauthorized)
			return
		}
		node, err := s.store.AuthenticateNode(r.Context(), id, token)
		if err != nil {
			failAuth()
			// Keep missing-node and invalid-token responses indistinguishable.
			writeError(w, errors.New("node credentials are invalid"), http.StatusUnauthorized)
			return
		}
		succeedAuth()
		next(w, r, node)
	}
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, session auth.Session) {
	setSessionCookieWithProxy(w, r, session, false)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, session auth.Session) {
	setSessionCookieWithProxy(w, r, session, s.sessionCookieSecure(r))
}

func setSessionCookieWithProxy(w http.ResponseWriter, r *http.Request, session auth.Session, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure || requestIsSecure(r),
		Expires:  session.ExpiresAt,
	})
}

func (s *Server) sessionCookieSecure(r *http.Request) bool {
	if requestIsSecureForProxy(r, s.proxyHeadersTrusted(r)) {
		return true
	}
	publicURL := s.cfg.PublicURL
	if s.store != nil {
		publicURL = s.controllerPublicURL(r.Context())
	}
	u, err := url.Parse(publicURL)
	return err == nil && strings.EqualFold(u.Scheme, "https")
}

func loginLimitKey(r *http.Request, username string) string {
	return loginUserIPLimitKey(requestClientIP(r), username)
}

func (s *Server) loginLimitKey(r *http.Request, username string) string {
	return loginUserIPLimitKey(s.loginIPLimitKey(r), username)
}

func (s *Server) loginIPLimitKey(r *http.Request) string {
	return requestClientIPForProxy(r, s.proxyHeadersTrusted(r))
}

func (s *Server) loginLimitKeys(r *http.Request, username string) []string {
	ip := s.loginIPLimitKey(r)
	return []string{
		"login:ip:" + ip,
		loginUserIPLimitKey(ip, username),
	}
}

func loginUserIPLimitKey(ip, username string) string {
	digest := sha256.Sum256([]byte(username + "\x00" + ip))
	return fmt.Sprintf("login:user-ip:%x", digest)
}

func (s *Server) allowLogin(keys []string) bool {
	return allowLimiterKeys(s.limiter, keys)
}

func (s *Server) nodeAuthLimiter() *auth.LoginLimiter {
	if s.nodeLimiter != nil {
		return s.nodeLimiter
	}
	return s.limiter
}

func (s *Server) nodeAuthLimitKeys(r *http.Request, nodeID string) []string {
	ip := s.loginIPLimitKey(r)
	digest := sha256.Sum256([]byte(nodeID + "\x00" + ip))
	return []string{
		"node-auth:ip:" + ip,
		fmt.Sprintf("node-auth:node-ip:%x", digest),
	}
}

func (s *Server) proxyHeadersTrusted(r *http.Request) bool {
	if !s.cfg.TrustProxyHeaders || r == nil {
		return false
	}
	peer := net.ParseIP(requestClientIP(r))
	if peer == nil {
		return false
	}
	for _, raw := range strings.Split(s.cfg.TrustedProxyCIDRs, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if ip := net.ParseIP(raw); ip != nil {
			if peer.Equal(ip) {
				return true
			}
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err == nil && network.Contains(peer) {
			return true
		}
	}
	return false
}

func allowLimiterKeys(limiter *auth.LoginLimiter, keys []string) bool {
	if limiter == nil {
		return true
	}
	for _, key := range keys {
		if !limiter.Allow(key) {
			return false
		}
	}
	return true
}

func failLimiterKeys(limiter *auth.LoginLimiter, keys []string) {
	if limiter == nil {
		return
	}
	for _, key := range keys {
		limiter.Fail(key)
	}
}

func successLimiterKeys(limiter *auth.LoginLimiter, keys []string) {
	if limiter == nil {
		return
	}
	for _, key := range keys {
		limiter.Success(key)
	}
}

func requestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	return normalizedIP(remoteAddrHost(r.RemoteAddr))
}

func requestClientIPForProxy(r *http.Request, trustProxyHeaders bool) string {
	peer := requestClientIP(r)
	if !trustProxyHeaders || r == nil {
		return peer
	}
	if value := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); value != "" {
		if candidate := normalizedForwardedIP(value); candidate != "" {
			return candidate
		}
		return peer
	}
	if value := strings.TrimSpace(r.Header.Get("X-Real-IP")); value != "" {
		if candidate := normalizedIP(value); candidate != "" {
			return candidate
		}
	}
	return peer
}

func forwardedHeaderValue(value string) string {
	if value == "" || len(value) > maxProxyHeaderBytes {
		return ""
	}
	for _, part := range strings.Split(value, ",") {
		if candidate := strings.TrimSpace(part); candidate != "" && len(candidate) <= maxProxyHeaderBytes {
			return candidate
		}
	}
	return ""
}

func normalizedForwardedIP(value string) string {
	return normalizedIP(forwardedHeaderValue(value))
}

func normalizedIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxProxyHeaderBytes {
		return ""
	}
	ip := net.ParseIP(trimEnclosingBrackets(value))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func remoteAddrHost(value string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err == nil {
		return trimEnclosingBrackets(host)
	}
	return trimEnclosingBrackets(strings.TrimSpace(value))
}

func requestIsSecure(r *http.Request) bool {
	return requestIsSecureForProxy(r, false)
}

func requestIsSecureForProxy(r *http.Request, trustProxyHeaders bool) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return trustProxyHeaders && strings.EqualFold(strings.TrimSpace(forwardedHeaderValue(r.Header.Get("X-Forwarded-Proto"))), "https")
}

func stateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) requireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		writeError(w, errors.New("cross-origin request rejected"), http.StatusForbidden)
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
			origin = referer
		}
	}
	if origin == "" {
		return true
	}
	requestedOrigin, err := originFromURL(origin)
	if err != nil || !strings.EqualFold(requestedOrigin, s.expectedOrigin(r)) {
		writeError(w, errors.New("cross-origin request rejected"), http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) expectedOrigin(r *http.Request) string {
	configured := strings.TrimSpace(s.cfg.PublicURL)
	if s.store != nil {
		configured = s.controllerPublicURL(r.Context())
	}
	if configured != "" {
		if origin, err := originFromURL(configured); err == nil {
			return origin
		}
	}
	scheme := "http"
	if requestIsSecureForProxy(r, s.proxyHeadersTrusted(r)) {
		scheme = "https"
	}
	origin, _ := originFromURL(scheme + "://" + strings.TrimSpace(r.Host))
	return origin
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

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func readJSON(r *http.Request, dest any) (err error) {
	defer func() {
		if closeErr := r.Body.Close(); err == nil {
			err = closeErr
		}
	}()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
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

func (s *Server) handleInstallScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="install.sh"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(installScript()))
}

func (s *Server) handleDownloadNodeReleaseManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	release := s.nodeRelease()
	if !release.UpdateEnabled {
		// This endpoint is public so node bootstrapping can discover the
		// release state. Do not expose local filesystem paths or read errors.
		release.DisabledReason = "node release is unavailable"
	}
	writeJSON(w, release)
}

func (s *Server) handleDownloadNodeBinarySignature(w http.ResponseWriter, r *http.Request) {
	targetOS, targetArch, err := normalizeNodeBinaryTarget(r.URL.Query().Get("os"), r.URL.Query().Get("arch"))
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(s.cfg.NodeBinaryDir) == "" {
		writeError(w, errors.New("node binary directory is not configured"), http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("nyarelay-node-%s-%s.sig", targetOS, targetArch)
	path := filepath.Join(s.cfg.NodeBinaryDir, filename)
	payload, err := readNodeReleaseFile(path, maxNodeReleaseMetadataBytes)
	if err != nil {
		writeError(w, errors.New("node binary signature not found"), http.StatusNotFound)
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil || len(signature) != ed25519.SignatureSize {
		writeError(w, errors.New("node binary signature not found"), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(signature)
}

func closeStore(st *store.Store) {
	_ = st.Close()
}

func closeWebSocket(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	_ = conn.Close(code, reason)
}

func (s *Server) handleDownloadNodeBinary(w http.ResponseWriter, r *http.Request) {
	path, filename, err := s.nodeBinaryPathForRequest(r)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if path == "" {
		writeError(w, errors.New("node binary path is not configured"), http.StatusInternalServerError)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxNodeReleaseArtifactBytes {
		writeError(w, errors.New("node binary not found"), http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("compress") == "gzip" {
		serveGzippedFile(w, r, path, filename)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, path)
}

func serveGzippedFile(w http.ResponseWriter, r *http.Request, path, filename string) {
	file, err := os.Open(path)
	if err != nil {
		writeError(w, errors.New("node binary not found"), http.StatusNotFound)
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.gz"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	gz := gzip.NewWriter(w)
	defer func() { _ = gz.Close() }()
	if _, err := file.WriteTo(gz); err != nil {
		slog.WarnContext(r.Context(), "failed to stream gzipped node binary", "err", err)
	}
}

func (s *Server) nodeBinaryPathForRequest(r *http.Request) (string, string, error) {
	targetOS := r.URL.Query().Get("os")
	targetArch := r.URL.Query().Get("arch")
	if targetOS == "" && targetArch == "" {
		return s.cfg.NodeBinaryPath, "nyarelay-node", nil
	}
	if strings.TrimSpace(s.cfg.NodeBinaryDir) == "" {
		return "", "", errors.New("node binary directory is not configured")
	}
	targetOS, targetArch, err := normalizeNodeBinaryTarget(targetOS, targetArch)
	if err != nil {
		return "", "", err
	}
	filename := fmt.Sprintf("nyarelay-node-%s-%s", targetOS, targetArch)
	return filepath.Join(s.cfg.NodeBinaryDir, filename), filename, nil
}

func normalizeNodeBinaryTarget(targetOS, targetArch string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(targetOS)) {
	case "linux":
		targetOS = "linux"
	default:
		return "", "", fmt.Errorf("unsupported node binary operating system: %s", targetOS)
	}
	switch strings.ToLower(strings.TrimSpace(targetArch)) {
	case "amd64", "x86_64":
		targetArch = "amd64"
	case "arm64", "aarch64":
		targetArch = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported node binary architecture: %s", targetArch)
	}
	return targetOS, targetArch, nil
}

func controllerBaseURL(configured string, r *http.Request) string {
	_ = r
	if normalized, err := normalizeControllerURL(configured); err == nil {
		return normalized
	}
	return ""
}

func (s *Server) controllerPublicURL(ctx context.Context) string {
	if s.store != nil {
		if value, ok, err := s.store.GetSetting(ctx, controllerPublicURLSetting); err == nil && ok {
			if normalized, err := normalizeControllerURL(value); err == nil && normalized != "" {
				return normalized
			}
		}
	}
	if normalized, err := normalizeControllerURL(s.cfg.PublicURL); err == nil {
		return normalized
	}
	return ""
}

func normalizeControllerURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", errors.New("invalid controller URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("controller URL must use http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("controller URL must include a host")
	}
	if u.Scheme == "http" && !isLoopbackURLHost(u.Hostname()) {
		return "", errors.New("remote controller URL must use https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("controller URL must not include credentials, query, or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("controller URL must not include a path")
	}
	u.Path = ""
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackURLHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func originFromURL(value string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return "", errors.New("invalid origin")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("invalid origin scheme")
	}
	if u.Hostname() == "" {
		return "", errors.New("invalid origin host")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
}

func validateUsername(username string) error {
	if username == "" {
		return errors.New("username is required")
	}
	if !utf8.ValidString(username) || len(username) > maxUsernameBytes {
		return errors.New("username is invalid")
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			return errors.New("username is invalid")
		}
	}
	return nil
}

func validateNodeInput(name string, labels map[string]string, publicHost string) error {
	if err := validateNodeText("node name", name, maxNodeNameBytes, false); err != nil {
		return err
	}
	if len(labels) > maxNodeLabelEntries {
		return fmt.Errorf("node has too many labels (maximum %d)", maxNodeLabelEntries)
	}
	for key, value := range labels {
		if err := validateNodeText("node label key", key, maxNodeLabelKeyBytes, false); err != nil {
			return err
		}
		if err := validateNodeText("node label value", value, maxNodeLabelValueBytes, true); err != nil {
			return err
		}
	}
	if err := validateNodeText("node public host", publicHost, maxNodeMetadataBytes, true); err != nil {
		return err
	}
	host := strings.TrimSpace(publicHost)
	if host == "" {
		return nil
	}
	host = trimEnclosingBrackets(host)
	if net.ParseIP(host) != nil {
		return nil
	}
	if strings.ContainsAny(host, "/?#@\\:") {
		return errors.New("node public host must be an IP address or hostname")
	}
	for _, r := range host {
		if unicode.IsSpace(r) {
			return errors.New("node public host must be an IP address or hostname")
		}
	}
	return nil
}

func validateNodeText(field, value string, maxBytes int, allowEmpty bool) error {
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

func validateNodeError(field, value string, maxBytes int, allowEmpty bool) error {
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
		if r == '\x00' || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') {
			return fmt.Errorf("%s contains an invalid control character", field)
		}
	}
	return nil
}

func validateNodeIdentity(expectedID, claimedID string) error {
	if claimedID == "" {
		return nil
	}
	if err := validateNodeText("node id", claimedID, validate.MaxIDBytes, false); err != nil {
		return err
	}
	if claimedID != expectedID {
		return errors.New("node id does not match authenticated node")
	}
	return nil
}

func validateNodeVersion(version string) error {
	return validateNodeText("node version", version, maxNodeVersionBytes, true)
}

func validateNodeSystem(system model.NodeSystem) error {
	for field, value := range map[string]string{
		"node hostname":         system.Hostname,
		"node operating system": system.OS,
		"node architecture":     system.Arch,
		"node IP":               system.IP,
	} {
		if err := validateNodeText(field, value, maxNodeMetadataBytes, true); err != nil {
			return err
		}
	}
	if system.IP != "" && net.ParseIP(trimEnclosingBrackets(strings.TrimSpace(system.IP))) == nil {
		return errors.New("node IP is invalid")
	}
	return nil
}

func validateNodeHeartbeat(version string, system model.NodeSystem) error {
	if err := validateNodeVersion(version); err != nil {
		return err
	}
	return validateNodeSystem(system)
}

func validateNodeHello(nodeID string, hello sharedprotocol.ControlMessage) error {
	if err := validateNodeIdentity(nodeID, hello.NodeID); err != nil {
		return err
	}
	return validateNodeHeartbeat(hello.Version, hello.System)
}

func validateNodeControlMessage(nodeID string, msg sharedprotocol.ControlMessage) error {
	if err := validateNodeIdentity(nodeID, msg.NodeID); err != nil {
		return err
	}
	switch msg.Type {
	case "heartbeat":
		if err := validateNodeHeartbeat(msg.Version, msg.System); err != nil {
			return err
		}
		if msg.UpdateReport != nil {
			return validateNodeUpdateReport(*msg.UpdateReport)
		}
	case "pull_config":
		return nil
	case "update_status":
		if msg.UpdateReport == nil {
			return errors.New("update status report is required")
		}
		return validateNodeUpdateReport(*msg.UpdateReport)
	default:
		return fmt.Errorf("unsupported node message type %q", msg.Type)
	}
	return nil
}

func validateNodeUpdateReport(report model.NodeUpdateReport) error {
	switch report.Status {
	case model.NodeUpdateIdle, model.NodeUpdateRequested, model.NodeUpdateRunning, model.NodeUpdateSucceeded, model.NodeUpdateFailed:
	default:
		return fmt.Errorf("unsupported node update status %q", report.Status)
	}
	if err := validateNodeVersion(report.Version); err != nil {
		return err
	}
	return validateNodeError("node update error", report.Error, maxNodeUpdateErrorBytes, true)
}

func validateMetricsReport(report model.MetricsReport) error {
	if report.NodeID != "" {
		if err := validateNodeText("metrics node id", report.NodeID, validate.MaxIDBytes, true); err != nil {
			return err
		}
	}
	if len(report.ForwardStats) > maxMetricStats || len(report.TunnelStats) > maxMetricStats {
		return fmt.Errorf("metrics report has too many statistics (maximum %d per kind)", maxMetricStats)
	}
	if len(report.ForwardStats)+len(report.TunnelStats) > maxMetricStats*2 {
		return fmt.Errorf("metrics report has too many statistics")
	}
	for _, stats := range []struct {
		kind string
		list []model.TrafficStat
	}{
		{kind: "forward", list: report.ForwardStats},
		{kind: "tunnel", list: report.TunnelStats},
	} {
		for _, stat := range stats.list {
			if err := validateNodeText(stats.kind+" metric id", stat.ID, maxMetricIDBytes, false); err != nil {
				return err
			}
			for field, value := range map[string]int64{
				stats.kind + " bytes_in":    stat.BytesIn,
				stats.kind + " bytes_out":   stat.BytesOut,
				stats.kind + " connections": stat.Connections,
			} {
				if value < 0 || value > maxMetricValue {
					return fmt.Errorf("%s is outside the permitted range", field)
				}
			}
		}
	}
	if report.Runtime.UptimeSeconds < 0 || report.Runtime.UptimeSeconds > maxMetricValue {
		return errors.New("metrics uptime is outside the permitted range")
	}
	if report.Runtime.Goroutines < 0 || report.Runtime.Goroutines > maxMetricGoroutines {
		return errors.New("metrics goroutine count is outside the permitted range")
	}
	if !report.ObservedAt.IsZero() {
		now := time.Now().UTC()
		if report.ObservedAt.Before(now.Add(-maxMetricTimeSkew)) || report.ObservedAt.After(now.Add(maxMetricTimeSkew)) {
			return errors.New("metrics timestamp is outside the permitted range")
		}
	}
	if len(report.AgentErrors) > maxAgentErrors {
		return fmt.Errorf("metrics report has too many agent errors (maximum %d)", maxAgentErrors)
	}
	for _, agentError := range report.AgentErrors {
		if err := validateNodeText("agent error scope", agentError.Scope, maxAgentErrorScopeBytes, true); err != nil {
			return err
		}
		if err := validateNodeError("agent error message", agentError.Message, maxAgentErrorMessageBytes, true); err != nil {
			return err
		}
	}
	return nil
}

func validateNodePortRange(portMin, portMax int) error {
	if portMin == 0 && portMax == 0 {
		return nil
	}
	if portMin == 0 || portMax == 0 {
		return fmt.Errorf("port_min and port_max must be set together")
	}
	if portMin < 1 || portMax > 65535 {
		return fmt.Errorf("port range must be within 1-65535")
	}
	if portMin > portMax {
		return fmt.Errorf("port_min must be less than or equal to port_max")
	}
	return nil
}

func (s *Server) loadFailureCooldown(ctx context.Context) error {
	next := s.cfg.FailureCooldown
	if next <= 0 {
		next = defaultFailureCooldown
	}
	value, ok, err := s.store.GetSetting(ctx, failureCooldownSetting)
	if err != nil {
		return err
	}
	if ok {
		parsed, err := parsePositiveDuration(value)
		if err != nil {
			return fmt.Errorf("invalid persisted setting %s: %w", failureCooldownSetting, err)
		}
		next = parsed
	}
	s.setFailureCooldown(next)
	return nil
}

func (s *Server) currentFailureCooldown() time.Duration {
	s.failureMu.RLock()
	defer s.failureMu.RUnlock()
	if s.failureCooldown <= 0 {
		return defaultFailureCooldown
	}
	return s.failureCooldown
}

func (s *Server) setFailureCooldown(next time.Duration) {
	if next <= 0 {
		next = defaultFailureCooldown
	}
	s.failureMu.Lock()
	s.failureCooldown = next
	s.failureMu.Unlock()
}

func historyCleanupConfigFromConfig(cfg Config) historyCleanupConfig {
	return historyCleanupConfig{
		MetricsRetention: cfg.MetricsRetention,
		AuditRetention:   cfg.AuditRetention,
		CleanupInterval:  cfg.CleanupInterval,
	}
}

func (s *Server) loadHistoryCleanupConfig(ctx context.Context) error {
	next := historyCleanupConfigFromConfig(s.cfg)
	settings := []struct {
		key    string
		target *time.Duration
	}{
		{historyMetricsRetentionSetting, &next.MetricsRetention},
		{historyAuditRetentionSetting, &next.AuditRetention},
		{historyCleanupIntervalSetting, &next.CleanupInterval},
	}
	for _, setting := range settings {
		value, ok, err := s.store.GetSetting(ctx, setting.key)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		parsed, err := parseNonNegativeDuration(value)
		if setting.key == historyCleanupIntervalSetting {
			parsed, err = parseCleanupInterval(value)
		}
		if err != nil {
			return fmt.Errorf("invalid persisted setting %s: %w", setting.key, err)
		}
		*setting.target = parsed
	}
	s.cleanupMu.Lock()
	s.cleanupConfig = next
	s.cleanupMu.Unlock()
	return nil
}

func (s *Server) currentHistoryCleanupConfig() historyCleanupConfig {
	s.cleanupMu.RLock()
	defer s.cleanupMu.RUnlock()
	return s.cleanupConfig
}

func (s *Server) setHistoryCleanupConfig(next historyCleanupConfig) {
	s.cleanupMu.Lock()
	s.cleanupConfig = next
	s.cleanupMu.Unlock()
	if s.cleanupWake == nil {
		return
	}
	select {
	case s.cleanupWake <- struct{}{}:
	default:
	}
}

func (s *Server) historyCleanupConfigResponse() map[string]string {
	cfg := s.currentHistoryCleanupConfig()
	return map[string]string{
		"metrics_retention": formatNonNegativeDuration(cfg.MetricsRetention),
		"audit_retention":   formatNonNegativeDuration(cfg.AuditRetention),
		"cleanup_interval":  formatNonNegativeDuration(cfg.CleanupInterval),
	}
}

func historyCleanupEnabled(cfg historyCleanupConfig) bool {
	return cfg.CleanupInterval > 0 && (cfg.MetricsRetention > 0 || cfg.AuditRetention > 0)
}

func (s *Server) historyCleanupLoop(ctx context.Context) {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	runCleanup := func() {
		if historyCleanupEnabled(s.currentHistoryCleanupConfig()) {
			s.cleanupHistory(ctx)
		}
	}
	schedule := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		cfg := s.currentHistoryCleanupConfig()
		if historyCleanupEnabled(cfg) {
			timer = time.NewTimer(cfg.CleanupInterval)
		}
	}

	runCleanup()
	schedule()
	for {
		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-s.cleanupWake:
			runCleanup()
			schedule()
		case <-timerC:
			runCleanup()
			schedule()
		}
	}
}

func (s *Server) cleanupHistory(ctx context.Context) {
	cfg := s.currentHistoryCleanupConfig()
	now := time.Now().UTC()
	var metricsBefore, auditBefore time.Time
	if cfg.MetricsRetention > 0 {
		metricsBefore = now.Add(-cfg.MetricsRetention)
	}
	if cfg.AuditRetention > 0 {
		auditBefore = now.Add(-cfg.AuditRetention)
	}
	metricsDeleted, auditDeleted, err := s.store.PruneHistory(ctx, metricsBefore, auditBefore)
	if err != nil {
		s.log.Warn("history cleanup failed", "error", err)
		return
	}
	if metricsDeleted > 0 || auditDeleted > 0 {
		s.log.Info("history cleanup completed", "metrics_deleted", metricsDeleted, "audit_deleted", auditDeleted)
	}
}
