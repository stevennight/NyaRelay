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
