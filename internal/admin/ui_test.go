package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/steveljko/edav/internal/storage"
)

func TestInitials(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"admin", "Ad"},
		{"Ada Lovelace", "AL"},
		{"Katherine G Johnson", "KJ"},
		{"grace", "Gr"},
		{"x", "X"},
		{"", "?"},
		{"   ", "?"},
		// Letters are upper-cased, not transliterated.
		{"Ædith Ćurić", "ÆĆ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := initials(tt.name); got != tt.want {
				t.Errorf("initials(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// The setup page exists to be copied from, so every address gets a button.
func TestSetupPageOffersCopyButtons(t *testing.T) {
	h := newHarness(t)
	h.login()

	got := body(t, h.get("/admin/setup"))
	for _, id := range []string{"base-url", "wk-caldav", "wk-carddav", "principal", "cal-home", "ab-home"} {
		if !strings.Contains(got, `data-copy="`+id+`"`) {
			t.Errorf("no copy button for %q", id)
		}
		if !strings.Contains(got, `id="`+id+`"`) {
			t.Errorf("no element with id %q for its copy button to read", id)
		}
	}
}

func TestContactSearchIsOfferedWhenThereAreContacts(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")

	// Nothing to search through yet.
	if got := body(t, h.get("/admin/collections/"+itoa(c.ID))); strings.Contains(got, `name="q"`) {
		t.Error("an empty address book offers a search box")
	}

	h.storeCard(c, "ada.vcf", phoneCard)
	got := body(t, h.get("/admin/collections/"+itoa(c.ID)))
	if !strings.Contains(got, `name="q"`) {
		t.Error("no search box once there are contacts")
	}
	// Typing searches the whole collection on the server rather than filtering
	// the rows that happen to be on screen.
	if !strings.Contains(got, `hx-target="#collection-results"`) {
		t.Errorf("the search does not reach the server as you type:\n%s", got)
	}
}

func TestContactFormOffersRowControls(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)+"/contacts/new"))
	for _, want := range []string{`data-add-row="phone-rows"`, `data-add-row="email-rows"`, "data-remove-row"} {
		if !strings.Contains(got, want) {
			t.Errorf("contact form is missing %q", want)
		}
	}
}

// Every scripted control has a path that works without scripting.
func TestDestructiveActionsHaveANonScriptedPath(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")
	c, err := storage.CreateCollection(t.Context(), h.db, &storage.Collection{
		OwnerID: bob.ID, Type: storage.CollectionAddressBook, URI: "contacts",
	})
	if err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}
	h.storeCard(c, "ada.vcf", phoneCard)

	tests := []struct {
		page string
		link string
	}{
		{"/admin/users/" + itoa(bob.ID), "/admin/users/" + itoa(bob.ID) + "/delete"},
		{"/admin/collections/" + itoa(c.ID), "/admin/collections/" + itoa(c.ID) + "/delete"},
		{
			"/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf",
			"/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf/delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			got := body(t, h.get(tt.page))
			if !strings.Contains(got, `href="`+tt.link+`"`) {
				t.Errorf("no non-scripted link to %q", tt.link)
			}
			if !strings.Contains(got, "no-js-only") {
				t.Error("the non-scripted path is not marked for browsers without scripting")
			}
			if resp := h.get(tt.link); resp.StatusCode != http.StatusOK {
				t.Errorf("the confirmation page returned %d", resp.StatusCode)
			}
		})
	}
}

func TestEnhancementScriptIsServed(t *testing.T) {
	h := newHarness(t)

	resp := h.get("/admin/static/app.js")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, "data-copy") {
		t.Error("app.js does not look like the enhancement script")
	}
}

func TestPagesCarryDarkModeAndResponsiveRules(t *testing.T) {
	h := newHarness(t)

	css := body(t, h.get("/admin/static/style.css"))
	for _, want := range []string{
		"prefers-color-scheme: dark",
		"prefers-reduced-motion",
		"@media (max-width: 52rem)",
		"focus-visible",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet is missing %q", want)
		}
	}
}

