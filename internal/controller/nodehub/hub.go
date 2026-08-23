package nodehub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const maxWatchersPerNode = 4

var ErrNotConnected = errors.New("node is not connected")

type Hub struct {
	mu       sync.Mutex
	revision int64
	watchers map[string]map[chan int64]struct{}
	sockets  map[string]*socketConn
}

func New() *Hub {
	return &Hub{watchers: make(map[string]map[chan int64]struct{}), sockets: make(map[string]*socketConn)}
}

func (h *Hub) SetRevision(revision int64) {
	h.mu.Lock()
	h.revision = revision
	for id, nodeWatchers := range h.watchers {
		for ch := range nodeWatchers {
			select {
			case ch <- revision:
			default:
			}
			close(ch)
		}
		delete(h.watchers, id)
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
	if len(h.watchers[nodeID]) >= maxWatchersPerNode {
		h.mu.Unlock()
		return current
	}
	ch := make(chan int64, 1)
	if h.watchers[nodeID] == nil {
		h.watchers[nodeID] = make(map[chan int64]struct{})
	}
	h.watchers[nodeID][ch] = struct{}{}
	h.mu.Unlock()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case revision := <-ch:
		h.removeWatcher(nodeID, ch)
		return revision
	case <-timer.C:
		h.removeWatcher(nodeID, ch)
		return h.Revision()
	case <-ctx.Done():
		h.removeWatcher(nodeID, ch)
		return h.Revision()
	}
}

func (h *Hub) removeWatcher(nodeID string, ch chan int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	nodeWatchers := h.watchers[nodeID]
	if nodeWatchers == nil {
		return
	}
	delete(nodeWatchers, ch)
	if len(nodeWatchers) == 0 {
		delete(h.watchers, nodeID)
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

func (h *Hub) UnregisterSocket(nodeID string, conn *websocket.Conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, ok := h.sockets[nodeID]; ok && current.conn == conn {
		delete(h.sockets, nodeID)
		return true
	}
	return false
}

func (h *Hub) Close(nodeID string, code websocket.StatusCode, reason string) bool {
	h.mu.Lock()
	sock := h.sockets[nodeID]
	h.mu.Unlock()
	if sock == nil {
		return false
	}
	_ = sock.Close(code, reason)
	return true
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
	return h.SendContext(context.Background(), nodeID, value)
}

func (h *Hub) SendContext(ctx context.Context, nodeID string, value any) error {
	h.mu.Lock()
	sock := h.sockets[nodeID]
	h.mu.Unlock()
	if sock == nil {
		return ErrNotConnected
	}
	return sock.Send(ctx, value)
}

type socketConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *socketConn) Send(ctx context.Context, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, c.conn, value)
}

func (c *socketConn) Close(code websocket.StatusCode, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close(code, reason)
}
