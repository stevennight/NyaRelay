package relay

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
)

type udpObservedPacket struct {
	payload []byte
	remote  *net.UDPAddr
}

func startObservedUDPServer(t *testing.T, handler func(*net.UDPConn, []byte, *net.UDPAddr)) (string, <-chan udpObservedPacket, func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	packets := make(chan udpObservedPacket, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65507)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			payload := append([]byte(nil), buf[:n]...)
			remoteCopy := *remote
			packets <- udpObservedPacket{payload: payload, remote: &remoteCopy}
			handler(conn, payload, &remoteCopy)
		}
	}()
	var closeOnce sync.Once
	closeServer := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
			<-done
		})
	}
	return conn.LocalAddr().String(), packets, closeServer
}

func udpResponseHandler(conn *net.UDPConn, payload []byte, remote *net.UDPAddr) {
	switch string(payload) {
	case "silent":
		return
	case "delayed":
		remoteCopy := *remote
		time.AfterFunc(50*time.Millisecond, func() {
			_, _ = conn.WriteToUDP([]byte("delayed-response"), &remoteCopy)
		})
	case "multi":
		_, _ = conn.WriteToUDP([]byte("response-one"), remote)
		_, _ = conn.WriteToUDP([]byte("response-two"), remote)
	default:
		_, _ = conn.WriteToUDP(payload, remote)
	}
}

func waitObservedPacket(t *testing.T, packets <-chan udpObservedPacket, want string) udpObservedPacket {
	t.Helper()
	select {
	case packet := <-packets:
		if string(packet.payload) != want {
			t.Fatalf("target payload = %q, want %q", packet.payload, want)
		}
		return packet
	case <-time.After(2 * time.Second):
		t.Fatalf("target did not receive %q", want)
		return udpObservedPacket{}
	}
}

func writeUDPTestPayload(t *testing.T, client udpTestClient, payload string) {
	t.Helper()
	if _, err := client.conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
}

func readUDPTestResponse(t *testing.T, client udpTestClient) string {
	t.Helper()
	_ = client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65507)
	n, err := client.conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func stopRelayService(service *Service) {
	service.mu.Lock()
	service.stopRuntimeLocked()
	service.mu.Unlock()
}

func TestUDPDirectSessionReusesSocketAndSupportsAsyncResponses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	targetAddr, packets, closeTarget := startObservedUDPServer(t, udpResponseHandler)
	defer closeTarget()

	listenAddr := freeUDPAddr(t)
	service := New(testLogger(), "node_entry")
	defer stopRelayService(service)
	if err := service.Apply(ctx, model.RelayConfig{
		NodeID:   "node_entry",
		Revision: 1,
		Tunnels:  []model.TunnelRuntime{directTunnel()},
		Forwards: []model.ForwardRuntime{{
			ID:        "fwd_udp_session_direct",
			Name:      "udp-session-direct",
			TunnelID:  "tun_direct",
			Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP},
			Listen:    listenAddr,
			Target:    targetAddr,
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	client := newUDPTestClient(t, listenAddr)
	defer client.close()
	writeUDPTestPayload(t, client, "silent")
	silent := waitObservedPacket(t, packets, "silent")
	writeUDPTestPayload(t, client, "echo")
	if got := readUDPTestResponse(t, client); got != "echo" {
		t.Fatalf("echo response = %q", got)
	}
	echo := waitObservedPacket(t, packets, "echo")
	if silent.remote.Port != echo.remote.Port {
		t.Fatalf("direct UDP source port changed: first=%d second=%d", silent.remote.Port, echo.remote.Port)
	}

	writeUDPTestPayload(t, client, "delayed")
	if got := readUDPTestResponse(t, client); got != "delayed-response" {
		t.Fatalf("delayed response = %q", got)
	}
	_ = waitObservedPacket(t, packets, "delayed")

	writeUDPTestPayload(t, client, "multi")
	responses := map[string]bool{readUDPTestResponse(t, client): true, readUDPTestResponse(t, client): true}
	if !responses["response-one"] || !responses["response-two"] {
		t.Fatalf("multiple responses = %v", responses)
	}
	_ = waitObservedPacket(t, packets, "multi")
}

func TestUDPChainSessionReusesSocketAndSupportsAsyncResponses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	targetAddr, packets, closeTarget := startObservedUDPServer(t, udpResponseHandler)
	defer closeTarget()

	entryListen := freeUDPAddr(t)
	stageListen := freeTCPAddr(t)
	tunnel := twoNodeTunnel(model.TunnelTransportDirect, stageListen, map[string]string{"secret": "test-secret"})
	exitService := New(testLogger(), "node_exit")
	exitForward := chainForward(entryListen, targetAddr, model.ForwardProtocolUDP)
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{exitForward}}); err != nil {
		t.Fatal(err)
	}
	defer stopRelayService(exitService)

	entryService := New(testLogger(), "node_entry")
	entryForward := chainForward(entryListen, "", model.ForwardProtocolUDP)
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{entryForward}}); err != nil {
		t.Fatal(err)
	}
	defer stopRelayService(entryService)

	client := newUDPTestClient(t, entryListen)
	defer client.close()
	writeUDPTestPayload(t, client, "silent")
	silent := waitObservedPacket(t, packets, "silent")
	writeUDPTestPayload(t, client, "echo")
	if got := readUDPTestResponse(t, client); got != "echo" {
		t.Fatalf("chain echo response = %q", got)
	}
	echo := waitObservedPacket(t, packets, "echo")
	if silent.remote.Port != echo.remote.Port {
		t.Fatalf("chain UDP source port changed: first=%d second=%d", silent.remote.Port, echo.remote.Port)
	}

	writeUDPTestPayload(t, client, "delayed")
	if got := readUDPTestResponse(t, client); got != "delayed-response" {
		t.Fatalf("chain delayed response = %q", got)
	}
	_ = waitObservedPacket(t, packets, "delayed")

	writeUDPTestPayload(t, client, "multi")
	responses := map[string]bool{readUDPTestResponse(t, client): true, readUDPTestResponse(t, client): true}
	if !responses["response-one"] || !responses["response-two"] {
		t.Fatalf("chain multiple responses = %v", responses)
	}
	_ = waitObservedPacket(t, packets, "multi")
}

