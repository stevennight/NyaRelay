package nodehub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Hub struct {
	mu       sync.Mutex
	revision int64
	watchers map[string]chan int64
	sockets  map[string]*socketConn
}

func New() *Hub {
	return &Hub{watchers: make(map[string]chan int64), sockets: make(map[string]*socketConn)}
}

func (h *Hub) SetRevision(revision int64) {
	h.mu.Lock()
	h.revision = revision
	for id, ch := range h.watchers {
		select {
		case ch <- revision:
		default:
		}
		delete(h.watchers, id)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *Hub) Revision() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revision
}

func (h *Hub) Wait(ctx context.Context, nodeID string, knownRevision int64, maxWait time.Duration) int64 {
	h.mu.Lock()
	current := h.revision
	if current > knownRevision {
		h.mu.Unlock()
		return current
	}
	ch := make(chan int64, 1)
	h.watchers[nodeID] = ch
	h.mu.Unlock()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case revision := <-ch:
		return revision
	case <-timer.C:
		return h.Revision()
	case <-ctx.Done():
		return h.Revision()
	}
}

func (h *Hub) RegisterSocket(nodeID string, conn *websocket.Conn) {
	h.mu.Lock()
	old := h.sockets[nodeID]
	h.sockets[nodeID] = &socketConn{conn: conn}
	h.mu.Unlock()
	if old != nil {
		_ = old.Close(websocket.StatusNormalClosure, "replaced")
	}
}

func (h *Hub) UnregisterSocket(nodeID string, conn *websocket.Conn) {
	h.mu.Lock()
	if current, ok := h.sockets[nodeID]; ok && current.conn == conn {
		delete(h.sockets, nodeID)
	}
	h.mu.Unlock()
}

func (h *Hub) NodeIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.sockets))
	for id := range h.sockets {
		out = append(out, id)
	}
	return out
}

func (h *Hub) Send(nodeID string, value any) error {
	h.mu.Lock()
	sock := h.sockets[nodeID]
	h.mu.Unlock()
	if sock == nil {
		return errors.New("node is not connected")
	}
	return sock.Send(value)
}

type socketConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *socketConn) Send(value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return wsjson.Write(context.Background(), c.conn, value)
}

func (c *socketConn) Close(code websocket.StatusCode, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close(code, reason)
}
