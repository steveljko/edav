package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/storage"
	"github.com/steveljko/edav/internal/vcard"
)

// pages are parsed against the shared layout, each producing its own template
// set so that "content" resolves to the right page.
var pages = []string{
	"login.html",
	"users.html",
	"user.html",
	"collection.html",
	"confirm.html",
	"setup.html",
	"contact.html",
}

// funcs are the few helpers the templates need. Anything more involved is
// computed in Go and handed over as data.
var funcs = template.FuncMap{
	"initials": initials,
	"lower":    strings.ToLower,
}

func (s *Server) parseTemplates() error {
	s.templates = make(map[string]*template.Template, len(pages))
	for _, name := range pages {
		t, err := template.New(name).Funcs(funcs).
			ParseFS(templateFS, "templates/layout.html", "templates/"+name)
		if err != nil {
			return fmt.Errorf("admin: parse %s: %w", name, err)
		}
		s.templates[name] = t
	}
	return nil
}

// initials reduces a name to the one or two letters shown in an avatar.
func initials(name string) string {
	fields := strings.Fields(name)
	switch len(fields) {
	case 0:
		return "?"
	case 1:
		r := []rune(fields[0])
		if len(r) == 1 {
			return strings.ToUpper(string(r[0]))
		}
		return strings.ToUpper(string(r[0])) + strings.ToLower(string(r[1]))
	default:
		first := []rune(fields[0])
		last := []rune(fields[len(fields)-1])
		return strings.ToUpper(string(first[0]) + string(last[0]))
	}
}

// formValues carries submitted input back into a re-rendered form, so a
// rejected submission does not lose what was typed.
type formValues struct {
	Username    string
	DisplayName string
	Email       string
	URI         string
}

type confirmation struct {
	Title  string
	Body   string
	Verb   string
	Action string
	Cancel string
}

type userRow struct {
	User         *storage.User
	Calendars    int
	AddressBooks int
}

// pageData is everything a template can reach. One struct rather than a
// per-page type: the layout needs several of these fields on every page.
type pageData struct {
	Title     string
	Nav       string
	CSRFToken string
	User      *storage.User
	Error     string
	Message   string
	Form      formValues

	Users         []userRow
	Subject       *storage.User
	IsSelf        bool
	Collections   []*storage.Collection
	Collection    *storage.Collection
	ColorValue    string
	ObjectCount   int
	CollectionURL string
	IsAddressBook bool
	Contacts      []contactRow
	Contact       *vcard.Contact
	ContactURI    string
	ContactAction string
	TypeOptions   []string
	Confirm       confirmation

	// Client setup.
	BaseURL             string
	HasBaseURL          bool
	Host                string
	Insecure            bool
	Principal           string
	CalendarHomePath    string
	AddressBookHomePath string
	CalendarHome        string
	AddressBookHome     string
	PathUser            string
	CalDAVEnabled       bool
	CardDAVEnabled      bool
	UserCount           int
	CalendarCount       int
	AddressBookCount    int
}

// page fills in the fields the layout always needs.
func (s *Server) page(r *http.Request, title, nav string, data pageData) pageData {
	data.Title = title
	data.Nav = nav

	if u, ok := auth.UserFrom(r.Context()); ok {
		data.User = u
	}
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		data.CSRFToken = sess.CSRFToken
	} else if sess, _, err := s.Sessions.Get(r); err == nil {
		data.CSRFToken = sess.CSRFToken
	}
	return data
}

// render writes a page. The buffer means a template that fails halfway does not
// leave a half-written page behind an already-sent 200.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	t, ok := s.templates[name]
	if !ok {
		s.fail(w, r, "unknown template "+name, fmt.Errorf("not parsed"))
		return
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.fail(w, r, "render "+name, err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	buf.WriteTo(w)
}

// renderStatus writes a page under a non-200 status, for a rejected form.
func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, code int, name string, data pageData) {
	w.WriteHeader(code)
	s.render(w, r, name, data)
}
