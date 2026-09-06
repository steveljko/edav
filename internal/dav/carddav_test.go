package dav

import (
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/storage"
)

// Deliberately awkward: unsorted properties, a mixed-case X- name and an
// unescaped comma, all of which a parse-and-re-encode round trip would change.
const adaCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"UID:ada-1\r\n" +
	"FN:Ada Lovelace\r\n" +
	"N:Lovelace;Ada;;;\r\n" +
	"X-ABLabel:my label\r\n" +
	"TEL;TYPE=CELL:+15551234567\r\n" +
	"X-APPLE-STRUCTURED-LOCATION;VALUE=URI:geo:1,2\r\n" +
	"END:VCARD\r\n"

const graceCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"UID:grace-1\r\n" +
	"FN:Grace Hopper\r\n" +
	"N:Hopper;Grace;;;\r\n" +
	"END:VCARD\r\n"

type harness struct {
	t      *testing.T
	db     *sql.DB
	server *httptest.Server
	user   *storage.User
}

const testPassword = "hunter2hunter2"

func newHarness(t *testing.T) *harness {
	t.Helper()

	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}
	u, err := storage.CreateUser(t.Context(), db, &storage.User{Username: "alice", PasswordHash: hash})
	if err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}

	mux := http.NewServeMux()
	(&Server{DB: db, Prefix: "/dav", CardDAVEnabled: true, CalDAVEnabled: true}).Register(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &harness{t: t, db: db, server: srv, user: u}
}

func (h *harness) addressBook(uri string) *storage.Collection {
	h.t.Helper()
	c, err := storage.CreateCollection(h.t.Context(), h.db, &storage.Collection{
		OwnerID:     h.user.ID,
		Type:        storage.CollectionAddressBook,
		URI:         uri,
		DisplayName: "Contacts",
	})
	if err != nil {
		h.t.Fatalf("CreateCollection(%q) = %v", uri, err)
	}
	return c
}

func (h *harness) do(method, path, body string, headers map[string]string) *http.Response {
	h.t.Helper()

	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.server.URL+path, r)
	if err != nil {
		h.t.Fatalf("NewRequest: %v", err)
	}
	req.SetBasicAuth("alice", testPassword)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) put(path, card string) *http.Response {
	h.t.Helper()
	return h.do(http.MethodPut, path, card, map[string]string{"Content-Type": "text/vcard"})
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestPutThenGetReturnsBytesVerbatim(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	const path = "/dav/addressbooks/alice/contacts/ada.vcf"

	resp := h.put(path, adaCard)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d: %s", resp.StatusCode, http.StatusCreated, body(t, resp))
	}
	putETag := resp.Header.Get("ETag")
	if putETag == "" {
		t.Error("PUT returned no ETag")
	}

	resp = h.do(http.MethodGet, path, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}

	got := body(t, resp)
	if got != adaCard {
		t.Errorf("GET returned different bytes than were PUT:\n in: %q\nout: %q", adaCard, got)
	}
	if got := resp.Header.Get("ETag"); got != putETag {
		t.Errorf("GET ETag = %q, want %q from the PUT", got, putETag)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/vcard") {
		t.Errorf("Content-Type = %q, want text/vcard", ct)
	}
}

func TestPutIsIdempotentForETag(t *testing.T) {
	h := newHarness(t)
	c := h.addressBook("contacts")
	const path = "/dav/addressbooks/alice/contacts/ada.vcf"

	first := h.put(path, adaCard).Header.Get("ETag")

	before, err := storage.CollectionByID(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}

	if got := h.put(path, adaCard).Header.Get("ETag"); got != first {
		t.Errorf("ETag changed on an identical rewrite: %q then %q", first, got)
	}
	after, err := storage.CollectionByID(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}
	if after.CTag != before.CTag {
		t.Errorf("ctag changed on an identical rewrite: %q then %q", before.CTag, after.CTag)
	}

	if got := h.put(path, adaCard+"NOTE:changed\r\n").Header.Get("ETag"); got == first {
		t.Error("ETag did not change when the card did")
	}
}