func TestWSTLSUDPChainSessionReusesSocket(t *testing.T) {
	testTLSUDPChainSession(t, model.TunnelTransportWSTLS, "ws-udp-session", true)
}

func TestTLSUDPChainSessionReusesSocket(t *testing.T) {
	testTLSUDPChainSession(t, model.TunnelTransportTLS, "tls-udp-session", false)
}

func TestMTLSUDPChainSessionReusesSocket(t *testing.T) {
	testTLSUDPChainSession(t, model.TunnelTransportMTLS, "mtls-udp-session", true)
}

func testTLSUDPChainSession(t *testing.T, transport model.TunnelTransport, certificateName string, includeClientCertificate bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	targetAddr, packets, closeTarget := startObservedUDPServer(t, udpResponseHandler)
	defer closeTarget()

	entryListen := freeUDPAddr(t)
	stageListen := freeTCPAddr(t)
	certs, err := sharedcrypto.GenerateTunnelCertificates(certificateName, "127.0.0.1")
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
	if includeClientCertificate {
		settings["client_cert"] = certs.ClientCert
		settings["client_key"] = certs.ClientKey
	}
	tunnel := twoNodeTunnel(transport, stageListen, settings)
	exitService := New(testLogger(), "node_exit")
	exitForward := chainForward(entryListen, targetAddr, model.ForwardProtocolUDP)
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{exitForward}}); err != nil {
		t.Fatal(err)
	}
	defer stopRelayService(exitService)
	entryService := New(testLogger(), "node_entry")
	entryForward := chainForward(entryListen, "", model.ForwardProtocolUDP)
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Tunnels: []model.TunnelRuntime{tunnel}, Forwards: []model.ForwardRuntime{entryForward}}); err != nil {
		t.Fatal(err)
	}
	defer stopRelayService(entryService)

	client := newUDPTestClient(t, entryListen)
	defer client.close()
	writeUDPTestPayload(t, client, "silent")
	silent := waitObservedPacket(t, packets, "silent")
	writeUDPTestPayload(t, client, "echo")
	if got := readUDPTestResponse(t, client); got != "echo" {
		t.Fatalf("%s udp echo response = %q", transport, got)
	}
	echo := waitObservedPacket(t, packets, "echo")
	if silent.remote.Port != echo.remote.Port {
		t.Fatalf("%s UDP source port changed: first=%d second=%d", transport, silent.remote.Port, echo.remote.Port)
	}

	writeUDPTestPayload(t, client, "delayed")
	if got := readUDPTestResponse(t, client); got != "delayed-response" {
		t.Fatalf("%s udp delayed response = %q", transport, got)
	}
	_ = waitObservedPacket(t, packets, "delayed")

	writeUDPTestPayload(t, client, "multi")
	responses := map[string]bool{readUDPTestResponse(t, client): true, readUDPTestResponse(t, client): true}
	if !responses["response-one"] || !responses["response-two"] {
		t.Fatalf("%s udp multiple responses = %v", transport, responses)
	}
	_ = waitObservedPacket(t, packets, "multi")
}
