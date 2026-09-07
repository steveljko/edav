package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/steveljko/edav/internal/dav"
	"github.com/steveljko/edav/internal/storage"
)

// An event as a phone would have stored it, carrying an alarm, a guest and a
// repeat that no form models.
const phoneEvent = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Apple Inc.//iOS 17.0//EN\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:evt-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20260401T090000\r\n" +
	"DTEND;TZID=Europe/Berlin:20260401T100000\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=6\r\n" +
	"SUMMARY:Standup\r\n" +
	"LOCATION:Room 3\r\n" +
	"SEQUENCE:2\r\n" +
	"ATTENDEE;CN=Ada:mailto:ada@example.com\r\n" +
	"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:Reminder\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func (h *harness) calendar(uri string) *storage.Collection {
	h.t.Helper()
	c, err := storage.CreateCollection(h.t.Context(), h.db, &storage.Collection{
		OwnerID: h.admin.ID, Type: storage.CollectionCalendar, URI: uri, DisplayName: "Work",
	})
	if err != nil {
		h.t.Fatalf("CreateCollection(%q) = %v", uri, err)
	}
	return c
}

func (h *harness) storeEvent(c *storage.Collection, uri, raw string) *storage.Object {
	h.t.Helper()

	bounds, err := dav.CalendarIndex([]byte(raw))
	if err != nil {
		h.t.Fatalf("CalendarIndex() = %v", err)
	}
	obj, err := storage.PutObject(h.t.Context(), h.db, &storage.Object{
		CollectionID: c.ID, URI: uri, Raw: []byte(raw),
		UID: bounds.UID, ComponentType: bounds.ComponentType, DisplayName: bounds.Summary,
		StartAt: bounds.Start, EndAt: bounds.End, Recurring: bounds.Recurring,
	})
	if err != nil {
		h.t.Fatalf("PutObject() = %v", err)
	}
	return obj
}

func (h *harness) object(c *storage.Collection, uri string) string {
	h.t.Helper()
	obj, err := storage.ObjectByURI(h.t.Context(), h.db, c.ID, uri)
	if err != nil {
		h.t.Fatalf("ObjectByURI(%q) = %v", uri, err)
	}
	return string(obj.Raw)
}

func TestEventListing(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	h.storeEvent(c, "standup.ics", phoneEvent)

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)))
	for _, want := range []string{"Standup", "Add an event", "repeats", "1 Apr 2026"} {
		if !strings.Contains(got, want) {
			t.Errorf("calendar page does not show %q:\n%s", want, got)
		}
	}
}

