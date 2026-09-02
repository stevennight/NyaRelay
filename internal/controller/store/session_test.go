package store

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
)

func TestSessionPersistsAcrossStoreReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "nyarelay.db")
	key := sha256.Sum256([]byte("persistent-session-test"))

	st, err := OpenWithSecretKey(ctx, path, key[:])
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser(ctx, "admin", "password-hash")
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.NewSession(user.ID, user.Username, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	var storedToken string
	if err := st.db.QueryRowContext(ctx, `SELECT token_hash FROM sessions`).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if storedToken == session.ID || storedToken != sessionTokenHash(session.ID) {
		t.Fatal("session token was not stored as the expected digest")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = OpenWithSecretKey(ctx, path, key[:])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	got, ok, err := st.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("session was lost when the store reopened")
	}
	if got.ID != session.ID || got.UserID != user.ID || got.Username != user.Username {
		t.Fatalf("restored session = %#v, want %#v", got, session)
	}
	if got.ExpiresAt.Unix() != session.ExpiresAt.Unix() {
		t.Fatalf("restored expiry = %v, want %v", got.ExpiresAt, session.ExpiresAt)
	}

	if err := st.DeleteSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Session(ctx, session.ID); err != nil || ok {
		t.Fatalf("deleted session lookup = ok %v, err %v", ok, err)
	}
}

func TestExpiredSessionIsRejectedAndRemoved(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	user, err := st.CreateUser(ctx, "admin", "password-hash")
	if err != nil {
		t.Fatal(err)
	}
	session := auth.Session{
		ID:        "expired-session",
		UserID:    user.ID,
		Username:  user.Username,
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := st.SaveSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Session(ctx, session.ID); err != nil || ok {
		t.Fatalf("expired session lookup = ok %v, err %v", ok, err)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired session rows = %d, want 0", count)
	}
}
