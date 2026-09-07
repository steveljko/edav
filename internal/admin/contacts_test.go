package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/steveljko/edav/internal/storage"
	"github.com/steveljko/edav/internal/vcard"
)

// A card as a phone would have written it, with parts no admin form models.
const phoneCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"PRODID:-//Apple Inc.//iPhone OS 17.0//EN\r\n" +
	"N:Lovelace;Ada;;;\r\n" +
	"FN:Ada Lovelace\r\n" +
	"TEL;type=CELL;type=VOICE;type=pref:+15551234567\r\n" +
	"EMAIL;type=INTERNET;type=HOME;type=pref:ada@example.com\r\n" +
	"item1.URL;type=pref:https://example.com/ada\r\n" +
	"item1.X-ABLabel:_$!<HomePage>!$_\r\n" +
	"X-SOCIALPROFILE;type=twitter:https://twitter.com/ada\r\n" +
	"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD\r\n" +
	"UID:ada-1\r\n" +
	"END:VCARD\r\n"

func (h *harness) addressBook(uri string) *storage.Collection {
	h.t.Helper()
	c, err := storage.CreateCollection(h.t.Context(), h.db, &storage.Collection{
		OwnerID: h.admin.ID, Type: storage.CollectionAddressBook, URI: uri, DisplayName: "Contacts",
	})
	if err != nil {
		h.t.Fatalf("CreateCollection(%q) = %v", uri, err)
	}
	return c
}

func (h *harness) storeCard(c *storage.Collection, uri, raw string) *storage.Object {
	h.t.Helper()
	contact, err := vcard.ReadContact([]byte(raw))
	if err != nil {
		h.t.Fatalf("ReadContact() = %v", err)
	}
	obj, err := storage.PutObject(h.t.Context(), h.db, &storage.Object{
		CollectionID: c.ID, URI: uri, Raw: []byte(raw),
		UID: contact.UID, ComponentType: "VCARD", DisplayName: contact.DisplayName(),
	})
	if err != nil {
		h.t.Fatalf("PutObject() = %v", err)
	}
	return obj
}

func (h *harness) card(c *storage.Collection, uri string) string {
	h.t.Helper()
	obj, err := storage.ObjectByURI(h.t.Context(), h.db, c.ID, uri)
	if err != nil {
		h.t.Fatalf("ObjectByURI(%q) = %v", uri, err)
	}
	return string(obj.Raw)
}

func TestContactListing(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)))
	for _, want := range []string{"Ada Lovelace", "ada.vcf", "Add a contact"} {
		if !strings.Contains(got, want) {
			t.Errorf("collection page does not show %q:\n%s", want, got)
		}
	}
}

