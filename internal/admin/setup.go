package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/storage"
)

func (s *Server) showSetup(w http.ResponseWriter, r *http.Request) {
	base := s.baseURL(r)

	data := pageData{
		BaseURL:        base,
		HasBaseURL:     s.BaseURL != "",
		Host:           hostOf(base),
		Insecure:       strings.HasPrefix(base, "http://") && !isLoopback(base),
		CalDAVEnabled:  s.CalDAVEnabled,
		CardDAVEnabled: s.CardDAVEnabled,
	}

	// Paths are shown for the signed-in administrator, since that is the
	// account whoever is reading this page can test with immediately.
	data.PathUser = "the user"
	if u, ok := auth.UserFrom(r.Context()); ok {
		data.Subject = u
		data.PathUser = u.Username
	}
	username := data.PathUser
	if data.Subject == nil {
		username = "{username}"
	}

	data.Principal = fmt.Sprintf("%s/principals/%s/", s.DAVPrefix, username)
	data.CalendarHomePath = fmt.Sprintf("%s/calendars/%s/", s.DAVPrefix, username)
	data.AddressBookHomePath = fmt.Sprintf("%s/addressbooks/%s/", s.DAVPrefix, username)
	data.CalendarHome = base + data.CalendarHomePath
	data.AddressBookHome = base + data.AddressBookHomePath

	if err := s.countTotals(r, &data); err != nil {
		s.fail(w, r, "count totals", err)
		return
	}

	s.render(w, r, "setup.html", s.page(r, "Client setup", "setup", data))
}

func (s *Server) countTotals(r *http.Request, data *pageData) error {
	users, err := storage.ListUsers(r.Context(), s.DB)
	if err != nil {
		return err
	}
	data.UserCount = len(users)

	for _, u := range users {
		collections, err := storage.ListCollections(r.Context(), s.DB, u.ID, "")
		if err != nil {
			return err
		}
		for _, c := range collections {
			if c.Type == storage.CollectionCalendar {
				data.CalendarCount++
			} else {
				data.AddressBookCount++
			}
			data.ObjectCount += s.countObjects(r, c.ID)
		}
	}
	return nil
}

// baseURL is the address clients should use. The configured value wins: behind
// a reverse proxy the request arrives on an address no client can reach.
func (s *Server) baseURL(r *http.Request) string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func hostOf(base string) string {
	host := base
	if _, after, ok := strings.Cut(host, "://"); ok {
		host = after
	}
	return strings.TrimSuffix(host, "/")
}

func isLoopback(base string) bool {
	host := hostOf(base)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}
