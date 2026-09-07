package vcard

import (
	"fmt"
	"strings"
	"time"

	"github.com/steveljko/edav/internal/contentline"
)

// Card is a vCard open for editing. Reading it never changes anything; only
// the properties passed to Set, SetAll and Delete are rewritten, and Bytes
// returns the rest exactly as it arrived.
//
// A card stored by this server is the client's own bytes, and clients put
// properties in there that no server-side model covers: X- extensions, photos,
// Apple's label groups. Parsing a card into a struct and serialising it back
// would drop all of that.
type Card struct {
	doc  *contentline.Document
	span contentline.Span
}

// Parse opens stored bytes for editing.
func Parse(raw []byte) (*Card, error) {
	doc := contentline.Parse(raw)
	if len(doc.Lines) == 0 {
		return nil, fmt.Errorf("vcard: empty card")
	}

	span, ok := doc.Component("VCARD")
	if !ok {
		return nil, fmt.Errorf("vcard: missing BEGIN:VCARD or END:VCARD")
	}
	if doc.Value(span, "VERSION") == "" {
		return nil, fmt.Errorf("vcard: missing VERSION")
	}
	return &Card{doc: doc, span: span}, nil
}

// Bytes renders the card.
func (v *Card) Bytes() []byte { return v.doc.Bytes() }

// Value returns the value of the first property with this name, unescaped.
func (v *Card) Value(name string) string { return v.doc.Value(v.span, name) }

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
	for _, i := range v.doc.FindAll(v.span, name) {
		l := v.doc.At(i)
		out = append(out, Property{
			Value: contentline.Unescape(l.Value),
			Type:  typeParam(l.Params),
		})
	}
	return out
}

// Set replaces the value of the first property with this name, leaving its
// parameters and position alone. An empty value removes the property; a
// property that is not present is appended before END:VCARD.
func (v *Card) Set(name, value string) {
	v.doc.Set(&v.span, name, value)
}

// SetRaw is Set for a structured value whose components the caller has already
// escaped, such as N or ADR. The separating semicolons must reach the card
// unescaped, which Set would not allow.
func (v *Card) SetRaw(name, value string) {
	v.doc.SetRaw(&v.span, name, value)
}

// SetAll replaces every occurrence of a repeatable property. Occurrences are
// matched to the existing lines in order, so editing one phone number rewrites
// only that line and leaves the others, with whatever parameters and grouping
// they carry, untouched.
func (v *Card) SetAll(name string, values []Property) {
	existing := v.doc.FindAll(v.span, name)

	for i, want := range values {
		if i >= len(existing) {
			v.doc.Insert(&v.span, contentline.NewLine(
				name, typeParams(want.Type), contentline.Escape(want.Value)))
			continue
		}

		l := v.doc.At(existing[i])
		sameValue := contentline.Unescape(l.Value) == want.Value
		sameType := hasType(l.Params, want.Type)
		if sameValue && sameType {
			continue // Unchanged: leave the original bytes in place.
		}

		params := l.Params
		if !sameType {
			// Only a genuinely different type replaces the parameters, so the
			// ones a client set alongside it are not lost to a no-op edit.
			params = typeParams(want.Type)
		}
		v.doc.Replace(existing[i], params, contentline.Escape(want.Value))
	}

	// Anything the form dropped goes, from the end so the indices hold.
	for i := len(existing) - 1; i >= len(values); i-- {
		v.doc.Remove(&v.span, existing[i])
	}
}

// Delete removes every occurrence of a property.
func (v *Card) Delete(name string) { v.doc.Delete(&v.span, name) }

// Touch stamps REV with the current time, which is how a client tells that a
// card it already holds has changed.
func (v *Card) Touch(now time.Time) {
	v.Set("REV", now.UTC().Format("20060102T150405Z"))
}

// typeParam is the one type worth showing in a form: the first that a person
// would recognise as a category, rather than a transport hint like INTERNET.
func typeParam(params string) string {
	values := contentline.Params(params, "TYPE")
	for _, v := range values {
		for _, known := range TypeOptions {
			if strings.EqualFold(v, known) {
				return strings.ToLower(v)
			}
		}
	}
	if len(values) > 0 {
		return strings.ToLower(values[0])
	}
	return ""
}

// hasType reports whether a line already carries this type, in which case its
// parameters are left as they are.
func hasType(params, want string) bool {
	if want == "" {
		return true
	}
	for _, v := range contentline.Params(params, "TYPE") {
		if strings.EqualFold(v, want) {
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
