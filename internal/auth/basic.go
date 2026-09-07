package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/steveljko/edav/internal/storage"
)

type contextKey struct{ name string }

var userContextKey = contextKey{"user"}

// UserFrom returns the authenticated user attached to the request context by
// RequireBasicAuth or a session lookup.
func UserFrom(ctx context.Context) (*storage.User, bool) {
	u, ok := ctx.Value(userContextKey).(*storage.User)
	return u, ok
}

// WithUser attaches an authenticated user to a context.
func WithUser(ctx context.Context, u *storage.User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// A hash of a fixed random password, verified against whenever the username is
// unknown so that response time does not reveal which accounts exist.
var decoyHash = sync.OnceValue(func() string {
	h, err := HashPassword("edav decoy password")
	if err != nil {
		panic("auth: cannot hash decoy password: " + err.Error())
	}
	return h
})

// RequireBasicAuth authenticates DAV clients with HTTP Basic. Digest is not
// supported: every client that matters does Basic, and over TLS it buys
// nothing.
//
// Successful verifications are cached for a few minutes. DAV clients resend
// the same credentials on every request of every sync, and repeating an
// Argon2id verification each time costs 17ms and 20MB for no benefit.
func RequireBasicAuth(db *sql.DB, realm string) func(http.Handler) http.Handler {
	cache := newCredentialCache(DefaultCredentialTTL)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()
			if !ok {
				challenge(w, realm)
				return
			}

			u, err := authenticate(r.Context(), db, username, password, cache)
			if err != nil {
				if errors.Is(err, ErrMismatch) || errors.Is(err, storage.ErrNotFound) {
					slog.Info("basic auth rejected", "username", username, "remote", r.RemoteAddr)
				} else {
					slog.Error("basic auth failed", "username", username, "error", err)
				}
				challenge(w, realm)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		})
	}
}

// Authenticate verifies a username and password against the database. It
// returns ErrMismatch or storage.ErrNotFound for a failed login; callers must
// report both to the client identically. Both paths do the same hashing work.
//
// Every call verifies the password. Use RequireBasicAuth where the same
// credentials arrive repeatedly.
func Authenticate(ctx context.Context, db *sql.DB, username, password string) (*storage.User, error) {
	return authenticate(ctx, db, username, password, nil)
}

func authenticate(ctx context.Context, db *sql.DB, username, password string, cache *credentialCache) (*storage.User, error) {
	u, err := storage.UserByUsername(ctx, db, username)
	if errors.Is(err, storage.ErrNotFound) {
		// An unknown username costs the same as a known one, so the response
		// time does not reveal which accounts exist. A cache cannot help here,
		// and must not: caching this would make the two paths differ.
		_ = VerifyPassword(decoyHash(), password)
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if cache != nil && cache.verified(username, password, u.PasswordHash, now) {
		return u, nil
	}
	if err := VerifyPassword(u.PasswordHash, password); err != nil {
		return nil, err
	}
	if cache != nil {
		cache.remember(username, password, u.PasswordHash, now)
	}
	return u, nil
}

func challenge(w http.ResponseWriter, realm string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// EqualStrings compares two strings without leaking their contents through
// timing. Their lengths are still observable.
func EqualStrings(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
