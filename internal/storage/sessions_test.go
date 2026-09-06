package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func mustCreateSession(t *testing.T, db *sql.DB, userID int64, token string, expires time.Time) *Session {
	t.Helper()
	s := &Session{
		Token:     token,
		UserID:    userID,
		CSRFToken: "csrf-" + token,
		CreatedAt: time.Now().Add(-time.Minute),
		ExpiresAt: expires,
	}
	if err := CreateSession(context.Background(), db, s); err != nil {
		t.Fatalf("CreateSession(%q) = %v", token, err)
	}
	return s
}

func TestSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	mustCreateSession(t, db, u.ID, "tok", time.Now().Add(time.Hour))

	got, gotUser, err := SessionByToken(ctx, db, "tok")
	if err != nil {
		t.Fatalf("SessionByToken() = %v", err)
	}
	if got.UserID != u.ID || got.CSRFToken != "csrf-tok" {
		t.Errorf("SessionByToken() = %+v", got)
	}
	if gotUser.ID != u.ID || gotUser.Username != "alice" {
		t.Errorf("SessionByToken() user = %+v", gotUser)
	}
}

func TestSessionByTokenUnknown(t *testing.T) {
	db := testDB(t)
	if _, _, err := SessionByToken(context.Background(), db, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionByToken() = %v, want ErrNotFound", err)
	}
}

func TestSessionByTokenRejectsAndDeletesExpired(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	mustCreateSession(t, db, u.ID, "stale", time.Now().Add(-time.Second))

	if _, _, err := SessionByToken(ctx, db, "stale"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SessionByToken() = %v, want ErrNotFound", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE token = 'stale'`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 0 {
		t.Error("expired session was not deleted on lookup")
	}
}

func TestDeleteSession(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	mustCreateSession(t, db, u.ID, "tok", time.Now().Add(time.Hour))

	if err := DeleteSession(ctx, db, "tok"); err != nil {
		t.Fatalf("DeleteSession() = %v", err)
	}
	if _, _, err := SessionByToken(ctx, db, "tok"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionByToken() after delete = %v, want ErrNotFound", err)
	}
	if err := DeleteSession(ctx, db, "tok"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteSession() on missing token = %v, want ErrNotFound", err)
	}
}

func TestDeleteUserSessions(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	alice := mustCreateUser(t, db, "alice")
	bob := mustCreateUser(t, db, "bob")
	mustCreateSession(t, db, alice.ID, "a1", time.Now().Add(time.Hour))
	mustCreateSession(t, db, alice.ID, "a2", time.Now().Add(time.Hour))
	mustCreateSession(t, db, bob.ID, "b1", time.Now().Add(time.Hour))

	if err := DeleteUserSessions(ctx, db, alice.ID); err != nil {
		t.Fatalf("DeleteUserSessions() = %v", err)
	}

	for _, token := range []string{"a1", "a2"} {
		if _, _, err := SessionByToken(ctx, db, token); !errors.Is(err, ErrNotFound) {
			t.Errorf("session %q survived = %v, want ErrNotFound", token, err)
		}
	}
	if _, _, err := SessionByToken(ctx, db, "b1"); err != nil {
		t.Errorf("another user's session was revoked: %v", err)
	}
}

func TestSessionsCascadeOnUserDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	mustCreateSession(t, db, u.ID, "tok", time.Now().Add(time.Hour))

	if err := DeleteUser(ctx, db, u.ID); err != nil {
		t.Fatalf("DeleteUser() = %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 0 {
		t.Errorf("sessions = %d after owner delete, want 0", n)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	now := time.Now()
	mustCreateSession(t, db, u.ID, "live", now.Add(time.Hour))
	mustCreateSession(t, db, u.ID, "stale1", now.Add(-time.Hour))
	mustCreateSession(t, db, u.ID, "stale2", now.Add(-time.Minute))

	n, err := DeleteExpiredSessions(ctx, db, now)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions() = %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteExpiredSessions() = %d, want 2", n)
	}
	if _, _, err := SessionByToken(ctx, db, "live"); err != nil {
		t.Errorf("live session was swept: %v", err)
	}
}
