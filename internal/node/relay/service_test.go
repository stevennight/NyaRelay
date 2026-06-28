package relay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
)

func TestSingleNodeTCPDirectOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	listenAddr := freeTCPAddr(t)
	service := New(testLogger(), "node_entry")
	err := service.Apply(ctx, model.RelayConfig{
		NodeID:   "node_entry",
		Revision: 1,
		Tunnels:  []model.TunnelRuntime{directTunnel()},
		Forwards: []model.ForwardRuntime{{
			ID:        "fwd_1",
			Name:      "single",
			TunnelID:  "tun_direct",
			Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
			Listen:    listenAddr,
			Target:    targetAddr,
			Enabled:   true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, listenAddr, "nya-single")
}

func TestTwoNodeTCPChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	stageListen := freeTCPAddr(t)
	tunnel := twoNodeTunnel(model.TunnelTransportDirect, stageListen, map[string]string{"secret": "test-secret"})

	exitService := New(testLogger(), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, targetAddr, model.ForwardProtocolTCP)}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(testLogger(), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, "", model.ForwardProtocolTCP)}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-hop")
}

func TestTwoNodeTCPMTLSChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	stageListen := freeTCPAddr(t)
	certs, err := sharedcrypto.GenerateTunnelCertificates("test-tunnel", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{
		"secret":      "test-secret",
		"server_name": "127.0.0.1",
		"ca_cert":     certs.CACert,
		"server_cert": certs.ServerCert,
		"server_key":  certs.ServerKey,
		"client_cert": certs.ClientCert,
		"client_key":  certs.ClientKey,
	}
	tunnel := twoNodeTunnel(model.TunnelTransportMTLS, stageListen, settings)

	exitService := New(testLogger(), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, targetAddr, model.ForwardProtocolTCP)}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(testLogger(), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, "", model.ForwardProtocolTCP)}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-mtls")
}

func TestThreeNodeTCPDirectChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	midListen := freeTCPAddr(t)
	exitListen := freeTCPAddr(t)
	tunnel := model.TunnelRuntime{
		ID:        "tun_three",
		Name:      "three",
		Type:      model.TunnelChain,
		Transport: model.TunnelTransportDirect,
		Stages: []model.TunnelRuntimeStage{
			runtimeStage(0, model.TunnelStageEntry, runtimeNode("node_entry", "", "", nil)),
			runtimeStage(1, model.TunnelStageMiddle, runtimeNode("node_mid", midListen, midListen, map[string]string{"secret": "secret-ab"})),
			runtimeStage(2, model.TunnelStageExit, runtimeNode("node_exit", exitListen, exitListen, map[string]string{"secret": "secret-bc"})),
		},
	}

	exitService := New(testLogger(), "node_exit")
	exitForward := chainForward(entryListen, targetAddr, model.ForwardProtocolTCP)
	exitForward.TunnelID = tunnel.ID
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{exitForward}}); err != nil {
		t.Fatal(err)
	}
	midService := New(testLogger(), "node_mid")
	midForward := chainForward(entryListen, "", model.ForwardProtocolTCP)
	midForward.TunnelID = tunnel.ID
	if err := midService.Apply(ctx, model.RelayConfig{NodeID: "node_mid", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{midForward}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(testLogger(), "node_entry")
	entryForward := chainForward(entryListen, "", model.ForwardProtocolTCP)
	entryForward.TunnelID = tunnel.ID
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{entryForward}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-three-hop")
}

func TestWSTLSChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	stageListen := freeTCPAddr(t)
	certs, err := sharedcrypto.GenerateTunnelCertificates("ws-tunnel", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{
		"secret":      "test-secret",
		"server_name": "127.0.0.1",
		"ca_cert":     certs.CACert,
		"server_cert": certs.ServerCert,
		"server_key":  certs.ServerKey,
	}
	tunnel := twoNodeTunnel(model.TunnelTransportWSTLS, stageListen, settings)

	exitService := New(testLogger(), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, targetAddr, model.ForwardProtocolTCP)}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(testLogger(), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, "", model.ForwardProtocolTCP)}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-ws-tls")
}

func TestSingleNodeUDPDirectOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := udpEchoServer(t)
	defer closeTarget()

	listenAddr := freeUDPAddr(t)
	service := New(testLogger(), "node_entry")
	if err := service.Apply(ctx, model.RelayConfig{
		NodeID:   "node_entry",
		Revision: 1,
		Tunnels:  []model.TunnelRuntime{directTunnel()},
		Forwards: []model.ForwardRuntime{{
			ID:        "fwd_udp",
			Name:      "single-udp",
			TunnelID:  "tun_direct",
			Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP},
			Listen:    listenAddr,
			Target:    targetAddr,
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	assertUDPRoundTrip(t, listenAddr, "nya-udp-single")
}

func TestTwoNodeUDPChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := udpEchoServer(t)
	defer closeTarget()

	entryListen := freeUDPAddr(t)
	stageListen := freeTCPAddr(t)
	tunnel := twoNodeTunnel(model.TunnelTransportDirect, stageListen, map[string]string{"secret": "test-secret"})

	exitService := New(testLogger(), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, targetAddr, model.ForwardProtocolUDP)}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(testLogger(), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{chainForward(entryListen, "", model.ForwardProtocolUDP)}}); err != nil {
		t.Fatal(err)
	}

	assertUDPRoundTrip(t, entryListen, "nya-udp-hop")
}

func directTunnel() model.TunnelRuntime {
	return model.TunnelRuntime{
		ID:        "tun_direct",
		Name:      "direct",
		Type:      model.TunnelDirect,
		Transport: model.TunnelTransportDirect,
		Stages: []model.TunnelRuntimeStage{
			runtimeStage(0, model.TunnelStageEntry, runtimeNode("node_entry", "", "", nil)),
		},
	}
}

func twoNodeTunnel(transport model.TunnelTransport, stageListen string, settings map[string]string) model.TunnelRuntime {
	return model.TunnelRuntime{
		ID:        "tun_chain",
		Name:      "chain",
		Type:      model.TunnelChain,
		Transport: transport,
		Stages: []model.TunnelRuntimeStage{
			runtimeStage(0, model.TunnelStageEntry, runtimeNode("node_entry", "", "", nil)),
			runtimeStage(1, model.TunnelStageExit, runtimeNode("node_exit", stageListen, stageListen, settings)),
		},
	}
}

func runtimeStage(index int, role model.TunnelStageRole, node model.TunnelRuntimeNode) model.TunnelRuntimeStage {
	return model.TunnelRuntimeStage{
		Index:    index,
		Role:     role,
		Strategy: "single",
		Nodes:    []model.TunnelRuntimeNode{node},
	}
}

func runtimeNode(nodeID, listen, public string, settings map[string]string) model.TunnelRuntimeNode {
	if settings == nil {
		settings = map[string]string{}
	}
	return model.TunnelRuntimeNode{
		NodeID:     nodeID,
		ListenAddr: listen,
		PublicAddr: public,
		Weight:     1,
		Settings:   settings,
	}
}

func chainForward(listen, target string, protocol model.ForwardProtocol) model.ForwardRuntime {
	return model.ForwardRuntime{
		ID:        "fwd_chain",
		Name:      "chain-forward",
		TunnelID:  "tun_chain",
		Protocols: []model.ForwardProtocol{protocol},
		Listen:    listen,
		Target:    target,
		Enabled:   true,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
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
	for i := 0; i < 30; i++ {
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
	conn, err := net.DialUDP("udp", nil, remote)
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

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
