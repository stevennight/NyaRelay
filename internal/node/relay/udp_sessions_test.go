package relay

import (
	"testing"
	"time"
)

func TestUDPSessionTableEvictsOldestEntryAtCapacity(t *testing.T) {
	sessions := newUDPCandidateSessions()
	sessions.maxEntries = 2
	now := time.Now()
	sessions.bind("first", 0, "node-a", now)
	sessions.bind("second", 1, "node-b", now.Add(time.Millisecond))
	sessions.bind("third", 2, "node-c", now.Add(2*time.Millisecond))

	if _, ok := sessions.get("first", now.Add(3*time.Millisecond)); ok {
		t.Fatal("oldest UDP session was not evicted")
	}
	if _, ok := sessions.get("second", now.Add(3*time.Millisecond)); !ok {
		t.Fatal("second UDP session was evicted unexpectedly")
	}
	if _, ok := sessions.get("third", now.Add(3*time.Millisecond)); !ok {
		t.Fatal("new UDP session was not retained")
	}
}
