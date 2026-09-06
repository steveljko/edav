package vcard

import (
	"strings"
	"testing"
	"time"
)

// A card of the kind clients actually store: mixed-case X- names, an Apple
// label group, a folded line, an escaped value, and a photo.
const appleCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"PRODID:-//Apple Inc.//iPhone OS 17.0//EN\r\n" +
	"N:Lovelace;Ada;;;\r\n" +
	"FN:Ada Lovelace\r\n" +
	"ORG:Analytical Engines\\, Ltd.;\r\n" +
	"TEL;type=CELL;type=VOICE;type=pref:+15551234567\r\n" +
	"TEL;type=HOME;type=VOICE:+15559876543\r\n" +
	"EMAIL;type=INTERNET;type=HOME;type=pref:ada@example.com\r\n" +
	"item1.ADR;type=HOME;type=pref:;;12 Baker St\\nFlat 3;London;;NW1;England\r\n" +
	"item1.X-ABADR:gb\r\n" +
	"item2.URL;type=pref:https://example.com/ada\r\n" +
	"item2.X-ABLabel:_$!<HomePage>!$_\r\n" +
	"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJ\r\n" +
	" CQkKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/wAA\r\n" +
	" RCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL\r\n" +
	"X-SOCIALPROFILE;type=twitter:https://twitter.com/ada\r\n" +
	"UID:ada-1\r\n" +
	"END:VCARD\r\n"

func parseOrFail(t *testing.T, raw string) *Card {
	t.Helper()
	v, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}
	return v
}

// The property the whole package exists for.
func TestUntouchedCardIsByteIdentical(t *testing.T) {
	v := parseOrFail(t, appleCard)
	if got := string(v.Bytes()); got != appleCard {
		t.Errorf("re-emitting an unedited card changed it:\n in: %q\nout: %q", appleCard, got)
	}
}

func TestEditingOnePropertyLeavesEverythingElseAlone(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.Set("FN", "Ada L. Byron")

	got := string(v.Bytes())

	if !strings.Contains(got, "FN:Ada L. Byron\r\n") {
		t.Errorf("FN was not updated:\n%s", got)
	}
	if strings.Contains(got, "FN:Ada Lovelace") {
		t.Error("the old FN is still present")
	}

	// Everything the form does not model must survive, byte for byte.
	for _, want := range []string{
		"PRODID:-//Apple Inc.//iPhone OS 17.0//EN\r\n",
		"item1.ADR;type=HOME;type=pref:;;12 Baker St\\nFlat 3;London;;NW1;England\r\n",
		"item1.X-ABADR:gb\r\n",
		"item2.X-ABLabel:_$!<HomePage>!$_\r\n",
		"X-SOCIALPROFILE;type=twitter:https://twitter.com/ada\r\n",
		"TEL;type=CELL;type=VOICE;type=pref:+15551234567\r\n",
		"UID:ada-1\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("editing FN lost %q:\n%s", want, got)
		}
	}

	// Including the folded photo, with its exact continuation lines.
	if !strings.Contains(got, "PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJ\r\n CQkKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/wAA\r\n") {
		t.Errorf("the folded PHOTO was altered:\n%s", got)
	}
}

func TestEditingPreservesPropertyOrder(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.Set("FN", "Changed")

	order := func(s string) []string {
		var names []string
		for _, l := range strings.Split(s, "\r\n") {
			if l == "" || strings.HasPrefix(l, " ") {
				continue
			}
			name, _, _ := strings.Cut(l, ":")
			name, _, _ = strings.Cut(name, ";")
			names = append(names, name)
		}
		return names
	}

	before, after := order(appleCard), order(string(v.Bytes()))
	if len(before) != len(after) {
		t.Fatalf("property count changed: %d then %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("property %d moved: %q then %q", i, before[i], after[i])
		}
	}
}

func TestSetKeepsParameters(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.Set("TEL", "+15550000000")

	got := string(v.Bytes())
	if !strings.Contains(got, "TEL;type=CELL;type=VOICE;type=pref:+15550000000\r\n") {
		t.Errorf("editing a value dropped its parameters:\n%s", got)
	}
}

