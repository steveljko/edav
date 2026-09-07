package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embedded so TZID lookups work in a scratch container, which carries no
	// zoneinfo of its own.
	_ "time/tzdata"

	"github.com/steveljko/edav/internal/admin"
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
	check := flag.Bool("healthcheck", false,
		"probe a running server's /healthz and exit; for container health checks, which have no shell on a scratch image")
	flag.Parse()

	if *check {
		if err := healthcheck(os.Getenv("EDAV_ADDR")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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

	handler, err := newMux(db, cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           logRequests(commonHeaders(handler)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr,
			"caldav", cfg.CalDAVEnabled, "carddav", cfg.CardDAVEnabled)
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

// healthcheck probes a server listening on addr. A bare ":8080" means the
// server listens on every interface, which from inside the container is
// reachable on the loopback address.
func healthcheck(addr string) error {
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("healthcheck: %s is not a host:port address: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: /healthz returned %s", resp.Status)
	}
	return nil
}

// recorder captures what a response turned out to be, so a request can be
// logged after it is answered.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests records every request. Debugging a DAV client means knowing what
// it actually asked for, which nothing else here reports; at the default level
// only server errors are logged, so a household's polling does not fill a disk.
//
// The user name is the one the request claimed, not one that was verified: it
// is recorded to make a log readable, not to attest to anything.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		username, _, _ := r.BasicAuth()

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start).Round(time.Millisecond),
			"user", username,
			"agent", r.UserAgent(),
		}

		switch {
		case rec.status >= 500:
			slog.Error("request failed", attrs...)
		default:
			slog.Debug("request", attrs...)
		}
	})
}

// commonHeaders applies what every response wants, DAV included. Anything
// specific to the admin interface, such as its content security policy, is set
// there instead.
//
// HSTS is deliberately absent: TLS terminates in front of this server, and the
// proxy that holds the certificate is the thing that knows whether promising a
// year of HTTPS is safe.
func commonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if h.Get("Referrer-Policy") == "" {
			h.Set("Referrer-Policy", "no-referrer")
		}
		next.ServeHTTP(w, r)
	})
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

func newMux(db *sql.DB, cfg *config.Config) (*http.ServeMux, error) {
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

	sessions := &auth.Sessions{DB: db, Secure: cfg.SecureCookies, Path: "/admin"}
	adminServer := &admin.Server{
		DB:             db,
		Sessions:       sessions,
		BaseURL:        cfg.BaseURL,
		DAVPrefix:      davPrefix,
		CalDAVEnabled:  cfg.CalDAVEnabled,
		CardDAVEnabled: cfg.CardDAVEnabled,
	}
	if err := adminServer.Register(mux); err != nil {
		return nil, err
	}

	// A browser opening the bare host should land on the admin interface;
	// nothing else answers there.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	return mux, nil
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
