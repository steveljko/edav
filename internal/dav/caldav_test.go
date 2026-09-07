package dav

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/steveljko/edav/internal/storage"
)

// A recurring event in a named zone, with a mixed-case X- property and an
// embedded VTIMEZONE, so a re-encode on the way out would be visible.
const weeklyEvent = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//edav//test//EN\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:weekly-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20260323T090000\r\n" +
	"DTEND;TZID=Europe/Berlin:20260323T100000\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=3\r\n" +
	"SUMMARY:Standup\r\n" +
	"X-ApplE-TrAvEl-DuratioN;VALUE=DURATION:PT15M\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

const marchEvent = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//edav//test//EN\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:march-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART:20260315T090000Z\r\n" +
	"DTEND:20260315T100000Z\r\n" +
	"SUMMARY:One-off in March\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

const juneEvent = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//edav//test//EN\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:june-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART:20260615T090000Z\r\n" +
	"DTEND:20260615T100000Z\r\n" +
	"SUMMARY:One-off in June\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// The zone exists only inside the object, under a name time.LoadLocation
// cannot resolve, and its observances start in 1601. This is what Outlook and
// Exchange actually send.
const outlookEvent = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Microsoft Corporation//Outlook//EN\r\n" +
	"BEGIN:VTIMEZONE\r\n" +
	"TZID:W. Europe Standard Time\r\n" +
	"BEGIN:STANDARD\r\n" +
	"DTSTART:16011028T030000\r\n" +
	"TZOFFSETFROM:+0200\r\n" +
	"TZOFFSETTO:+0100\r\n" +
	"RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10\r\n" +
	"END:STANDARD\r\n" +
	"BEGIN:DAYLIGHT\r\n" +
	"DTSTART:16010325T020000\r\n" +
	"TZOFFSETFROM:+0100\r\n" +
	"TZOFFSETTO:+0200\r\n" +
	"RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=3\r\n" +
	"END:DAYLIGHT\r\n" +
	"END:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:outlook-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART;TZID=W. Europe Standard Time:20260323T090000\r\n" +
	"DTEND;TZID=W. Europe Standard Time:20260323T100000\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=3\r\n" +
	"SUMMARY:Outlook standup\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func (h *harness) calendar(uri string) *storage.Collection {
	h.t.Helper()
	c, err := storage.CreateCollection(h.t.Context(), h.db, &storage.Collection{
		OwnerID:     h.user.ID,
		Type:        storage.CollectionCalendar,
		URI:         uri,
		DisplayName: "Work",
	})
	if err != nil {
		h.t.Fatalf("CreateCollection(%q) = %v", uri, err)
	}
	return c
}

func (h *harness) putEvent(path, event string) *http.Response {
	h.t.Helper()
	return h.do(http.MethodPut, path, event, map[string]string{"Content-Type": "text/calendar"})
}

func TestCalendarPutThenGetReturnsBytesVerbatim(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")
	const path = "/dav/calendars/alice/work/standup.ics"

	resp := h.putEvent(path, weeklyEvent)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT = %d, want 201: %s", resp.StatusCode, body(t, resp))
	}
	putETag := resp.Header.Get("ETag")

	resp = h.do(http.MethodGet, path, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d, want 200", resp.StatusCode)
	}

	got := body(t, resp)
	if got != weeklyEvent {
		t.Errorf("GET returned different bytes than were PUT:\n in: %q\nout: %q", weeklyEvent, got)
	}
	if got := resp.Header.Get("ETag"); got != putETag {
		t.Errorf("GET ETag = %q, want %q", got, putETag)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar", ct)
	}
}