// A calendar has no contact pages, and its routes must not resolve.
func TestContactRoutesRejectCalendars(t *testing.T) {
	h := newHarness(t)
	h.login()
	cal, err := storage.CreateCollection(t.Context(), h.db, &storage.Collection{
		OwnerID: h.admin.ID, Type: storage.CollectionCalendar, URI: "work",
	})
	if err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	if resp := h.get("/admin/collections/" + itoa(cal.ID) + "/contacts/new"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if got := body(t, h.get("/admin/collections/"+itoa(cal.ID))); strings.Contains(got, "Add a contact") {
		t.Error("a calendar page offers contact creation")
	}
}

func TestCreateContact(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	page := "/admin/collections/" + itoa(c.ID)

	resp := h.post(page+"/contacts", h.form(page+"/contacts/new", url.Values{
		"given_name":   {"Grace"},
		"family_name":  {"Hopper"},
		"organisation": {"US Navy"},
		"phone_value":  {"+15557654321", ""},
		"phone_type":   {"cell", "home"},
		"email_value":  {"grace@example.com"},
		"email_type":   {"work"},
		"note":         {"Coined the term debugging"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	objects, err := storage.ListObjects(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("ListObjects() = %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("objects = %d, want 1", len(objects))
	}

	obj := objects[0]
	if obj.DisplayName != "Grace Hopper" {
		t.Errorf("indexed name = %q", obj.DisplayName)
	}
	if obj.UID == "" || obj.ComponentType != "VCARD" {
		t.Errorf("index columns = %+v", obj)
	}
	if !strings.HasSuffix(obj.URI, ".vcf") {
		t.Errorf("URI = %q, want a .vcf name", obj.URI)
	}

	raw := string(obj.Raw)
	for _, want := range []string{
		"FN:Grace Hopper\r\n",
		"N:Hopper;Grace;;;\r\n",
		"ORG:US Navy\r\n",
		"TEL;TYPE=CELL:+15557654321\r\n",
		"EMAIL;TYPE=WORK:grace@example.com\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("stored card missing %q:\n%s", want, raw)
		}
	}
	// The blank second phone row must not have become a property.
	if strings.Count(raw, "TEL") != 1 {
		t.Errorf("a blank row became a property:\n%s", raw)
	}
}

func TestCreateContactRequiresAName(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	page := "/admin/collections/" + itoa(c.ID)

	resp := h.post(page+"/contacts", h.form(page+"/contacts/new", url.Values{
		"note": {"nobody"},
	}))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, "needs at least a name") {
		t.Errorf("the page does not explain the problem:\n%s", got)
	}
}

// The reason this feature was built the way it was.
func TestEditingAContactPreservesEverythingElse(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)
	page := "/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf"

	resp := h.post(page, h.form(page, url.Values{
		"uid":            {"ada-1"},
		"given_name":     {"Ada"},
		"family_name":    {"Byron"},
		"formatted_name": {"Ada L. Byron"},
		"phone_value":    {"+15551234567"},
		"phone_type":     {"cell"},
		"email_value":    {"ada@example.com"},
		"email_type":     {"home"},
		// A browser submits every rendered field, including the ones the
		// person did not touch.
		"url": {"https://example.com/ada"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	raw := h.card(c, "ada.vcf")

	if !strings.Contains(raw, "FN:Ada L. Byron\r\n") {
		t.Errorf("the name was not updated:\n%s", raw)
	}
	if !strings.Contains(raw, "N:Byron;Ada;;;\r\n") {
		t.Errorf("the structured name was not updated:\n%s", raw)
	}

	for _, want := range []string{
		"PRODID:-//Apple Inc.//iPhone OS 17.0//EN\r\n",
		"item1.URL;type=pref:https://example.com/ada\r\n",
		"item1.X-ABLabel:_$!<HomePage>!$_\r\n",
		"X-SOCIALPROFILE;type=twitter:https://twitter.com/ada\r\n",
		"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD\r\n",
		"UID:ada-1\r\n",
		// The phone kept all three of its original type parameters, because
		// its value did not change.
		"TEL;type=CELL;type=VOICE;type=pref:+15551234567\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("editing the name lost %q:\n%s", want, raw)
		}
	}
}

func TestEditingBumpsETagAndCTag(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	before := h.storeCard(c, "ada.vcf", phoneCard)

	beforeCollection, err := storage.CollectionByID(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}

	page := "/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf"
	h.post(page, h.form(page, url.Values{
		"uid":            {"ada-1"},
		"formatted_name": {"Ada Renamed"},
		"given_name":     {"Ada"},
		"family_name":    {"Lovelace"},
	}))

	after, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "ada.vcf")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if after.ETag == before.ETag {
		t.Error("the ETag did not change after an edit")
	}
	if after.DisplayName != "Ada Renamed" {
		t.Errorf("the indexed name was not updated: %q", after.DisplayName)
	}

	afterCollection, err := storage.CollectionByID(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}
	if afterCollection.CTag == beforeCollection.CTag {
		t.Error("the collection ctag did not change, so clients would not resync")
	}
}

func TestDeleteContact(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)
	page := "/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf"

	confirm := h.get(page + "/delete")
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirmation page = %d, want 200", confirm.StatusCode)
	}
	if got := body(t, confirm); !strings.Contains(got, "Delete Ada Lovelace?") {
		t.Errorf("confirmation page is wrong:\n%s", got)
	}

	resp := h.post(page+"/delete", h.form(page, nil))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}
	if _, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "ada.vcf"); err == nil {
		t.Error("the contact survived deletion")
	}
}

func TestContactMutationsRequireCSRFToken(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)
	base := "/admin/collections/" + itoa(c.ID)

	tests := []struct {
		name string
		path string
	}{
		{"create", base + "/contacts"},
		{"update", base + "/contacts/ada.vcf"},
		{"delete", base + "/contacts/ada.vcf/delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.post(tt.path, url.Values{"formatted_name": {"x"}})
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
}

// A card this server cannot parse must be reported, not replaced with an empty
// editable one.
func TestUneditableCardIsReportedNotDestroyed(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")

	const broken = "BEGIN:VCARD\r\nUID:broken-1\r\nFN:No Version\r\nEND:VCARD\r\n"
	if _, err := storage.PutObject(t.Context(), h.db, &storage.Object{
		CollectionID: c.ID, URI: "broken.vcf", Raw: []byte(broken),
		UID: "broken-1", ComponentType: "VCARD", DisplayName: "No Version",
	}); err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	resp := h.get("/admin/collections/" + itoa(c.ID) + "/contacts/broken.vcf")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, "cannot be edited here") {
		t.Errorf("the page does not explain the problem:\n%s", got)
	}
	if h.card(c, "broken.vcf") != broken {
		t.Error("the unreadable card was altered by opening it")
	}
}

// Opening the edit form must not write anything at all.
func TestOpeningTheEditFormDoesNotWrite(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	before := h.storeCard(c, "ada.vcf", phoneCard)

	if resp := h.get("/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	after, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "ada.vcf")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if after.ETag != before.ETag {
		t.Error("opening the edit form changed the card")
	}
	if h.card(c, "ada.vcf") != phoneCard {
		t.Error("opening the edit form rewrote the stored bytes")
	}
}

// Clearing a field removes the property, and a grouped property takes its
// label with it rather than leaving one behind.
func TestClearingAFieldRemovesThePropertyAndItsLabel(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)
	page := "/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf"

	h.post(page, h.form(page, url.Values{
		"uid":            {"ada-1"},
		"formatted_name": {"Ada Lovelace"},
		"given_name":     {"Ada"},
		"family_name":    {"Lovelace"},
		"url":            {""},
	}))

	raw := h.card(c, "ada.vcf")
	if strings.Contains(raw, "item1.URL") {
		t.Errorf("the cleared URL was kept:\n%s", raw)
	}
	if strings.Contains(raw, "X-ABLabel") {
		t.Errorf("the label of the removed URL was orphaned:\n%s", raw)
	}
	if !strings.Contains(raw, "PHOTO;ENCODING=b") {
		t.Errorf("clearing a field removed something unrelated:\n%s", raw)
	}
}

