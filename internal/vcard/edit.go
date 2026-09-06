package vcard

import (
	"fmt"
	"strings"
	"time"
)

// Card is a vCard open for editing. Reading it never changes anything; only
// the properties passed to Set, SetAll and Delete are rewritten, and Bytes
// returns the rest exactly as it arrived.
type Card struct {
	c *card
}

// Parse opens stored bytes for editing.
func Parse(raw []byte) (*Card, error) {
	c := parse(raw)
	if len(c.lines) == 0 {
		return nil, fmt.Errorf("vcard: empty card")
	}
	if !c.has("BEGIN") || !c.has("END") {
		return nil, fmt.Errorf("vcard: missing BEGIN:VCARD or END:VCARD")
	}
	if !c.has("VERSION") {
		return nil, fmt.Errorf("vcard: missing VERSION")
	}
	return &Card{c: c}, nil
}

// Bytes renders the card.
func (v *Card) Bytes() []byte { return v.c.bytes() }

// Value returns the value of the first property with this name, unescaped.
func (v *Card) Value(name string) string {
	for _, l := range v.c.lines {
		if l.name == strings.ToUpper(name) {
			return unescape(l.value)
		}
	}
	return ""
}

// Property is one occurrence of a repeatable property, such as one of several
// phone numbers.
type Property struct {
	Value string
	// Type is the TYPE parameter, lower-cased, empty when absent.
	Type string
}

// Values returns every occurrence of a property, in the order the card lists
// them.
func (v *Card) Values(name string) []Property {
	var out []Property
	for _, l := range v.c.lines {
		if l.name != strings.ToUpper(name) {
			continue
		}
		out = append(out, Property{Value: unescape(l.value), Type: typeParam(l.params)})
	}
	return out
}

// Set replaces the value of the first property with this name, leaving its
// parameters and position alone. An empty value removes the property; a
// property that is not present is appended before END:VCARD.
func (v *Card) Set(name, value string) {
	name = strings.ToUpper(name)

	for i := range v.c.lines {
		if v.c.lines[i].name != name {
			continue
		}
		if value == "" {
			v.c.remove(i)
			return
		}
		v.c.lines[i].value = escape(value)
		v.c.lines[i].dirty = true
		return
	}
	if value != "" {
		v.c.insert(line{name: name, value: escape(value), added: true})
	}
}

// SetAll replaces every occurrence of a repeatable property. Occurrences are
// matched to the existing lines in order, so editing one phone number rewrites
// only that line and leaves the others, with whatever parameters and grouping
// they carry, untouched.
func (v *Card) SetAll(name string, values []Property) {
	name = strings.ToUpper(name)

	var existing []int
	for i, l := range v.c.lines {
		if l.name == name {
			existing = append(existing, i)
		}
	}

	for i, want := range values {
		if i >= len(existing) {
			v.c.insert(line{
				name:   name,
				params: typeParams(want.Type),
				value:  escape(want.Value),
				added:  true,
			})
			continue
		}

		at := existing[i]
		l := &v.c.lines[at]
		if unescape(l.value) == want.Value && typeParam(l.params) == want.Type {
			continue // Unchanged: leave the original bytes in place.
		}
		l.value = escape(want.Value)
		if typeParam(l.params) != want.Type {
			l.params = typeParams(want.Type)
		}
		l.dirty = true
	}

	// Anything the form dropped goes, from the end so the indices hold.
	for i := len(existing) - 1; i >= len(values); i-- {
		v.c.remove(existing[i])
	}
}

// Delete removes every occurrence of a property.
func (v *Card) Delete(name string) {
	name = strings.ToUpper(name)
	for i := len(v.c.lines) - 1; i >= 0; i-- {
		if v.c.lines[i].name == name {
			v.c.remove(i)
		}
	}
}

// Touch stamps REV with the current time, which is how a client tells that a
// card it already holds has changed.
func (v *Card) Touch(now time.Time) {
	v.Set("REV", now.UTC().Format("20060102T150405Z"))
}

func (c *card) has(name string) bool {
	for _, l := range c.lines {
		if l.name == name {
			return true
		}
	}
	return false
}

func (c *card) remove(i int) {
	c.lines = append(c.lines[:i], c.lines[i+1:]...)
}

// insert places a new line before END:VCARD, so the card stays well formed.
func (c *card) insert(l line) {
	l.eol = c.defaultEOL()

	for i, existing := range c.lines {
		if existing.name == "END" {
			c.lines = append(c.lines[:i], append([]line{l}, c.lines[i:]...)...)
			return
		}
	}
	c.lines = append(c.lines, l)
}

func typeParam(params string) string {
	for _, part := range strings.Split(strings.TrimPrefix(params, ";"), ";") {
		name, value, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(name, "TYPE") {
			return strings.ToLower(strings.Trim(value, `"`))
		}
	}
	return ""
}

func typeParams(typ string) string {
	if typ == "" {
		return ""
	}
	return ";TYPE=" + strings.ToUpper(typ)
}

// escape applies RFC 6350 3.4 escaping to a value.
func escape(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		"\n", "\\n",
		"\r", "",
		",", "\\,",
		";", "\\;",
	)
	return r.Replace(s)
}

// unescape reverses escape. It is written as a scan rather than a Replacer
// because the sequences overlap: "\\," is a literal backslash then a separator,
// not an escaped comma.
func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}

	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