func TestCalendarPutIndexesTheEvent(t *testing.T) {
	h := newHarness(t)
	c := h.calendar("work")
	h.putEvent("/dav/calendars/alice/work/standup.ics", weeklyEvent)

	obj, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "standup.ics")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}

	if !bytes.Equal(obj.Raw, []byte(weeklyEvent)) {
		t.Error("stored bytes differ from what the client sent")
	}
	if obj.UID != "weekly-1" {
		t.Errorf("UID = %q, want weekly-1", obj.UID)
	}
	if obj.ComponentType != "VEVENT" {
		t.Errorf("ComponentType = %q, want VEVENT", obj.ComponentType)
	}
	if obj.DisplayName != "Standup" {
		t.Errorf("DisplayName = %q, want Standup", obj.DisplayName)
	}
	if !obj.Recurring {
		t.Error("Recurring = false, want true")
	}
	// 09:00 Berlin on 23 March is 08:00Z; the series ends three weeks later,
	// after the clocks change, so the last instance ends at 08:00Z.
	if obj.StartAt == nil || obj.StartAt.UTC().Format("2006-01-02T15:04:05Z") != "2026-03-23T08:00:00Z" {
		t.Errorf("StartAt = %v, want 2026-03-23T08:00:00Z", obj.StartAt)
	}
	if obj.EndAt == nil || obj.EndAt.UTC().Format("2006-01-02T15:04:05Z") != "2026-04-06T08:00:00Z" {
		t.Errorf("EndAt = %v, want 2026-04-06T08:00:00Z", obj.EndAt)
	}
}

func TestCalendarPutRejectsMalformed(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")

	tests := []struct {
		name  string
		event string
	}{
		{"not a calendar", "hello"},
		{
			"no UID",
			"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\n" +
				"DTSTAMP:20260101T000000Z\r\nDTSTART:20260315T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.putEvent("/dav/calendars/alice/work/bad.ics", tt.event)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", resp.StatusCode, body(t, resp))
			}
		})
	}
}

