package vcard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/steveljko/edav/internal/contentline"
)

// Contact is the part of a vCard the admin interface edits. Everything else in
// the card is left alone; this is a view over a few properties, not a
// replacement for the card.
type Contact struct {
	UID           string
	FormattedName string
	FamilyName    string
	GivenName     string
	Organisation  string
	Title         string
	Note          string
	Phones        []Property
	Emails        []Property
	URL           string
}

// TypeOptions are the TYPE values the interface offers. Any other value already
// on a card is preserved; these are only what the form can choose from.
var TypeOptions = []string{"cell", "home", "work", "other"}

// ReadContact reads the editable properties out of a card.
func ReadContact(raw []byte) (*Contact, error) {
	v, err := Parse(raw)
	if err != nil {
		return nil, err
	}

	c := &Contact{
		UID:           v.Value("UID"),
		FormattedName: v.Value("FN"),
		Organisation:  firstField(v.doc.RawValue(v.span, "ORG")),
		Title:         v.Value("TITLE"),
		Note:          v.Value("NOTE"),
		Phones:        v.Values("TEL"),
		Emails:        v.Values("EMAIL"),
		URL:           v.Value("URL"),
	}

	// N is "family;given;additional;prefix;suffix". It is split before being
	// unescaped, or a name containing an escaped semicolon would be read as two
	// fields.
	if n := v.doc.RawValue(v.span, "N"); n != "" {
		parts := contentline.SplitStructured(n)
		if len(parts) > 0 {
			c.FamilyName = parts[0]
		}
		if len(parts) > 1 {
			c.GivenName = parts[1]
		}
	}
	return c, nil
}

// Apply writes a contact's fields onto an existing card, touching only the
// properties it models, and stamps REV.
func Apply(raw []byte, c *Contact, now time.Time) ([]byte, error) {
	v, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}

	c.normalise()

	v.Set("FN", c.FormattedName)
	v.SetRaw("N", c.structuredName())
	v.Set("TITLE", c.Title)
	v.Set("NOTE", c.Note)
	v.Set("URL", c.URL)
	v.SetAll("TEL", nonEmpty(c.Phones))
	v.SetAll("EMAIL", nonEmpty(c.Emails))

	// ORG is structured too; only its first field is edited here, so the rest
	// of an existing value is kept.
	v.SetRaw("ORG", c.organisationValue(v.doc.RawValue(v.span, "ORG")))

	v.Touch(now)
	return v.Bytes(), nil
}

// New builds a card for a contact that does not exist yet.
func New(c *Contact, now time.Time) ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}

	uid := c.UID
	if uid == "" {
		uid = NewUID()
	}

	base := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:" + uid + "\r\nEND:VCARD\r\n"
	out, err := Apply([]byte(base), c, now)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NewUID returns a random identifier for a new card.
func NewUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// A UID this server generates is not security sensitive, but it must
		// be unique; there is no sensible way to continue without entropy.
		panic("vcard: read random uid: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Filename is the collection-relative URI for a new card.
func Filename(uid string) string {
	return strings.NewReplacer("/", "-", "\\", "-", "?", "-", "#", "-", "%", "-").
		Replace(uid) + ".vcf"
}

func (c *Contact) validate() error {
	if strings.TrimSpace(c.FormattedName) == "" &&
		strings.TrimSpace(c.GivenName) == "" && strings.TrimSpace(c.FamilyName) == "" {
		return fmt.Errorf("vcard: a contact needs at least a name")
	}
	return nil
}

// DisplayName is what the contact should be called, falling back to the
// structured name when FN is absent.
func (c *Contact) DisplayName() string {
	if c.FormattedName != "" {
		return c.FormattedName
	}
	return strings.TrimSpace(c.GivenName + " " + c.FamilyName)
}

func (c *Contact) structuredName() string {
	if c.FamilyName == "" && c.GivenName == "" {
		return ""
	}
	return contentline.EscapeComponent(c.FamilyName) + ";" + contentline.EscapeComponent(c.GivenName) + ";;;"
}

// organisationValue keeps any department fields an existing ORG carried.
func (c *Contact) organisationValue(existing string) string {
	if c.Organisation == "" {
		return ""
	}

	rest := contentline.SplitStructured(existing)
	value := contentline.EscapeComponent(c.Organisation)
	if len(rest) > 1 {
		for _, part := range rest[1:] {
			value += ";" + contentline.EscapeComponent(part)
		}
	}
	return value
}

// FN is required by RFC 6350, so a contact given only a structured name still
// gets one.
func (c *Contact) normalise() {
	if c.FormattedName == "" {
		c.FormattedName = c.DisplayName()
	}
}

func nonEmpty(props []Property) []Property {
	out := make([]Property, 0, len(props))
	for _, p := range props {
		if strings.TrimSpace(p.Value) != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstField(s string) string {
	return contentline.SplitStructured(s)[0]
}
