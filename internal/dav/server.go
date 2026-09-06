package dav

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/dav/carddav"
)

// Realm is the HTTP Basic authentication realm shown to DAV clients.
const Realm = "edav"

// Server mounts the DAV endpoints on a mux.
type Server struct {
	DB *sql.DB
	// Prefix is the DAV root, without a trailing slash, for example "/dav".
	Prefix string

	CardDAVEnabled bool
}

func (s *Server) paths() Paths {
	return Paths{Prefix: strings.TrimSuffix(s.Prefix, "/")}
}

// Register mounts the DAV routes. The well-known redirects are registered
// outside the authentication middleware on purpose: iOS and macOS probe them
// before they have credentials to offer, and answering 401 there ends discovery
// with no useful error shown to the user.
func (s *Server) Register(mux *http.ServeMux) {
	p := s.paths()

	if s.CardDAVEnabled {
		mux.Handle("/.well-known/carddav", wellKnown(p.Root()))
	}

	backend := &CardDAVBackend{DB: s.DB, Paths: p}
	handler := &carddav.Handler{Backend: backend, Prefix: p.Prefix}

	mux.Handle(p.Prefix+"/", auth.RequireBasicAuth(s.DB, Realm)(handler))
}

// wellKnown redirects to the DAV root. RFC 6764 §6 asks for a 301 so that
// clients cache the location and stop probing on every sync.
func wellKnown(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