func TestCalendarQueryTimeRange(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")
	h.putEvent("/dav/calendars/alice/work/march.ics", marchEvent)
	h.putEvent("/dav/calendars/alice/work/june.ics", juneEvent)
	h.putEvent("/dav/calendars/alice/work/standup.ics", weeklyEvent)

	query := func(start, end string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/><C:calendar-data/></D:prop>
  <C:filter>
    <C:comp-filter name="VCALENDAR">
      <C:comp-filter name="VEVENT">
        <C:time-range start="` + start + `" end="` + end + `"/>
      </C:comp-filter>
    </C:comp-filter>
  </C:filter>
</C:calendar-query>`
	}

	tests := []struct {
		name       string
		start, end string
		want       []string
		absent     []string
	}{
		{
			name: "march only", start: "20260301T000000Z", end: "20260401T000000Z",
			want: []string{"march.ics", "standup.ics"}, absent: []string{"june.ics"},
		},
		{
			name: "june only", start: "20260601T000000Z", end: "20260701T000000Z",
			want: []string{"june.ics"}, absent: []string{"march.ics", "standup.ics"},
		},
		{
			// Inside the recurring series' overall span, but between two
			// occurrences: the SQL prefilter keeps it, expansion rejects it.
			name: "gap between occurrences", start: "20260325T000000Z", end: "20260327T000000Z",
			want: nil, absent: []string{"march.ics", "june.ics", "standup.ics"},
		},
		{
			name: "a later occurrence of the series", start: "20260406T000000Z", end: "20260407T000000Z",
			want: []string{"standup.ics"}, absent: []string{"march.ics", "june.ics"},
		},
		{
			name: "whole year", start: "20260101T000000Z", end: "20270101T000000Z",
			want: []string{"march.ics", "june.ics", "standup.ics"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.do("REPORT", "/dav/calendars/alice/work/", query(tt.start, tt.end),
				map[string]string{"Content-Type": "application/xml", "Depth": "1"})
			if resp.StatusCode != http.StatusMultiStatus {
				t.Fatalf("status = %d, want 207: %s", resp.StatusCode, body(t, resp))
			}

			got := body(t, resp)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("range %s..%s missed %s:\n%s", tt.start, tt.end, want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("range %s..%s wrongly matched %s:\n%s", tt.start, tt.end, absent, got)
				}
			}
		})
	}
}

func TestCalendarMultigetReturnsBytesVerbatim(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")
	h.putEvent("/dav/calendars/alice/work/standup.ics", weeklyEvent)

	const report = `<?xml version="1.0" encoding="UTF-8"?>
<C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/><C:calendar-data/></D:prop>
  <D:href>/dav/calendars/alice/work/standup.ics</D:href>
</C:calendar-multiget>`

	resp := h.do("REPORT", "/dav/calendars/alice/work/", report,
		map[string]string{"Content-Type": "application/xml", "Depth": "1"})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207: %s", resp.StatusCode, body(t, resp))
	}

	got := body(t, resp)
	for _, want := range []string{
		"X-ApplE-TrAvEl-DuratioN",
		"DTSTART;TZID=Europe/Berlin:20260323T090000",
		"RRULE:FREQ=WEEKLY;COUNT=3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("multiget lost %q:\n%s", want, got)
		}
	}
}

func TestWellKnownCalDAVRedirectsWithoutAuth(t *testing.T) {
	h := newHarness(t)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(h.server.URL + "/.well-known/caldav")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/dav/" {
		t.Errorf("Location = %q, want /dav/", got)
	}
}

// A principal is one resource whichever handler answers for it. A client that
// finds only one home set there never discovers the other protocol.
func TestPrincipalAdvertisesBothHomeSets(t *testing.T) {
	h := newHarness(t)

	const propfind = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"
            xmlns:A="urn:ietf:params:xml:ns:carddav">
  <D:prop>
    <D:current-user-principal/>
    <C:calendar-home-set/>
    <A:addressbook-home-set/>
  </D:prop>
</D:propfind>`

	resp := h.do("PROPFIND", "/dav/principals/alice/", propfind,
		map[string]string{"Content-Type": "application/xml", "Depth": "0"})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", resp.StatusCode)
	}

	got := body(t, resp)
	for _, want := range []string{
		"/dav/principals/alice/",
		"calendar-home-set",
		"/dav/calendars/alice/",
		"addressbook-home-set",
		"/dav/addressbooks/alice/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("principal response does not mention %q:\n%s", want, got)
		}
	}
}

