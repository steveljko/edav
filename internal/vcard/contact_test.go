package vcard

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)

func TestReadContact(t *testing.T) {
	c, err := ReadContact([]byte(appleCard))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}

	if c.UID != "ada-1" {
		t.Errorf("UID = %q", c.UID)
	}
	if c.FormattedName != "Ada Lovelace" {
		t.Errorf("FormattedName = %q", c.FormattedName)
	}
	if c.FamilyName != "Lovelace" || c.GivenName != "Ada" {
		t.Errorf("name = %q / %q, want Lovelace / Ada", c.FamilyName, c.GivenName)
	}
	if c.Organisation != "Analytical Engines, Ltd." {
		t.Errorf("Organisation = %q", c.Organisation)
	}
	if len(c.Phones) != 2 || c.Phones[0].Value != "+15551234567" {
		t.Errorf("Phones = %+v", c.Phones)
	}
	if len(c.Emails) != 1 || c.Emails[0].Value != "ada@example.com" {
		t.Errorf("Emails = %+v", c.Emails)
	}
	if c.URL != "https://example.com/ada" {
		t.Errorf("URL = %q", c.URL)
	}
}

// The whole point: an edit through the form must not disturb what the form
// does not show.
func TestApplyPreservesUnmodelledProperties(t *testing.T) {
	c, err := ReadContact([]byte(appleCard))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	c.FormattedName = "Ada L. Byron"

	out, err := Apply([]byte(appleCard), c, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	got := string(out)

	for _, want := range []string{
		"PRODID:-//Apple Inc.//iPhone OS 17.0//EN\r\n",
		"item1.ADR;type=HOME;type=pref:;;12 Baker St\\nFlat 3;London;;NW1;England\r\n",
		"item1.X-ABADR:gb\r\n",
		"item2.X-ABLabel:_$!<HomePage>!$_\r\n",
		"X-SOCIALPROFILE;type=twitter:https://twitter.com/ada\r\n",
		"PHOTO;ENCODING=b;TYPE=JPEG:",
		"UID:ada-1\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("editing the name lost %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "FN:Ada L. Byron\r\n") {
		t.Errorf("FN was not updated:\n%s", got)
	}
	if !strings.Contains(got, "REV:20260315T093000Z") {
		t.Errorf("REV was not stamped:\n%s", got)
	}
}

// A round trip that changes nothing should still only add REV, so an
// accidental save does not rewrite the card.
func TestApplyWithoutChangesTouchesOnlyRev(t *testing.T) {
	c, err := ReadContact([]byte(appleCard))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}

	out, err := Apply([]byte(appleCard), c, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	want := strings.Replace(appleCard, "END:VCARD\r\n", "REV:20260315T093000Z\r\nEND:VCARD\r\n", 1)
	if string(out) != want {
		t.Errorf("an unchanged save rewrote the card:\n in: %q\nout: %q", want, string(out))
	}
}

func TestApplyStructuredNameIsNotOverEscaped(t *testing.T) {
	c, err := ReadContact([]byte(appleCard))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	c.FamilyName = "Byron"
	c.GivenName = "Ada"

	out, err := Apply([]byte(appleCard), c, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if !strings.Contains(string(out), "N:Byron;Ada;;;\r\n") {
		t.Errorf("N was written wrong:\n%s", out)
	}
}

// A semicolon inside a name component must be escaped, while the ones dividing
// the components must not be.
func TestStructuredNameEscapesWithinComponents(t *testing.T) {
	c := &Contact{FamilyName: "Smith; Jones", GivenName: "Ada"}
	out, err := New(c, testNow)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if !strings.Contains(string(out), `N:Smith\; Jones;Ada;;;`) {
		t.Errorf("N was not escaped correctly:\n%s", out)
	}

	back, err := ReadContact(out)
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	if back.FamilyName != "Smith; Jones" || back.GivenName != "Ada" {
		t.Errorf("round trip = %q / %q", back.FamilyName, back.GivenName)
	}
}

func TestApplyKeepsExtraOrganisationFields(t *testing.T) {
	raw := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:x\r\nFN:Someone\r\n" +
		"ORG:Acme;Research;Optics\r\nEND:VCARD\r\n"

	c, err := ReadContact([]byte(raw))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	if c.Organisation != "Acme" {
		t.Fatalf("Organisation = %q, want Acme", c.Organisation)
	}
	c.Organisation = "Acme Corp"

	out, err := Apply([]byte(raw), c, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if !strings.Contains(string(out), "ORG:Acme Corp;Research;Optics\r\n") {
		t.Errorf("the department fields were lost:\n%s", out)
	}
}

func TestApplyEditsPhonesIndividually(t *testing.T) {
	c, err := ReadContact([]byte(appleCard))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	c.Phones[1].Value = "+15550001111"

	out, err := Apply([]byte(appleCard), c, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "TEL;type=CELL;type=VOICE;type=pref:+15551234567\r\n") {
		t.Errorf("the untouched phone was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "+15550001111") {
		t.Errorf("the edited phone was not written:\n%s", got)
	}
}

func TestApplyDropsBlankEntries(t *testing.T) {
	c, err := ReadContact([]byte(appleCard))
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	// A form submits empty rows for the inputs nobody filled in.
	c.Phones = append(c.Phones, Property{Value: "   ", Type: "work"}, Property{Value: "", Type: ""})

	out, err := Apply([]byte(appleCard), c, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if n := strings.Count(string(out), "TEL"); n != 2 {
		t.Errorf("TEL count = %d, want 2; blank rows became properties:\n%s", n, out)
	}
}

func TestNewContact(t *testing.T) {
	c := &Contact{
		GivenName:    "Grace",
		FamilyName:   "Hopper",
		Organisation: "US Navy",
		Title:        "Rear Admiral",
		Emails:       []Property{{Value: "grace@example.com", Type: "work"}},
		Phones:       []Property{{Value: "+15557654321", Type: "cell"}},
		Note:         "Coined the term debugging",
	}

	out, err := New(c, testNow)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	got := string(out)

	for _, want := range []string{
		"BEGIN:VCARD\r\n",
		"VERSION:3.0\r\n",
		"FN:Grace Hopper\r\n",
		"N:Hopper;Grace;;;\r\n",
		"ORG:US Navy\r\n",
		"TITLE:Rear Admiral\r\n",
		"EMAIL;TYPE=WORK:grace@example.com\r\n",
		"TEL;TYPE=CELL:+15557654321\r\n",
		"NOTE:Coined the term debugging\r\n",
		"REV:20260315T093000Z\r\n",
		"END:VCARD\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("new card is missing %q:\n%s", want, got)
		}
	}

	// It has to be a card the rest of the server accepts.
	if _, err := Parse(out); err != nil {
		t.Errorf("the generated card does not parse: %v", err)
	}
	back, err := ReadContact(out)
	if err != nil {
		t.Fatalf("ReadContact() = %v", err)
	}
	if back.UID == "" {
		t.Error("no UID was generated")
	}
	if back.DisplayName() != "Grace Hopper" {
		t.Errorf("DisplayName() = %q", back.DisplayName())
	}
}

func TestNewContactDerivesFormattedName(t *testing.T) {
	out, err := New(&Contact{GivenName: "Ada", FamilyName: "Lovelace"}, testNow)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	// FN is required by RFC 6350, so it must be derived rather than omitted.
	if !strings.Contains(string(out), "FN:Ada Lovelace\r\n") {
		t.Errorf("FN was not derived from the structured name:\n%s", out)
	}
}

func TestNewContactRequiresAName(t *testing.T) {
	if _, err := New(&Contact{Note: "no name"}, testNow); err == nil {
		t.Error("New() = nil, want an error for a nameless contact")
	}
	if _, err := New(&Contact{FormattedName: "  "}, testNow); err == nil {
		t.Error("New() accepted a blank name")
	}
}

func TestUIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		uid := NewUID()
		if seen[uid] {
			t.Fatalf("duplicate UID %q", uid)
		}
		seen[uid] = true
	}
}

func TestFilenameIsPathSafe(t *testing.T) {
	tests := []struct {
		uid  string
		want string
	}{
		{"abc123", "abc123.vcf"},
		{"urn:uuid:4fbe8971", "urn:uuid:4fbe8971.vcf"},
		{"a/b", "a-b.vcf"},
		{"../escape", "..-escape.vcf"},
		{"a%2Fb", "a-2Fb.vcf"},
		{"a?b#c", "a-b-c.vcf"},
	}

	for _, tt := range tests {
		if got := Filename(tt.uid); got != tt.want {
			t.Errorf("Filename(%q) = %q, want %q", tt.uid, got, tt.want)
		}
		if strings.ContainsAny(Filename(tt.uid), `/\?#%`) {
			t.Errorf("Filename(%q) still contains a path character", tt.uid)
		}
	}
}

func TestApplyRejectsMalformedCard(t *testing.T) {
	if _, err := Apply([]byte("not a card"), &Contact{FormattedName: "x"}, testNow); err == nil {
		t.Error("Apply() = nil, want an error")
	}
}
