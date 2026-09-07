// Package ical edits calendar objects without rewriting the parts it was not
// asked to change.
//
// The same reasoning as internal/vcard: a stored object is the client's bytes,
// carrying alarms, attendees, attachments and vendor extensions that no
// server-side model covers. An edit rewrites the properties a form shows and
// leaves every other line exactly as it arrived.
package ical

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/steveljko/edav/internal/contentline"
)

const (
	dateLayout     = "20060102"
	dateTimeLayout = "20060102T150405"
	utcLayout      = "20060102T150405Z"
)

// Event is the part of a calendar object the admin interface edits.
type Event struct {
	UID         string
	Summary     string
	Location    string
	Description string

	// Start and End are wall times in Timezone, or dates when AllDay.
	Start  time.Time
	End    time.Time
	AllDay bool
	// Timezone is an IANA name, empty for UTC.
	Timezone string

	// Recurrence is the RRULE as stored, empty when the event does not repeat.
	// It is read for display and carried through an edit untouched; this
	// package does not compose or alter one.
	Recurrence string
}

// Object is a calendar object open for editing.
type Object struct {
	doc   *contentline.Document
	event contentline.Span
}

// Parse opens stored bytes for editing. Only the first VEVENT is editable
// here: the others are recurrence overrides, which a form cannot express and
// which are left untouched.
func Parse(raw []byte) (*Object, error) {
	doc := contentline.Parse(raw)
	if len(doc.Lines) == 0 {
		return nil, fmt.Errorf("ical: empty object")
	}
	if _, ok := doc.Component("VCALENDAR"); !ok {
		return nil, fmt.Errorf("ical: missing BEGIN:VCALENDAR or END:VCALENDAR")
	}

	span, ok := doc.Component("VEVENT")
	if !ok {
		return nil, fmt.Errorf("ical: no VEVENT to edit")
	}
	return &Object{doc: doc, event: span}, nil
}

// Bytes renders the object.
func (o *Object) Bytes() []byte { return o.doc.Bytes() }

// HasOverrides reports whether the object carries recurrence overrides, which
// an edit here cannot express and must not disturb.
func (o *Object) HasOverrides() bool {
	for _, span := range o.doc.Components("VEVENT") {
		if _, ok := o.doc.Find(span, "RECURRENCE-ID"); ok {
			return true
		}
	}
	return false
}

// ReadEvent reads the editable properties out of a calendar object.
func ReadEvent(raw []byte) (*Event, error) {
	o, err := Parse(raw)
	if err != nil {
		return nil, err
	}

	e := &Event{
		UID:         o.doc.Value(o.event, "UID"),
		Summary:     o.doc.Value(o.event, "SUMMARY"),
		Location:    o.doc.Value(o.event, "LOCATION"),
		Description: o.doc.Value(o.event, "DESCRIPTION"),
		Recurrence:  o.doc.RawValue(o.event, "RRULE"),
	}

	start, allDay, zone, err := readTime(o, "DTSTART")
	if err != nil {
		return nil, err
	}
	e.Start, e.AllDay, e.Timezone = start, allDay, zone

	if _, ok := o.doc.Find(o.event, "DTEND"); ok {
		end, _, _, err := readTime(o, "DTEND")
		if err != nil {
			return nil, err
		}
		e.End = end
	} else if d := o.doc.Value(o.event, "DURATION"); d != "" {
		if dur, err := parseDuration(d); err == nil {
			e.End = e.Start.Add(dur)
		}
	}
	if e.End.IsZero() {
		e.End = e.Start
		if allDay {
			e.End = e.Start.AddDate(0, 0, 1)
		}
	}
	return e, nil
}

// readTime reads a date or date-time property as the wall time it names,
// alongside whether it is a whole day and which zone it is in.
func readTime(o *Object, name string) (time.Time, bool, string, error) {
	i, ok := o.doc.Find(o.event, name)
	if !ok {
		return time.Time{}, false, "", fmt.Errorf("ical: %s has no %s", "VEVENT", name)
	}

	line := o.doc.At(i)
	value := strings.TrimSpace(line.Value)
	zone := contentline.Param(line.Params, "TZID")

	switch {
	case len(value) == len(dateLayout):
		t, err := time.ParseInLocation(dateLayout, value, time.UTC)
		return t, true, zone, err
	case strings.HasSuffix(value, "Z"):
		t, err := time.ParseInLocation(utcLayout, value, time.UTC)
		return t, false, "", err
	case len(value) == len(dateTimeLayout):
		t, err := time.ParseInLocation(dateTimeLayout, value, time.UTC)
		return t, false, zone, err
	default:
		return time.Time{}, false, zone, fmt.Errorf("ical: cannot read %s value %q", name, value)
	}
}

// Apply writes an event's fields onto an existing object, touching only the
// properties it models, and stamps DTSTAMP and SEQUENCE.
//
// RRULE, EXDATE, RDATE and any recurrence overrides are left exactly as they
// were: a form that cannot express them must not be able to destroy them.
func Apply(raw []byte, e *Event, now time.Time) ([]byte, error) {
	o, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := e.validate(); err != nil {
		return nil, err
	}

	o.doc.Set(&o.event, "SUMMARY", e.Summary)
	o.doc.Set(&o.event, "LOCATION", e.Location)
	o.doc.Set(&o.event, "DESCRIPTION", e.Description)

	params, start := formatTime(e.Start, e.AllDay, e.Timezone)
	o.doc.SetWithParams(&o.event, "DTSTART", params, start)

	// An event carrying DURATION instead of DTEND keeps that shape rather than
	// gaining a second way to say when it ends.
	if _, hasDuration := o.doc.Find(o.event, "DURATION"); hasDuration {
		o.doc.Set(&o.event, "DURATION", formatDuration(e.End.Sub(e.Start)))
	} else {
		endParams, end := formatTime(e.End, e.AllDay, e.Timezone)
		o.doc.SetWithParams(&o.event, "DTEND", endParams, end)
	}

	o.doc.Set(&o.event, "DTSTAMP", now.UTC().Format(utcLayout))
	o.doc.Set(&o.event, "LAST-MODIFIED", now.UTC().Format(utcLayout))
	bumpSequence(o)

	return o.Bytes(), nil
}

