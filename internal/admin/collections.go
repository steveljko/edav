package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/steveljko/edav/internal/storage"
)

// A slug becomes a path segment in every client URL for the collection, so it
// is kept to what survives a URL untouched.
var slugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// defaultColor is what the colour picker shows for a collection that has none;
// an <input type=color> has no empty state.
const defaultColor = "#3b5bdb"

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.user(w, r)
	if !ok {
		return
	}

	form := formValues{
		URI:         strings.TrimSpace(r.PostFormValue("uri")),
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
	}

	reject := func(message string) {
		data, err := s.userPage(r, subject, pageData{Form: form})
		if err != nil {
			s.fail(w, r, "show user", err)
			return
		}
		data.Error = message
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "user.html", data)
	}

	if !slugPattern.MatchString(form.URI) {
		reject("A URL slug may only contain letters, digits, dots, dashes and underscores, and must start with a letter or digit.")
		return
	}

	typ := storage.CollectionType(r.PostFormValue("type"))
	if typ != storage.CollectionCalendar && typ != storage.CollectionAddressBook {
		reject("Choose either a calendar or an address book.")
		return
	}

	created, err := storage.CreateCollection(r.Context(), s.DB, &storage.Collection{
		OwnerID:     subject.ID,
		Type:        typ,
		URI:         form.URI,
		DisplayName: form.DisplayName,
	})
	if errors.Is(err, storage.ErrConflict) {
		reject(fmt.Sprintf("%s already has a collection at %q.", subject.Username, form.URI))
		return
	}
	if err != nil {
		s.fail(w, r, "create collection", err)
		return
	}

	slog.Info("admin created collection", "uri", created.URI, "type", created.Type, "owner", subject.Username)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", created.ID))
}

func (s *Server) showCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(w, r)
	if !ok {
		return
	}
	data, err := s.collectionPage(r, c, pageData{})
	if err != nil {
		s.fail(w, r, "show collection", err)
		return
	}
	s.render(w, r, "collection.html", data)
}

func (s *Server) collectionPage(r *http.Request, c *storage.Collection, data pageData) (pageData, error) {
	owner, err := storage.UserByID(r.Context(), s.DB, c.OwnerID)
	if err != nil {
		return data, err
	}

	data.Collection = c
	data.Subject = owner
	data.ObjectCount = s.countObjects(r, c.ID)
	data.ColorValue = c.Color
	if data.ColorValue == "" {
		data.ColorValue = defaultColor
	}
	data.CollectionURL = s.baseURL(r) + s.collectionPath(c.Type, owner.Username, c.URI)

	if c.Type == storage.CollectionAddressBook {
		data.IsAddressBook = true
		if data.Contacts, err = s.contactRows(r, c.ID); err != nil {
			return data, err
		}
	}

	title := c.DisplayName
	if title == "" {
		title = c.URI
	}
	return s.page(r, title, "users", data), nil
}

func (s *Server) updateCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(w, r)
	if !ok {
		return
	}

	updated := *c
	updated.DisplayName = strings.TrimSpace(r.PostFormValue("display_name"))
	updated.Description = strings.TrimSpace(r.PostFormValue("description"))
	updated.Color = strings.TrimSpace(r.PostFormValue("color"))

	if err := storage.UpdateCollection(r.Context(), s.DB, &updated); err != nil {
		s.fail(w, r, "update collection", err)
		return
	}

	slog.Info("admin updated collection", "uri", updated.URI, "id", updated.ID)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

func (s *Server) confirmDeleteCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(w, r)
	if !ok {
		return
	}

	name := c.DisplayName
	if name == "" {
		name = c.URI
	}
	count := s.countObjects(r, c.ID)

	data := s.page(r, "Delete "+name, "users", pageData{
		Confirm: confirmation{
			Title: "Delete " + name + "?",
			Body: fmt.Sprintf("This removes the collection and all %d object%s in it. It cannot be undone.",
				count, plural(count)),
			Verb:   "Delete this collection",
			Action: fmt.Sprintf("/admin/collections/%d/delete", c.ID),
			Cancel: fmt.Sprintf("/admin/collections/%d", c.ID),
		},
	})
	s.render(w, r, "confirm.html", data)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(w, r)
	if !ok {
		return
	}

	if err := storage.DeleteCollection(r.Context(), s.DB, c.ID); err != nil {
		s.fail(w, r, "delete collection", err)
		return
	}

	slog.Info("admin deleted collection", "uri", c.URI, "id", c.ID)
	s.redirect(w, r, fmt.Sprintf("/admin/users/%d", c.OwnerID))
}

func (s *Server) collectionPath(typ storage.CollectionType, username, uri string) string {
	segment := "calendars"
	if typ == storage.CollectionAddressBook {
		segment = "addressbooks"
	}
	return fmt.Sprintf("%s/%s/%s/%s/", s.DAVPrefix, segment, username, uri)
}
