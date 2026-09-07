package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/steveljko/edav/internal/storage"
	"github.com/steveljko/edav/internal/vcard"
)

// contactRow is a contact as the collection listing shows it. It is built from
// the indexed columns rather than by parsing every stored card.
type contactRow struct {
	URI  string
	Name string
	UID  string
}

// contactsPerPage is what one page of an address book shows. Large enough that
// a household never sees a second page, small enough that importing thousands
// of contacts does not turn this into a document nobody can load.
const contactsPerPage = 200

func (s *Server) contactRows(r *http.Request, collectionID int64) ([]contactRow, int, int, error) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page := max(atoiOr(r.URL.Query().Get("page"), 1), 1)

	objects, total, err := storage.SearchObjects(r.Context(), s.DB, collectionID,
		query, contactsPerPage, (page-1)*contactsPerPage)
	if err != nil {
		return nil, 0, 0, err
	}

	rows := make([]contactRow, 0, len(objects))
	for _, o := range objects {
		name := o.DisplayName
		if name == "" {
			name = o.URI
		}
		rows = append(rows, contactRow{URI: o.URI, Name: name, UID: o.UID})
	}
	return rows, total, page, nil
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// addressBookOf resolves a collection that must be an address book, since the
// contact routes are meaningless for a calendar.
func (s *Server) addressBookOf(w http.ResponseWriter, r *http.Request) (*storage.Collection, bool) {
	c, ok := s.collection(w, r)
	if !ok {
		return nil, false
	}
	if c.Type != storage.CollectionAddressBook {
		http.NotFound(w, r)
		return nil, false
	}
	return c, true
}

func (s *Server) newContact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.addressBookOf(w, r)
	if !ok {
		return
	}

	data, err := s.contactPage(r, c, nil, &vcard.Contact{
		Phones: []vcard.Property{{Type: "cell"}},
		Emails: []vcard.Property{{Type: "home"}},
	})
	if err != nil {
		s.fail(w, r, "new contact", err)
		return
	}
	s.render(w, r, "contact.html", data)
}

func (s *Server) createContact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.addressBookOf(w, r)
	if !ok {
		return
	}

	contact := contactFromForm(r)
	raw, err := vcard.New(contact, time.Now())
	if err != nil {
		s.rejectContact(w, r, c, nil, contact, humanise(err))
		return
	}

	uri := vcard.Filename(contact.UID)
	if uri == ".vcf" {
		uri = vcard.Filename(vcard.NewUID())
	}
	if _, err := s.storeContact(r, c, uri, raw); err != nil {
		s.fail(w, r, "create contact", err)
		return
	}

	slog.Info("admin created contact", "collection", c.URI, "uri", uri)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

func (s *Server) showContact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.addressBookOf(w, r)
	if !ok {
		return
	}

	obj, err := storage.ObjectByURI(r.Context(), s.DB, c.ID, r.PathValue("uri"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	contact, err := vcard.ReadContact(obj.Raw)
	if err != nil {
		// A card this server cannot read is still the client's data, so it is
		// reported rather than replaced with something editable.
		data, pageErr := s.collectionPage(r, c, pageData{})
		if pageErr != nil {
			s.fail(w, r, "show collection", pageErr)
			return
		}
		data.Error = fmt.Sprintf("%q cannot be edited here: %v. It is unchanged, and a DAV client can still read it.", obj.URI, err)
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "collection.html", data)
		return
	}

	data, err := s.contactPage(r, c, obj, contact)
	if err != nil {
		s.fail(w, r, "show contact", err)
		return
	}
	s.render(w, r, "contact.html", data)
}

