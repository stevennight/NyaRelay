package relay

import (
	"context"
	"sync"
	"time"
)

const (
	udpSessionIdleTimeout = 120 * time.Second
	udpSessionGCInterval  = 30 * time.Second
	maxUDPSessionEntries  = 100000
)

type udpCandidateSessions struct {
	mu          sync.Mutex
	idleTimeout time.Duration
	gcInterval  time.Duration
	maxEntries  int
	sessions    map[string]udpCandidateSession
}

type udpCandidateSession struct {
	candidateNodeID string
	candidateIndex  int
	createdAt       time.Time
	lastSeen        time.Time
}

func newUDPCandidateSessions() *udpCandidateSessions {
	return &udpCandidateSessions{
		idleTimeout: udpSessionIdleTimeout,
		gcInterval:  udpSessionGCInterval,
		maxEntries:  maxUDPSessionEntries,
		sessions:    make(map[string]udpCandidateSession),
	}
}

func (s *udpCandidateSessions) get(key string, now time.Time) (udpCandidateSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	if !ok {
		return udpCandidateSession{}, false
	}
	if s.expiredLocked(session, now) {
		delete(s.sessions, key)
		return udpCandidateSession{}, false
	}
	session.lastSeen = now
	s.sessions[key] = session
	return session, true
}

func (s *udpCandidateSessions) bind(key string, candidateIndex int, candidateNodeID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[key]; ok {
		existing.candidateIndex = candidateIndex
		existing.candidateNodeID = candidateNodeID
		existing.lastSeen = now
		s.sessions[key] = existing
		return
	}
	limit := s.maxEntries
	if limit <= 0 {
		limit = maxUDPSessionEntries
	}
	if len(s.sessions) >= limit {
		s.evictOldestLocked()
	}
	s.sessions[key] = udpCandidateSession{
		candidateIndex:  candidateIndex,
		candidateNodeID: candidateNodeID,
		createdAt:       now,
		lastSeen:        now,
	}
}

func (s *udpCandidateSessions) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, session := range s.sessions {
		if oldestKey == "" || session.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = session.lastSeen
		}
	}
	if oldestKey != "" {
		delete(s.sessions, oldestKey)
	}
}

func (s *udpCandidateSessions) delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
}

func (s *udpCandidateSessions) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]udpCandidateSession)
}

func (s *udpCandidateSessions) gcLoop(ctx context.Context) {
	interval := s.gcInterval
	if interval <= 0 {
		interval = udpSessionGCInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.gc(now)
		}
	}
}

func (s *udpCandidateSessions) gc(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, session := range s.sessions {
		if s.expiredLocked(session, now) {
			delete(s.sessions, key)
		}
	}
}

func (s *udpCandidateSessions) expiredLocked(session udpCandidateSession, now time.Time) bool {
	timeout := s.idleTimeout
	if timeout <= 0 {
		timeout = udpSessionIdleTimeout
	}
	return !session.lastSeen.IsZero() && now.Sub(session.lastSeen) > timeout
}