// bumpSequence advances SEQUENCE, which is how a client that already holds the
// event knows this version supersedes the one it has.
func bumpSequence(o *Object) {
	next := 1
	if current := o.doc.Value(o.event, "SEQUENCE"); current != "" {
		var n int
		if _, err := fmt.Sscanf(current, "%d", &n); err == nil {
			next = n + 1
		}
	}
	o.doc.Set(&o.event, "SEQUENCE", fmt.Sprint(next))
}

// New builds a calendar object for an event that does not exist yet.
func New(e *Event, now time.Time) ([]byte, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}

	uid := e.UID
	if uid == "" {
		uid = NewUID()
	}

	eol := "\r\n"
	base := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//edav//EN",
		"BEGIN:VEVENT",
		"UID:" + uid,
		"DTSTAMP:" + now.UTC().Format(utcLayout),
		"END:VEVENT",
		"END:VCALENDAR",
		"",
	}, eol)

	out, err := Apply([]byte(base), e, now)
	if err != nil {
		return nil, err
	}

	// A repeat is only ever composed here, never edited: an existing rule
	// reaches Apply untouched.
	if e.Recurrence != "" {
		o, err := Parse(out)
		if err != nil {
			return nil, err
		}
		o.doc.SetRaw(&o.event, "RRULE", e.Recurrence)
		out = o.Bytes()
	}
	return out, nil
}

// NewUID returns a random identifier for a new object.
func NewUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("ical: read random uid: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Filename is the collection-relative URI for a new object.
func Filename(uid string) string {
	return strings.NewReplacer("/", "-", "\\", "-", "?", "-", "#", "-", "%", "-").
		Replace(uid) + ".ics"
}

func (e *Event) validate() error {
	if strings.TrimSpace(e.Summary) == "" {
		return fmt.Errorf("ical: an event needs a title")
	}
	if e.Start.IsZero() {
		return fmt.Errorf("ical: an event needs a start")
	}
	if e.End.Before(e.Start) {
		return fmt.Errorf("ical: an event cannot end before it starts")
	}
	if e.Timezone != "" && !e.AllDay {
		if _, err := time.LoadLocation(e.Timezone); err != nil {
			return fmt.Errorf("ical: unknown time zone %q", e.Timezone)
		}
	}
	return nil
}

// formatTime renders a start or end, with the parameters that describe it.
func formatTime(t time.Time, allDay bool, zone string) (params, value string) {
	if allDay {
		return ";VALUE=DATE", t.Format(dateLayout)
	}
	if zone == "" {
		return "", t.Format(utcLayout)
	}
	return ";TZID=" + zone, t.Format(dateTimeLayout)
}

// Instant resolves a wall time in the event's zone to an absolute instant, for
// the index columns.
func (e *Event) Instant(t time.Time) time.Time {
	if e.Timezone == "" {
		return t.UTC()
	}
	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		return t.UTC()
	}
	return time.Date(t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(), 0, loc).UTC()
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}

	var b strings.Builder
	b.WriteString("P")

	if days := int(d / (24 * time.Hour)); days > 0 {
		fmt.Fprintf(&b, "%dD", days)
		d -= time.Duration(days) * 24 * time.Hour
	}
	if d == 0 {
		return b.String()
	}

	b.WriteString("T")
	if hours := int(d / time.Hour); hours > 0 {
		fmt.Fprintf(&b, "%dH", hours)
		d -= time.Duration(hours) * time.Hour
	}
	if minutes := int(d / time.Minute); minutes > 0 {
		fmt.Fprintf(&b, "%dM", minutes)
		d -= time.Duration(minutes) * time.Minute
	}
	if seconds := int(d / time.Second); seconds > 0 {
		fmt.Fprintf(&b, "%dS", seconds)
	}
	return b.String()
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")

	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("ical: %q is not a duration", s)
	}
	s = s[1:]

	var total time.Duration
	var inTime bool
	var number strings.Builder

	for _, r := range s {
		switch {
		case r == 'T':
			inTime = true
		case r >= '0' && r <= '9':
			number.WriteRune(r)
		default:
			var n int
			if _, err := fmt.Sscanf(number.String(), "%d", &n); err != nil {
				return 0, fmt.Errorf("ical: %q is not a duration", s)
			}
			number.Reset()

			switch r {
			case 'W':
				total += time.Duration(n) * 7 * 24 * time.Hour
			case 'D':
				total += time.Duration(n) * 24 * time.Hour
			case 'H':
				total += time.Duration(n) * time.Hour
			case 'M':
				if inTime {
					total += time.Duration(n) * time.Minute
				} else {
					return 0, fmt.Errorf("ical: months are not a fixed duration")
				}
			case 'S':
				total += time.Duration(n) * time.Second
			default:
				return 0, fmt.Errorf("ical: %q is not a duration", s)
			}
		}
	}

	if negative {
		total = -total
	}
	return total, nil
}
