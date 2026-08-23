package controller

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/store"
	"nyarelay/internal/shared/model"
	sharedprotocol "nyarelay/internal/shared/protocol"
	"nyarelay/internal/shared/validate"
)

func TestLoginLimitKeyIgnoresRemotePort(t *testing.T) {
	reqA := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	reqA.RemoteAddr = "198.51.100.10:40001"
	reqB := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	reqB.RemoteAddr = "198.51.100.10:40002"

	if gotA, gotB := loginLimitKey(reqA, "admin"), loginLimitKey(reqB, "admin"); gotA != gotB {
		t.Fatalf("limit key mismatch: %q != %q", gotA, gotB)
	}
}

func TestLoginLimitKeyIgnoresUntrustedForwardedAddress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.10:40001"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.10")

	if got := requestClientIPForProxy(req, false); got != "10.0.0.10" {
		t.Fatalf("client ip = %q, want socket peer ip", got)
	}
}

func TestLoginLimitKeysSeparateUsersAndShareIP(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "198.51.100.10:40001"

	adminKeys := s.loginLimitKeys(req, "admin")
	otherKeys := s.loginLimitKeys(req, "other")
	if len(adminKeys) != 2 || len(otherKeys) != 2 {
		t.Fatalf("login key counts = %d and %d, want 2", len(adminKeys), len(otherKeys))
	}
	if adminKeys[0] != otherKeys[0] {
		t.Fatalf("IP limit keys differ: %q != %q", adminKeys[0], otherKeys[0])
	}
	if adminKeys[1] == otherKeys[1] {
		t.Fatalf("user/IP limit keys should differ: %q", adminKeys[1])
	}
	if len(adminKeys[1]) > 128 {
		t.Fatalf("user/IP limit key is unexpectedly large: %d", len(adminKeys[1]))
	}
}

