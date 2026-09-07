// Package admin serves the web interface for managing users and collections.
// DAV clients never touch it.
package admin

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server renders the admin interface.
type Server struct {
	DB       *sql.DB
	Sessions *auth.Sessions

	// BaseURL is the public address of this server, used for the client setup
	// page. Empty means derive it from the request.
	BaseURL string
	// DAVPrefix is the DAV root, without a trailing slash.
	DAVPrefix string

	CalDAVEnabled  bool
	CardDAVEnabled bool

	templates map[string]*template.Template
	logins    *auth.LoginThrottle
}

const (
	loginPath = "/admin/login"
	basePath  = "/admin/"
)

// Register mounts the admin routes. Everything except the login page and the
// static assets requires an administrator session.
func (s *Server) Register(mux *http.ServeMux) error {
	if err := s.parseTemplates(); err != nil {
		return err
	}
	s.logins = auth.NewLoginThrottle()

	mux.Handle("GET /admin/static/", secureHeaders(
		http.StripPrefix("/admin/", http.FileServerFS(staticFS))))

	mux.Handle("GET "+loginPath, secureHeaders(http.HandlerFunc(s.showLogin)))
	mux.Handle("POST "+loginPath, secureHeaders(http.HandlerFunc(s.doLogin)))

	// Logout only needs a session, not administrator rights: an account that
	// has just been demoted must still be able to sign out.
	session := s.Sessions.RequireSession(loginPath)
	mux.Handle("POST /admin/logout", secureHeaders(session(http.HandlerFunc(s.doLogout))))

	admin := func(h http.HandlerFunc) http.Handler {
		return secureHeaders(session(s.requireAdmin(h)))
	}

	mux.Handle("GET /admin/{$}", admin(s.listUsers))
	mux.Handle("POST /admin/users", admin(s.createUser))
	mux.Handle("GET /admin/users/{id}", admin(s.showUser))
	mux.Handle("POST /admin/users/{id}", admin(s.updateUser))
	mux.Handle("POST /admin/users/{id}/password", admin(s.resetPassword))
	mux.Handle("GET /admin/users/{id}/delete", admin(s.confirmDeleteUser))
	mux.Handle("POST /admin/users/{id}/delete", admin(s.deleteUser))
	mux.Handle("POST /admin/users/{id}/collections", admin(s.createCollection))
	mux.Handle("GET /admin/collections/{id}", admin(s.showCollection))
	mux.Handle("POST /admin/collections/{id}", admin(s.updateCollection))
	mux.Handle("GET /admin/collections/{id}/delete", admin(s.confirmDeleteCollection))
	mux.Handle("POST /admin/collections/{id}/delete", admin(s.deleteCollection))
	mux.Handle("GET /admin/collections/{id}/contacts/new", admin(s.newContact))
	mux.Handle("POST /admin/collections/{id}/contacts", admin(s.createContact))
	mux.Handle("GET /admin/collections/{id}/contacts/{uri}", admin(s.showContact))
	mux.Handle("POST /admin/collections/{id}/contacts/{uri}", admin(s.updateContact))
	mux.Handle("GET /admin/collections/{id}/contacts/{uri}/delete", admin(s.confirmDeleteContact))
	mux.Handle("POST /admin/collections/{id}/contacts/{uri}/delete", admin(s.deleteContact))
	mux.Handle("GET /admin/setup", admin(s.showSetup))

	return nil
}

// requireAdmin gates the interface on the administrator flag. A plain DAV user
// has an account but no business here.
func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok || !u.IsAdmin {
			http.Error(w, "administrator access required", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func (s *Server) showLogin(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.Sessions.Get(r); err == nil {
		http.Redirect(w, r, basePath, http.StatusSeeOther)
		return
	}
	s.render(w, r, "login.html", s.page(r, "Sign in", "", pageData{}))
}

func (s *Server) doLogin(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")

	now := time.Now()
	if retry, ok := s.logins.Allow(username, now); !ok {
		slog.Warn("admin login throttled", "username", username, "remote", r.RemoteAddr)
		data := s.page(r, "Sign in", "", pageData{Form: formValues{Username: username}})
		data.Error = fmt.Sprintf("Too many attempts. Try again in %s.", humaniseWait(retry))
		w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds()), 1)))
		s.renderStatus(w, r, http.StatusTooManyRequests, "login.html", data)
		return
	}

	// The login form itself cannot carry a session CSRF token, since there is
	// no session yet. SameSite=Lax on the session cookie is what stops a
	// cross-site login here.
	u, err := auth.Authenticate(r.Context(), s.DB, username, password)
	if err != nil {
		s.logins.Fail(username, now)
		slog.Info("admin login rejected", "username", username, "remote", r.RemoteAddr)
		data := s.page(r, "Sign in", "", pageData{Form: formValues{Username: username}})
		data.Error = "Incorrect username or password."
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, "login.html", data)
		return
	}
	if !u.IsAdmin {
		slog.Info("admin login by non-administrator", "username", username)
		data := s.page(r, "Sign in", "", pageData{Form: formValues{Username: username}})
		data.Error = "That account does not have administrator access."
		w.WriteHeader(http.StatusForbidden)
		s.render(w, r, "login.html", data)
		return
	}

	s.logins.Succeed(username)

	if _, err := s.Sessions.Create(r.Context(), w, u.ID); err != nil {
		s.fail(w, r, "create session", err)
		return
	}
	slog.Info("admin signed in", "username", u.Username)
	http.Redirect(w, r, basePath, http.StatusSeeOther)
}

func (s *Server) doLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Sessions.Destroy(w, r); err != nil {
		s.fail(w, r, "destroy session", err)
		return
	}
	s.redirect(w, r, loginPath)
}

// redirect sends the browser onward, telling htmx to follow it as a navigation
// rather than splicing the target page into the element that triggered it.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	slog.Error("admin request failed", "what", what, "path", r.URL.Path, "error", err)
	http.Error(w, "something went wrong", http.StatusInternalServerError)
}

// humaniseWait rounds a delay to something worth reading on a form.
func humaniseWait(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	minutes := int(d.Round(time.Minute).Minutes())
	if minutes == 1 {
		return "a minute"
	}
	return strconv.Itoa(minutes) + " minutes"
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (s *Server) user(w http.ResponseWriter, r *http.Request) (*storage.User, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	u, err := storage.UserByID(r.Context(), s.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return u, true
}

func (s *Server) collection(w http.ResponseWriter, r *http.Request) (*storage.Collection, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	c, err := storage.CollectionByID(r.Context(), s.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return c, true
}

// countObjects reports how many objects a collection holds, for the confirm
// text on a deletion.
func (s *Server) countObjects(r *http.Request, collectionID int64) int {
	objects, err := storage.ListObjects(r.Context(), s.DB, collectionID)
	if err != nil {
		slog.Error("count objects", "collection", collectionID, "error", err)
		return 0
	}
	return len(objects)
}
