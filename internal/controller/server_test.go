package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	clear := ""
	tunnel, _, err := srv.prepareTunnel(context.Background(), tunnelRequest{
		ID:           "tun_entry_default",
		Name:         "entry default",
		Type:         model.TunnelDirect,
		Transport:    model.TunnelTransportDirect,
		EntryAddress: &clear,
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