// An address book has no event pages, and its routes must not resolve.
func TestEventRoutesRejectAddressBooks(t *testing.T) {
	h := newHarness(t)
	h.login()
	book := h.addressBook("contacts")

	if resp := h.get("/admin/collections/" + itoa(book.ID) + "/events/new"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if got := body(t, h.get("/admin/collections/"+itoa(book.ID))); strings.Contains(got, "Add an event") {
		t.Error("an address book page offers event creation")
	}
}

func TestCreateEvent(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	page := "/admin/collections/" + itoa(c.ID)

	resp := h.post(page+"/events", h.form(page+"/events/new", url.Values{
		"summary":     {"Dentist"},
		"location":    {"High Street"},
		"description": {"Bring the referral"},
		"start_date":  {"2026-05-04"},
		"start_time":  {"11:00"},
		"end_date":    {"2026-05-04"},
		"end_time":    {"11:45"},
		"timezone":    {"Europe/Berlin"},
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
	if obj.DisplayName != "Dentist" || obj.ComponentType != "VEVENT" || obj.UID == "" {
		t.Errorf("index columns = %+v", obj)
	}
	// 11:00 Berlin in May is 09:00Z.
	if obj.StartAt == nil || obj.StartAt.UTC().Format(time.RFC3339) != "2026-05-04T09:00:00Z" {
		t.Errorf("StartAt = %v, want 2026-05-04T09:00:00Z", obj.StartAt)
	}

	raw := string(obj.Raw)
	for _, want := range []string{
		"SUMMARY:Dentist\r\n",
		"LOCATION:High Street\r\n",
		"DTSTART;TZID=Europe/Berlin:20260504T110000\r\n",
		"DTEND;TZID=Europe/Berlin:20260504T114500\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("stored object missing %q:\n%s", want, raw)
		}
	}
}

func TestCreateAllDayEvent(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	page := "/admin/collections/" + itoa(c.ID)

	resp := h.post(page+"/events", h.form(page+"/events/new", url.Values{
		"summary":    {"Conference"},
		"all_day":    {"1"},
		"start_date": {"2026-05-04"},
		"end_date":   {"2026-05-06"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	objects, _ := storage.ListObjects(t.Context(), h.db, c.ID)
	raw := string(objects[0].Raw)

	if !strings.Contains(raw, "DTSTART;VALUE=DATE:20260504\r\n") {
		t.Errorf("start is not a date:\n%s", raw)
	}
	// The last day covered is the 6th, so the exclusive end is the 7th.
	if !strings.Contains(raw, "DTEND;VALUE=DATE:20260507\r\n") {
		t.Errorf("end is not the day after the last one covered:\n%s", raw)
	}
}

func TestCreateEventWithARepeat(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	page := "/admin/collections/" + itoa(c.ID)

	h.post(page+"/events", h.form(page+"/events/new", url.Values{
		"summary":    {"Standup"},
		"start_date": {"2026-05-04"},
		"start_time": {"09:00"},
		"end_date":   {"2026-05-04"},
		"end_time":   {"09:15"},
		"recurrence": {"FREQ=WEEKLY"},
	}))

	objects, _ := storage.ListObjects(t.Context(), h.db, c.ID)
	if len(objects) != 1 {
		t.Fatalf("objects = %d, want 1", len(objects))
	}
	if !objects[0].Recurring {
		t.Error("the event was not indexed as recurring")
	}
	if !strings.Contains(string(objects[0].Raw), "RRULE:FREQ=WEEKLY\r\n") {
		t.Errorf("the repeat was not written:\n%s", objects[0].Raw)
	}
}

func TestCreateEventRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	page := "/admin/collections/" + itoa(c.ID)

	tests := []struct {
		name     string
		values   url.Values
		wantText string
	}{
		{"no title", url.Values{"start_date": {"2026-05-04"}, "end_date": {"2026-05-04"}}, "needs a title"},
		{"no start", url.Values{"summary": {"x"}, "end_date": {"2026-05-04"}}, "not a valid date"},
		{
			"ends before it starts",
			url.Values{
				"summary": {"x"}, "start_date": {"2026-05-04"}, "start_time": {"11:00"},
				"end_date": {"2026-05-04"}, "end_time": {"10:00"},
			},
			"cannot end before",
		},
		{
			"unknown zone",
			url.Values{
				"summary": {"x"}, "start_date": {"2026-05-04"}, "start_time": {"11:00"},
				"end_date": {"2026-05-04"}, "end_time": {"12:00"}, "timezone": {"Mars/Olympus"},
			},
			"Unknown time zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := h.post(page+"/events", h.form(page+"/events/new", tt.values))
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", resp.StatusCode)
			}
			if got := body(t, resp); !strings.Contains(got, tt.wantText) {
				t.Errorf("the page does not explain the problem (%q)", tt.wantText)
			}
		})
	}
}

// The reason the editor works on lines: an edit must not cost the event its
// alarm, its guests or its repeat.
func TestEditingAnEventPreservesEverythingElse(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	h.storeEvent(c, "standup.ics", phoneEvent)
	page := "/admin/collections/" + itoa(c.ID) + "/events/standup.ics"

	resp := h.post(page, h.form(page, url.Values{
		"uid":        {"evt-1"},
		"summary":    {"Morning standup"},
		"location":   {"Room 5"},
		"start_date": {"2026-04-01"},
		"start_time": {"09:30"},
		"end_date":   {"2026-04-01"},
		"end_time":   {"10:00"},
		"timezone":   {"Europe/Berlin"},
		"recurrence": {"FREQ=WEEKLY;COUNT=6"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	raw := h.object(c, "standup.ics")

	if !strings.Contains(raw, "SUMMARY:Morning standup\r\n") {
		t.Errorf("the title was not updated:\n%s", raw)
	}
	if !strings.Contains(raw, "DTSTART;TZID=Europe/Berlin:20260401T093000\r\n") {
		t.Errorf("the start was not moved:\n%s", raw)
	}

	for _, want := range []string{
		"PRODID:-//Apple Inc.//iOS 17.0//EN\r\n",
		"RRULE:FREQ=WEEKLY;COUNT=6\r\n",
		"ATTENDEE;CN=Ada:mailto:ada@example.com\r\n",
		"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n",
		"BEGIN:VALARM\r\n",
		"DESCRIPTION:Reminder\r\n",
		"TRIGGER:-PT15M\r\n",
		"UID:evt-1\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("editing the event lost %q:\n%s", want, raw)
		}
	}
}

// A repeating event says so, and says the repeat is not editable here rather
// than offering a control that would silently replace it.
func TestRecurringEventExplainsItsRepeat(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	h.storeEvent(c, "standup.ics", phoneEvent)

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)+"/events/standup.ics"))
	if !strings.Contains(got, "Every week, 6 times") {
		t.Errorf("the repeat is not described:\n%s", got)
	}
	if !strings.Contains(got, "not editable here") {
		t.Error("the page does not say the repeat cannot be changed here")
	}
	if strings.Contains(got, `<select id="recurrence"`) {
		t.Error("a repeat menu is offered for an event that already repeats")
	}
}

func TestEditingAnEventBumpsETagAndCTag(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	before := h.storeEvent(c, "standup.ics", phoneEvent)

	beforeCollection, err := storage.CollectionByID(t.Context(), h.db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}

	page := "/admin/collections/" + itoa(c.ID) + "/events/standup.ics"
	h.post(page, h.form(page, url.Values{
		"uid": {"evt-1"}, "summary": {"Renamed"},
		"start_date": {"2026-04-01"}, "start_time": {"09:00"},
		"end_date": {"2026-04-01"}, "end_time": {"10:00"},
		"timezone": {"Europe/Berlin"}, "recurrence": {"FREQ=WEEKLY;COUNT=6"},
	}))

	after, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "standup.ics")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if after.ETag == before.ETag {
		t.Error("the ETag did not change after an edit")
	}
	if after.DisplayName != "Renamed" {
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

func TestDeleteEvent(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	h.storeEvent(c, "standup.ics", phoneEvent)
	page := "/admin/collections/" + itoa(c.ID) + "/events/standup.ics"

	confirm := h.get(page + "/delete")
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirmation page = %d, want 200", confirm.StatusCode)
	}
	// A repeating event's confirmation has to say that every occurrence goes.
	if got := body(t, confirm); !strings.Contains(got, "every occurrence") {
		t.Errorf("the confirmation does not mention the repeat:\n%s", got)
	}

	resp := h.post(page+"/delete", h.form(page, nil))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}
	if _, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "standup.ics"); err == nil {
		t.Error("the event survived deletion")
	}
}

func TestEventMutationsRequireCSRFToken(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	h.storeEvent(c, "standup.ics", phoneEvent)
	base := "/admin/collections/" + itoa(c.ID)

	for _, path := range []string{
		base + "/events",
		base + "/events/standup.ics",
		base + "/events/standup.ics/delete",
	} {
		t.Run(path, func(t *testing.T) {
			if resp := h.post(path, url.Values{"summary": {"x"}}); resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
}

func TestOpeningTheEventFormDoesNotWrite(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")
	before := h.storeEvent(c, "standup.ics", phoneEvent)

	if resp := h.get("/admin/collections/" + itoa(c.ID) + "/events/standup.ics"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	after, err := storage.ObjectByURI(t.Context(), h.db, c.ID, "standup.ics")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if after.ETag != before.ETag {
		t.Error("opening the form changed the event")
	}
	if h.object(c, "standup.ics") != phoneEvent {
		t.Error("opening the form rewrote the stored bytes")
	}
}

func TestUnreadableEventIsReportedNotDestroyed(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.calendar("work")

	const broken = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nEND:VCALENDAR\r\n"
	if _, err := storage.PutObject(t.Context(), h.db, &storage.Object{
		CollectionID: c.ID, URI: "broken.ics", Raw: []byte(broken),
		UID: "broken-1", ComponentType: "VEVENT", DisplayName: "Broken",
	}); err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	resp := h.get("/admin/collections/" + itoa(c.ID) + "/events/broken.ics")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, "cannot be edited here") {
		t.Errorf("the page does not explain the problem:\n%s", got)
	}
	if h.object(c, "broken.ics") != broken {
		t.Error("the unreadable object was altered by opening it")
	}
}

func TestDescribeRecurrence(t *testing.T) {
	tests := []struct {
		rule string
		want string
	}{
		{"FREQ=DAILY", "Every day"},
		{"FREQ=WEEKLY", "Every week"},
		{"FREQ=WEEKLY;COUNT=6", "Every week, 6 times"},
		{"FREQ=WEEKLY;INTERVAL=2", "Every other week"},
		{"FREQ=DAILY;INTERVAL=3", "Every 3 days"},
		{"FREQ=WEEKLY;BYDAY=MO,WE,FR", "Every week on Monday, Wednesday and Friday"},
		{"FREQ=WEEKLY;BYDAY=TU,TH", "Every week on Tuesday and Thursday"},
		{"FREQ=YEARLY;UNTIL=20301231T000000Z", "Every year, until 2030-12-31"},
		// An ordinal position in the month is not rendered rather than
		// rendered wrongly.
		{"FREQ=MONTHLY;BYDAY=-1SU", "Every month"},
		{"FREQ=NONSENSE", "Repeats on a schedule this page cannot describe"},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			if got := describeRecurrence(tt.rule); got != tt.want {
				t.Errorf("describeRecurrence(%q) = %q, want %q", tt.rule, got, tt.want)
			}
		})
	}
}