func (s *Server) updateContact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.addressBookOf(w, r)
	if !ok {
		return
	}

	obj, err := storage.ObjectByURI(r.Context(), s.DB, c.ID, r.PathValue("uri"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	contact := contactFromForm(r)
	// The edit is applied to the stored bytes, not to a card rebuilt from the
	// form, so everything the form does not show survives.
	raw, err := vcard.Apply(obj.Raw, contact, time.Now())
	if err != nil {
		s.rejectContact(w, r, c, obj, contact, humanise(err))
		return
	}

	if _, err := s.storeContact(r, c, obj.URI, raw); err != nil {
		s.fail(w, r, "update contact", err)
		return
	}

	slog.Info("admin updated contact", "collection", c.URI, "uri", obj.URI)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

func (s *Server) confirmDeleteContact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.addressBookOf(w, r)
	if !ok {
		return
	}

	obj, err := storage.ObjectByURI(r.Context(), s.DB, c.ID, r.PathValue("uri"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	name := obj.DisplayName
	if name == "" {
		name = obj.URI
	}

	data := s.page(r, "Delete "+name, "users", pageData{
		Confirm: confirmation{
			Title:  "Delete " + name + "?",
			Body:   "This removes the contact from the address book and from every client that syncs it. It cannot be undone.",
			Verb:   "Delete this contact",
			Action: fmt.Sprintf("/admin/collections/%d/contacts/%s/delete", c.ID, obj.URI),
			Cancel: fmt.Sprintf("/admin/collections/%d", c.ID),
		},
	})
	s.render(w, r, "confirm.html", data)
}

func (s *Server) deleteContact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.addressBookOf(w, r)
	if !ok {
		return
	}

	uri := r.PathValue("uri")
	err := storage.DeleteObject(r.Context(), s.DB, c.ID, uri)
	if errors.Is(err, storage.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, "delete contact", err)
		return
	}

	slog.Info("admin deleted contact", "collection", c.URI, "uri", uri)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

// storeContact writes through the same path a DAV client uses, so the ETag,
// ctag and change log all move exactly as they would for a client write and
// syncing clients pick the edit up.
func (s *Server) storeContact(r *http.Request, c *storage.Collection, uri string, raw []byte) (*storage.Object, error) {
	contact, err := vcard.ReadContact(raw)
	if err != nil {
		return nil, err
	}
	return storage.PutObject(r.Context(), s.DB, &storage.Object{
		CollectionID:  c.ID,
		URI:           uri,
		Raw:           raw,
		UID:           contact.UID,
		ComponentType: "VCARD",
		DisplayName:   contact.DisplayName(),
	})
}

func (s *Server) contactPage(r *http.Request, c *storage.Collection, obj *storage.Object, contact *vcard.Contact) (pageData, error) {
	owner, err := storage.UserByID(r.Context(), s.DB, c.OwnerID)
	if err != nil {
		return pageData{}, err
	}

	// An empty row at the end lets a value be added without any JavaScript.
	contact.Phones = append(contact.Phones, vcard.Property{})
	contact.Emails = append(contact.Emails, vcard.Property{})

	data := pageData{
		Collection:  c,
		Subject:     owner,
		Contact:     contact,
		TypeOptions: vcard.TypeOptions,
	}

	title := "New contact"
	if obj != nil {
		data.ContactURI = obj.URI
		data.ContactAction = fmt.Sprintf("/admin/collections/%d/contacts/%s", c.ID, obj.URI)
		title = contact.DisplayName()
	} else {
		data.ContactAction = fmt.Sprintf("/admin/collections/%d/contacts", c.ID)
	}
	return s.page(r, title, "users", data), nil
}

func (s *Server) rejectContact(w http.ResponseWriter, r *http.Request, c *storage.Collection, obj *storage.Object, contact *vcard.Contact, message string) {
	data, err := s.contactPage(r, c, obj, contact)
	if err != nil {
		s.fail(w, r, "show contact", err)
		return
	}
	data.Error = message
	s.renderStatus(w, r, http.StatusUnprocessableEntity, "contact.html", data)
}

func contactFromForm(r *http.Request) *vcard.Contact {
	get := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }

	return &vcard.Contact{
		UID:           get("uid"),
		FormattedName: get("formatted_name"),
		GivenName:     get("given_name"),
		FamilyName:    get("family_name"),
		Organisation:  get("organisation"),
		Title:         get("title"),
		URL:           get("url"),
		Note:          get("note"),
		Phones:        propertiesFromForm(r, "phone"),
		Emails:        propertiesFromForm(r, "email"),
	}
}

// propertiesFromForm pairs the value and type inputs by position, dropping the
// rows left blank.
func propertiesFromForm(r *http.Request, field string) []vcard.Property {
	values := r.PostForm[field+"_value"]
	types := r.PostForm[field+"_type"]

	out := make([]vcard.Property, 0, len(values))
	for i, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p := vcard.Property{Value: v}
		if i < len(types) {
			p.Type = strings.ToLower(strings.TrimSpace(types[i]))
		}
		out = append(out, p)
	}
	return out
}

// humanise turns a package error into something worth showing on a form.
func humanise(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 && strings.HasPrefix(msg, "vcard: ") {
		msg = msg[i+2:]
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}