func TestSetAppendsMissingProperty(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.Set("NOTE", "Wrote the first algorithm")

	got := string(v.Bytes())
	if !strings.Contains(got, "NOTE:Wrote the first algorithm\r\n") {
		t.Errorf("NOTE was not added:\n%s", got)
	}
	// It has to land before END, or the card is malformed.
	if strings.Index(got, "NOTE:") > strings.Index(got, "END:VCARD") {
		t.Error("NOTE was added after END:VCARD")
	}
}

func TestSetEmptyRemovesProperty(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.Set("ORG", "")

	if got := string(v.Bytes()); strings.Contains(got, "ORG:") {
		t.Errorf("ORG was not removed:\n%s", got)
	}
}

func TestValuesReadsRepeatedProperties(t *testing.T) {
	v := parseOrFail(t, appleCard)

	tels := v.Values("TEL")
	if len(tels) != 2 {
		t.Fatalf("TEL count = %d, want 2", len(tels))
	}
	if tels[0].Value != "+15551234567" || tels[0].Type != "cell" {
		t.Errorf("TEL[0] = %+v", tels[0])
	}
	if tels[1].Value != "+15559876543" || tels[1].Type != "home" {
		t.Errorf("TEL[1] = %+v", tels[1])
	}
}

func TestSetAllEditsOnlyTheChangedOccurrence(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.SetAll("TEL", []Property{
		{Value: "+15551234567", Type: "cell"}, // unchanged
		{Value: "+15550001111", Type: "home"}, // edited
	})

	got := string(v.Bytes())
	// The untouched one keeps all three of its original type parameters.
	if !strings.Contains(got, "TEL;type=CELL;type=VOICE;type=pref:+15551234567\r\n") {
		t.Errorf("the unchanged TEL was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "+15550001111") {
		t.Errorf("the edited TEL was not written:\n%s", got)
	}
	if strings.Contains(got, "+15559876543") {
		t.Errorf("the old value is still present:\n%s", got)
	}
}

func TestSetAllAddsAndRemoves(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.SetAll("TEL", []Property{
		{Value: "+15551234567", Type: "cell"},
		{Value: "+15559876543", Type: "home"},
		{Value: "+15552223333", Type: "work"},
	})
	if got := string(v.Bytes()); !strings.Contains(got, "TEL;TYPE=WORK:+15552223333") {
		t.Errorf("a third TEL was not added:\n%s", got)
	}

	v = parseOrFail(t, appleCard)
	v.SetAll("TEL", []Property{{Value: "+15551234567", Type: "cell"}})
	got := string(v.Bytes())
	if strings.Contains(got, "+15559876543") {
		t.Errorf("the removed TEL is still present:\n%s", got)
	}
	if !strings.Contains(got, "+15551234567") {
		t.Errorf("the kept TEL was removed:\n%s", got)
	}
}

func TestSetAllEmptyRemovesAll(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.SetAll("TEL", nil)

	if got := string(v.Bytes()); strings.Contains(got, "TEL") {
		t.Errorf("TEL properties remain:\n%s", got)
	}
}

func TestEscaping(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"comma", "Engines, Ltd."},
		{"semicolon", "a;b"},
		{"backslash", `back\slash`},
		{"newline", "line one\nline two"},
		{"all of them", `a,b;c\d` + "\ne"},
		{"unicode", "Ada Lovelace 💾"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := parseOrFail(t, appleCard)
			v.Set("NOTE", tt.value)

			// A written value must read back unchanged, and must not have
			// broken the card into extra lines.
			again, err := Parse(v.Bytes())
			if err != nil {
				t.Fatalf("re-parse: %v", err)
			}
			if got := again.Value("NOTE"); got != tt.value {
				t.Errorf("NOTE round-tripped to %q, want %q", got, tt.value)
			}
		})
	}
}

func TestReadingEscapedValue(t *testing.T) {
	v := parseOrFail(t, appleCard)
	if got := v.Value("ORG"); got != "Analytical Engines, Ltd.;" {
		t.Errorf("ORG = %q, want the unescaped value", got)
	}
}