// A large address book must not render as one enormous document.
func TestContactListPaginates(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")

	for i := range contactsPerPage + 25 {
		name := fmt.Sprintf("Person %03d", i)
		card := strings.Replace(phoneCard, "FN:Ada Lovelace", "FN:"+name, 1)
		card = strings.Replace(card, "UID:ada-1", fmt.Sprintf("UID:p-%03d", i), 1)
		h.storeCard(c, fmt.Sprintf("p%03d.vcf", i), card)
	}

	page := "/admin/collections/" + itoa(c.ID)
	first := body(t, h.get(page))

	if n := strings.Count(first, `<td class="subject">`); n != contactsPerPage {
		t.Errorf("first page rendered %d rows, want %d", n, contactsPerPage)
	}
	if !strings.Contains(first, "Page 1 of 2") {
		t.Errorf("no pager on an over-long list:\n%s", first[:min(len(first), 2000)])
	}
	if !strings.Contains(first, "page=2") {
		t.Error("no link to the next page")
	}

	second := body(t, h.get(page+"?page=2"))
	if n := strings.Count(second, `<td class="subject">`); n != 25 {
		t.Errorf("second page rendered %d rows, want 25", n)
	}
	if !strings.Contains(second, "page=1") {
		t.Error("no link back to the first page")
	}
}

// The search box has to reach the whole collection, not only the page in view.
func TestContactSearchIsServerSide(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")

	for i := range contactsPerPage + 10 {
		name := fmt.Sprintf("Person %03d", i)
		card := strings.Replace(phoneCard, "FN:Ada Lovelace", "FN:"+name, 1)
		card = strings.Replace(card, "UID:ada-1", fmt.Sprintf("UID:p-%03d", i), 1)
		h.storeCard(c, fmt.Sprintf("p%03d.vcf", i), card)
	}

	// A name that only exists past the first page.
	page := "/admin/collections/" + itoa(c.ID)
	if strings.Contains(body(t, h.get(page)), "Person 205") {
		t.Fatal("the test name is on the first page, so it proves nothing")
	}

	got := body(t, h.get(page+"?q=Person+205"))
	if !strings.Contains(got, "Person 205") {
		t.Error("search did not reach past the first page")
	}
	if n := strings.Count(got, `<td class="subject">`); n != 1 {
		t.Errorf("search returned %d rows, want 1", n)
	}
}

func TestContactSearchWithNoMatchesExplainsItself(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)+"?q=nobody"))
	if !strings.Contains(got, "Nothing matches") {
		t.Errorf("an empty result does not explain itself:\n%s", got)
	}
	if !strings.Contains(got, "show everything") {
		t.Error("no way back to the unfiltered list")
	}
}

// Searching must work with scripting off, which means a real form submission.
func TestContactSearchWorksWithoutScripting(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)))
	if !strings.Contains(got, `<form method="get"`) {
		t.Error("the search box is not a form, so it needs scripting to work")
	}
	if !strings.Contains(got, `name="q"`) {
		t.Error("the search input has no name to submit under")
	}
}