// The theme has to work three ways: following the system, and forced either
// direction. An explicit choice must win over the system preference, which
// needs the dark tokens declared under both selectors.
func TestThemeCoversSystemAndExplicitChoice(t *testing.T) {
	h := newHarness(t)

	css := body(t, h.get("/admin/static/style.css"))
	for _, want := range []string{
		`:root[data-theme="light"]`,
		`:root:not([data-theme="light"])`,
		`:root[data-theme="dark"]`,
		"prefers-color-scheme: dark",
		"color-scheme: dark",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet is missing %q", want)
		}
	}
}

// Colours come from tokens so a theme is one block to change, not a hunt
// through the rules.
func TestStylesheetKeepsColoursInTokens(t *testing.T) {
	h := newHarness(t)
	css := body(t, h.get("/admin/static/style.css"))

	_, rules, ok := strings.Cut(css, "/* Base ---")
	if !ok {
		t.Fatal("cannot find where the token blocks end")
	}
	if strings.ContainsAny(rules, "#") {
		for _, line := range strings.Split(rules, "\n") {
			if i := strings.Index(line, "#"); i >= 0 && len(line) > i+3 {
				if isHexColour(line[i:]) {
					t.Errorf("hardcoded colour outside the token blocks: %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

func isHexColour(s string) bool {
	if len(s) < 4 || s[0] != '#' {
		return false
	}
	for i := 1; i < len(s) && i <= 6; i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return i >= 4
		}
	}
	return true
}

func TestThemeControlIsOfferedAndDefaultsToSystem(t *testing.T) {
	h := newHarness(t)
	h.login()

	got := body(t, h.get("/admin/"))
	for _, want := range []string{
		`data-theme-set="light"`,
		`data-theme-set="system" aria-pressed="true"`,
		`data-theme-set="dark"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("theme control is missing %q", want)
		}
	}

	// The stored choice must be applied before the stylesheet loads, or every
	// page load flashes the other theme first.
	script := strings.Index(got, "edav-theme")
	sheet := strings.Index(got, "style.css")
	if script < 0 || sheet < 0 || script > sheet {
		t.Error("the theme is not applied before the stylesheet loads")
	}
}

// Ad-hoc spacing is what makes an interface feel loose; the templates use the
// scale rather than reaching for inline styles.
func TestPagesUseTheSpacingScaleNotInlineStyles(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	pages := []string{
		"/admin/",
		"/admin/users/" + itoa(h.admin.ID),
		"/admin/collections/" + itoa(c.ID),
		"/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf",
		"/admin/setup",
	}

	for _, page := range pages {
		t.Run(page, func(t *testing.T) {
			got := body(t, h.get(page))
			for _, line := range strings.Split(got, "\n") {
				// A collection's colour is chosen by the user, so it is the one
				// value that cannot come from a class.
				if strings.Contains(line, `style="`) && !strings.Contains(line, "swatch") {
					t.Errorf("inline style outside the swatch: %s", strings.TrimSpace(line))
				}
			}
		})
	}
}

// An admin page that can be framed is a delete confirmation an attacker can
// collect the click for.
func TestAdminPagesCarryASecurityPolicy(t *testing.T) {
	h := newHarness(t)
	h.login()

	for _, path := range []string{"/admin/", "/admin/login", "/admin/setup", "/admin/static/style.css"} {
		t.Run(path, func(t *testing.T) {
			resp := h.get(path)

			csp := resp.Header.Get("Content-Security-Policy")
			for _, want := range []string{
				"default-src 'self'",
				"frame-ancestors 'none'",
				"base-uri 'none'",
				"form-action 'self'",
				"script-src 'self' 'nonce-",
			} {
				if !strings.Contains(csp, want) {
					t.Errorf("policy is missing %q: %s", want, csp)
				}
			}
			if strings.Contains(csp, "script-src") && strings.Contains(csp, "'unsafe-inline' 'nonce") {
				t.Error("scripts allow unsafe-inline, which defeats the nonce")
			}
			if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY", got)
			}
			if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
		})
	}
}

