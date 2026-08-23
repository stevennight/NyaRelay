package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/nodehub"
	"nyarelay/internal/controller/store"
	"nyarelay/internal/node"
	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	"nyarelay/internal/shared/protocol"
	sharedversion "nyarelay/internal/shared/version"
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

var (
	nextTCPTestPort  = 10000
	nextUDPTestPort  = 10000
	nextDualTestPort = 10000
	portCursorMu     sync.Mutex
)

func newControllerHarness(t *testing.T, listenAddr string) *controllerHarness {
	t.Helper()
	return newControllerHarnessInDir(t, t.TempDir(), listenAddr)
}

func newControllerHarnessInDir(t *testing.T, dir, listenAddr string) *controllerHarness {
	t.Helper()
	dbPath := filepath.Join(dir, "nyarelay.db")
	testSecretKey := sha256.Sum256([]byte("nyarelay-controller-test:" + dir))
	st, err := store.OpenWithSecretKey(context.Background(), dbPath, testSecretKey[:])
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

	entry, token := createNode(t, h.server, "entry-1")
	nodeCancel := startNode(t, h.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	listenAddr := freeTCPAddr(t)
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_tcp_1", entry.ID))
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_tcp_1",
		Name:      "single-tcp",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertTCPRoundTrip(t, listenAddr, "nya-single")
	h.close()
	assertTCPRoundTrip(t, listenAddr, "nya-after-close")
}

func TestControllerNodeSingleNodeUDPIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, token := createNode(t, h.server, "entry-udp")
	nodeCancel := startNode(t, h.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	targetAddr, closeTarget := udpEchoServer(t)
	defer closeTarget()

	listenAddr := freeUDPAddr(t)
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_udp_1", entry.ID))
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_udp_1",
		Name:      "single-udp",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP},
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertUDPRoundTrip(t, listenAddr, "nya-udp")
}

func TestControllerNodeTCPAndUDPSamePortIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, token := createNode(t, h.server, "entry-dual")
	nodeCancel := startNode(t, h.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	targetAddr, closeTarget := dualProtoEchoServer(t)
	defer closeTarget()

	listenAddr := freeDualProtoAddr(t)
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_dual", entry.ID))
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_dual",
		Name:      "dual",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertTCPRoundTrip(t, listenAddr, "nya-dual-tcp")
	assertUDPRoundTrip(t, listenAddr, "nya-dual-udp")
}

func TestControllerNodeThreeNodeTCPIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, entryToken := createNode(t, h.server, "entry")
	mid, midToken := createNode(t, h.server, "mid")
	exit, exitToken := createNode(t, h.server, "exit")

	entryCancel := startNode(t, h.url, entry.ID, entryToken, pub, t.TempDir())
	midCancel := startNode(t, h.url, mid.ID, midToken, pub, t.TempDir())
	exitCancel := startNode(t, h.url, exit.ID, exitToken, pub, t.TempDir())
	defer entryCancel()
	defer midCancel()
	defer exitCancel()

	waitForNodeOnline(t, h.store, entry.ID)
	waitForNodeOnline(t, h.store, mid.ID)
	waitForNodeOnline(t, h.store, exit.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	midListen := freeTCPAddr(t)
	exitListen := freeTCPAddr(t)
	tunnel := upsertTunnel(t, h.server, chainTunnelRequest("tun_three", entry.ID, []string{mid.ID}, exit.ID, []string{midListen, exitListen}))
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_three",
		Name:      "three-hop",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    entryListen,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertTCPRoundTrip(t, entryListen, "nya-three-hop")
}