func TestFoldedValueIsReadWhole(t *testing.T) {
	v := parseOrFail(t, appleCard)
	photo := v.Value("PHOTO")

	if strings.ContainsAny(photo, "\r\n") {
		t.Error("a folded value still contains line breaks")
	}
	if !strings.HasPrefix(photo, "/9j/4AAQSkZJRgABAQ") || !strings.HasSuffix(photo, "BgcICQoL") {
		t.Errorf("folded value was not joined correctly: %q", photo)
	}
}

const minimalCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"UID:min-1\r\n" +
	"FN:Someone\r\n" +
	"END:VCARD\r\n"

func TestLongValueIsFolded(t *testing.T) {
	v := parseOrFail(t, minimalCard)
	long := strings.Repeat("abcdefghij", 30)
	v.Set("NOTE", long)

	got := string(v.Bytes())
	for _, l := range strings.Split(got, "\r\n") {
		if len(l) > 75 {
			t.Errorf("line of %d octets exceeds the 75 octet limit: %q", len(l), l)
		}
	}

	again, err := Parse([]byte(got))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if again.Value("NOTE") != long {
		t.Error("a folded long value did not survive a round trip")
	}
}

// A client may store lines longer than the fold limit. They are not ours to
// reflow, and rewriting them would churn the ETag for no reason.
func TestOverlongUntouchedLineIsPreserved(t *testing.T) {
	raw := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:x\r\nNOTE:" +
		strings.Repeat("z", 200) + "\r\nEND:VCARD\r\n"

	v := parseOrFail(t, raw)
	if got := string(v.Bytes()); got != raw {
		t.Error("an over-long line was reflowed even though it was not edited")
	}
}

func TestFoldingDoesNotSplitMultibyteCharacters(t *testing.T) {
	v := parseOrFail(t, minimalCard)
	long := strings.Repeat("é", 100)
	v.Set("NOTE", long)

	got := v.Bytes()
	if !isValidUTF8(got) {
		t.Error("folding split a UTF-8 sequence")
	}

	again, err := Parse(got)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if again.Value("NOTE") != long {
		t.Error("a folded multi-byte value did not survive a round trip")
	}
}

func TestBareLFCardKeepsItsLineEndings(t *testing.T) {
	lf := strings.ReplaceAll(
		"BEGIN:VCARD\r\nVERSION:3.0\r\nUID:x\r\nFN:Someone\r\nEND:VCARD\r\n", "\r\n", "\n")

	v := parseOrFail(t, lf)
	if got := string(v.Bytes()); got != lf {
		t.Errorf("an untouched LF card changed:\n in: %q\nout: %q", lf, got)
	}

	v = parseOrFail(t, lf)
	v.Set("FN", "Someone Else")
	got := string(v.Bytes())
	if strings.Contains(got, "\r\n") {
		t.Errorf("editing an LF card introduced CRLF:\n%q", got)
	}
}

func TestTouchSetsRev(t *testing.T) {
	v := parseOrFail(t, appleCard)
	v.Touch(time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC))

	if got := v.Value("REV"); got != "20260315T093000Z" {
		t.Errorf("REV = %q, want 20260315T093000Z", got)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not a card", "hello there"},
		{"no BEGIN", "VERSION:3.0\r\nUID:x\r\nEND:VCARD\r\n"},
		{"no END", "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:x\r\n"},
		{"no VERSION", "BEGIN:VCARD\r\nUID:x\r\nEND:VCARD\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.raw)); err == nil {
				t.Error("Parse() = nil, want an error")
			}
		})
	}
}

func isValidUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		var n int
		switch {
		case c < 0x80:
			n = 1
		case c&0xE0 == 0xC0:
			n = 2
		case c&0xF0 == 0xE0:
			n = 3
		case c&0xF8 == 0xF0:
			n = 4
		default:
			return false
		}
		if i+n > len(b) {
			return false
		}
		for j := 1; j < n; j++ {
			if b[i+j]&0xC0 != 0x80 {
				return false
			}
		}
		i += n
	}
	return true
}
