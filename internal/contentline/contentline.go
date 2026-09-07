// Package contentline reads and rewrites the content-line format shared by
// vCard and iCalendar, changing only the lines it is asked to change.
//
// Both formats are lists of "[group.]NAME[;params]:value" lines, folded at 75
// octets, grouped by BEGIN and END. A document parsed here keeps every line's
// original text, so anything untouched is written back byte for byte: the
// custom properties clients store, their folding, and their line endings.
package contentline

import (
	"strings"
)

// Line is one logical content line.
type Line struct {
	// raw is the line exactly as read, still folded, without its terminator.
	raw string
	// eol is the terminator that followed it, so a document written with bare
	// LF comes back with bare LF.
	eol string

	Group  string
	Name   string // upper-cased, for comparison
	Params string // raw, including the leading ';'
	Value  string // still escaped

	dirty bool
	added bool
}

// Document is the ordered content lines of one vCard or iCalendar object.
type Document struct {
	Lines []Line
}

// Parse splits raw bytes into logical content lines.
func Parse(raw []byte) *Document {
	d := &Document{}

	s := string(raw)
	for len(s) > 0 {
		text, eol, rest := nextLogicalLine(s)
		s = rest
		if text == "" && eol == "" {
			break
		}

		l := Line{raw: text, eol: eol}
		l.Group, l.Name, l.Params, l.Value = splitLine(unfold(text))
		d.Lines = append(d.Lines, l)
	}
	return d
}

// Bytes renders the document. Untouched lines are written back exactly as they
// were read.
func (d *Document) Bytes() []byte {
	var b strings.Builder
	for _, l := range d.Lines {
		if l.dirty || l.added {
			b.WriteString(fold(l.rebuild(), d.EOL()))
		} else {
			b.WriteString(l.raw)
		}

		eol := l.eol
		if eol == "" {
			eol = d.EOL()
		}
		b.WriteString(eol)
	}
	return []byte(b.String())
}

// EOL follows whatever the document already uses. Both formats require CRLF,
// but one that arrived with bare LF is written back with bare LF.
func (d *Document) EOL() string {
	for _, l := range d.Lines {
		if l.eol != "" {
			return l.eol
		}
	}
	return "\r\n"
}

// Span is a component: the index of its BEGIN line and of its END line.
type Span struct {
	Begin, End int
}

// Whole is the span covering every line, for a document addressed as one unit.
func (d *Document) Whole() Span {
	return Span{Begin: -1, End: len(d.Lines)}
}

// Component finds the first component with this name, at any depth. iCalendar
// nests them, so a VEVENT is found inside its VCALENDAR without the caller
// having to walk the tree.
func (d *Document) Component(name string) (Span, bool) {
	spans := d.Components(name)
	if len(spans) == 0 {
		return Span{}, false
	}
	return spans[0], true
}

// Components finds every component with this name, outermost first.
func (d *Document) Components(name string) []Span {
	name = strings.ToUpper(name)

	var spans []Span
	var open []int

	for i, l := range d.Lines {
		switch l.Name {
		case "BEGIN":
			open = append(open, i)
		case "END":
			if len(open) == 0 {
				continue
			}
			begin := open[len(open)-1]
			open = open[:len(open)-1]
			if strings.EqualFold(d.Lines[begin].Value, name) {
				spans = append(spans, Span{Begin: begin, End: i})
			}
		}
	}

	// Components closes innermost first; callers expect document order.
	for i, j := 0, len(spans)-1; i < j; i, j = i+1, j-1 {
		spans[i], spans[j] = spans[j], spans[i]
	}
	return spans
}

// contains reports whether an index belongs to this component directly, rather
// than to one nested inside it.
func (d *Document) contains(span Span, i int) bool {
	if i <= span.Begin || i >= span.End {
		return false
	}

	depth := 0
	for j := span.Begin + 1; j < i; j++ {
		switch d.Lines[j].Name {
		case "BEGIN":
			depth++
		case "END":
			depth--
		}
	}
	return depth == 0
}

// Find returns the index of the first property with this name inside the
// component, ignoring any nested one.
func (d *Document) Find(span Span, name string) (int, bool) {
	name = strings.ToUpper(name)
	for i := span.Begin + 1; i < span.End && i < len(d.Lines); i++ {
		if d.Lines[i].Name == name && d.contains(span, i) {
			return i, true
		}
	}
	return 0, false
}

