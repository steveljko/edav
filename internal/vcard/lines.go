// Package vcard edits vCards without rewriting the parts it was not asked to
// change.
//
// A card stored by this server is the client's own bytes, and clients put
// properties in there that no server-side model covers: X- extensions,
// photos, Apple's label groups. Parsing a card into a struct and serialising
// it back would drop all of that. So an edit here works on the card's content
// lines: the lines a form touched are rewritten, and every other line is
// emitted exactly as it arrived, down to its original folding and line ending.
package vcard

import (
	"strings"
)

// line is one logical content line: "[group.]NAME[;params]:value".
type line struct {
	// raw is the line exactly as read, still folded, without its terminator.
	raw string
	// eol is the terminator that followed it, so a card written with bare LF
	// comes back with bare LF.
	eol string

	group  string
	name   string // upper-cased, for comparison
	params string // raw, including the leading ';'
	value  string

	// dirty marks a line whose value was replaced, and which therefore has to
	// be rebuilt rather than emitted as it came in.
	dirty bool
	// added marks a line this package created, which never had raw bytes.
	added bool
}

// card is the ordered content lines of one vCard.
type card struct {
	lines []line
}

// parse splits raw bytes into logical content lines, undoing folding for the
// purpose of reading values while keeping the folded text for re-emission.
func parse(raw []byte) *card {
	c := &card{}

	s := string(raw)
	for len(s) > 0 {
		text, eol, rest := nextLogicalLine(s)
		s = rest
		if text == "" && eol == "" {
			break
		}

		l := line{raw: text, eol: eol}
		l.group, l.name, l.params, l.value = splitLine(unfold(text))
		c.lines = append(c.lines, l)
	}
	return c
}

// nextLogicalLine returns one content line, following folds. A fold is a line
// break followed by a space or tab, and belongs to the line it continues.
func nextLogicalLine(s string) (text, eol, rest string) {
	var b strings.Builder

	for {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 {
			b.WriteString(s)
			return b.String(), "", ""
		}

		term := "\n"
		next := i + 1
		if s[i] == '\r' {
			if next < len(s) && s[next] == '\n' {
				term = "\r\n"
				next++
			} else {
				term = "\r"
			}
		}

		b.WriteString(s[:i])
		rest = s[next:]

		// A continuation keeps its break and leading whitespace, because the
		// line has to be re-emitted byte for byte if it is not edited.
		if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
			b.WriteString(term)
			b.WriteByte(rest[0])
			s = rest[1:]
			continue
		}
		return b.String(), term, rest
	}
}

// unfold removes the line breaks and the single whitespace that follows them,
// yielding the logical value.
func unfold(text string) string {
	if !strings.ContainsAny(text, "\r\n") {
		return text
	}

	var b strings.Builder
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			if i+1 < len(text) && (text[i+1] == ' ' || text[i+1] == '\t') {
				i++
			}
		case '\n':
			if i+1 < len(text) && (text[i+1] == ' ' || text[i+1] == '\t') {
				i++
			}
		default:
			b.WriteByte(text[i])
		}
	}
	return b.String()
}

// splitLine breaks a logical line into its parts. The colon that ends the name
// and parameters is the first one outside a quoted parameter value.
func splitLine(s string) (group, name, params, value string) {
	colon := -1
	quoted := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return "", strings.ToUpper(s), "", ""
	}

	head, value := s[:colon], s[colon+1:]
	if i := strings.Index(head, ";"); i >= 0 {
		params = head[i:]
		head = head[:i]
	}
	if i := strings.Index(head, "."); i >= 0 {
		group = head[:i]
		head = head[i+1:]
	}
	return group, strings.ToUpper(head), params, value
}

// bytes renders the card. Untouched lines are written back exactly as they
// were read.
func (c *card) bytes() []byte {
	var b strings.Builder
	for _, l := range c.lines {
		if l.dirty || l.added {
			b.WriteString(fold(l.rebuild(), c.defaultEOL()))
		} else {
			b.WriteString(l.raw)
		}

		eol := l.eol
		if eol == "" {
			eol = c.defaultEOL()
		}
		b.WriteString(eol)
	}
	return []byte(b.String())
}

// defaultEOL follows whatever the card already uses. RFC 6350 requires CRLF,
// but a card that arrived with bare LF is written back with bare LF.
func (c *card) defaultEOL() string {
	for _, l := range c.lines {
		if l.eol != "" {
			return l.eol
		}
	}
	return "\r\n"
}

func (l line) rebuild() string {
	var b strings.Builder
	if l.group != "" {
		b.WriteString(l.group)
		b.WriteByte('.')
	}
	b.WriteString(l.name)
	b.WriteString(l.params)
	b.WriteByte(':')
	b.WriteString(l.value)
	return b.String()
}

// fold wraps a rebuilt line at 75 octets, as RFC 6350 3.2 asks. Splits happen
// on octet boundaries that do not divide a UTF-8 sequence, since a lone
// continuation byte is not a character.
func fold(s, eol string) string {
	const limit = 75

	if len(s) <= limit {
		return s
	}

	var b strings.Builder
	for start := 0; start < len(s); {
		// A continuation carries a leading space, which counts toward the
		// limit.
		width := limit
		if start > 0 {
			b.WriteString(" ")
			width--
		}

		end := start + width
		if end >= len(s) {
			b.WriteString(s[start:])
			break
		}
		for end > start && s[end]&0xC0 == 0x80 {
			end--
		}

		b.WriteString(s[start:end])
		b.WriteString(eol)
		start = end
	}
	return b.String()
}