func TestControllerNodeTCPMultiCandidateFailoverIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, entryToken := createNode(t, h.server, "entry-failover")
	midA, _ := createNode(t, h.server, "mid-a")
	midB, midBToken := createNode(t, h.server, "mid-b")
	exit, exitToken := createNode(t, h.server, "exit-failover")

	for _, item := range []struct {
		node       model.Node
		publicHost string
		portMin    int
		portMax    int
	}{
		{node: entry, publicHost: "127.0.0.1", portMin: 12000, portMax: 12005},
		{node: midA, publicHost: "127.0.0.1", portMin: 12010, portMax: 12015},
		{node: midB, publicHost: "127.0.0.1", portMin: 12020, portMax: 12025},
		{node: exit, publicHost: "127.0.0.1", portMin: 12030, portMax: 12035},
	} {
		item.node.PublicHost = item.publicHost
		item.node.PortMin = item.portMin
		item.node.PortMax = item.portMax
		if err := h.store.UpdateNode(context.Background(), item.node); err != nil {
			t.Fatal(err)
		}
	}

	entryCancel := startNode(t, h.url, entry.ID, entryToken, pub, t.TempDir())
	midBCancel := startNode(t, h.url, midB.ID, midBToken, pub, t.TempDir())
	exitCancel := startNode(t, h.url, exit.ID, exitToken, pub, t.TempDir())
	defer entryCancel()
	defer midBCancel()
	defer exitCancel()

	waitForNodeOnline(t, h.store, entry.ID)
	waitForNodeOnline(t, h.store, midB.ID)
	waitForNodeOnline(t, h.store, exit.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	tunnel := upsertTunnel(t, h.server, tunnelRequest{
		ID:        "tun_failover",
		Name:      "failover",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Enabled:   boolPtr(true),
		Stages: []model.TunnelStage{
			{
				ID:       "stage_entry",
				TunnelID: "tun_failover",
				Index:    0,
				Role:     model.TunnelStageEntry,
				Strategy: "single",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_entry_node", TunnelID: "tun_failover", StageID: "stage_entry", NodeID: entry.ID, Weight: 1},
				},
			},
			{
				ID:       "stage_mid",
				TunnelID: "tun_failover",
				Index:    1,
				Role:     model.TunnelStageMiddle,
				Strategy: "failover",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_mid_a", TunnelID: "tun_failover", StageID: "stage_mid", NodeID: midA.ID, Weight: 1},
					{ID: "stage_mid_b", TunnelID: "tun_failover", StageID: "stage_mid", NodeID: midB.ID, Weight: 1},
				},
			},
			{
				ID:       "stage_exit",
				TunnelID: "tun_failover",
				Index:    2,
				Role:     model.TunnelStageExit,
				Strategy: "single",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_exit_node", TunnelID: "tun_failover", StageID: "stage_exit", NodeID: exit.ID, Weight: 1},
				},
			},
		},
	})

	forward := upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_failover",
		Name:      "failover",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})
	if forward.Listen != ":12000" {
		t.Fatalf("listen = %q, want :12000", forward.Listen)
	}

	assertTCPRoundTrip(t, "127.0.0.1"+forward.Listen, "nya-failover")
}

func TestControllerNodeUDPMultiCandidateRoundRobinIntegration(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, entryToken := createNode(t, h.server, "entry-udp-multi")
	tcpOnly, _ := createNode(t, h.server, "udp-tcp-only")
	exitA, _ := createNode(t, h.server, "udp-exit-a")
	exitB, _ := createNode(t, h.server, "udp-exit-b")

	entryCancel := startNode(t, h.url, entry.ID, entryToken, pub, t.TempDir())
	defer entryCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	entryListen := freeUDPAddr(t)
	tcpOnlyListen := freeTCPAddr(t)
	exitAListen := freeTCPAddr(t)
	exitBListen := freeTCPAddr(t)
	closeTCPOnly := udpStageResponder(t, tcpOnlyListen, "tcp-only:")
	closeExitA := udpStageResponder(t, exitAListen, "a:")
	closeExitB := udpStageResponder(t, exitBListen, "b:")
	defer closeTCPOnly()
	defer closeExitA()
	defer closeExitB()

	tunnel := upsertTunnel(t, h.server, tunnelRequest{
		ID:        "tun_udp_multi_controller",
		Name:      "udp multi controller",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Enabled:   boolPtr(true),
		Stages: []model.TunnelStage{
			{
				ID:       "stage_udp_entry",
				TunnelID: "tun_udp_multi_controller",
				Index:    0,
				Role:     model.TunnelStageEntry,
				Strategy: "single",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_udp_entry_node", TunnelID: "tun_udp_multi_controller", StageID: "stage_udp_entry", NodeID: entry.ID, Weight: 1},
				},
			},
			{
				ID:          "stage_udp_exit",
				TunnelID:    "tun_udp_multi_controller",
				Index:       1,
				Role:        model.TunnelStageExit,
				Strategy:    "failover",
				UDPStrategy: "round_robin",
				Nodes: []model.TunnelStageNode{
					{
						ID:         "stage_udp_a_tcp_only",
						TunnelID:   "tun_udp_multi_controller",
						StageID:    "stage_udp_exit",
						NodeID:     tcpOnly.ID,
						Weight:     1,
						Protocols:  []model.ForwardProtocol{model.ForwardProtocolTCP},
						ListenAddr: tcpOnlyListen,
						PublicAddr: tcpOnlyListen,
					},
					{
						ID:         "stage_udp_b_exit_a",
						TunnelID:   "tun_udp_multi_controller",
						StageID:    "stage_udp_exit",
						NodeID:     exitA.ID,
						Weight:     1,
						Protocols:  []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
						ListenAddr: exitAListen,
						PublicAddr: exitAListen,
					},
					{
						ID:         "stage_udp_0_exit_b",
						TunnelID:   "tun_udp_multi_controller",
						StageID:    "stage_udp_exit",
						NodeID:     exitB.ID,
						Weight:     1,
						Protocols:  []model.ForwardProtocol{model.ForwardProtocolUDP},
						ListenAddr: exitBListen,
						PublicAddr: exitBListen,
					},
				},
			},
		},
	})
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_udp_multi_controller",
		Name:      "udp multi controller",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP},
		Listen:    entryListen,
		Target:    "127.0.0.1:9",
		Enabled:   boolPtr(true),
	})

	clientOne := newControllerUDPClient(t, entryListen)
	defer clientOne.close()
	if got := clientOne.roundTripEventually(t, "one-1", "a:"); got != "a:one-1" {
		t.Fatalf("first udp session = %q, want a:one-1", got)
	}
	if got := clientOne.roundTrip(t, "one-2"); got != "a:one-2" {
		t.Fatalf("same udp session moved candidates: %q", got)
	}

	clientTwo := newControllerUDPClient(t, entryListen)
	defer clientTwo.close()
	if got := clientTwo.roundTrip(t, "two-1"); got != "b:two-1" {
		t.Fatalf("second udp session = %q, want b:two-1", got)
	}
}

