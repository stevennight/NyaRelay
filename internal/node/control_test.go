package node

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