// FindAll returns every index for this property inside the component.
func (d *Document) FindAll(span Span, name string) []int {
	name = strings.ToUpper(name)

	var out []int
	for i := span.Begin + 1; i < span.End && i < len(d.Lines); i++ {
		if d.Lines[i].Name == name && d.contains(span, i) {
			out = append(out, i)
		}
	}
	return out
}

// Value returns the unescaped value of a property, empty when absent.
func (d *Document) Value(span Span, name string) string {
	i, ok := d.Find(span, name)
	if !ok {
		return ""
	}
	return Unescape(d.Lines[i].Value)
}

// RawValue returns a value without unescaping it, for structured properties
// whose components are split separately.
func (d *Document) RawValue(span Span, name string) string {
	i, ok := d.Find(span, name)
	if !ok {
		return ""
	}
	return d.Lines[i].Value
}

// Param returns a parameter of a property, empty when absent.
func (d *Document) Param(span Span, name, param string) string {
	i, ok := d.Find(span, name)
	if !ok {
		return ""
	}
	return Param(d.Lines[i].Params, param)
}

// Set replaces the value of a property, keeping its parameters and position.
// An empty value removes it; a property that is not present is appended just
// before the component's END.
func (d *Document) Set(span *Span, name, value string) {
	d.set(span, name, Escape(value), nil)
}

// SetRaw is Set for a value the caller has already escaped, such as a
// structured value whose separating semicolons must survive.
func (d *Document) SetRaw(span *Span, name, value string) {
	d.set(span, name, value, nil)
}

// SetWithParams is Set for a property whose parameters are being replaced too,
// such as a date-time gaining or losing a TZID.
func (d *Document) SetWithParams(span *Span, name, params, value string) {
	d.set(span, name, value, &params)
}

// set writes a value. A nil params leaves whatever the line already carries,
// which is what an edit of the value alone must do: a client's parameters are
// not the form's to discard.
func (d *Document) set(span *Span, name, value string, params *string) {
	name = strings.ToUpper(name)

	if i, ok := d.Find(*span, name); ok {
		if value == "" {
			d.Remove(span, i)
			return
		}
		d.Lines[i].Value = value
		if params != nil {
			d.Lines[i].Params = *params
		}
		d.Lines[i].dirty = true
		return
	}
	if value == "" {
		return
	}

	var p string
	if params != nil {
		p = *params
	}
	d.Insert(span, Line{Name: name, Params: p, Value: value, added: true})
}

// Insert places a new line just before the component's END.
func (d *Document) Insert(span *Span, l Line) {
	l.eol = d.EOL()
	l.added = true

	at := span.End
	if at < 0 || at > len(d.Lines) {
		at = len(d.Lines)
	}

	d.Lines = append(d.Lines[:at], append([]Line{l}, d.Lines[at:]...)...)
	span.End++
}

// Remove drops a line and, when it belongs to a group, the other lines of that
// group with it. A group binds a property to its satellites -- Apple writes
// "item1.URL" alongside "item1.X-ABLabel" -- and leaving the label behind
// without the property it labels corrupts the object.
func (d *Document) Remove(span *Span, i int) {
	group := d.Lines[i].Group
	if group == "" {
		d.Lines = append(d.Lines[:i], d.Lines[i+1:]...)
		span.End--
		return
	}

	kept := d.Lines[:0]
	removed := 0
	for j, l := range d.Lines {
		if l.Group == group && d.contains(*span, j) {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	d.Lines = kept
	span.End -= removed
}

// Delete removes every occurrence of a property inside the component.
func (d *Document) Delete(span *Span, name string) {
	name = strings.ToUpper(name)
	for {
		i, ok := d.Find(*span, name)
		if !ok {
			return
		}
		d.Remove(span, i)
	}
}

// NewLine builds a line for Insert.
func NewLine(name, params, value string) Line {
	return Line{Name: strings.ToUpper(name), Params: params, Value: value, added: true}
}

// At returns a line by index, for callers walking FindAll.
func (d *Document) At(i int) Line { return d.Lines[i] }

// Replace rewrites one line's value and parameters in place.
func (d *Document) Replace(i int, params, value string) {
	d.Lines[i].Params = params
	d.Lines[i].Value = value
	d.Lines[i].dirty = true
}

func (l Line) rebuild() string {
	var b strings.Builder
	if l.Group != "" {
		b.WriteString(l.Group)
		b.WriteByte('.')
	}
	b.WriteString(l.Name)
	b.WriteString(l.Params)
	b.WriteByte(':')
	b.WriteString(l.Value)
	return b.String()
}
