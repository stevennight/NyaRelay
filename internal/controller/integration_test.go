package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/nodehub"
	"nyarelay/internal/controller/store"
	"nyarelay/internal/node"
	"nyarelay/internal/shared/model"
)

type controllerHarness struct {
	t          *testing.T
	server     *Server
	httpServer *http.Server
	listener   net.Listener
	store      *store.Store
	url        string
	closeOnce  sync.Once
}

func newControllerHarness(t *testing.T, listenAddr string) *controllerHarness {
	t.Helper()

	return newControllerHarnessInDir(t, t.TempDir(), listenAddr)
}

func newControllerHarnessInDir(t *testing.T, dir, listenAddr string) *controllerHarness {
	t.Helper()

	dbPath := filepath.Join(dir, "nyarelay.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		cfg: Config{
			ListenAddr: listenAddr,
			DataDir:    dir,
			DBPath:     dbPath,
		},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:    st,
		sessions: auth.NewSessions(time.Hour),
		limiter:  auth.NewLoginLimiter(),
		hub:      nodehub.New(),
		mux:      http.NewServeMux(),
	}
	if err := srv.ensureSigningKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.routes()

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{
		Handler:           secureHeaders(srv.mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := httpSrv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("controller server failed: %v", err)
		}
	}()

	h := &controllerHarness{
		t:          t,
		server:     srv,
		httpServer: httpSrv,
		listener:   ln,
		store:      st,
		url:        "http://" + ln.Addr().String(),
	}
	t.Cleanup(h.close)
	return h
}

func (h *controllerHarness) close() {
	h.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.httpServer.Shutdown(ctx)
		_ = h.listener.Close()
		_ = h.store.Close()
	})
}

func TestControllerNodeSingleNodeTCPIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	nodeNode, token := createNode(t, h.server, "entry-1")
	nodeDir := t.TempDir()
	nodeCancel := startNode(t, h.url, nodeNode.ID, token, pub, nodeDir)
	defer nodeCancel()

	waitForNodeOnline(t, h.store, nodeNode.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	listenAddr := freeTCPAddr(t)
	route := model.Route{
		ID:        "route_tcp_1",
		Name:      "single-tcp",
		Protocol:  model.ProtocolTCP,
		EntryNode: nodeNode.ID,
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   true,
	}
	upsertRoute(t, h.server, route)

	assertTCPRoundTrip(t, listenAddr, "nya-single")
	h.close()
	assertTCPRoundTrip(t, listenAddr, "nya-after-close")
}

func TestControllerNodeSingleNodeUDPIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	nodeNode, token := createNode(t, h.server, "entry-udp")
	nodeCancel := startNode(t, h.url, nodeNode.ID, token, pub, t.TempDir())
	defer nodeCancel()

	waitForNodeOnline(t, h.store, nodeNode.ID)

	targetAddr, closeTarget := udpEchoServer(t)
	defer closeTarget()

	listenAddr := freeUDPAddr(t)
	route := model.Route{
		ID:        "route_udp_1",
		Name:      "single-udp",
		Protocol:  model.ProtocolUDP,
		EntryNode: nodeNode.ID,
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   true,
	}
	upsertRoute(t, h.server, route)

	assertUDPRoundTrip(t, listenAddr, "nya-udp")
}

func TestControllerNodeThreeNodeTCPIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entryNode, entryToken := createNode(t, h.server, "entry")
	midNode, midToken := createNode(t, h.server, "mid")
	exitNode, exitToken := createNode(t, h.server, "exit")

	entryCancel := startNode(t, h.url, entryNode.ID, entryToken, pub, t.TempDir())
	midCancel := startNode(t, h.url, midNode.ID, midToken, pub, t.TempDir())
	exitCancel := startNode(t, h.url, exitNode.ID, exitToken, pub, t.TempDir())
	defer entryCancel()
	defer midCancel()
	defer exitCancel()

	waitForNodeOnline(t, h.store, entryNode.ID)
	waitForNodeOnline(t, h.store, midNode.ID)
	waitForNodeOnline(t, h.store, exitNode.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	linkABListen := freeTCPAddr(t)
	linkBCListen := freeTCPAddr(t)

	linkAB := model.Link{
		ID:         "link_ab",
		Name:       "entry-to-mid",
		Type:       model.LinkDirect,
		FromNode:   entryNode.ID,
		ToNode:     midNode.ID,
		BindAddr:   linkABListen,
		PublicAddr: linkABListen,
		Enabled:    true,
	}
	linkBC := model.Link{
		ID:         "link_bc",
		Name:       "mid-to-exit",
		Type:       model.LinkDirect,
		FromNode:   midNode.ID,
		ToNode:     exitNode.ID,
		BindAddr:   linkBCListen,
		PublicAddr: linkBCListen,
		Enabled:    true,
	}
	upsertLink(t, h.server, linkAB)
	upsertLink(t, h.server, linkBC)

	listenAddr := freeTCPAddr(t)
	route := model.Route{
		ID:        "route_multi_1",
		Name:      "three-hop",
		Protocol:  model.ProtocolTCP,
		EntryNode: entryNode.ID,
		Listen:    listenAddr,
		Hops:      []model.RouteHop{{LinkID: linkAB.ID}, {LinkID: linkBC.ID}},
		Target:    targetAddr,
		Enabled:   true,
	}
	upsertRoute(t, h.server, route)

	assertTCPRoundTrip(t, listenAddr, "nya-three-hop")
}

