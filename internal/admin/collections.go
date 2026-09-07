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

	typ := storage.CollectionType(r.PostFormValue("type"))
	if typ != storage.CollectionCalendar && typ != storage.CollectionAddressBook {
		reject("Choose either a calendar or an address book.")
		return
	}

	// A slug is derived from the name when none was given, so nobody has to
	// think about URLs to add a calendar. One typed by hand is still checked.
	derived := form.URI == ""
	if derived {
		form.URI = Slugify(form.DisplayName)
	}
	if !slugPattern.MatchString(form.URI) {
		reject("A URL slug may only contain letters, digits, dots, dashes and underscores, and must start with a letter or digit.")
		return
	}

	uri := form.URI
	if derived {
		// A derived slug collides silently otherwise, and reporting a conflict
		// with a value nobody typed is not a useful thing to say.
		uri = s.freeSlug(r, subject.ID, uri)
	}

	created, err := storage.CreateCollection(r.Context(), s.DB, &storage.Collection{
		OwnerID:     subject.ID,
		Type:        typ,
		URI:         uri,
		DisplayName: form.DisplayName,
	})
	if errors.Is(err, storage.ErrConflict) {
		reject(fmt.Sprintf("%s already has a collection at %q.", subject.Username, uri))
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

	// Searching and paging swap the results in place, so a keystroke costs one
	// small response rather than a whole page.
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, r, "collection.html", "collection-results", data)
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

	data.Query = strings.TrimSpace(r.URL.Query().Get("q"))

	var total, perPage int
	switch c.Type {
	case storage.CollectionAddressBook:
		data.IsAddressBook = true
		perPage = contactsPerPage
		if data.Contacts, total, data.Page, err = s.contactRows(r, c.ID); err != nil {
			return data, err
		}
	case storage.CollectionCalendar:
		data.IsCalendar = true
		perPage = eventsPerPage
		if data.Events, total, data.Page, err = s.eventRows(r, c.ID); err != nil {
			return data, err
		}
	}

	data.MatchCount = total
	data.Pages = (total + perPage - 1) / perPage
	if data.Page > 1 {
		data.PrevPage = data.Page - 1
	}
	if data.Page < data.Pages {
		data.NextPage = data.Page + 1
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

// Slugify turns a name into something that survives a URL untouched. It is the
// same transformation the form does as you type, so what the field previews is
// what the server stores.
func Slugify(name string) string {
	var b strings.Builder
	dash := false

	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '.' || r == '_':
			b.WriteRune(r)
			dash = false
		default:
			// Accented letters and anything else become a separator rather
			// than being dropped, so "Café Meetings" does not read as
			// "cafmeetings".
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}

	slug := strings.Trim(b.String(), "-._")
	if slug == "" {
		return ""
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-._")
	}
	return slug
}

// freeSlug appends a number until the slug is unused, the way a file manager
// does rather than refusing a name that is already taken.
func (s *Server) freeSlug(r *http.Request, ownerID int64, slug string) string {
	candidate := slug
	for n := 2; n < 100; n++ {
		if _, err := storage.CollectionByURI(r.Context(), s.DB, ownerID, candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", slug, n)
	}
	return candidate
}
