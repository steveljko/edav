package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/steveljko/edav/internal/storage"
)

func testSessions(db *sql.DB) *Sessions {
	return &Sessions{DB: db, Secure: true, Path: "/admin"}
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	t.Fatal("no session cookie was set")
	return nil
}

func TestSessionsCreateSetsHardenedCookie(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)

	rec := httptest.NewRecorder()
	sess, err := s.Create(t.Context(), rec, u.ID)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	c := sessionCookie(t, rec)
	if c.Value != sess.Token {
		t.Errorf("cookie value = %q, want the session token", c.Value)
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if !c.Secure {
		t.Error("cookie is not Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/admin" {
		t.Errorf("Path = %q, want /admin", c.Path)
	}
	if sess.CSRFToken == "" || sess.CSRFToken == sess.Token {
		t.Error("CSRF token is empty or reuses the session token")
	}
}

func TestSessionsCreateIssuesDistinctTokens(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)

	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		sess, err := s.Create(t.Context(), httptest.NewRecorder(), u.ID)
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
		if seen[sess.Token] {
			t.Fatalf("duplicate session token %q", sess.Token)
		}
		seen[sess.Token] = true
	}
}

func TestSessionsGet(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)

	rec := httptest.NewRecorder()
	sess, err := s.Create(t.Context(), rec, u.ID)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(sessionCookie(t, rec))

	got, gotUser, err := s.Get(req)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if got.Token != sess.Token || gotUser.ID != u.ID {
		t.Errorf("Get() = %+v, %+v", got, gotUser)
	}
}

func TestSessionsGetRejects(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)

	if err := storage.CreateSession(t.Context(), db, &storage.Session{
		Token:     "expired",
		UserID:    u.ID,
		CSRFToken: "csrf",
		CreatedAt: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession() = %v", err)
	}
	expiredCookie := &http.Cookie{Name: SessionCookieName, Value: "expired"}

	tests := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"unknown token", &http.Cookie{Name: SessionCookieName, Value: "nonexistent"}},
		{"empty token", &http.Cookie{Name: SessionCookieName, Value: ""}},
		{"expired session", expiredCookie},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			if _, _, err := s.Get(req); err != ErrNoSession {
				t.Errorf("Get() = %v, want ErrNoSession", err)
			}
		})
	}
}

func TestSessionsDestroy(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)

	rec := httptest.NewRecorder()
	sess, err := s.Create(t.Context(), rec, u.ID)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(sessionCookie(t, rec))

	out := httptest.NewRecorder()
	if err := s.Destroy(out, req); err != nil {
		t.Fatalf("Destroy() = %v", err)
	}
	if c := sessionCookie(t, out); c.MaxAge >= 0 {
		t.Errorf("cleared cookie MaxAge = %d, want negative", c.MaxAge)
	}
	if _, _, err := storage.SessionByToken(t.Context(), db, sess.Token); err == nil {
		t.Error("session row survived Destroy()")
	}
}

func TestSessionsDestroyWithoutCookie(t *testing.T) {
	db := testDB(t)
	s := testSessions(db)

	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	if err := s.Destroy(httptest.NewRecorder(), req); err != nil {
		t.Errorf("Destroy() without a cookie = %v, want nil", err)
	}
}

func guarded(t *testing.T, s *Sessions) http.Handler {
	t.Helper()
	return s.RequireSession("/admin/login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok {
			t.Error("handler reached without a user in context")
		}
		if _, ok := SessionFrom(r.Context()); !ok {
			t.Error("handler reached without a session in context")
		}
		w.Write([]byte("ok " + u.Username))
	}))
}

func TestRequireSessionRedirectsAnonymous(t *testing.T) {
	db := testDB(t)
	s := testSessions(db)
	h := guarded(t, s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", got)
	}
}

func TestRequireSessionRedirectsHtmxWithHeader(t *testing.T) {
	db := testDB(t)
	s := testSessions(db)
	h := guarded(t, s)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 so htmx can act on the redirect", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/admin/login" {
		t.Errorf("HX-Redirect = %q, want /admin/login", got)
	}
}

func TestRequireSessionAllowsSafeMethods(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)
	h := guarded(t, s)

	rec := httptest.NewRecorder()
	if _, err := s.Create(t.Context(), rec, u.ID); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	cookie := sessionCookie(t, rec)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/admin/", nil)
		req.AddCookie(cookie)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)

		if out.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", method, out.Code)
		}
	}
}

func TestRequireSessionEnforcesCSRF(t *testing.T) {
	db := testDB(t)
	u := testUser(t, db, "alice", "hunter2hunter2")
	s := testSessions(db)
	h := guarded(t, s)

	rec := httptest.NewRecorder()
	sess, err := s.Create(t.Context(), rec, u.ID)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	cookie := sessionCookie(t, rec)

	tests := []struct {
		name       string
		method     string
		formToken  string
		headerTok  string
		wantStatus int
	}{
		{"post with form token", http.MethodPost, sess.CSRFToken, "", http.StatusOK},
		{"post with header token", http.MethodPost, "", sess.CSRFToken, http.StatusOK},
		{"post without token", http.MethodPost, "", "", http.StatusForbidden},
		{"post with wrong token", http.MethodPost, "wrong", "", http.StatusForbidden},
		{"post with another session's token", http.MethodPost, "", "not-the-token", http.StatusForbidden},
		{"delete without token", http.MethodDelete, "", "", http.StatusForbidden},
		{"put without token", http.MethodPut, "", "", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := url.Values{}
			if tt.formToken != "" {
				body.Set(CSRFFieldName, tt.formToken)
			}
			req := httptest.NewRequest(tt.method, "/admin/users", strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.headerTok != "" {
				req.Header.Set(CSRFHeaderName, tt.headerTok)
			}
			req.AddCookie(cookie)

			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", out.Code, tt.wantStatus)
			}
		})
	}
}

func TestRequireSessionClearsCookieOnUnknownSession(t *testing.T) {
	db := testDB(t)
	s := testSessions(db)
	h := guarded(t, s)

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "stale"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if c := sessionCookie(t, rec); c.MaxAge >= 0 {
		t.Errorf("stale cookie MaxAge = %d, want negative", c.MaxAge)
	}
}