// The nonce admits the theme script and nothing else, so it has to be fresh
// each time and actually present on the tag.
func TestScriptNonceIsPerRequestAndUsed(t *testing.T) {
	h := newHarness(t)
	h.login()

	nonces := make(map[string]bool)
	for range 3 {
		resp := h.get("/admin/")
		csp := resp.Header.Get("Content-Security-Policy")

		_, rest, ok := strings.Cut(csp, "'nonce-")
		if !ok {
			t.Fatalf("no nonce in the policy: %s", csp)
		}
		nonce, _, _ := strings.Cut(rest, "'")
		if len(nonce) < 16 {
			t.Errorf("nonce %q is too short to be worth having", nonce)
		}
		if nonces[nonce] {
			t.Errorf("nonce %q was reused across requests", nonce)
		}
		nonces[nonce] = true

		if got := body(t, resp); !strings.Contains(got, `nonce="`+nonce+`"`) {
			t.Error("the page does not carry the nonce its policy admits")
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Personal", "personal"},
		{"Work Calendar", "work-calendar"},
		{"  Trimmed  ", "trimmed"},
		{"Family & Friends", "family-friends"},
		{"Café Meetings", "caf-meetings"},
		{"Ada's Contacts", "ada-s-contacts"},
		{"multiple   spaces", "multiple-spaces"},
		{"-leading-and-trailing-", "leading-and-trailing"},
		{"UPPER", "upper"},
		{"keep.dots_and_underscores", "keep.dots_and_underscores"},
		{"2026 Planning", "2026-planning"},
		{"", ""},
		{"!!!", ""},
		{strings.Repeat("a", 80), strings.Repeat("a", 60)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.name); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// Nobody should have to think about URLs to add a calendar.
func TestCollectionSlugIsDerivedFromTheName(t *testing.T) {
	h := newHarness(t)
	h.login()
	page := "/admin/users/" + itoa(h.admin.ID)

	resp := h.post(page+"/collections", h.form(page, url.Values{
		"type":         {"calendar"},
		"display_name": {"Work Calendar"},
		"uri":          {""},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
	}

	if _, err := storage.CollectionByURI(t.Context(), h.db, h.admin.ID, "work-calendar"); err != nil {
		t.Errorf("no collection at the derived slug: %v", err)
	}
}

// A derived slug that collides gets a number rather than an error about a
// value nobody typed.
func TestDerivedSlugAvoidsACollision(t *testing.T) {
	h := newHarness(t)
	h.login()
	page := "/admin/users/" + itoa(h.admin.ID)

	for range 3 {
		resp := h.post(page+"/collections", h.form(page, url.Values{
			"type": {"calendar"}, "display_name": {"Work"}, "uri": {""},
		}))
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", resp.StatusCode, body(t, resp))
		}
	}

	for _, want := range []string{"work", "work-2", "work-3"} {
		if _, err := storage.CollectionByURI(t.Context(), h.db, h.admin.ID, want); err != nil {
			t.Errorf("no collection at %q: %v", want, err)
		}
	}
}

// A slug typed by hand is still the one used, and still checked.
func TestExplicitSlugIsRespected(t *testing.T) {
	h := newHarness(t)
	h.login()
	page := "/admin/users/" + itoa(h.admin.ID)

	resp := h.post(page+"/collections", h.form(page, url.Values{
		"type": {"calendar"}, "display_name": {"Work Calendar"}, "uri": {"w"},
	}))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if _, err := storage.CollectionByURI(t.Context(), h.db, h.admin.ID, "w"); err != nil {
		t.Errorf("the typed slug was not used: %v", err)
	}

	resp = h.post(page+"/collections", h.form(page, url.Values{
		"type": {"calendar"}, "display_name": {"Bad"}, "uri": {"has spaces"},
	}))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an invalid typed slug = %d, want 422", resp.StatusCode)
	}
}

func TestSlugFieldFollowsTheNameField(t *testing.T) {
	h := newHarness(t)
	h.login()

	got := body(t, h.get("/admin/users/"+itoa(h.admin.ID)))
	if !strings.Contains(got, `data-slug-source="uri"`) {
		t.Error("the name field does not drive the slug field")
	}
	if !strings.Contains(got, `id="uri"`) || !strings.Contains(got, "data-slug") {
		t.Error("the slug field is not marked for the script to fill")
	}
	// It must not be required, or leaving it blank cannot mean "derive it".
	if strings.Contains(got, `name="uri" value="{{.Form.URI}}" placeholder="personal"`) {
		t.Error("the slug field still demands a value")
	}
}

// The browser's confirm() cannot say what is about to be deleted in the
// interface's own voice, so the page draws its own.
func TestDestructiveActionsUseThePageOwnDialog(t *testing.T) {
	h := newHarness(t)
	h.login()
	bob := h.createUser("bob")
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	pages := []string{
		"/admin/users/" + itoa(bob.ID),
		"/admin/collections/" + itoa(c.ID),
		"/admin/collections/" + itoa(c.ID) + "/contacts/ada.vcf",
	}

	for _, page := range pages {
		t.Run(page, func(t *testing.T) {
			got := body(t, h.get(page))

			if strings.Contains(got, "hx-confirm") {
				t.Error("still using the browser's confirmation dialog")
			}
			for _, want := range []string{"data-confirm=", "data-confirm-title=", "data-confirm-verb="} {
				if !strings.Contains(got, want) {
					t.Errorf("the button carries no %s wording", want)
				}
			}
			if !strings.Contains(got, `<dialog class="modal" id="confirm-modal"`) {
				t.Error("the page has no dialog to show")
			}
		})
	}
}

// A paged listing is a table with the detail worth scanning, not a bare list
// of names.
func TestListingsAreTablesWithDetail(t *testing.T) {
	h := newHarness(t)
	h.login()

	book := h.addressBook("contacts")
	h.storeCard(book, "ada.vcf", phoneCard)

	cal := h.calendar("work")
	h.storeEvent(cal, "standup.ics", phoneEvent)

	contacts := body(t, h.get("/admin/collections/"+itoa(book.ID)))
	// The digits without the leading plus: html/template writes "+" as "&#43;"
	// in text, which a browser renders back as "+".
	for _, want := range []string{`<table class="data">`, "<th>Email</th>", "<th>Phone</th>",
		"ada@example.com", "15551234567"} {
		if !strings.Contains(contacts, want) {
			t.Errorf("contact table is missing %q", want)
		}
	}

	events := body(t, h.get("/admin/collections/"+itoa(cal.ID)))
	for _, want := range []string{`<table class="data">`, "<th>When</th>", "<th>Where</th>",
		"Room 3", "1 Apr 2026"} {
		if !strings.Contains(events, want) {
			t.Errorf("event table is missing %q", want)
		}
	}
}

// Typing in the search box replaces the results, not the page. The response to
// such a request has to be the fragment alone, or the layout arrives nested
// inside itself.
func TestSearchReturnsOnlyTheResultsFragment(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	page := "/admin/collections/" + itoa(c.ID)

	full := body(t, h.get(page))
	if !strings.Contains(full, "<!doctype html>") || !strings.Contains(full, `id="collection-results"`) {
		t.Fatal("the full page is not a whole document with a results region")
	}

	req, err := http.NewRequest(http.MethodGet, h.server.URL+page+"?q=ada", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("HX-Request", "true")

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	fragment := body(t, resp)
	if strings.Contains(fragment, "<!doctype html>") || strings.Contains(fragment, "<body") {
		t.Errorf("the fragment carries the whole layout:\n%s", fragment)
	}
	if !strings.Contains(fragment, `<table class="data">`) {
		t.Errorf("the fragment has no results table:\n%s", fragment)
	}
	if !strings.Contains(fragment, "Ada Lovelace") {
		t.Errorf("the fragment did not honour the search:\n%s", fragment)
	}
}

// The search still works with scripting off, so htmx is an enhancement rather
// than the only way through.
func TestSearchStillWorksAsAPlainForm(t *testing.T) {
	h := newHarness(t)
	h.login()
	c := h.addressBook("contacts")
	h.storeCard(c, "ada.vcf", phoneCard)

	got := body(t, h.get("/admin/collections/"+itoa(c.ID)))
	if !strings.Contains(got, `<form method="get"`) {
		t.Error("the search box is not a form")
	}

	full := body(t, h.get("/admin/collections/"+itoa(c.ID)+"?q=nobody"))
	if !strings.Contains(full, "<!doctype html>") {
		t.Error("a plain search did not return a whole page")
	}
	if !strings.Contains(full, "Nothing matches") {
		t.Error("a plain search did not apply the query")
	}
}
