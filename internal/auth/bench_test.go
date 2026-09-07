package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/steveljko/edav/internal/storage"
)

func benchHandler(b *testing.B) http.Handler {
	b.Helper()
	db := benchDB(b)
	return RequireBasicAuth(db, "edav")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
}

func BenchmarkAuthenticatedRequest(b *testing.B) {
	h := benchHandler(b)
	req := httptest.NewRequest(http.MethodGet, "/dav/", nil)
	req.SetBasicAuth("alice", "hunter2hunter2")

	// Warm the cache the way a client's first request does.
	h.ServeHTTP(httptest.NewRecorder(), req)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkAuthenticatedRequestUncached(b *testing.B) {
	db := benchDB(b)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Authenticate(b.Context(), db, "alice", "hunter2hunter2"); err != nil {
			b.Fatal(err)
		}
	}
}

func benchDB(b *testing.B) *sql.DB {
	b.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("storage.Open() = %v", err)
	}
	b.Cleanup(func() { db.Close() })

	hash, err := HashPassword("hunter2hunter2")
	if err != nil {
		b.Fatalf("HashPassword() = %v", err)
	}
	if _, err := storage.CreateUser(context.Background(), db, &storage.User{
		Username: "alice", PasswordHash: hash,
	}); err != nil {
		b.Fatalf("CreateUser() = %v", err)
	}
	return db
}
