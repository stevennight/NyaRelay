package auth

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

type Session struct {
	ID        string
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

func NewSession(userID int64, username string, lifetime time.Duration) (Session, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Session{}, err
	}
	session := Session{
		ID:        base64.RawURLEncoding.EncodeToString(raw[:]),
		UserID:    userID,
		Username:  username,
		ExpiresAt: time.Now().Add(lifetime),
	}
	return session, nil
}
