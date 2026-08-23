package nodehub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	hub.RegisterSocket("node-1", first, false)
	hub.RegisterSocket("node-1", second, true)

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

func TestSupportsLongConfigLeaseTracksSocket(t *testing.T) {
	hub := New()
	hub.RegisterSocket("legacy", nil, false)
	hub.RegisterSocket("modern", nil, true)

	if hub.SupportsLongConfigLease("legacy") {
		t.Fatal("legacy socket advertised long config lease support")
	}
	if !hub.SupportsLongConfigLease("modern") {
		t.Fatal("modern socket did not advertise long config lease support")
	}
	if hub.SupportsLongConfigLease("missing") {
		t.Fatal("missing socket advertised long config lease support")
	}
}

func TestWaitCleansCanceledWatcherAndNotifiesAllWaiters(t *testing.T) {
	hub := New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hub.Wait(ctx, "node-1", 0, time.Minute)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled watcher did not return")
	}

	hub.mu.Lock()
	if len(hub.watchers) != 0 {
		hub.mu.Unlock()
		t.Fatalf("canceled watcher remained: %#v", hub.watchers)
	}
	hub.mu.Unlock()

	firstDone := make(chan int64, 1)
	secondDone := make(chan int64, 1)
	go func() { firstDone <- hub.Wait(context.Background(), "node-1", 0, time.Minute) }()
	go func() { secondDone <- hub.Wait(context.Background(), "node-1", 0, time.Minute) }()
	time.Sleep(10 * time.Millisecond)
	hub.SetRevision(4)
	select {
	case got := <-firstDone:
		if got != 4 {
			t.Fatalf("first waiter revision = %d, want 4", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first waiter was not notified")
	}
	select {
	case got := <-secondDone:
		if got != 4 {
			t.Fatalf("second waiter revision = %d, want 4", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second waiter was not notified")
	}
}