func TestControllerForwardTargetUpdateAppliesWithoutRestart(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, token := createNode(t, h.server, "entry-update")
	nodeCancel := startNode(t, h.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	targetOne, closeOne := tcpReplyServer(t, "one")
	defer closeOne()
	targetTwo, closeTwo := tcpReplyServer(t, "two")
	defer closeTwo()

	listenAddr := freeTCPAddr(t)
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_update", entry.ID))
	forward := upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_update",
		Name:      "update",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    listenAddr,
		Target:    targetOne,
		Enabled:   boolPtr(true),
	})

	assertTCPResponse(t, listenAddr, "ping", "one")

	upsertForward(t, h.server, forwardRequest{
		ID:        forward.ID,
		Name:      forward.Name,
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    listenAddr,
		Target:    targetTwo,
		Enabled:   boolPtr(true),
	})

	assertTCPResponse(t, listenAddr, "ping", "two")
}

func TestControllerForwardDeleteStopsListener(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, token := createNode(t, h.server, "entry-delete")
	nodeCancel := startNode(t, h.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	listenAddr := freeTCPAddr(t)
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_delete", entry.ID))
	forward := upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_delete",
		Name:      "delete",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertTCPRoundTrip(t, listenAddr, "before-delete")

	req := httptest.NewRequest(http.MethodDelete, "/api/forwards/"+forward.ID, nil)
	req.SetPathValue("id", forward.ID)
	rec := httptest.NewRecorder()
	h.server.handleDeleteForward(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete forward failed: %d %s", rec.Code, rec.Body.String())
	}

	assertTCPDialEventuallyFails(t, listenAddr)
}

func TestControllerTunnelDisableStopsForward(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	pub := mustSigningKey(t, h.store)

	entry, token := createNode(t, h.server, "entry-disable")
	nodeCancel := startNode(t, h.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h.store, entry.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	listenAddr := freeTCPAddr(t)
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_disable", entry.ID))
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_disable",
		Name:      "disable",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertTCPRoundTrip(t, listenAddr, "before-disable")

	req := httptest.NewRequest(http.MethodPost, "/api/tunnels/"+tunnel.ID+"/disable", nil)
	req.SetPathValue("id", tunnel.ID)
	rec := httptest.NewRecorder()
	h.server.handleDisableTunnel(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable tunnel failed: %d %s", rec.Code, rec.Body.String())
	}

	assertTCPDialEventuallyFails(t, listenAddr)
}

func TestControllerRestartAndNodeReconnectIntegration(t *testing.T) {
	dir := t.TempDir()
	controllerAddr := freeTCPAddr(t)

	h1 := newControllerHarnessInDir(t, dir, controllerAddr)
	pub := mustSigningKey(t, h1.store)

	entry, token := createNode(t, h1.server, "entry-restart")
	nodeCancel := startNode(t, h1.url, entry.ID, token, pub, t.TempDir())
	defer nodeCancel()
	waitForNodeOnline(t, h1.store, entry.ID)

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()
	listenAddr := freeTCPAddr(t)

	tunnel := upsertTunnel(t, h1.server, directTunnelRequest("tun_restart", entry.ID))
	upsertForward(t, h1.server, forwardRequest{
		ID:        "fwd_restart",
		Name:      "restart",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    listenAddr,
		Target:    targetAddr,
		Enabled:   boolPtr(true),
	})

	assertTCPRoundTrip(t, listenAddr, "nya-before-restart")
	h1.close()
	assertTCPRoundTrip(t, listenAddr, "nya-during-controller-down")

	h2 := newControllerHarnessInDir(t, dir, controllerAddr)
	defer h2.close()

	waitForNodeOnline(t, h2.store, entry.ID)
	assertTCPRoundTrip(t, listenAddr, "nya-after-restart")
}

func TestNodeInstallEndpointReturnsCommand(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	_, _ = mustSigningKey(t, h.store), h
	updatePublicKey, _, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restoreUpdateKey := setUpdatePublicKeyForTest(t, updatePublicKey)
	defer restoreUpdateKey()
	h.server.cfg.PublicURL = "https://relay.example.com"

	entry, token := createNode(t, h.server, "installer")
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/"+entry.ID+"/install", nil)
	req.SetPathValue("id", entry.ID)
	rec := httptest.NewRecorder()
	h.server.handleGetNodeInstall(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("install endpoint failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp NodeInstallInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Node.ID != entry.ID || resp.Token != token {
		t.Fatalf("unexpected install info: %#v", resp)
	}
	if resp.Command == "" || resp.ScriptURL == "" || resp.BinaryURL == "" {
		t.Fatalf("missing install fields: %#v", resp)
	}
}

func TestNodeInstallRejectsRemoteControllerWithoutUpdateKey(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	h.server.cfg.PublicURL = "https://relay.example.com"
	old := sharedversion.UpdatePublicKey
	sharedversion.UpdatePublicKey = ""
	t.Cleanup(func() { sharedversion.UpdatePublicKey = old })

	req := httptest.NewRequest(http.MethodPost, "/api/nodes", bytes.NewReader([]byte(`{"name":"remote-installer"}`)))
	rec := httptest.NewRecorder()
	h.server.handleCreateNode(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("create node status = %d, body = %s", rec.Code, rec.Body.String())
	}
	nodes, err := h.store.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("remote install failure created %d node(s)", len(nodes))
	}
}

func TestForwardAutoAssignsPortWithinNodeRange(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	entry, _ := createNode(t, h.server, "entry-auto")
	entry.PortMin = 12000
	entry.PortMax = 12001
	if err := h.store.UpdateNode(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_auto", entry.ID))

	first := upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_auto_1",
		Name:      "auto-1",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Target:    "127.0.0.1:443",
		Enabled:   boolPtr(true),
	})
	second := upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_auto_2",
		Name:      "auto-2",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Target:    "127.0.0.1:443",
		Enabled:   boolPtr(true),
	})
	got := map[string]bool{first.Listen: true, second.Listen: true}
	if len(got) != 2 || !got[":12000"] || !got[":12001"] {
		t.Fatalf("auto ports = %q and %q, want :12000 and :12001 in any order", first.Listen, second.Listen)
	}
}

