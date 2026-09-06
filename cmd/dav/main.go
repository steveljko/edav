package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embedded so TZID lookups work in a scratch container, which carries no
	// zoneinfo of its own.
	_ "time/tzdata"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/config"
	"github.com/steveljko/edav/internal/dav"
	"github.com/steveljko/edav/internal/storage"
)

const (
	shutdownTimeout      = 10 * time.Second
	sessionSweepInterval = time.Hour
	davPrefix            = "/dav"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))

	if err := run(cfg); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	slog.Info("database ready", "path", cfg.DBPath)

	if err := seedAdmin(ctx, db, cfg); err != nil {
		return err
	}
	go sweepSessions(ctx, db)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newMux(db, cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr,
			"caldav", cfg.CalDAVEnabled, "carddav", cfg.CardDAVEnabled, "webdav", cfg.WebDAVEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func seedAdmin(ctx context.Context, db *sql.DB, cfg *config.Config) error {
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	u, err := storage.EnsureAdmin(ctx, db, cfg.AdminUsername, hash)
	if err != nil {
		return fmt.Errorf("seed admin account: %w", err)
	}
	slog.Info("admin account ready", "username", u.Username, "id", u.ID)
	return nil
}

func sweepSessions(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := storage.DeleteExpiredSessions(ctx, db, time.Now())
			if err != nil {
				slog.Error("session sweep failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Debug("swept expired sessions", "count", n)
			}
		}
	}
}

func newMux(db *sql.DB, cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(db))

	if cfg.CardDAVEnabled || cfg.CalDAVEnabled {
		(&dav.Server{
			DB:             db,
			Prefix:         davPrefix,
			CardDAVEnabled: cfg.CardDAVEnabled,
			CalDAVEnabled:  cfg.CalDAVEnabled,
		}).Register(mux)
	}
	return mux
}

func healthz(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			slog.Error("health check failed", "error", err)
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	}
}
