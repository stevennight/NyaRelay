package nodehub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestUnregisterSocketDoesNotRemoveReplacement(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		<-done
		_ = conn.Close(websocket.StatusNormalClosure, "done")
	}))
	defer func() {
		close(done)
		server.Close()
	}()

	dial := func() *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(server.URL, "http")
		conn, _, err := websocket.Dial(context.Background(), url, nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}

	first := dial()
	second := dial()
	hub := New()
	hub.RegisterSocket("node-1", first)
	hub.RegisterSocket("node-1", second)

	if hub.UnregisterSocket("node-1", first) {
		t.Fatal("replacement connection was treated as current")
	}
	if got := hub.NodeIDs(); len(got) != 1 || got[0] != "node-1" {
		t.Fatalf("current socket was removed by replacement: %v", got)
	}
	if !hub.UnregisterSocket("node-1", second) {
		t.Fatal("current connection was not unregistered")
	}
	if got := hub.NodeIDs(); len(got) != 0 {
		t.Fatalf("socket remained registered after current connection closed: %v", got)
	}
}