func TestCalendarMkcolAndDelete(t *testing.T) {
	h := newHarness(t)

	const mkcol = `<?xml version="1.0" encoding="UTF-8"?>
<D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:set><D:prop>
    <D:resourcetype><D:collection/><C:calendar/></D:resourcetype>
    <D:displayname>Personal</D:displayname>
  </D:prop></D:set>
</D:mkcol>`

	resp := h.do("MKCOL", "/dav/calendars/alice/personal/", mkcol,
		map[string]string{"Content-Type": "application/xml"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("MKCOL = %d, want 201: %s", resp.StatusCode, body(t, resp))
	}

	c, err := storage.CollectionByURI(t.Context(), h.db, h.user.ID, "personal")
	if err != nil {
		t.Fatalf("CollectionByURI() = %v", err)
	}
	if c.Type != storage.CollectionCalendar || c.DisplayName != "Personal" {
		t.Errorf("created collection = %+v", c)
	}

	if resp := h.do(http.MethodDelete, "/dav/calendars/alice/personal/", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", resp.StatusCode)
	}
}

func TestCalendarAndAddressBookNamespacesAreSeparate(t *testing.T) {
	h := newHarness(t)
	h.calendar("shared")
	h.addressBook("contacts")

	// A calendar slug must not resolve as an address book, or vice versa.
	if resp := h.do(http.MethodGet, "/dav/addressbooks/alice/shared/x.vcf", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("calendar reached through the address book tree = %d, want 404", resp.StatusCode)
	}
	if resp := h.putEvent("/dav/calendars/alice/contacts/x.ics", marchEvent); resp.StatusCode != http.StatusNotFound {
		t.Errorf("address book reached through the calendar tree = %d, want 404", resp.StatusCode)
	}
}

func TestAnotherUsersCalendarIsForbidden(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")

	for _, path := range []string{
		"/dav/calendars/bob/work/",
		"/dav/calendars/bob/work/secret.ics",
	} {
		resp := h.do(http.MethodGet, path, "", nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", path, resp.StatusCode)
		}
	}
}

// A calendar-query over an object whose zone is defined only by an embedded
// VTIMEZONE must not fall back to a lookup that can only see IANA names.
func TestCalendarQueryWithEmbeddedTimezone(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")

	if resp := h.putEvent("/dav/calendars/alice/work/outlook.ics", outlookEvent); resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT = %d, want 201: %s", resp.StatusCode, body(t, resp))
	}

	query := func(start, end string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/></D:prop>
  <C:filter>
    <C:comp-filter name="VCALENDAR">
      <C:comp-filter name="VEVENT">
        <C:time-range start="` + start + `" end="` + end + `"/>
      </C:comp-filter>
    </C:comp-filter>
  </C:filter>
</C:calendar-query>`
	}

	tests := []struct {
		name       string
		start, end string
		want       bool
	}{
		{"first occurrence", "20260323T000000Z", "20260324T000000Z", true},
		{"gap between occurrences", "20260325T000000Z", "20260326T000000Z", false},
		{"third occurrence, after the clocks change", "20260406T000000Z", "20260407T000000Z", true},
		{"after the series ends", "20260501T000000Z", "20260502T000000Z", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.do("REPORT", "/dav/calendars/alice/work/", query(tt.start, tt.end),
				map[string]string{"Content-Type": "application/xml", "Depth": "1"})
			if resp.StatusCode != http.StatusMultiStatus {
				t.Fatalf("status = %d, want 207: %s", resp.StatusCode, body(t, resp))
			}
			if got := strings.Contains(body(t, resp), "outlook.ics"); got != tt.want {
				t.Errorf("matched = %v, want %v", got, tt.want)
			}
		})
	}
}

// A calendar's default timezone is what a client applies to a floating time.
// It was stored and editable but never served, so clients fell back to their
// own guess.
func TestCalendarTimezoneIsServed(t *testing.T) {
	h := newHarness(t)
	c := h.calendar("work")

	const tz = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//edav//EN\r\n" +
		"BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nEND:VTIMEZONE\r\nEND:VCALENDAR\r\n"

	c.Timezone = tz
	if err := storage.UpdateCollection(t.Context(), h.db, c); err != nil {
		t.Fatalf("UpdateCollection() = %v", err)
	}

	const propfind = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><C:calendar-timezone/></D:prop>
</D:propfind>`

	resp := h.do("PROPFIND", "/dav/calendars/alice/work/", propfind,
		map[string]string{"Content-Type": "application/xml", "Depth": "0"})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", resp.StatusCode)
	}

	got := body(t, resp)
	if !strings.Contains(got, "calendar-timezone") {
		t.Errorf("the property was not returned:\n%s", got)
	}
	if !strings.Contains(got, "TZID:Europe/Berlin") {
		t.Errorf("the timezone body was not returned:\n%s", got)
	}
}

// A calendar without one must not claim an empty timezone.
func TestCalendarWithoutTimezoneOmitsTheProperty(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")

	const propfind = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><C:calendar-timezone/></D:prop>
</D:propfind>`

	resp := h.do("PROPFIND", "/dav/calendars/alice/work/", propfind,
		map[string]string{"Content-Type": "application/xml", "Depth": "0"})

	got := body(t, resp)
	if !strings.Contains(got, "404") {
		t.Errorf("an absent timezone was not reported as not found:\n%s", got)
	}
}
