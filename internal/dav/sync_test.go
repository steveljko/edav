package dav

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// multistatus is enough of the response to assert on without asserting the
// exact XML shape, which is the protocol layer's business.
type multistatus struct {
	XMLName   xml.Name `xml:"DAV: multistatus"`
	SyncToken string   `xml:"sync-token"`
	Responses []struct {
		Href     string `xml:"href"`
		Status   string `xml:"status"`
		PropStat []struct {
			Status string `xml:"status"`
			Prop   struct {
				ETag string `xml:"getetag"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

func syncReport(token string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<D:sync-collection xmlns:D="DAV:">
  <D:sync-token>` + token + `</D:sync-token>
  <D:sync-level>1</D:sync-level>
  <D:prop><D:getetag/></D:prop>
</D:sync-collection>`
}

func (h *harness) sync(path, token string) (*http.Response, multistatus) {
	h.t.Helper()

	resp := h.do("REPORT", path, syncReport(token),
		map[string]string{"Content-Type": "application/xml", "Depth": "1"})
	if resp.StatusCode != http.StatusMultiStatus {
		return resp, multistatus{}
	}

	var ms multistatus
	raw := body(h.t, resp)
	if err := xml.Unmarshal([]byte(raw), &ms); err != nil {
		h.t.Fatalf("parse multistatus: %v\n%s", err, raw)
	}
	return resp, ms
}

// changed splits a sync response into the members to fetch and the paths to
// drop. RFC 6578 3.2 reports a removed member as a bare 404 response.
func (ms multistatus) changed() (updated, deleted []string) {
	for _, r := range ms.Responses {
		name := r.Href
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if strings.Contains(r.Status, "404") {
			deleted = append(deleted, name)
			continue
		}
		updated = append(updated, name)
	}
	return updated, deleted
}

func hasAll(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

func TestSyncInitialListsEveryMember(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	resp, ms := h.sync("/dav/addressbooks/alice/contacts/", "")
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", resp.StatusCode)
	}
	if ms.SyncToken == "" {
		t.Fatal("response carried no sync-token")
	}

	updated, deleted := ms.changed()
	if !hasAll(updated, []string{"ada.vcf", "grace.vcf"}) {
		t.Errorf("updated = %v, want both cards", updated)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted = %v, want none", deleted)
	}
}

func TestSyncReportsOnlyWhatChangedSince(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	_, first := h.sync("/dav/addressbooks/alice/contacts/", "")
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	_, second := h.sync("/dav/addressbooks/alice/contacts/", first.SyncToken)
	updated, deleted := second.changed()
	if !hasAll(updated, []string{"grace.vcf"}) {
		t.Errorf("updated = %v, want only grace.vcf", updated)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted = %v, want none", deleted)
	}
	if second.SyncToken == first.SyncToken {
		t.Error("sync token did not advance after a change")
	}
}

// The scenario the brief singles out: a client that synced before a deletion
// must be told about it, or it keeps a contact the server no longer has.
func TestSyncTokenFromBeforeADeletion(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	_, before := h.sync("/dav/addressbooks/alice/contacts/", "")

	if resp := h.do(http.MethodDelete, "/dav/addressbooks/alice/contacts/ada.vcf", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", resp.StatusCode)
	}

	_, after := h.sync("/dav/addressbooks/alice/contacts/", before.SyncToken)
	updated, deleted := after.changed()
	if len(updated) != 0 {
		t.Errorf("updated = %v, want none", updated)
	}
	if !hasAll(deleted, []string{"ada.vcf"}) {
		t.Errorf("deleted = %v, want ada.vcf", deleted)
	}
}

func TestSyncReportsUpdatesAndDeletionsTogether(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	_, before := h.sync("/dav/addressbooks/alice/contacts/", "")

	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard+"NOTE:edited\r\n")
	h.do(http.MethodDelete, "/dav/addressbooks/alice/contacts/grace.vcf", "", nil)
	h.put("/dav/addressbooks/alice/contacts/hopper.vcf", graceCard)

	_, after := h.sync("/dav/addressbooks/alice/contacts/", before.SyncToken)
	updated, deleted := after.changed()
	if !hasAll(updated, []string{"ada.vcf", "hopper.vcf"}) {
		t.Errorf("updated = %v, want ada.vcf and hopper.vcf", updated)
	}
	if !hasAll(deleted, []string{"grace.vcf"}) {
		t.Errorf("deleted = %v, want grace.vcf", deleted)
	}
}

func TestSyncWithNoChangesReturnsNothing(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	_, first := h.sync("/dav/addressbooks/alice/contacts/", "")
	_, second := h.sync("/dav/addressbooks/alice/contacts/", first.SyncToken)

	updated, deleted := second.changed()
	if len(updated) != 0 || len(deleted) != 0 {
		t.Errorf("updated = %v, deleted = %v, want both empty", updated, deleted)
	}
	if second.SyncToken != first.SyncToken {
		t.Errorf("token moved without a change: %q then %q", first.SyncToken, second.SyncToken)
	}
}

// An identical rewrite is not a change, so a syncing client is not sent to
// refetch bytes it already has.
func TestSyncIgnoresRewritesThatChangeNothing(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	_, first := h.sync("/dav/addressbooks/alice/contacts/", "")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	_, second := h.sync("/dav/addressbooks/alice/contacts/", first.SyncToken)
	if updated, _ := second.changed(); len(updated) != 0 {
		t.Errorf("updated = %v, want none", updated)
	}
}

// RFC 6578 3.2: an unusable token is a DAV:valid-sync-token precondition
// failure, which is how the client is told to discard its state and start over
// rather than retrying the same token forever.
func TestSyncRejectsUnusableToken(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	tests := []struct {
		name  string
		token string
	}{
		{"from another server", "http://other.example/sync/42"},
		{"wrong prefix", "urn:elsewhere:sync:1"},
		{"not a number", "urn:edav:sync:banana"},
		{"negative", "urn:edav:sync:-1"},
		{"ahead of the collection", "urn:edav:sync:99999"},
		{"empty prefix only", "urn:edav:sync:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := h.sync("/dav/addressbooks/alice/contacts/", tt.token)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
			if got := body(t, resp); !strings.Contains(got, "valid-sync-token") {
				t.Errorf("response does not carry valid-sync-token:\n%s", got)
			}
		})
	}
}

// After being rejected, the client starts over with an empty token and must get
// the whole collection back.
func TestSyncFallsBackToFullResync(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)
	h.put("/dav/addressbooks/alice/contacts/grace.vcf", graceCard)

	resp, _ := h.sync("/dav/addressbooks/alice/contacts/", "urn:edav:sync:99999")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	_, ms := h.sync("/dav/addressbooks/alice/contacts/", "")
	updated, _ := ms.changed()
	if !hasAll(updated, []string{"ada.vcf", "grace.vcf"}) {
		t.Errorf("full resync returned %v, want both cards", updated)
	}
}