func TestRequestClientIPForProxyValidatesAndBoundsHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
		want   string
	}{
		{name: "valid forwarded for", header: "X-Forwarded-For", value: "203.0.113.9, 10.0.0.10", want: "203.0.113.9"},
		{name: "valid real ip", header: "X-Real-IP", value: "2001:db8::9", want: "2001:db8::9"},
		{name: "invalid forwarded for", header: "X-Forwarded-For", value: "not-an-ip", want: "10.0.0.10"},
		{name: "oversized forwarded for", header: "X-Forwarded-For", value: strings.Repeat("1", maxProxyHeaderBytes+1), want: "10.0.0.10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			req.RemoteAddr = "10.0.0.10:40001"
			req.Header.Set(tt.header, tt.value)
			if got := requestClientIPForProxy(req, true); got != tt.want {
				t.Fatalf("client ip = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServerProxyHeadersRequireTrustedPeer(t *testing.T) {
	s := &Server{cfg: Config{
		TrustProxyHeaders: true,
		TrustedProxyCIDRs: "10.0.0.0/8, 2001:db8::10",
	}}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	request.RemoteAddr = "192.0.2.10:40001"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := s.loginIPLimitKey(request); got != "192.0.2.10" {
		t.Fatalf("untrusted proxy client ip = %q, want socket peer", got)
	}

	request.RemoteAddr = "10.0.0.10:40001"
	if got := s.loginIPLimitKey(request); got != "203.0.113.9" {
		t.Fatalf("trusted proxy client ip = %q, want forwarded address", got)
	}
}

func TestWithNodeRejectsOversizedCredentialsBeforeStoreLookup(t *testing.T) {
	s := &Server{store: func() *store.Store {
		st, err := store.Open(context.Background(), ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}()}
	handler := s.withNode(func(http.ResponseWriter, *http.Request, model.Node) {
		t.Fatal("oversized credentials reached node handler")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/node/config", nil)
	req.Header.Set("X-NyaRelay-Node-ID", strings.Repeat("n", validate.MaxIDBytes+1))
	req.Header.Set("X-NyaRelay-Node-Token", "token")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWithNodeReturnsSameErrorForUnknownNodeAndInvalidToken(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertNode(ctx, model.Node{ID: "node-auth", Name: "node-auth", Approved: true}, "valid-token"); err != nil {
		t.Fatal(err)
	}

	s := &Server{store: st, nodeLimiter: auth.NewLoginLimiter()}
	handler := s.withNode(func(http.ResponseWriter, *http.Request, model.Node) {
		t.Fatal("invalid node credentials reached handler")
	})
	request := func(id, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/node/config", nil)
		req.RemoteAddr = "198.51.100.20:40001"
		req.Header.Set("X-NyaRelay-Node-ID", id)
		req.Header.Set("X-NyaRelay-Node-Token", token)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	unknown := request("node-does-not-exist", "valid-token")
	invalidToken := request("node-auth", "wrong-token")
	if unknown.Code != http.StatusUnauthorized || invalidToken.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d/%d, want 401/401", unknown.Code, invalidToken.Code)
	}
	if unknown.Body.String() != invalidToken.Body.String() {
		t.Fatalf("unknown-node and invalid-token responses differ: %q vs %q", unknown.Body.String(), invalidToken.Body.String())
	}
}

func TestWithNodeRateLimitsRepeatedAuthenticationFailures(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertNode(ctx, model.Node{ID: "node-rate", Name: "node-rate", Approved: true}, "valid-token"); err != nil {
		t.Fatal(err)
	}

	s := &Server{store: st, nodeLimiter: auth.NewLoginLimiter()}
	handler := s.withNode(func(http.ResponseWriter, *http.Request, model.Node) {
		t.Fatal("invalid node credentials reached handler")
	})
	for attempt := 0; attempt < 5; attempt++ {
		req := httptest.NewRequest(http.MethodGet, "/api/node/config", nil)
		req.RemoteAddr = "198.51.100.30:40001"
		req.Header.Set("X-NyaRelay-Node-ID", "node-rate")
		req.Header.Set("X-NyaRelay-Node-Token", "wrong-token")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/node/config", nil)
	req.RemoteAddr = "198.51.100.30:40002"
	req.Header.Set("X-NyaRelay-Node-ID", "node-rate")
	req.Header.Set("X-NyaRelay-Node-Token", "wrong-token")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status = %d, want 429", rec.Code)
	}
}

func TestValidateNodePortRangeAllowsPortsBelowDefault(t *testing.T) {
	if err := validateNodePortRange(80, 443); err != nil {
		t.Fatalf("expected low valid port range to pass: %v", err)
	}
}

func TestValidateNodePortRangeRejectsInvalidPorts(t *testing.T) {
	tests := []struct {
		name    string
		portMin int
		portMax int
	}{
		{name: "below valid port range", portMin: -1, portMax: 443},
		{name: "above valid port range", portMin: 10000, portMax: 65536},
		{name: "reversed range", portMin: 443, portMax: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateNodePortRange(tt.portMin, tt.portMax); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSetSessionCookieIgnoresUntrustedForwardedHTTPS(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel.example/api/auth/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	session := auth.Session{
		ID:        "session-1",
		UserID:    1,
		Username:  "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	setSessionCookie(rec, req, session)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatal("session cookie should not trust an untrusted forwarded proto header")
	}
}

func TestSetSessionCookieUsesSecureWhenProxyHeadersAreTrusted(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel.example/api/auth/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	session := auth.Session{ID: "session-1", UserID: 1, Username: "admin", ExpiresAt: time.Now().Add(time.Hour)}

	setSessionCookieWithProxy(rec, req, session, true)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatal("session cookie should be secure when proxy headers are explicitly trusted")
	}
}

func TestControllerBaseURLDoesNotTrustRequestHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/nodes", nil)
	if got := controllerBaseURL("", req); got != "" {
		t.Fatalf("controller URL = %q, want empty without configured public URL", got)
	}
	if got := controllerBaseURL("https://relay.example.com", req); got != "https://relay.example.com" {
		t.Fatalf("controller URL = %q", got)
	}
}

func TestRequireSameOriginRejectsCrossOriginStateChange(t *testing.T) {
	s := &Server{cfg: Config{PublicURL: "https://relay.example.com"}}
	req := httptest.NewRequest(http.MethodPost, "https://relay.example.com/api/setup", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	if s.requireSameOrigin(rec, req) {
		t.Fatal("cross-origin request was accepted")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestNormalizeControllerURLRejectsUnsafeForms(t *testing.T) {
	for _, value := range []string{"javascript:alert(1)", "https://user:pass@relay.example.com", "https://relay.example.com/path", "http://relay.example.com"} {
		if _, err := normalizeControllerURL(value); err == nil {
			t.Fatalf("expected URL %q to be rejected", value)
		}
	}
}

func TestNormalizeNodeBinaryTarget(t *testing.T) {
	tests := []struct {
		name       string
		targetOS   string
		targetArch string
		wantOS     string
		wantArch   string
		wantErr    bool
	}{
		{name: "linux amd64", targetOS: "linux", targetArch: "amd64", wantOS: "linux", wantArch: "amd64"},
		{name: "linux x86_64", targetOS: "linux", targetArch: "x86_64", wantOS: "linux", wantArch: "amd64"},
		{name: "linux arm64", targetOS: "linux", targetArch: "arm64", wantOS: "linux", wantArch: "arm64"},
		{name: "linux aarch64", targetOS: "linux", targetArch: "aarch64", wantOS: "linux", wantArch: "arm64"},
		{name: "unsupported os", targetOS: "darwin", targetArch: "arm64", wantErr: true},
		{name: "unsupported arch", targetOS: "linux", targetArch: "armv7l", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOS, gotArch, err := normalizeNodeBinaryTarget(tt.targetOS, tt.targetArch)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gotOS != tt.wantOS || gotArch != tt.wantArch {
				t.Fatalf("target = %s/%s, want %s/%s", gotOS, gotArch, tt.wantOS, tt.wantArch)
			}
		})
	}
}

func TestNodeBinaryPathForRequestUsesPlatformSpecificBinary(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{cfg: Config{
		NodeBinaryPath: filepath.Join(dir, "nyarelay-node"),
		NodeBinaryDir:  dir,
	}}
	req := httptest.NewRequest(http.MethodGet, "/downloads/nyarelay-node?os=linux&arch=aarch64", nil)

	path, filename, err := srv.nodeBinaryPathForRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "nyarelay-node-linux-arm64" {
		t.Fatalf("filename = %q", filename)
	}
	if path != filepath.Join(dir, "nyarelay-node-linux-arm64") {
		t.Fatalf("path = %q", path)
	}
}

func TestNodeBinaryPathForRequestRejectsMissingPlatformDirectory(t *testing.T) {
	srv := &Server{cfg: Config{NodeBinaryPath: "nyarelay-node"}}
	req := httptest.NewRequest(http.MethodGet, "/downloads/nyarelay-node?os=linux&arch=amd64", nil)

	if _, _, err := srv.nodeBinaryPathForRequest(req); err == nil {
		t.Fatal("expected missing node binary directory to fail")
	}
}

func TestDownloadNodeBinaryCanStreamGzip(t *testing.T) {
	dir := t.TempDir()
	content := []byte("nyarelay-node-binary")
	path := filepath.Join(dir, "nyarelay-node-linux-amd64")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: Config{
		NodeBinaryPath: filepath.Join(dir, "nyarelay-node"),
		NodeBinaryDir:  dir,
	}}
	req := httptest.NewRequest(http.MethodGet, "/downloads/nyarelay-node?os=linux&arch=amd64&compress=gzip", nil)
	rec := httptest.NewRecorder()

	srv.handleDownloadNodeBinary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("download failed: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/gzip" {
		t.Fatalf("content type = %q", got)
	}
	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(content) {
		t.Fatalf("body = %q, want %q", body, content)
	}
}

func TestDownloadNodeBinaryIgnoresStalePrecompressedGzip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nyarelay-node-linux-amd64")
	if err := os.WriteFile(path, []byte("raw-node-binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	gzPath := path + ".gz"
	gzFile, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	gzWriter := gzip.NewWriter(gzFile)
	if _, err := gzWriter.Write([]byte("precompressed-node-binary")); err != nil {
		t.Fatal(err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzFile.Close(); err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: Config{
		NodeBinaryPath: filepath.Join(dir, "nyarelay-node"),
		NodeBinaryDir:  dir,
	}}
	req := httptest.NewRequest(http.MethodGet, "/downloads/nyarelay-node?os=linux&arch=amd64&compress=gzip", nil)
	rec := httptest.NewRecorder()

	srv.handleDownloadNodeBinary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("download failed: %d %s", rec.Code, rec.Body.String())
	}
	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "raw-node-binary" {
		t.Fatalf("body = %q, want current raw binary", body)
	}
}

func TestFirstHostHandlesIPv6(t *testing.T) {
	if got := firstHost("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatalf("firstHost = %q, want IPv6 host", got)
	}
}

func TestPrepareTunnelEntryAddressOverride(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	entry := model.Node{
		ID:         "entry",
		Name:       "entry",
		Status:     model.NodeOnline,
		PublicHost: "entry.example.com",
		Approved:   true,
	}
	if err := srv.store.UpsertNode(context.Background(), entry, "token"); err != nil {
		t.Fatal(err)
	}

	override := "portal.example.com"
	tunnel, _, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:           "tun_entry_override",
		Name:         "entry override",
		Type:         model.TunnelDirect,
		Transport:    model.TunnelTransportDirect,
		EntryAddress: &override,
		Stages: []model.TunnelStage{{
			Nodes: []model.TunnelStageNode{{NodeID: entry.ID}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tunnel.EntryAddress != "portal.example.com" {
		t.Fatalf("entry address = %q, want override", tunnel.EntryAddress)
	}
}

func TestPrepareTunnelEntryAddressDefaultsToFirstEntryNode(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	nodes := []model.Node{
		{
			ID:         "entry-a",
			Name:       "entry-a",
			Status:     model.NodeOnline,
			PublicHost: "a.example.com",
			Approved:   true,
		},
		{
			ID:         "entry-b",
			Name:       "entry-b",
			Status:     model.NodeOnline,
			PublicHost: "b.example.com",
			Approved:   true,
		},
	}
	for _, node := range nodes {
		if err := srv.store.UpsertNode(context.Background(), node, "token"); err != nil {
			t.Fatal(err)
		}
	}
	emptyEntryAddress := ""
	tunnel, _, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:           "tun_entry_default",
		Name:         "entry default",
		Type:         model.TunnelDirect,
		Transport:    model.TunnelTransportDirect,
		EntryAddress: &emptyEntryAddress,
		Stages: []model.TunnelStage{{
			Nodes: []model.TunnelStageNode{
				{NodeID: "entry-a"},
				{NodeID: "entry-b"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tunnel.EntryAddress != "a.example.com" {
		t.Fatalf("entry address = %q, want first entry node host", tunnel.EntryAddress)
	}
}

func TestPrepareTunnelChainStageUsesReportedNodeIP(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	nodes := []model.Node{
		{
			ID:       "entry-reported-ip",
			Name:     "entry-reported-ip",
			Status:   model.NodeOnline,
			System:   model.NodeSystem{IP: "198.51.100.10"},
			PortMin:  10000,
			PortMax:  10010,
			Approved: true,
		},
		{
			ID:       "middle-reported-ip",
			Name:     "middle-reported-ip",
			Status:   model.NodeOnline,
			System:   model.NodeSystem{IP: "198.51.100.20"},
			PortMin:  10000,
			PortMax:  10010,
			Approved: true,
		},
	}
	for _, node := range nodes {
		if err := srv.store.UpsertNode(context.Background(), node, "token-"+node.ID); err != nil {
			t.Fatal(err)
		}
	}

	tunnel, _, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:        "tun_reported_ip",
		Name:      "reported ip",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Stages: []model.TunnelStage{
			{Nodes: []model.TunnelStageNode{{NodeID: "entry-reported-ip"}}},
			{Nodes: []model.TunnelStageNode{{NodeID: "middle-reported-ip"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tunnel.EntryAddress != "198.51.100.10" {
		t.Fatalf("entry address = %q, want reported entry IP", tunnel.EntryAddress)
	}
	if got := tunnel.Stages[1].Nodes[0].PublicAddr; got != "198.51.100.20:10000" {
		t.Fatalf("chain stage public address = %q, want reported IP and allocated port", got)
	}
}

func TestPrepareTunnelPatchKeepsExistingEntryAddressWhenOmitted(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	entry := model.Node{
		ID:         "entry",
		Name:       "entry",
		Status:     model.NodeOnline,
		PublicHost: "entry.example.com",
		Approved:   true,
	}
	if err := srv.store.UpsertNode(context.Background(), entry, "token"); err != nil {
		t.Fatal(err)
	}
	existing := model.Tunnel{
		ID:           "tun_existing_entry",
		Name:         "existing",
		Type:         model.TunnelDirect,
		Transport:    model.TunnelTransportDirect,
		EntryAddress: "portal.example.com",
		Enabled:      true,
		Stages: []model.TunnelStage{{
			ID:       "stage",
			TunnelID: "tun_existing_entry",
			Index:    0,
			Role:     model.TunnelStageEntry,
			Strategy: "single",
			Nodes: []model.TunnelStageNode{{
				ID:       "stage_node",
				TunnelID: "tun_existing_entry",
				StageID:  "stage",
				NodeID:   entry.ID,
			}},
		}},
	}
	if _, err := srv.store.SaveTunnel(context.Background(), existing, nil); err != nil {
		t.Fatal(err)
	}

	tunnel, _, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:        existing.ID,
		Name:      "existing patched",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Stages: []model.TunnelStage{{
			Nodes: []model.TunnelStageNode{{NodeID: entry.ID}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tunnel.EntryAddress != "portal.example.com" {
		t.Fatalf("entry address = %q, want existing value", tunnel.EntryAddress)
	}
}

func TestPrepareTunnelAssignsNewIDToInsertedStageBeforeExistingExit(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	for _, node := range []model.Node{
		{ID: "entry", Name: "entry", Status: model.NodeOnline, PublicHost: "entry.example.com", PortMin: 10000, PortMax: 20000, Approved: true},
		{ID: "middle", Name: "middle", Status: model.NodeOnline, PublicHost: "middle.example.com", PortMin: 10000, PortMax: 20000, Approved: true},
		{ID: "exit", Name: "exit", Status: model.NodeOnline, PublicHost: "exit.example.com", PortMin: 10000, PortMax: 20000, Approved: true},
		{ID: "new-exit", Name: "new-exit", Status: model.NodeOnline, PublicHost: "new-exit.example.com", PortMin: 10000, PortMax: 20000, Approved: true},
	} {
		if err := srv.store.UpsertNode(context.Background(), node, "token-"+node.ID); err != nil {
			t.Fatal(err)
		}
	}

	existing := model.Tunnel{
		ID:        "tun-stage-insert",
		Name:      "stage insert",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Enabled:   true,
		Stages: []model.TunnelStage{
			{ID: "stage-entry", Index: 0, Role: model.TunnelStageEntry, Strategy: "single", Nodes: []model.TunnelStageNode{{ID: "stage-node-entry", NodeID: "entry"}}},
			{ID: "stage-middle", Index: 1, Role: model.TunnelStageMiddle, Strategy: "single", Nodes: []model.TunnelStageNode{{ID: "stage-node-middle", NodeID: "middle", ListenAddr: ":10001", PublicAddr: "middle.example.com:10001"}}},
			{ID: "stage-exit", Index: 2, Role: model.TunnelStageExit, Strategy: "single", Nodes: []model.TunnelStageNode{{ID: "stage-node-exit", NodeID: "exit", ListenAddr: ":10002", PublicAddr: "exit.example.com:10002"}}},
		},
	}
	if _, err := srv.store.SaveTunnel(context.Background(), existing, nil); err != nil {
		t.Fatal(err)
	}

	prepared, allocations, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:        existing.ID,
		Name:      existing.Name,
		Type:      existing.Type,
		Transport: existing.Transport,
		Stages: []model.TunnelStage{
			{ID: "stage-entry", Nodes: []model.TunnelStageNode{{ID: "stage-node-entry", NodeID: "entry"}}},
			{ID: "stage-exit", Nodes: []model.TunnelStageNode{{ID: "stage-node-exit", NodeID: "exit"}}},
			{Nodes: []model.TunnelStageNode{{NodeID: "new-exit"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Stages[1].ID != "stage-exit" {
		t.Fatalf("moved stage ID = %q, want stage-exit", prepared.Stages[1].ID)
	}
	if prepared.Stages[2].ID == "stage-exit" || prepared.Stages[2].ID == "" {
		t.Fatalf("inserted stage ID = %q, want a new unique ID", prepared.Stages[2].ID)
	}
	if prepared.Stages[1].ID == prepared.Stages[2].ID {
		t.Fatalf("stage IDs are not unique: %q", prepared.Stages[1].ID)
	}
	if _, err := srv.store.SaveTunnel(context.Background(), prepared, allocations); err != nil {
		t.Fatalf("save prepared tunnel: %v", err)
	}
}

func TestPrepareTunnelKeepsPerProtocolStageFields(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	entry := model.Node{ID: "entry", Name: "entry", Status: model.NodeOnline, PublicHost: "entry.example.com", Approved: true}
	if err := srv.store.UpsertNode(context.Background(), entry, "token"); err != nil {
		t.Fatal(err)
	}

	tunnel, _, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:        "tun_protocol_fields",
		Name:      "protocol fields",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Stages: []model.TunnelStage{{
			Strategy:    "failover",
			TCPStrategy: "round_robin",
			UDPStrategy: "random",
			Nodes: []model.TunnelStageNode{{
				NodeID:    entry.ID,
				Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP, model.ForwardProtocolTCP},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stage := tunnel.Stages[0]
	if stage.Strategy != "failover" || stage.TCPStrategy != "round_robin" || stage.UDPStrategy != "random" {
		t.Fatalf("stage strategies = %q/%q/%q", stage.Strategy, stage.TCPStrategy, stage.UDPStrategy)
	}
	if got := stage.Nodes[0].Protocols; len(got) != 2 || got[0] != model.ForwardProtocolTCP || got[1] != model.ForwardProtocolUDP {
		t.Fatalf("node protocols = %#v, want normalized tcp+udp", got)
	}
}

func TestPrepareForwardRejectsMissingEffectiveProtocolCandidate(t *testing.T) {
	srv := testPrepareTunnelServer(t)
	entry := model.Node{ID: "entry", Name: "entry", Status: model.NodeOnline, PublicHost: "entry.example.com", PortMin: 10000, PortMax: 10010, Approved: true}
	if err := srv.store.UpsertNode(context.Background(), entry, "token"); err != nil {
		t.Fatal(err)
	}
	tunnel := model.Tunnel{
		ID:        "tun_tcp_only",
		Name:      "tcp only",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Enabled:   true,
		Stages: []model.TunnelStage{{
			ID:       "stage",
			TunnelID: "tun_tcp_only",
			Index:    0,
			Role:     model.TunnelStageEntry,
			Strategy: "single",
			Nodes: []model.TunnelStageNode{{
				ID:        "stage_node",
				TunnelID:  "tun_tcp_only",
				StageID:   "stage",
				NodeID:    entry.ID,
				Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
			}},
		}},
	}
	if _, err := srv.store.SaveTunnel(context.Background(), tunnel, nil); err != nil {
		t.Fatal(err)
	}

	_, _, err := srv.prepareForward(context.Background(), forwardRequest{
		ID:        "fwd_udp_missing",
		Name:      "udp missing",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
		Listen:    "127.0.0.1:10001",
		Target:    "127.0.0.1:10002",
	})
	if err == nil {
		t.Fatal("expected missing udp candidate to fail")
	}
	if got := err.Error(); got != "stage 0 has no udp candidate" {
		t.Fatalf("error = %q", got)
	}
}

func TestScopeConfigForNodeCarriesProtocolStageFields(t *testing.T) {
	tunnel := model.Tunnel{
		ID:        "tun_scope_protocols",
		Name:      "scope protocols",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Enabled:   true,
		Stages: []model.TunnelStage{{
			ID:          "stage",
			TunnelID:    "tun_scope_protocols",
			Index:       0,
			Role:        model.TunnelStageEntry,
			Strategy:    "failover",
			TCPStrategy: "round_robin",
			UDPStrategy: "random",
			Nodes: []model.TunnelStageNode{{
				ID:        "stage_node",
				TunnelID:  "tun_scope_protocols",
				StageID:   "stage",
				NodeID:    "entry",
				Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
				Weight:    3,
			}},
		}},
	}
	forward := model.Forward{
		ID:        "fwd_scope_protocols",
		Name:      "scope protocols",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
		Listen:    "127.0.0.1:10001",
		Target:    "127.0.0.1:10002",
		Enabled:   true,
	}

	tunnels, forwards := scopeConfigForNode("entry", []model.Tunnel{tunnel}, []model.Forward{forward})
	if len(tunnels) != 1 || len(forwards) != 1 {
		t.Fatalf("scope returned tunnels=%d forwards=%d", len(tunnels), len(forwards))
	}
	stage := tunnels[0].Stages[0]
	if stage.TCPStrategy != "round_robin" || stage.UDPStrategy != "random" {
		t.Fatalf("runtime strategies = %q/%q", stage.TCPStrategy, stage.UDPStrategy)
	}
	if got := stage.Nodes[0].Protocols; len(got) != 1 || got[0] != model.ForwardProtocolTCP {
		t.Fatalf("runtime protocols = %#v", got)
	}
	if got := forwards[0].Protocols; len(got) != 1 || got[0] != model.ForwardProtocolTCP {
		t.Fatalf("forward runtime protocols = %#v, want tcp only for tcp-only entry", got)
	}
}

func testPrepareTunnelServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return &Server{store: st}
}

func TestValidateNodeInputBoundsMetadata(t *testing.T) {
	if err := validateNodeInput("node-1", map[string]string{"region": "cn"}, "node.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := validateNodeInput("node-1", nil, "https://node.example.com"); err == nil {
		t.Fatal("expected URL-shaped public host to be rejected")
	}
	labels := make(map[string]string, maxNodeLabelEntries+1)
	for index := 0; index <= maxNodeLabelEntries; index++ {
		labels["label"+strconv.Itoa(index)] = "value"
	}
	if err := validateNodeInput("node-1", labels, ""); err == nil {
		t.Fatal("expected oversized label map to be rejected")
	}
}

func TestValidateNodeControlMessageRejectsSpoofedOrMalformedReports(t *testing.T) {
	if err := validateNodeHello("node-1", sharedprotocol.ControlMessage{
		Type:    "hello",
		NodeID:  "node-2",
		Version: "1.0.0",
	}); err == nil {
		t.Fatal("expected spoofed hello node id to be rejected")
	}
	if err := validateNodeControlMessage("node-1", sharedprotocol.ControlMessage{
		Type:    "heartbeat",
		NodeID:  "node-1",
		Version: "1.0.0",
		UpdateReport: &model.NodeUpdateReport{
			Status: "unknown",
		},
	}); err == nil {
		t.Fatal("expected unknown update status to be rejected")
	}
}

func TestValidateMetricsReportRejectsUntrustedValues(t *testing.T) {
	if err := validateMetricsReport(model.MetricsReport{
		ObservedAt: time.Now().UTC(),
		ForwardStats: []model.TrafficStat{{
			ID:      "forward:fwd-1",
			BytesIn: -1,
		}},
	}); err == nil {
		t.Fatal("expected negative metric value to be rejected")
	}
	if err := validateMetricsReport(model.MetricsReport{
		ObservedAt: time.Now().UTC().Add(25 * time.Hour),
	}); err == nil {
		t.Fatal("expected far-future metric timestamp to be rejected")
	}
}
