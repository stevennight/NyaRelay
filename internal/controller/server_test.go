package controller

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/store"
	"nyarelay/internal/shared/model"
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

func TestLoginLimitKeyPrefersForwardedAddress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.10:40001"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.10")

	if got := loginLimitKey(req, "admin"); got != "203.0.113.9:admin" {
		t.Fatalf("limit key = %q, want forwarded client ip", got)
	}
}

func TestSetSessionCookieUsesSecureWhenForwardedHTTPS(t *testing.T) {
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
	if !cookies[0].Secure {
		t.Fatal("session cookie should be secure for https requests")
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
