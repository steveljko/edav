package admin

import (
	"net/http"
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
	if got := body(t, h.get("/admin/collections/"+itoa(c.ID))); strings.Contains(got, `data-filter=`) {
		t.Error("an empty address book offers a search box")
	}

	h.storeCard(c, "ada.vcf", phoneCard)
	got := body(t, h.get("/admin/collections/"+itoa(c.ID)))
	if !strings.Contains(got, `data-filter="contact-list"`) {
		t.Error("no search box once there are contacts")
	}
	// Rows carry their own search text rather than the filter guessing.
	if !strings.Contains(got, `data-search="ada lovelace ada.vcf"`) {
		t.Errorf("contact row has no search text:\n%s", got)
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