func TestPutRejectsMalformedCard(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")

	tests := []struct {
		name string
		card string
	}{
		{"not a vcard", "hello"},
		{"no UID", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Nobody\r\nEND:VCARD\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.put("/dav/addressbooks/alice/contacts/bad.vcf", tt.card)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestPutPreconditions(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	const path = "/dav/addressbooks/alice/contacts/ada.vcf"

	resp := h.do(http.MethodPut, path, adaCard, map[string]string{
		"Content-Type":  "text/vcard",
		"If-None-Match": "*",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first If-None-Match PUT = %d, want 201", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")

	resp = h.do(http.MethodPut, path, graceCard, map[string]string{
		"Content-Type":  "text/vcard",
		"If-None-Match": "*",
	})
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("If-None-Match on an existing resource = %d, want 412", resp.StatusCode)
	}

	resp = h.do(http.MethodPut, path, graceCard, map[string]string{
		"Content-Type": "text/vcard",
		"If-Match":     `"not-the-etag"`,
	})
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("If-Match with a stale ETag = %d, want 412", resp.StatusCode)
	}

	resp = h.do(http.MethodPut, path, graceCard, map[string]string{
		"Content-Type": "text/vcard",
		"If-Match":     etag,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("If-Match with the current ETag = %d, want 201", resp.StatusCode)
	}
}

func TestDeleteAddressObject(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	const path = "/dav/addressbooks/alice/contacts/ada.vcf"

	h.put(path, adaCard)

	if resp := h.do(http.MethodDelete, path, "", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", resp.StatusCode)
	}
	if resp := h.do(http.MethodGet, path, "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", resp.StatusCode)
	}
	if resp := h.do(http.MethodDelete, path, "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("second DELETE = %d, want 404", resp.StatusCode)
	}
}

func TestWellKnownRedirectsWithoutAuth(t *testing.T) {
	h := newHarness(t)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(h.server.URL + "/.well-known/carddav")
	if err != nil {
		t.Fatalf("GET well-known: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/dav/" {
		t.Errorf("Location = %q, want /dav/", got)
	}
}

func TestDAVEndpointRequiresAuth(t *testing.T) {
	h := newHarness(t)

	resp, err := h.server.Client().Get(h.server.URL + "/dav/addressbooks/alice/contacts/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("no WWW-Authenticate challenge")
	}
}

func TestAnotherUsersTreeIsForbidden(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}
	bob, err := storage.CreateUser(t.Context(), h.db, &storage.User{Username: "bob", PasswordHash: hash})
	if err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}
	if _, err := storage.CreateCollection(t.Context(), h.db, &storage.Collection{
		OwnerID: bob.ID, Type: storage.CollectionAddressBook, URI: "private",
	}); err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	// Authenticated as alice, reaching into bob's tree.
	for _, path := range []string{
		"/dav/addressbooks/bob/private/",
		"/dav/addressbooks/bob/private/secret.vcf",
		"/dav/principals/bob/",
	} {
		resp := h.do(http.MethodGet, path, "", nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", path, resp.StatusCode)
		}
	}
}

func TestPropfindPrincipalDiscovery(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")

	const propfind = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop>
    <D:current-user-principal/>
    <C:addressbook-home-set/>
  </D:prop>
</D:propfind>`

	resp := h.do("PROPFIND", "/dav/principals/alice/", propfind, map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "0",
	})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", resp.StatusCode)
	}

	got := body(t, resp)
	for _, want := range []string{
		"<current-user-principal",
		"/dav/principals/alice/",
		"addressbook-home-set",
		"/dav/addressbooks/alice/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PROPFIND response does not mention %q:\n%s", want, got)
		}
	}
}

func TestPropfindListsAddressBookMembers(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	const propfind = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop><D:getetag/><D:getcontenttype/></D:prop>
</D:propfind>`

	resp := h.do("PROPFIND", "/dav/addressbooks/alice/contacts/", propfind, map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "1",
	})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", resp.StatusCode)
	}

	got := body(t, resp)
	for _, want := range []string{"ada.vcf", "grace.vcf", "getetag"} {
		if !strings.Contains(got, want) {
			t.Errorf("PROPFIND response does not mention %q:\n%s", want, got)
		}
	}
}

