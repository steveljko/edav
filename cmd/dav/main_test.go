package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/steveljko/edav/internal/config"
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

func TestMuxRoutes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"healthz", http.MethodGet, "/healthz", http.StatusOK, "ok\n"},
		{"healthz rejects post", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, ""},
		{"unknown path", http.MethodGet, "/nope", http.StatusNotFound, ""},
	}

	mux, err := newMux(testDB(t), &config.Config{})
	if err != nil {
		t.Fatalf("newMux() = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestHealthzReportsClosedDatabase(t *testing.T) {
	db := testDB(t)
	db.Close()

	mux, err := newMux(db, &config.Config{})
	if err != nil {
		t.Fatalf("newMux() = %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
