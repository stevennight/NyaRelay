package metrics

import (
	"sync"
	"sync/atomic"

	"nyarelay/internal/shared/model"
)

type Counters struct {
	mu    sync.Mutex
	items map[string]*Counter
}

type Counter struct {
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64
	connections atomic.Int64
}

func New() *Counters {
	return &Counters{items: make(map[string]*Counter)}
}

func (c *Counters) Get(id string) *Counter {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.items[id]
	if item == nil {
		item = &Counter{}
		c.items[id] = item
	}
	return item
}

func (c *Counters) Snapshot() []model.TrafficStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.TrafficStat, 0, len(c.items))
	for id, item := range c.items {
		out = append(out, model.TrafficStat{
			ID:          id,
			BytesIn:     item.bytesIn.Load(),
			BytesOut:    item.bytesOut.Load(),
			Connections: item.connections.Load(),
		})
	}
	return out
}

func (c *Counters) SnapshotAndReset() []model.TrafficStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.TrafficStat, 0, len(c.items))
	for id, item := range c.items {
		out = append(out, model.TrafficStat{
			ID:          id,
			BytesIn:     item.bytesIn.Swap(0),
			BytesOut:    item.bytesOut.Swap(0),
			Connections: item.connections.Swap(0),
		})
	}
	return out
}

func (c *Counter) AddIn(n int64) {
	c.bytesIn.Add(n)
}

func (c *Counter) AddOut(n int64) {
	c.bytesOut.Add(n)
}

func (c *Counter) AddConnection() {
	c.connections.Add(1)
}
