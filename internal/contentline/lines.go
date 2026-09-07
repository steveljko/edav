package contentline

import "strings"

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

// Param reads one parameter off a raw parameter string.
func Param(params, name string) string {
	for _, part := range strings.Split(strings.TrimPrefix(params, ";"), ";") {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(key, name) {
			return strings.Trim(value, `"`)
		}
	}
	return ""
}

// Params reads every occurrence of one parameter, since a property routinely
// carries several of the same name.
func Params(params, name string) []string {
	var out []string
	for _, part := range strings.Split(strings.TrimPrefix(params, ";"), ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(key, name) {
			continue
		}
		for _, v := range strings.Split(value, ",") {
			out = append(out, strings.Trim(v, `"`))
		}
	}
	return out
}

// Escape applies the escaping both formats share to a value.
func Escape(s string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"\n", "\\n",
		"\r", "",
		",", "\\,",
		";", "\\;",
	).Replace(s)
}

// EscapeComponent escapes one component of a structured value, where the
// semicolon separating components must survive but one inside a component must
// not.
func EscapeComponent(s string) string {
	return Escape(s)
}

// Unescape reverses Escape. It is written as a scan rather than a Replacer
// because the sequences overlap: "\\," is a literal backslash then a
// separator, not an escaped comma.
func Unescape(s string) string {
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

// SplitStructured breaks a structured value on unescaped semicolons.
func SplitStructured(s string) []string {
	var parts []string
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
		case s[i] == ';':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(s[i])
		}
	}
	parts = append(parts, b.String())
	return parts
}