// A token taken before an object existed at all, then deleted before the client
// ever saw it, must not be reported: the client never had it to drop.
func TestSyncTokenScopedToOneCollection(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.addressBook("work")
	h.put("/dav/addressbooks/alice/contacts/ada.vcf", adaCard)

	_, contacts := h.sync("/dav/addressbooks/alice/contacts/", "")
	h.put("/dav/addressbooks/alice/work/grace.vcf", graceCard)

	_, after := h.sync("/dav/addressbooks/alice/contacts/", contacts.SyncToken)
	if updated, _ := after.changed(); len(updated) != 0 {
		t.Errorf("updated = %v, want none: the change was in another collection", updated)
	}
}

func TestSyncCalendarCollection(t *testing.T) {
	h := newHarness(t)
	h.calendar("work")
	h.putEvent("/dav/calendars/alice/work/march.ics", marchEvent)
	h.putEvent("/dav/calendars/alice/work/june.ics", juneEvent)

	_, first := h.sync("/dav/calendars/alice/work/", "")
	updated, _ := first.changed()
	if !hasAll(updated, []string{"march.ics", "june.ics"}) {
		t.Fatalf("initial sync = %v, want both events", updated)
	}

	h.do(http.MethodDelete, "/dav/calendars/alice/work/june.ics", "", nil)
	h.putEvent("/dav/calendars/alice/work/standup.ics", weeklyEvent)

	_, second := h.sync("/dav/calendars/alice/work/", first.SyncToken)
	updated, deleted := second.changed()
	if !hasAll(updated, []string{"standup.ics"}) {
		t.Errorf("updated = %v, want standup.ics", updated)
	}
	if !hasAll(deleted, []string{"june.ics"}) {
		t.Errorf("deleted = %v, want june.ics", deleted)
	}
}

// A client walking the log one sync at a time must see every change exactly
// once, which is the property that keeps the two sides converging.
func TestSyncWalksEveryChangeExactlyOnce(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")

	_, ms := h.sync("/dav/addressbooks/alice/contacts/", "")
	token := ms.SyncToken

	seen := make(map[string]int)
	for i := range 5 {
		uri := "/dav/addressbooks/alice/contacts/c" + string(rune('0'+i)) + ".vcf"
		h.put(uri, strings.Replace(adaCard, "UID:ada-1", "UID:ada-"+string(rune('0'+i)), 1))

		_, ms := h.sync("/dav/addressbooks/alice/contacts/", token)
		updated, _ := ms.changed()
		if len(updated) != 1 {
			t.Fatalf("round %d: updated = %v, want exactly one", i, updated)
		}
		seen[updated[0]]++
		token = ms.SyncToken
	}

	if len(seen) != 5 {
		t.Errorf("saw %d distinct objects, want 5", len(seen))
	}
	for uri, n := range seen {
		if n != 1 {
			t.Errorf("%s reported %d times, want once", uri, n)
		}
	}
}

// A client discovers sync-collection through supported-report-set; without it
// it falls back to polling the ctag and refetching every ETag.
func TestCollectionAdvertisesSyncSupport(t *testing.T) {
	h := newHarness(t)
	h.addressBook("contacts")
	h.calendar("work")

	const propfind = `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop><D:supported-report-set/><D:sync-token/></D:prop>
</D:propfind>`

	for _, path := range []string{
		"/dav/addressbooks/alice/contacts/",
		"/dav/calendars/alice/work/",
	} {
		t.Run(path, func(t *testing.T) {
			resp := h.do("PROPFIND", path, propfind,
				map[string]string{"Content-Type": "application/xml", "Depth": "0"})
			if resp.StatusCode != http.StatusMultiStatus {
				t.Fatalf("status = %d, want 207", resp.StatusCode)
			}

			got := body(t, resp)
			for _, want := range []string{"supported-report-set", "sync-collection", "sync-token", "urn:edav:sync:"} {
				if !strings.Contains(got, want) {
					t.Errorf("PROPFIND response does not mention %q:\n%s", want, got)
				}
			}
		})
	}
}
