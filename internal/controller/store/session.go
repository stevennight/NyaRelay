package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"nyarelay/internal/controller/auth"
)

const maxSessionEntries = 10000

func (s *Store) SaveSession(ctx context.Context, session auth.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		sessionTokenHash(session.ID), session.UserID, session.ExpiresAt.Unix(),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash IN (
		SELECT token_hash FROM sessions
		ORDER BY expires_at ASC
		LIMIT MAX((SELECT COUNT(*) FROM sessions) - ?, 0)
	)`, maxSessionEntries); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Session(ctx context.Context, token string) (auth.Session, bool, error) {
	var session auth.Session
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, `SELECT sessions.user_id, users.username, sessions.expires_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ?`, sessionTokenHash(token)).Scan(
		&session.UserID, &session.Username, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, false, nil
	}
	if err != nil {
		return auth.Session{}, false, err
	}
	session.ID = token
	session.ExpiresAt = time.Unix(expiresAt, 0)
	if !time.Now().Before(session.ExpiresAt) {
		if err := s.DeleteSession(ctx, token); err != nil {
			return auth.Session{}, false, err
		}
		return auth.Session{}, false, nil
	}
	return session, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, sessionTokenHash(token))
	return err
}

func sessionTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