func TestForwardRejectsDuplicatePortOnSameEntryNode(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	entry, _ := createNode(t, h.server, "entry-dup")
	tunnel := upsertTunnel(t, h.server, directTunnelRequest("tun_dup", entry.ID))
	upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_dup_1",
		Name:      "dup-1",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    ":13000",
		Target:    "127.0.0.1:443",
		Enabled:   boolPtr(true),
	})

	payload, err := json.Marshal(forwardRequest{
		ID:        "fwd_dup_2",
		Name:      "dup-2",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    "0.0.0.0:13000",
		Target:    "127.0.0.1:443",
		Enabled:   boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/forwards", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.server.handleUpsertForward(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected duplicate forward port to fail, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestForwardAutoAssignsCommonPortAcrossMultipleEntryNodes(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")
	entryA, _ := createNode(t, h.server, "entry-a")
	entryB, _ := createNode(t, h.server, "entry-b")
	entryA.PortMin = 12000
	entryA.PortMax = 12001
	entryB.PortMin = 12000
	entryB.PortMax = 12001
	if err := h.store.UpdateNode(context.Background(), entryA); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateNode(context.Background(), entryB); err != nil {
		t.Fatal(err)
	}

	tunnel := upsertTunnel(t, h.server, tunnelRequest{
		ID:        "tun_multi_entry",
		Name:      "multi-entry",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Enabled:   boolPtr(true),
		Stages: []model.TunnelStage{
			{
				ID:       "stage_entry",
				TunnelID: "tun_multi_entry",
				Index:    0,
				Role:     model.TunnelStageEntry,
				Strategy: "failover",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_node_a", TunnelID: "tun_multi_entry", StageID: "stage_entry", NodeID: entryA.ID, Weight: 1},
					{ID: "stage_node_b", TunnelID: "tun_multi_entry", StageID: "stage_entry", NodeID: entryB.ID, Weight: 1},
				},
			},
		},
	})

	forward := upsertForward(t, h.server, forwardRequest{
		ID:        "fwd_multi_entry",
		Name:      "multi-entry",
		TunnelID:  tunnel.ID,
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Target:    "127.0.0.1:443",
		Enabled:   boolPtr(true),
	})

	if forward.Listen != ":12000" {
		t.Fatalf("listen = %q, want :12000", forward.Listen)
	}
	allocations, err := h.store.ListPortAllocations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 2 {
		t.Fatalf("allocations = %d, want 2", len(allocations))
	}
	seen := map[string]bool{}
	for _, allocation := range allocations {
		if allocation.Port != 12000 {
			t.Fatalf("allocation port = %d, want 12000", allocation.Port)
		}
		seen[allocation.NodeID] = true
	}
	if !seen[entryA.ID] || !seen[entryB.ID] {
		t.Fatalf("allocations did not cover both entry nodes: %#v", allocations)
	}
}

