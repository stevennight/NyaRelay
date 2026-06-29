package controller

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/nodehub"
	"nyarelay/internal/controller/store"
	"nyarelay/internal/shared/model"
	sharedprotocol "nyarelay/internal/shared/protocol"
)

func TestNodeWebSocketReceivesConfigPush(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "nyarelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestStore(t, st)

	s := &Server{
		cfg: Config{
			PublicURL: "https://panel.example",
		},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:    st,
		sessions: auth.NewSessions(time.Hour),
		limiter:  auth.NewLoginLimiter(),
		hub:      nodehub.New(),
		mux:      http.NewServeMux(),
	}
	if err := s.ensureSigningKey(ctx); err != nil {
		t.Fatal(err)
	}
	s.routes()

	ts := httptest.NewServer(secureHeaders(s.mux))
	defer ts.Close()

	node := model.Node{
		ID:        "node_1",
		Name:      "node-1",
		Status:    model.NodeOffline,
		Approved:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.UpsertNode(ctx, node, "node-token"); err != nil {
		t.Fatal(err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/node/ws"
	headers := http.Header{}
	headers.Set("X-NyaRelay-Node-ID", node.ID)
	headers.Set("X-NyaRelay-Node-Token", "node-token")
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestWebSocket(t, conn, websocket.StatusNormalClosure, "done")

	hello := sharedprotocol.ControlMessage{
		Type:    "hello",
		NodeID:  node.ID,
		Version: "1.0.0",
		System: model.NodeSystem{
			Hostname: "node-1",
			OS:       "linux",
			Arch:     "amd64",
		},
	}
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatal(err)
	}

	var first sharedprotocol.ControlMessage
	if err := wsjson.Read(ctx, conn, &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "config" || first.Config == nil {
		t.Fatalf("unexpected first message: %#v", first)
	}
	if first.Config.Config.NodeID != node.ID {
		t.Fatalf("config node mismatch: got %s", first.Config.Config.NodeID)
	}

	tunnel := upsertTunnel(t, s, directTunnelRequest("tun_ws_push", node.ID))
	upsertForward(t, s, forwardRequest{
		ID:        "fwd_ws_push",
		Name:      "ws-push",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    "127.0.0.1:18443",
		Target:    "127.0.0.1:443",
		Enabled:   boolPtr(true),
	})

	var second sharedprotocol.ControlMessage
	for i := 0; i < 3; i++ {
		if err := wsjson.Read(ctx, conn, &second); err != nil {
			t.Fatal(err)
		}
		if second.Type != "config" || second.Config == nil {
			t.Fatalf("unexpected second message: %#v", second)
		}
		if len(second.Config.Config.Forwards) == 1 {
			break
		}
	}
	if second.Config.Config.Revision <= first.Config.Config.Revision {
		t.Fatalf("revision did not advance: %d -> %d", first.Config.Config.Revision, second.Config.Config.Revision)
	}
	if len(second.Config.Config.Forwards) != 1 {
		t.Fatalf("expected one forward in pushed config, got %d", len(second.Config.Config.Forwards))
	}
}

func TestRevokedNodeCannotConnectWebSocket(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "nyarelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestStore(t, st)

	s := &Server{
		cfg: Config{
			PublicURL: "https://panel.example",
		},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:    st,
		sessions: auth.NewSessions(time.Hour),
		limiter:  auth.NewLoginLimiter(),
		hub:      nodehub.New(),
		mux:      http.NewServeMux(),
	}
	if err := s.ensureSigningKey(ctx); err != nil {
		t.Fatal(err)
	}
	s.routes()

	ts := httptest.NewServer(secureHeaders(s.mux))
	defer ts.Close()

	node := model.Node{
		ID:        "node_2",
		Name:      "node-2",
		Status:    model.NodeRevoked,
		Approved:  true,
		Revoked:   true,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.UpsertNode(ctx, node, "node-token"); err != nil {
		t.Fatal(err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/node/ws"
	headers := http.Header{}
	headers.Set("X-NyaRelay-Node-ID", node.ID)
	headers.Set("X-NyaRelay-Node-Token", "node-token")
	if _, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: headers}); err == nil {
		t.Fatal("expected revoked node websocket dial to fail")
	}
}

func closeTestStore(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}
}

func closeTestWebSocket(t *testing.T, conn *websocket.Conn, code websocket.StatusCode, reason string) {
	t.Helper()
	if err := conn.Close(code, reason); err != nil {
		t.Errorf("close websocket: %v", err)
	}
}
