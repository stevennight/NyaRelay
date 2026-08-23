package auth

import (
	"sync"
	"time"
)

type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]attempt
}

const (
	loginAttemptTTL = 15 * time.Minute
	maxLoginEntries = 10000
)

type attempt struct {
	Count     int
	BlockedTo time.Time
	UpdatedAt time.Time
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{attempts: make(map[string]attempt)}
}

func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.pruneLocked(now)
	item := l.attempts[key]
	if !item.BlockedTo.IsZero() && now.Before(item.BlockedTo) {
		return false
	}
	return true
}

func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.pruneLocked(now)
	if _, ok := l.attempts[key]; !ok && len(l.attempts) >= maxLoginEntries {
		l.evictOldestLocked()
	}
	item := l.attempts[key]
	if now.Sub(item.UpdatedAt) > loginAttemptTTL {
		item.Count = 0
		item.BlockedTo = time.Time{}
	}
	item.Count++
	item.UpdatedAt = now
	if item.Count >= 5 {
		item.BlockedTo = now.Add(time.Duration(item.Count-4) * time.Minute)
	}
	l.attempts[key] = item
}

func (l *LoginLimiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (l *LoginLimiter) pruneLocked(now time.Time) {
	for key, item := range l.attempts {
		if now.Sub(item.UpdatedAt) > loginAttemptTTL && (item.BlockedTo.IsZero() || !now.Before(item.BlockedTo)) {
			delete(l.attempts, key)
		}
	}
}

func (l *LoginLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, item := range l.attempts {
		if oldestKey == "" || item.UpdatedAt.Before(oldest) {
			oldestKey = key
			oldest = item.UpdatedAt
		}
	}
	if oldestKey != "" {
		delete(l.attempts, oldestKey)
	}
}