func TestMultigetReturnsBytesVerbatim(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	const report = `<?xml version="1.0" encoding="UTF-8"?>
<C:addressbook-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop><D:getetag/><C:address-data/></D:prop>
  <D:href>/dav/addressbooks/alice/contacts/ada.vcf</D:href>
</C:addressbook-multiget>`

	resp := h.do("REPORT", "/dav/addressbooks/alice/contacts/", report, map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "1",
	})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207: %s", resp.StatusCode, body(t, resp))
	}

	got := body(t, resp)
	// The card is XML-escaped in the response, but the parts a re-encode would
	// have mangled must survive intact.
	for _, want := range []string{"X-ABLabel:my label", "geo:1,2", "UID:ada-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("multiget response lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "X-ABLABEL") {
		t.Error("multiget response upper-cased X-ABLabel")
	}
	if strings.Contains(got, `geo:1\,2`) {
		t.Error("multiget response re-escaped the comma in geo:1,2")
	}
}

func TestAddressbookQueryFiltersByProperty(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	const report = `<?xml version="1.0" encoding="UTF-8"?>
<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop><D:getetag/><C:address-data/></D:prop>
  <C:filter>
    <C:prop-filter name="FN">
      <C:text-match collation="i;unicode-casemap" match-type="contains">Lovelace</C:text-match>
    </C:prop-filter>
  </C:filter>
</C:addressbook-query>`

	resp := h.do("REPORT", "/dav/addressbooks/alice/contacts/", report, map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "1",
	})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207: %s", resp.StatusCode, body(t, resp))
	}

	got := body(t, resp)
	if !strings.Contains(got, "ada.vcf") {
		t.Errorf("query did not match Ada:\n%s", got)
	}
	if strings.Contains(got, "grace.vcf") {
		t.Errorf("query matched Grace, who does not contain Lovelace:\n%s", got)
	}
}

func TestMkcolAndDeleteAddressBook(t *testing.T) {
	h := newHarness(t)

	const mkcol = `<?xml version="1.0" encoding="UTF-8"?>
<D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:set><D:prop>
    <D:resourcetype><D:collection/><C:addressbook/></D:resourcetype>
    <D:displayname>Work Contacts</D:displayname>
  </D:prop></D:set>
</D:mkcol>`

	resp := h.do("MKCOL", "/dav/addressbooks/alice/work/", mkcol, map[string]string{
		"Content-Type": "application/xml",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("MKCOL = %d, want 201: %s", resp.StatusCode, body(t, resp))
	}

	c, err := storage.CollectionByURI(t.Context(), h.db, h.user.ID, "work")
	if err != nil {
		t.Fatalf("CollectionByURI() = %v", err)
	}
	if c.DisplayName != "Work Contacts" || c.Type != storage.CollectionAddressBook {
		t.Errorf("created collection = %+v", c)
	}

	if resp := h.do(http.MethodDelete, "/dav/addressbooks/alice/work/", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", resp.StatusCode)
	}
	if _, err := storage.CollectionByURI(t.Context(), h.db, h.user.ID, "work"); err == nil {
		t.Error("collection survived DELETE")
	}
}

func TestStoredBytesAreNeverRewritten(t *testing.T) {
	h := newHarness(t)
	c := h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	obj, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "ada.vcf")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if !bytes.Equal(obj.Raw, []byte(adaCard)) {
		t.Errorf("stored bytes differ from what the client sent:\n in: %q\nout: %q", adaCard, obj.Raw)
	}
	if obj.UID != "ada-1" {
		t.Errorf("indexed UID = %q, want ada-1", obj.UID)
	}
	if obj.DisplayName != "Ada Lovelace" {
		t.Errorf("indexed FN = %q, want Ada Lovelace", obj.DisplayName)
	}
	if obj.ComponentType != "VCARD" {
		t.Errorf("component type = %q, want VCARD", obj.ComponentType)
	}
}

func TestUnknownCollectionIsNotFound(t *testing.T) {
	h := newHarness(t)

	resp := h.do(http.MethodGet, "/dav/addressbooks/alice/nosuch/ada.vcf", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