func TestCompileConfigScopesAndRedactsTunnelSecrets(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")

	entry, _ := createNode(t, h.server, "entry-scope")
	mid, _ := createNode(t, h.server, "mid-scope")
	exit, _ := createNode(t, h.server, "exit-scope")

	for _, testNode := range []model.Node{entry, mid, exit} {
		testNode.PublicHost = testNode.Name + ".example.com"
		testNode.PortMin = 12000
		testNode.PortMax = 12010
		if err := h.store.UpdateNode(context.Background(), testNode); err != nil {
			t.Fatal(err)
		}
	}
	tunnel := upsertTunnel(t, h.server, tunnelRequest{
		ID:        "tun_scope",
		Name:      "scope",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportMTLS,
		Enabled:   boolPtr(true),
		Stages: []model.TunnelStage{
			{
				ID:       "stage_entry",
				TunnelID: "tun_scope",
				Index:    0,
				Role:     model.TunnelStageEntry,
				Strategy: "failover",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_entry_node", TunnelID: "tun_scope", StageID: "stage_entry", NodeID: entry.ID, Weight: 1},
				},
			},
			{
				ID:       "stage_mid",
				TunnelID: "tun_scope",
				Index:    1,
				Role:     model.TunnelStageMiddle,
				Strategy: "round_robin",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_mid_node", TunnelID: "tun_scope", StageID: "stage_mid", NodeID: mid.ID, Weight: 1},
				},
			},
			{
				ID:       "stage_exit",
				TunnelID: "tun_scope",
				Index:    2,
				Role:     model.TunnelStageExit,
				Strategy: "single",
				Nodes: []model.TunnelStageNode{
					{ID: "stage_exit_node", TunnelID: "tun_scope", StageID: "stage_exit", NodeID: exit.ID, Weight: 1},
				},
			},
		},
	})
	entrySigned, err := h.server.compileConfig(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	entryTunnels := entrySigned.Config.Tunnels
	if len(entryTunnels) != 1 {
		t.Fatalf("entry config tunnels = %d, want 1", len(entryTunnels))
	}
	entryStage0 := mustRuntimeNodeSettings(t, entryTunnels[0], 0, entry.ID)
	if len(entryStage0) != 0 {
		t.Fatalf("entry listener settings = %#v, want empty", entryStage0)
	}
	entryStage1 := mustRuntimeNodeSettings(t, entryTunnels[0], 1, mid.ID)
	if entryStage1["secret"] == "" || entryStage1["ca_cert"] == "" || entryStage1["client_cert"] == "" || entryStage1["client_key"] == "" {
		t.Fatalf("entry dialer settings incomplete: %#v", entryStage1)
	}
	if entryStage1["server_cert"] != "" || entryStage1["server_key"] != "" {
		t.Fatalf("entry dialer leaked listener material: %#v", entryStage1)
	}
	entryStage2 := mustRuntimeNodeSettings(t, entryTunnels[0], 2, exit.ID)
	if len(entryStage2) != 0 {
		t.Fatalf("entry config leaked exit settings: %#v", entryStage2)
	}

	midSigned, err := h.server.compileConfig(context.Background(), mid.ID)
	if err != nil {
		t.Fatal(err)
	}
	midTunnels := midSigned.Config.Tunnels
	if len(midTunnels) != 1 {
		t.Fatalf("mid config tunnels = %d, want 1", len(midTunnels))
	}
	midStage1 := mustRuntimeNodeSettings(t, midTunnels[0], 1, mid.ID)
	if midStage1["secret"] == "" || midStage1["server_cert"] == "" || midStage1["server_key"] == "" || midStage1["ca_cert"] == "" {
		t.Fatalf("mid listener settings incomplete: %#v", midStage1)
	}
	if midStage1["client_cert"] != "" || midStage1["client_key"] != "" {
		t.Fatalf("mid listener leaked client material: %#v", midStage1)
	}
	midStage2 := mustRuntimeNodeSettings(t, midTunnels[0], 2, exit.ID)
	if midStage2["secret"] == "" || midStage2["ca_cert"] == "" || midStage2["client_cert"] == "" || midStage2["client_key"] == "" {
		t.Fatalf("mid dialer settings incomplete: %#v", midStage2)
	}
	if midStage2["server_cert"] != "" || midStage2["server_key"] != "" {
		t.Fatalf("mid dialer leaked listener material: %#v", midStage2)
	}

	exitSigned, err := h.server.compileConfig(context.Background(), exit.ID)
	if err != nil {
		t.Fatal(err)
	}
	exitTunnels := exitSigned.Config.Tunnels
	if len(exitTunnels) != 1 {
		t.Fatalf("exit config tunnels = %d, want 1", len(exitTunnels))
	}
	exitStage2 := mustRuntimeNodeSettings(t, exitTunnels[0], 2, exit.ID)
	if exitStage2["secret"] == "" || exitStage2["server_cert"] == "" || exitStage2["server_key"] == "" || exitStage2["ca_cert"] == "" {
		t.Fatalf("exit listener settings incomplete: %#v", exitStage2)
	}
	if exitStage2["client_cert"] != "" || exitStage2["client_key"] != "" {
		t.Fatalf("exit listener leaked client material: %#v", exitStage2)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tunnels/"+tunnel.ID, nil)
	req.SetPathValue("id", tunnel.ID)
	rec := httptest.NewRecorder()
	h.server.handleGetTunnel(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("get tunnel failed: %d %s", rec.Code, rec.Body.String())
	}
	var redacted model.Tunnel
	if err := json.Unmarshal(rec.Body.Bytes(), &redacted); err != nil {
		t.Fatal(err)
	}
	for _, stage := range redacted.Stages {
		for _, stageNode := range stage.Nodes {
			assertRedactedTunnelSettings(t, stageNode.Settings)
		}
	}
}

func TestCompileConfigOmitsRevokedTunnelCandidates(t *testing.T) {
	h := newControllerHarness(t, "127.0.0.1:0")

	entry, _ := createNode(t, h.server, "entry-revocation")
	exitA, _ := createNode(t, h.server, "exit-a-revocation")
	exitB, _ := createNode(t, h.server, "exit-b-revocation")
	for _, node := range []model.Node{entry, exitA, exitB} {
		node.PublicHost = node.Name + ".example.com"
		node.PortMin = 12000
		node.PortMax = 12010
		if err := h.store.UpdateNode(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}

	tunnel := upsertTunnel(t, h.server, tunnelRequest{
		ID:        "tun_revocation",
		Name:      "revocation",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Enabled:   boolPtr(true),
		Stages: []model.TunnelStage{
			{ID: "stage_entry", Nodes: []model.TunnelStageNode{{ID: "node_entry", NodeID: entry.ID, Weight: 1}}},
			{ID: "stage_exit", Nodes: []model.TunnelStageNode{
				{ID: "node_exit_a", NodeID: exitA.ID, Weight: 1},
				{ID: "node_exit_b", NodeID: exitB.ID, Weight: 1},
			}},
		},
	})
	if err := h.store.RevokeNode(context.Background(), exitB.ID); err != nil {
		t.Fatal(err)
	}

	signed, err := h.server.compileConfig(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(signed.Config.Tunnels) != 1 {
		t.Fatalf("entry config tunnels = %d, want 1", len(signed.Config.Tunnels))
	}
	var exitNodes []model.TunnelRuntimeNode
	for _, stage := range signed.Config.Tunnels[0].Stages {
		if stage.Role == model.TunnelStageExit {
			exitNodes = stage.Nodes
		}
	}
	if len(exitNodes) != 1 || exitNodes[0].NodeID != exitA.ID {
		t.Fatalf("revoked exit candidate was not removed: %#v", exitNodes)
	}
	_ = tunnel
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

func upsertTunnel(t *testing.T, s *Server, reqBody tunnelRequest) model.Tunnel {
	t.Helper()
	payload, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleUpsertTunnel(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert tunnel failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp model.Tunnel
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func upsertForward(t *testing.T, s *Server, reqBody forwardRequest) model.Forward {
	t.Helper()
	payload, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/forwards", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleUpsertForward(rec, req, auth.Session{UserID: 1, Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert forward failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp model.Forward
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustRuntimeNodeSettings(t *testing.T, tunnel model.TunnelRuntime, stageIndex int, nodeID string) map[string]string {
	t.Helper()
	for _, stage := range tunnel.Stages {
		if stage.Index != stageIndex {
			continue
		}
		for _, stageNode := range stage.Nodes {
			if stageNode.NodeID == nodeID {
				return stageNode.Settings
			}
		}
	}
	t.Fatalf("node %s not found in tunnel %s stage %d", nodeID, tunnel.ID, stageIndex)
	return nil
}

func assertRedactedTunnelSettings(t *testing.T, settings map[string]string) {
	t.Helper()
	for _, key := range []string{"secret", "server_key", "client_key", "server_cert", "client_cert", "ca_cert"} {
		if settings[key] != "" {
			t.Fatalf("setting %s leaked as %q", key, settings[key])
		}
	}
}

func directTunnelRequest(id, entryNode string) tunnelRequest {
	return tunnelRequest{
		ID:        id,
		Name:      id,
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		EntryNode: entryNode,
		Enabled:   boolPtr(true),
	}
}

func chainTunnelRequest(id, entryNode string, middleNodes []string, exitNode string, listens []string) tunnelRequest {
	nodeIDs := append([]string{entryNode}, middleNodes...)
	nodeIDs = append(nodeIDs, exitNode)
	stages := make([]model.TunnelStage, 0, len(nodeIDs))
	listenIndex := 0
	for i, nodeID := range nodeIDs {
		stageID := "stage_" + id + "_" + nodeID
		stage := model.TunnelStage{
			ID:       stageID,
			TunnelID: id,
			Index:    i,
			Role:     roleForStage(model.TunnelChain, i, len(nodeIDs)),
			Strategy: "single",
			Nodes: []model.TunnelStageNode{{
				ID:       "stage_node_" + id + "_" + nodeID,
				TunnelID: id,
				StageID:  stageID,
				NodeID:   nodeID,
				Weight:   1,
			}},
		}
		if i > 0 {
			stage.Nodes[0].ListenAddr = listens[listenIndex]
			stage.Nodes[0].PublicAddr = listens[listenIndex]
			listenIndex++
		}
		stages = append(stages, stage)
	}
	return tunnelRequest{
		ID:        id,
		Name:      id,
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Enabled:   boolPtr(true),
		Stages:    stages,
	}
}

func boolPtr(v bool) *bool {
	return &v
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
		currentNode, err := st.GetNode(context.Background(), nodeID)
		if err == nil && currentNode.Status == model.NodeOnline {
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
				defer closeTestConn(c)
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func tcpReplyServer(t *testing.T, reply string) (string, func()) {
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
				defer closeTestConn(c)
				buf := make([]byte, 32)
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte(reply))
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func dualProtoEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := tcpLn.Addr().(*net.TCPAddr).Port
	udpConn, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		_ = tcpLn.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := tcpLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer closeTestConn(c)
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, remote, err := udpConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = udpConn.WriteTo(buf[:n], remote)
		}
	}()
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), func() {
		_ = tcpLn.Close()
		_ = udpConn.Close()
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

func udpStageResponder(t *testing.T, addr, prefix string) func() {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
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
			go func(conn net.Conn) {
				defer closeTestConn(conn)
				hello, err := protocol.ReadHello(conn)
				if err != nil || hello.Network != "udp" {
					return
				}
				frame, err := protocol.ReadUDPDatagramFrame(conn)
				if err != nil {
					return
				}
				response := protocol.UDPDatagramFrame{
					ForwardID: frame.ForwardID,
					SessionID: frame.SessionID,
					Payload:   append([]byte(prefix), frame.Payload...),
				}
				_ = protocol.WriteUDPDatagramFrame(conn, response)
			}(conn)
		}
	}()
	return func() {
		_ = ln.Close()
		<-done
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	portCursorMu.Lock()
	defer portCursorMu.Unlock()
	addr := freeTCPAddrFromCursor(&nextTCPTestPort)
	if addr != "" {
		return addr
	}
	t.Fatal("no free TCP port available in node range")
	return ""
}

func freeUDPAddr(t *testing.T) string {
	t.Helper()
	portCursorMu.Lock()
	defer portCursorMu.Unlock()
	addr := freeUDPAddrFromCursor(&nextUDPTestPort)
	if addr != "" {
		return addr
	}
	t.Fatal("no free UDP port available in node range")
	return ""
}

func freeDualProtoAddr(t *testing.T) string {
	t.Helper()
	portCursorMu.Lock()
	defer portCursorMu.Unlock()
	addr := freeDualProtoAddrFromCursor(&nextDualTestPort)
	if addr != "" {
		return addr
	}
	t.Fatal("no free TCP/UDP shared port available in node range")
	return ""
}

func freeTCPAddrFromCursor(cursor *int) string {
	start := *cursor
	for _, port := range append(rangePorts(start, 65535), rangePorts(10000, start-1)...) {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		_ = ln.Close()
		*cursor = port + 1
		if *cursor > 65535 {
			*cursor = 10000
		}
		return addr
	}
	return ""
}

func freeDualProtoAddrFromCursor(cursor *int) string {
	start := *cursor
	for _, port := range append(rangePorts(start, 65535), rangePorts(10000, start-1)...) {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		udpConn, err := net.ListenPacket("udp", addr)
		if err != nil {
			_ = ln.Close()
			continue
		}
		_ = ln.Close()
		_ = udpConn.Close()
		*cursor = port + 1
		if *cursor > 65535 {
			*cursor = 10000
		}
		return addr
	}
	return ""
}

func freeUDPAddrFromCursor(cursor *int) string {
	start := *cursor
	for _, port := range append(rangePorts(start, 65535), rangePorts(10000, start-1)...) {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			continue
		}
		_ = conn.Close()
		*cursor = port + 1
		if *cursor > 65535 {
			*cursor = 10000
		}
		return addr
	}
	return ""
}

func rangePorts(start, end int) []int {
	if end < start {
		return nil
	}
	ports := make([]int, 0, end-start+1)
	for port := start; port <= end; port++ {
		ports = append(ports, port)
	}
	return ports
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
	defer closeTestConn(conn)
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

func assertTCPResponse(t *testing.T, addr, payload, want string) {
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
	defer closeTestConn(conn)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != want {
		t.Fatalf("got %q, want %q", string(buf), want)
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
	defer closeTestConn(conn)
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

type controllerUDPClient struct {
	conn *net.UDPConn
}

func newControllerUDPClient(t *testing.T, addr string) controllerUDPClient {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	return controllerUDPClient{conn: conn}
}

func (c controllerUDPClient) close() {
	_ = c.conn.Close()
}

func closeTestConn(conn net.Conn) {
	_ = conn.Close()
}

func (c controllerUDPClient) roundTrip(t *testing.T, payload string) string {
	t.Helper()
	_ = c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func (c controllerUDPClient) roundTripEventually(t *testing.T, payload, wantPrefix string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_ = c.conn.SetDeadline(time.Now().Add(250 * time.Millisecond))
		if _, err := c.conn.Write([]byte(payload)); err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		buf := make([]byte, 1024)
		n, err := c.conn.Read(buf)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		got := string(buf[:n])
		if !strings.HasPrefix(got, wantPrefix) {
			t.Fatalf("udp response = %q, want prefix %q", got, wantPrefix)
		}
		return got
	}
	t.Fatalf("udp response never arrived; last error: %v", lastErr)
	return ""
}

func assertTCPDialEventuallyFails(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected dial to %s to fail", addr)
}