func TestControllerRestartAndNodeReconnectIntegration(t *testing.T) {
	dir := t.TempDir()
	controllerAddr := freeTCPAddr(t)

	h1 := newControllerHarnessInDir(t, dir, controllerAddr)
	pub := mustSigningKey(t, h1.store)

	nodeNode, token := createNode(t, h1.server, "entry-restart")
	nodeDir := t.TempDir()
	nodeCancel := startNode(t, h1.url, nodeNode.ID, token, pub, nodeDir)
	defer nodeCancel()

	waitForNodeOnline(t, h1.store, nodeNode.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()
	routeListen := freeTCPAddr(t)

	route := model.Route{
		ID:        "route_restart_1",
		Name:      "restart",
		Protocol:  model.ProtocolTCP,
		EntryNode: nodeNode.ID,
		Listen:    routeListen,
		Target:    targetAddr,
		Enabled:   true,
	}
	upsertRoute(t, h1.server, route)

	assertTCPRoundTrip(t, routeListen, "nya-before-restart")

	h1.close()

	assertTCPRoundTrip(t, routeListen, "nya-during-controller-down")

	h2 := newControllerHarnessInDir(t, dir, controllerAddr)
	defer h2.close()

	waitForNodeOnline(t, h2.store, nodeNode.ID)
	assertTCPRoundTrip(t, routeListen, "nya-after-restart")
}

func TestNodeInstallEndpointReturnsCommand(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	_, _ = mustSigningKey(t, h.store), h

	nodeNode, token := createNode(t, h.server, "installer")
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeNode.ID+"/install", nil)
	req.SetPathValue("id", nodeNode.ID)
	req.Header.Set("X-NyaRelay-Node-ID", "admin")
	req.Header.Set("X-NyaRelay-Node-Token", "admin-token")
	rec := httptest.NewRecorder()
	h.server.handleGetNodeInstall(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("install endpoint failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp NodeInstallInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Node.ID != nodeNode.ID || resp.Token != token {
		t.Fatalf("unexpected install info: %#v", resp)
	}
	if resp.Command == "" || resp.ScriptURL == "" || resp.BinaryURL == "" {
		t.Fatalf("missing install fields: %#v", resp)
	}
}

func TestRouteAutoAssignsPortWithinNodeRange(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	nodeNode, _ := createNode(t, h.server, "entry-auto")
	nodeNode.PortMin = 12000
	nodeNode.PortMax = 12001
	if err := h.store.UpdateNode(context.Background(), nodeNode); err != nil {
		t.Fatal(err)
	}

	first := upsertRoute(t, h.server, model.Route{
		ID:        "route_auto_1",
		Name:      "auto-1",
		Protocol:  model.ProtocolTCP,
		EntryNode: nodeNode.ID,
		Target:    "127.0.0.1:443",
		Enabled:   true,
	})
	second := upsertRoute(t, h.server, model.Route{
		ID:        "route_auto_2",
		Name:      "auto-2",
		Protocol:  model.ProtocolTCP,
		EntryNode: nodeNode.ID,
		Target:    "127.0.0.1:443",
		Enabled:   true,
	})
	got := map[string]bool{first.Listen: true, second.Listen: true}
	if len(got) != 2 || !got[":12000"] || !got[":12001"] {
		t.Fatalf("auto ports = %q and %q, want :12000 and :12001 in any order", first.Listen, second.Listen)
	}
}

func TestRouteRejectsDuplicatePortOnSameEntryNode(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	nodeNode, _ := createNode(t, h.server, "entry-dup")
	upsertRoute(t, h.server, model.Route{
		ID:        "route_dup_1",
		Name:      "dup-1",
		Protocol:  model.ProtocolTCP,
		EntryNode: nodeNode.ID,
		Listen:    ":13000",
		Target:    "127.0.0.1:443",
		Enabled:   true,
	})

	payload, err := json.Marshal(model.Route{
		ID:        "route_dup_2",
		Name:      "dup-2",
		Protocol:  model.ProtocolTCP,
		EntryNode: nodeNode.ID,
		Listen:    "0.0.0.0:13000",
		Target:    "127.0.0.1:443",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.server.handleUpsertRoute(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected duplicate route port to fail, got %d %s", rec.Code, rec.Body.String())
	}
}

func mustSigningKey(t *testing.T, st *store.Store) string {
	t.Helper()
	pub, _, err := st.GetSetting(context.Background(), signingPubSetting)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func createNode(t *testing.T, s *Server, name string) (model.Node, string) {
	t.Helper()
	body := map[string]any{"name": name}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleCreateNode(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create node failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Node  model.Node `json:"node"`
		Token string     `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Node.ID == "" {
		t.Fatal("node id missing")
	}
	return resp.Node, resp.Token
}

func upsertLink(t *testing.T, s *Server, link model.Link) model.Link {
	t.Helper()
	payload, err := json.Marshal(struct {
		ID         string            `json:"id"`
		Name       string            `json:"name"`
		Type       model.LinkType    `json:"type"`
		FromNode   string            `json:"from_node"`
		ToNode     string            `json:"to_node"`
		BindAddr   string            `json:"bind_addr"`
		PublicAddr string            `json:"public_addr"`
		ServerName string            `json:"server_name"`
		Enabled    bool              `json:"enabled"`
		Settings   map[string]string `json:"settings"`
	}{
		ID:         link.ID,
		Name:       link.Name,
		Type:       link.Type,
		FromNode:   link.FromNode,
		ToNode:     link.ToNode,
		BindAddr:   link.BindAddr,
		PublicAddr: link.PublicAddr,
		ServerName: link.ServerName,
		Enabled:    link.Enabled,
		Settings:   link.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/links", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleUpsertLink(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert link failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp model.Link
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func upsertRoute(t *testing.T, s *Server, route model.Route) model.Route {
	t.Helper()
	payload, err := json.Marshal(struct {
		ID        string              `json:"id"`
		Name      string              `json:"name"`
		Protocol  model.RouteProtocol `json:"protocol"`
		EntryNode string              `json:"entry_node"`
		Listen    string              `json:"listen"`
		Hops      []model.RouteHop    `json:"hops"`
		Target    string              `json:"target"`
		Enabled   bool                `json:"enabled"`
	}{
		ID:        route.ID,
		Name:      route.Name,
		Protocol:  route.Protocol,
		EntryNode: route.EntryNode,
		Listen:    route.Listen,
		Hops:      route.Hops,
		Target:    route.Target,
		Enabled:   route.Enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/routes", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleUpsertRoute(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert route failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp model.Route
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func startNode(t *testing.T, controllerURL, nodeID, token, signingKey, dataDir string) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- node.Run(ctx, []string{
			"--controller", controllerURL,
			"--id", nodeID,
			"--token", token,
			"--signing-key", signingKey,
			"--data", dataDir,
			"--log-level", "error",
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("node run failed: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("node did not shut down")
		}
	})
	return cancel
}

func waitForNodeOnline(t *testing.T, st *store.Store, nodeID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		node, err := st.GetNode(context.Background(), nodeID)
		if err == nil && node.Status == model.NodeOnline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node %s never became online", nodeID)
}

func tcpEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func udpEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], remote)
		}
	}()
	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
		<-done
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func freeUDPAddr(t *testing.T) string {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	out := conn.LocalAddr().String()
	_ = conn.Close()
	return out
}

func assertTCPRoundTrip(t *testing.T, addr, payload string) {
	t.Helper()
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != payload {
		t.Fatalf("got %q, want %q", string(buf), payload)
	}
}

func assertUDPRoundTrip(t *testing.T, addr, payload string) {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	var conn *net.UDPConn
	for i := 0; i < 50; i++ {
		conn, err = net.DialUDP("udp", nil, remote)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != payload {
		t.Fatalf("got %q, want %q", string(buf[:n]), payload)
	}
}
