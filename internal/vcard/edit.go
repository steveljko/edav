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

// SetRaw is Set for a structured value whose components the caller has already
// escaped, such as N or ADR. The separating semicolons must reach the card
// unescaped, which Set would not allow.
func (v *Card) SetRaw(name, value string) {
	name = strings.ToUpper(name)

	for i := range v.c.lines {
		if v.c.lines[i].name != name {
			continue
		}
		if value == "" {
			v.c.remove(i)
			return
		}
		v.c.lines[i].value = value
		v.c.lines[i].dirty = true
		return
	}
	if value != "" {
		v.c.insert(line{name: name, value: value, added: true})
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

		sameValue := unescape(l.value) == want.Value
		sameType := hasType(l.params, want.Type)
		if sameValue && sameType {
			continue // Unchanged: leave the original bytes in place.
		}

		l.value = escape(want.Value)
		if !sameType {
			// Only a genuinely different type replaces the parameters, so the
			// ones a client set alongside it are not lost to a no-op edit.
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

// remove drops a line and, when it belongs to a group, the other lines of that
// group with it. A group binds a property to its satellites -- Apple writes
// "item1.URL" alongside "item1.X-ABLabel" -- and leaving the label behind
// without the property it labels corrupts the card.
func (c *card) remove(i int) {
	group := c.lines[i].group
	if group == "" {
		c.lines = append(c.lines[:i], c.lines[i+1:]...)
		return
	}

	kept := c.lines[:0]
	for _, l := range c.lines {
		if l.group != group {
			kept = append(kept, l)
		}
	}
	c.lines = kept
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

// typeValues returns every TYPE parameter on a line. A property routinely
// carries several -- "TYPE=INTERNET;TYPE=HOME;TYPE=pref" -- and treating only
// the first as the type throws the rest away on the next save.
func typeValues(params string) []string {
	var out []string
	for _, part := range strings.Split(strings.TrimPrefix(params, ";"), ";") {
		name, value, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(name, "TYPE") {
			continue
		}
		for _, v := range strings.Split(value, ",") {
			out = append(out, strings.ToLower(strings.Trim(v, `"`)))
		}
	}
	return out
}

// typeParam is the one type worth showing in a form: the first that a person
// would recognise as a category, rather than a transport hint like INTERNET.
func typeParam(params string) string {
	values := typeValues(params)
	for _, v := range values {
		for _, known := range TypeOptions {
			if v == known {
				return v
			}
		}
	}
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

// hasType reports whether a line already carries this type, in which case its
// parameters are left as they are.
func hasType(params, want string) bool {
	if want == "" {
		return true
	}
	for _, v := range typeValues(params) {
		if v == want {
			return true
		}
	}
	return false
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
