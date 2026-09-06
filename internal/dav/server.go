package dav

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/dav/caldav"
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
	CalDAVEnabled  bool
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
	if s.CalDAVEnabled {
		mux.Handle("/.well-known/caldav", wellKnown(p.Root()))
	}

	protected := auth.RequireBasicAuth(s.DB, Realm)

	// The two handlers own disjoint subtrees, so each is mounted on its own
	// prefix rather than being chained; only the principal and the root are
	// shared, and both handlers answer those identically.
	if s.CardDAVEnabled {
		h := &carddav.Handler{Backend: &CardDAVBackend{DB: s.DB, Paths: p}, Prefix: p.Prefix}
		mux.Handle(p.Prefix+"/"+addressBooksSegment+"/", protected(h))
	}
	if s.CalDAVEnabled {
		h := &caldav.Handler{Backend: &CalDAVBackend{DB: s.DB, Paths: p}, Prefix: p.Prefix}
		mux.Handle(p.Prefix+"/"+calendarsSegment+"/", protected(h))
	}

	mux.Handle(p.Prefix+"/", protected(s.principalHandler(p)))
}

// principalHandler answers requests at the DAV root and on principals. Both
// protocol handlers can serve these, so whichever is enabled is used; a client
// discovering one still finds its way to the other's home set through the
// principal's properties.
func (s *Server) principalHandler(p Paths) http.Handler {
	if s.CalDAVEnabled {
		return &caldav.Handler{Backend: &CalDAVBackend{DB: s.DB, Paths: p}, Prefix: p.Prefix}
	}
	return &carddav.Handler{Backend: &CardDAVBackend{DB: s.DB, Paths: p}, Prefix: p.Prefix}
}

// wellKnown redirects to the DAV root. RFC 6764 §6 asks for a 301 so that
// clients cache the location and stop probing on every sync.
func wellKnown(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
