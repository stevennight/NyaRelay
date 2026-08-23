package node

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nyarelay/internal/shared/model"
	sharedprotocol "nyarelay/internal/shared/protocol"
)

func TestControlLoopReconnectsWhenHeartbeatPingTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var connections atomic.Int32
	var reconnectOnce sync.Once
	reconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var hello sharedprotocol.ControlMessage
		if err := wsjson.Read(r.Context(), conn, &hello); err != nil {
			return
		}
		if connections.Add(1) >= 2 {
			reconnectOnce.Do(func() { close(reconnected) })
		}

		// Stop reading after hello. The client can still write TCP data, but
		// no pong is produced, so the application-level ping must force a reconnect.
		<-ctx.Done()
	}))
	defer server.Close()

	cfg := Config{
		ControllerURL: server.URL,
		NodeID:        "node-1",
		NodeToken:     "node-token",
	}
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		controlLoopWithOptions(
			ctx,
			newClient(cfg),
			cfg,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			func(context.Context, model.SignedConfig, string) error { return nil },
			controlLoopOptions{
				heartbeatInterval: 10 * time.Millisecond,
				writeTimeout:      50 * time.Millisecond,
				pingTimeout:       50 * time.Millisecond,
			},
		)
	}()

	select {
	case <-reconnected:
	case <-ctx.Done():
		t.Fatalf("control loop did not reconnect: %v", ctx.Err())
	}
	cancel()

	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("control loop did not stop")
	}
}

func TestControlLoopAcceptsConfigLargerThanDefaultWebSocketLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	configReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var hello sharedprotocol.ControlMessage
		if err := wsjson.Read(r.Context(), conn, &hello); err != nil {
			return
		}

		msg := sharedprotocol.ControlMessage{
			Type: "config",
			Config: &model.SignedConfig{
				Config: model.RelayConfig{
					Revision: 1,
					NodeID:   "node-1",
					Forwards: []model.ForwardRuntime{{Name: strings.Repeat("x", 40*1024)}},
				},
			},
		}
		if err := wsjson.Write(r.Context(), conn, msg); err != nil {
			return
		}
		<-ctx.Done()
	}))
	defer server.Close()

	cfg := Config{
		ControllerURL: server.URL,
		NodeID:        "node-1",
		NodeToken:     "node-token",
	}
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		controlLoopWithOptions(
			ctx,
			newClient(cfg),
			cfg,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			func(_ context.Context, signed model.SignedConfig, source string) error {
				if source != "ws" || len(signed.Config.Forwards) != 1 {
					t.Errorf("unexpected config apply: source=%q forwards=%d", source, len(signed.Config.Forwards))
					return nil
				}
				close(configReceived)
				return nil
			},
			controlLoopOptions{
				heartbeatInterval: time.Hour,
				writeTimeout:      50 * time.Millisecond,
				pingTimeout:       50 * time.Millisecond,
			},
		)
	}()

	select {
	case <-configReceived:
		cancel()
	case <-ctx.Done():
		t.Fatalf("control loop did not accept the large config: %v", ctx.Err())
	}

	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("control loop did not stop")
	}
}

func TestControlLoopRefreshesConfigBeforeLeaseExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	refreshRequested := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var hello sharedprotocol.ControlMessage
		if err := wsjson.Read(r.Context(), conn, &hello); err != nil {
			return
		}
		var refresh sharedprotocol.ControlMessage
		if err := wsjson.Read(r.Context(), conn, &refresh); err != nil {
			return
		}
		if refresh.Type != "pull_config" || refresh.NodeID != "node-1" {
			return
		}
		close(refreshRequested)
		_ = wsjson.Write(r.Context(), conn, sharedprotocol.ControlMessage{
			Type: "config",
			Config: &model.SignedConfig{Config: model.RelayConfig{
				Revision: 2,
				NodeID:   "node-1",
			}},
		})
		<-ctx.Done()
	}))
	defer server.Close()

	cfg := Config{
		ControllerURL: server.URL,
		NodeID:        "node-1",
		NodeToken:     "node-token",
	}
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		controlLoopWithOptions(
			ctx,
			newClient(cfg),
			cfg,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			func(_ context.Context, signed model.SignedConfig, source string) error {
				if source != "ws" || signed.Config.Revision != 2 {
					t.Errorf("unexpected refreshed config: source=%q revision=%d", source, signed.Config.Revision)
				}
				return nil
			},
			controlLoopOptions{
				heartbeatInterval:     time.Hour,
				configRefreshInterval: 10 * time.Millisecond,
				writeTimeout:          50 * time.Millisecond,
				pingTimeout:           50 * time.Millisecond,
			},
		)
	}()

	select {
	case <-refreshRequested:
		cancel()
	case <-ctx.Done():
		t.Fatalf("control loop did not request config refresh: %v", ctx.Err())
	}

	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("control loop did not stop")
	}
}

func TestControlFailureLogLevels(t *testing.T) {
	var warningOutput bytes.Buffer
	handlerOptions := &slog.HandlerOptions{Level: slog.LevelDebug}
	warningLog := slog.New(slog.NewTextHandler(&warningOutput, handlerOptions))
	logControlFailure(warningLog, context.Background(), "control failure", "error", errors.New("unavailable"))
	if !strings.Contains(warningOutput.String(), "level=WARN") {
		t.Fatalf("expected warning log, got %q", warningOutput.String())
	}

	var debugOutput bytes.Buffer
	debugLog := slog.New(slog.NewTextHandler(&debugOutput, handlerOptions))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logControlFailure(debugLog, ctx, "control shutdown", "error", context.Canceled)
	if !strings.Contains(debugOutput.String(), "level=DEBUG") {
		t.Fatalf("expected debug log during shutdown, got %q", debugOutput.String())
	}

	var closeOutput bytes.Buffer
	closeLog := slog.New(slog.NewTextHandler(&closeOutput, handlerOptions))
	logControlReadFailure(closeLog, context.Background(), websocket.CloseError{Code: websocket.StatusNormalClosure})
	if !strings.Contains(closeOutput.String(), "level=DEBUG") {
		t.Fatalf("expected debug log for normal close, got %q", closeOutput.String())
	}
}

func TestControllerURLRejectsUnencryptedRemoteEndpoint(t *testing.T) {
	if _, err := validateControllerURL("http://controller.example.com"); err == nil {
		t.Fatal("unencrypted remote controller URL must be rejected")
	}
	if _, err := validateControllerURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback HTTP controller URL should be allowed: %v", err)
	}
}

func TestNodeClientDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/api/node/config", http.StatusFound)
	}))
	defer server.Close()

	c := newClient(Config{ControllerURL: server.URL, NodeID: "node-1", NodeToken: "node-token"})
	if _, err := c.config(context.Background()); err == nil {
		t.Fatal("redirect response must not be followed")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("redirect target was contacted %d times", got)
	}
}
