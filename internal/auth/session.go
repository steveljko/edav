package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/steveljko/edav/internal/storage"
)

const (
	SessionCookieName = "edav_session"
	// CSRFFieldName is the form field carrying the CSRF token.
	CSRFFieldName = "csrf_token"
	// CSRFHeaderName lets htmx send the token on requests without a form body.
	CSRFHeaderName = "X-CSRF-Token"

	DefaultSessionLifetime = 30 * 24 * time.Hour

	tokenBytes = 32
)

// ErrNoSession is returned when a request carries no usable session.
var ErrNoSession = errors.New("auth: no session")

// Sessions issues and validates admin cookie sessions.
type Sessions struct {
	DB *sql.DB
	// Lifetime is the absolute session lifetime; zero means
	// DefaultSessionLifetime.
	Lifetime time.Duration
	// Secure sets the Secure cookie attribute. It must be true in production;
	// turning it off is only useful for local plain-HTTP development, since
	// browsers refuse Secure cookies over http.
	Secure bool
	// Path scopes the cookie to the admin UI.
	Path string
}

func (s *Sessions) lifetime() time.Duration {
	if s.Lifetime <= 0 {
		return DefaultSessionLifetime
	}
	return s.Lifetime
}

func (s *Sessions) path() string {
	if s.Path == "" {
		return "/"
	}
	return s.Path
}

// Create records a new session for the user and sets the session cookie.
func (s *Sessions) Create(ctx context.Context, w http.ResponseWriter, userID int64) (*storage.Session, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	sess := &storage.Session{
		Token:     token,
		UserID:    userID,
		CSRFToken: csrfToken,
		CreatedAt: now,
		ExpiresAt: now.Add(s.lifetime()),
	}
	if err := storage.CreateSession(ctx, s.DB, sess); err != nil {
		return nil, err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     s.path(),
		Expires:  sess.ExpiresAt,
		MaxAge:   int(s.lifetime().Seconds()),
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	return sess, nil
}

// Get resolves the session cookie on a request. It returns ErrNoSession when
// the cookie is absent, unknown or expired.
func (s *Sessions) Get(r *http.Request) (*storage.Session, *storage.User, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, nil, ErrNoSession
	}

	sess, user, err := storage.SessionByToken(r.Context(), s.DB, cookie.Value)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, ErrNoSession
	}
	if err != nil {
		return nil, nil, err
	}
	return sess, user, nil
}

// Destroy deletes the session behind the request's cookie and clears it.
func (s *Sessions) Destroy(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil {
		if err := storage.DeleteSession(r.Context(), s.DB, cookie.Value); err != nil &&
			!errors.Is(err, storage.ErrNotFound) {
			return err
		}
	}
	s.Clear(w)
	return nil
}

// Clear expires the session cookie in the client.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     s.path(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

type sessionContextKey struct{}

// SessionFrom returns the session attached to the request context by
// RequireSession.
func SessionFrom(ctx context.Context) (*storage.Session, bool) {
	sess, ok := ctx.Value(sessionContextKey{}).(*storage.Session)
	return sess, ok
}

// RequireSession admits requests carrying a valid session cookie and redirects
// everything else to loginPath, attaching the session and user to the context.
// Mutating requests must also carry a matching CSRF token.
func (s *Sessions) RequireSession(loginPath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, user, err := s.Get(r)
			if err != nil {
				if !errors.Is(err, ErrNoSession) {
					slog.Error("session lookup failed", "error", err)
				}
				s.Clear(w)
				redirectToLogin(w, r, loginPath)
				return
			}

			if isMutating(r.Method) && !EqualStrings(requestCSRFToken(r), sess.CSRFToken) {
				slog.Warn("csrf token mismatch", "user", user.Username, "path", r.URL.Path)
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}

			ctx := context.WithValue(WithUser(r.Context(), user), sessionContextKey{}, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func redirectToLogin(w http.ResponseWriter, r *http.Request, loginPath string) {
	// htmx follows a 200 with HX-Redirect; a 302 would be swallowed into the
	// target element as a login page fragment.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", loginPath)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, loginPath, http.StatusSeeOther)
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func requestCSRFToken(r *http.Request) string {
	if token := r.Header.Get(CSRFHeaderName); token != "" {
		return token
	}
	return r.PostFormValue(CSRFFieldName)
}

func randomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
