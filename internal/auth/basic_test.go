package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/steveljko/edav/internal/storage"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testUser(t *testing.T, db *sql.DB, username, password string) *storage.User {
	t.Helper()
	hash, err := hashPasswordWith(password, testParams)
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}
	u, err := storage.CreateUser(context.Background(), db, &storage.User{
		Username:     username,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}
	return u
}

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	alice := testUser(t, db, "alice", "hunter2hunter2")

	tests := []struct {
		name     string
		username string
		password string
		wantErr  error
	}{
		{"correct", "alice", "hunter2hunter2", nil},
		{"username is case-insensitive", "ALICE", "hunter2hunter2", nil},
		{"wrong password", "alice", "hunter3", ErrMismatch},
		{"empty password", "alice", "", ErrMismatch},
		{"unknown user", "mallory", "hunter2hunter2", storage.ErrNotFound},
		{"empty username", "", "hunter2hunter2", storage.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := Authenticate(ctx, db, tt.username, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate() = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && u.ID != alice.ID {
				t.Errorf("Authenticate() returned user %d, want %d", u.ID, alice.ID)
			}
			if tt.wantErr != nil && u != nil {
				t.Errorf("Authenticate() returned user %+v alongside an error", u)
			}
		})
	}
}

func protected(t *testing.T, db *sql.DB) http.Handler {
	t.Helper()
	return RequireBasicAuth(db, "edav")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok {
			t.Error("handler reached without a user in context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(u.Username))
	}))
}

func TestRequireBasicAuth(t *testing.T) {
	db := testDB(t)
	testUser(t, db, "alice", "hunter2hunter2")
	h := protected(t, db)

	tests := []struct {
		name       string
		setAuth    bool
		username   string
		password   string
		wantStatus int
	}{
		{name: "no credentials", wantStatus: http.StatusUnauthorized},
		{name: "wrong password", setAuth: true, username: "alice", password: "nope", wantStatus: http.StatusUnauthorized},
		{name: "unknown user", setAuth: true, username: "mallory", password: "hunter2hunter2", wantStatus: http.StatusUnauthorized},
		{name: "correct", setAuth: true, username: "alice", password: "hunter2hunter2", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/dav/", nil)
			if tt.setAuth {
				req.SetBasicAuth(tt.username, tt.password)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="edav", charset="UTF-8"` {
					t.Errorf("WWW-Authenticate = %q", got)
				}
			}
			if tt.wantStatus == http.StatusOK && rec.Body.String() != "alice" {
				t.Errorf("body = %q, want %q", rec.Body.String(), "alice")
			}
		})
	}
}

func TestRequireBasicAuthRejectsMalformedHeader(t *testing.T) {
	db := testDB(t)
	testUser(t, db, "alice", "hunter2hunter2")
	h := protected(t, db)

	for _, header := range []string{
		"",
		"Basic",
		"Basic not-base64!",
		"Bearer sometoken",
		"Basic " + "YWxpY2U=", // "alice", no colon
	} {
		req := httptest.NewRequest(http.MethodGet, "/dav/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status = %d, want 401", header, rec.Code)
		}
	}
}

func TestEqualStrings(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"token", "token", true},
		{"token", "toke", false},
		{"token", "TOKEN", false},
		{"token", "", false},
	}
	for _, tt := range tests {
		if got := EqualStrings(tt.a, tt.b); got != tt.want {
			t.Errorf("EqualStrings(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
