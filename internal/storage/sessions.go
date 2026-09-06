package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Session struct {
	Token     string
	UserID    int64
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func CreateSession(ctx context.Context, db *sql.DB, s *Session) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, csrf_token, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		s.Token, s.UserID, s.CSRFToken, s.CreatedAt.Unix(), s.ExpiresAt.Unix(),
	); err != nil {
		return fmt.Errorf("create session for user %d: %w", s.UserID, err)
	}
	return nil
}

// SessionByToken returns the session and its user. Expired sessions are treated
// as absent and deleted on the way out, so a stale cookie cleans up after
// itself instead of waiting for the sweeper.
func SessionByToken(ctx context.Context, db *sql.DB, token string) (*Session, *User, error) {
	var s Session
	var created, expires int64
	var u User
	var userCreated, userUpdated int64

	err := db.QueryRowContext(ctx,
		`SELECT s.token, s.user_id, s.csrf_token, s.created_at, s.expires_at,
		        u.id, u.username, u.display_name, u.email, u.password_hash, u.is_admin,
		        u.created_at, u.updated_at
		   FROM sessions s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.token = ?`, token,
	).Scan(&s.Token, &s.UserID, &s.CSRFToken, &created, &expires,
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash, &u.IsAdmin,
		&userCreated, &userUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("session: %w", err)
	}

	s.CreatedAt = time.Unix(created, 0).UTC()
	s.ExpiresAt = time.Unix(expires, 0).UTC()
	if !time.Now().Before(s.ExpiresAt) {
		if err := DeleteSession(ctx, db, token); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("session: %w", ErrNotFound)
	}

	u.CreatedAt = time.Unix(userCreated, 0).UTC()
	u.UpdatedAt = time.Unix(userUpdated, 0).UTC()
	return &s, &u, nil
}

func DeleteSession(ctx context.Context, db *sql.DB, token string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return checkAffected(res, "delete session")
}

// DeleteUserSessions revokes every session for a user, for password changes and
// account deletion.
func DeleteUserSessions(ctx context.Context, db *sql.DB, userID int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete sessions for user %d: %w", userID, err)
	}
	return nil
}

// DeleteExpiredSessions removes sessions that expired before now and reports
// how many were swept.
func DeleteExpiredSessions(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: rows affected: %w", err)
	}
	return n, nil
}
