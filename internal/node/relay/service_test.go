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
	service := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	err := service.Apply(ctx, model.RelayConfig{
		NodeID:   "node_entry",
		Revision: 1,
		Routes: []model.Route{{
			ID:        "route_1",
			Name:      "single",
			Protocol:  model.ProtocolTCP,
			EntryNode: "node_entry",
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

func TestTwoNodeTCPDirectLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	linkListen := freeTCPAddr(t)
	link := model.Link{
		ID:         "link_1",
		Name:       "entry-to-exit",
		Type:       model.LinkDirect,
		FromNode:   "node_entry",
		ToNode:     "node_exit",
		BindAddr:   linkListen,
		PublicAddr: linkListen,
		Enabled:    true,
		Settings:   map[string]string{"secret": "test-secret"},
	}
	route := model.Route{
		ID:        "route_1",
		Name:      "multi",
		Protocol:  model.ProtocolTCP,
		EntryNode: "node_entry",
		Listen:    entryListen,
		Hops:      []model.RouteHop{{LinkID: "link_1"}},
		Target:    targetAddr,
		Enabled:   true,
	}

	exitService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Links: []model.Link{link}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Links: []model.Link{link}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-hop")
}

func TestTwoNodeTCPMTLSLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	linkListen := freeTCPAddr(t)
	certs, err := sharedcrypto.GenerateLinkCertificates("test-link", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	link := model.Link{
		ID:         "link_mtls",
		Name:       "entry-to-exit-mtls",
		Type:       model.LinkMTLS,
		FromNode:   "node_entry",
		ToNode:     "node_exit",
		BindAddr:   linkListen,
		PublicAddr: linkListen,
		ServerName: "127.0.0.1",
		Enabled:    true,
		Settings: map[string]string{
			"secret":      "test-secret",
			"ca_cert":     certs.CACert,
			"server_cert": certs.ServerCert,
			"server_key":  certs.ServerKey,
			"client_cert": certs.ClientCert,
			"client_key":  certs.ClientKey,
		},
	}
	route := model.Route{
		ID:        "route_1",
		Name:      "mtls",
		Protocol:  model.ProtocolTCP,
		EntryNode: "node_entry",
		Listen:    entryListen,
		Hops:      []model.RouteHop{{LinkID: "link_mtls"}},
		Target:    targetAddr,
		Enabled:   true,
	}

	exitLink := link
	exitLink.Settings = map[string]string{
		"secret":      "test-secret",
		"ca_cert":     certs.CACert,
		"server_cert": certs.ServerCert,
		"server_key":  certs.ServerKey,
	}
	entryLink := link
	entryLink.Settings = map[string]string{
		"secret":      "test-secret",
		"ca_cert":     certs.CACert,
		"client_cert": certs.ClientCert,
		"client_key":  certs.ClientKey,
	}

	exitService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Links: []model.Link{exitLink}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Links: []model.Link{entryLink}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-mtls")
}

func TestThreeNodeTCPDirectMultiHop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	linkABListen := freeTCPAddr(t)
	linkBCListen := freeTCPAddr(t)

	linkAB := model.Link{
		ID:         "link_ab",
		Name:       "entry-to-mid",
		Type:       model.LinkDirect,
		FromNode:   "node_entry",
		ToNode:     "node_mid",
		BindAddr:   linkABListen,
		PublicAddr: linkABListen,
		Enabled:    true,
		Settings:   map[string]string{"secret": "test-secret"},
	}
	linkBC := model.Link{
		ID:         "link_bc",
		Name:       "mid-to-exit",
		Type:       model.LinkDirect,
		FromNode:   "node_mid",
		ToNode:     "node_exit",
		BindAddr:   linkBCListen,
		PublicAddr: linkBCListen,
		Enabled:    true,
		Settings:   map[string]string{"secret": "test-secret"},
	}
	route := model.Route{
		ID:        "route_3",
		Name:      "three-hop",
		Protocol:  model.ProtocolTCP,
		EntryNode: "node_entry",
		Listen:    entryListen,
		Hops:      []model.RouteHop{{LinkID: "link_ab"}, {LinkID: "link_bc"}},
		Target:    targetAddr,
		Enabled:   true,
	}

	exitService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Links: []model.Link{linkBC}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}
	midService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_mid")
	if err := midService.Apply(ctx, model.RelayConfig{NodeID: "node_mid", Revision: 1, Links: []model.Link{linkAB, linkBC}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Links: []model.Link{linkAB}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}

	assertTCPRoundTrip(t, entryListen, "nya-three-hop")
}

func TestWSTLSLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := tcpEchoServer(t)
	defer closeTarget()

	entryListen := freeTCPAddr(t)
	linkListen := freeTCPAddr(t)
	certs, err := sharedcrypto.GenerateLinkCertificates("ws-link", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	link := model.Link{
		ID:         "link_ws",
		Name:       "entry-to-exit-ws",
		Type:       model.LinkWSTLS,
		FromNode:   "node_entry",
		ToNode:     "node_exit",
		BindAddr:   linkListen,
		PublicAddr: linkListen,
		ServerName: "127.0.0.1",
		Enabled:    true,
		Settings: map[string]string{
			"secret":      "test-secret",
			"ca_cert":     certs.CACert,
			"server_cert": certs.ServerCert,
			"server_key":  certs.ServerKey,
		},
	}
	route := model.Route{
		ID:        "route_ws",
		Name:      "ws-hop",
		Protocol:  model.ProtocolTCP,
		EntryNode: "node_entry",
		Listen:    entryListen,
		Hops:      []model.RouteHop{{LinkID: "link_ws"}},
		Target:    targetAddr,
		Enabled:   true,
	}

	exitService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Links: []model.Link{link}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}
	entryLink := link
	entryLink.Settings = map[string]string{
		"secret":  "test-secret",
		"ca_cert": certs.CACert,
	}
	entryService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Links: []model.Link{entryLink}, Routes: []model.Route{route}}); err != nil {
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
	service := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	if err := service.Apply(ctx, model.RelayConfig{
		NodeID:   "node_entry",
		Revision: 1,
		Routes: []model.Route{{
			ID:        "route_udp",
			Name:      "single-udp",
			Protocol:  model.ProtocolUDP,
			EntryNode: "node_entry",
			Listen:    listenAddr,
			Target:    targetAddr,
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	assertUDPRoundTrip(t, listenAddr, "nya-udp-single")
}

func TestTwoNodeUDPDirectLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetAddr, closeTarget := udpEchoServer(t)
	defer closeTarget()

	entryListen := freeUDPAddr(t)
	linkListen := freeUDPAddr(t)
	link := model.Link{
		ID:         "link_udp",
		Name:       "entry-to-exit-udp",
		Type:       model.LinkDirect,
		FromNode:   "node_entry",
		ToNode:     "node_exit",
		BindAddr:   linkListen,
		PublicAddr: linkListen,
		Enabled:    true,
		Settings:   map[string]string{"secret": "test-secret"},
	}
	route := model.Route{
		ID:        "route_udp",
		Name:      "udp-hop",
		Protocol:  model.ProtocolUDP,
		EntryNode: "node_entry",
		Listen:    entryListen,
		Hops:      []model.RouteHop{{LinkID: "link_udp"}},
		Target:    targetAddr,
		Enabled:   true,
	}

	exitService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_exit")
	if err := exitService.Apply(ctx, model.RelayConfig{NodeID: "node_exit", Revision: 1, Links: []model.Link{link}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}
	entryService := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "node_entry")
	if err := entryService.Apply(ctx, model.RelayConfig{NodeID: "node_entry", Revision: 1, Links: []model.Link{link}, Routes: []model.Route{route}}); err != nil {
		t.Fatal(err)
	}

	assertUDPRoundTrip(t, entryListen, "nya-udp-hop")
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
