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
