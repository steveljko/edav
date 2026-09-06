// Package recurrence expands iCalendar recurrence rules into concrete
// instances, and resolves the time zones those instances are expressed in.
package recurrence

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// Timezones resolves TZID references. It prefers the IANA database, and falls
// back to the VTIMEZONE components carried in the calendar object.
//
// The fallback is not optional in practice: Outlook and Exchange emit names
// like "W. Europe Standard Time", and some clients prefix IANA names with their
// own namespace ("/freeassociation.sourceforge.net/Europe/Berlin"). Neither
// resolves through time.LoadLocation, and neither is rare.
type Timezones struct {
	embedded map[string][]observance
}

// observance is one STANDARD or DAYLIGHT block of a VTIMEZONE.
type observance struct {
	start      time.Time // DTSTART, a wall time carried in UTC
	offsetFrom int       // seconds east of UTC before this observance
	offsetTo   int       // seconds east of UTC during this observance
	rule       *rrule.RRule
	rdates     []time.Time
}

// NewTimezones collects the VTIMEZONE components of a calendar. A nil calendar
// yields a resolver that still handles IANA names.
func NewTimezones(cal *ical.Calendar) *Timezones {
	tz := &Timezones{embedded: make(map[string][]observance)}
	if cal == nil {
		return tz
	}

	for _, comp := range cal.Children {
		if comp.Name != ical.CompTimezone {
			continue
		}
		tzid, err := comp.Props.Text(ical.PropTimezoneID)
		if err != nil || tzid == "" {
			continue
		}

		var observances []observance
		for _, child := range comp.Children {
			if child.Name != "STANDARD" && child.Name != "DAYLIGHT" {
				continue
			}
			o, err := parseObservance(child)
			if err != nil {
				continue
			}
			observances = append(observances, o)
		}
		if len(observances) > 0 {
			tz.embedded[tzid] = observances
		}
	}
	return tz
}

func parseObservance(comp *ical.Component) (observance, error) {
	var o observance

	startProp := comp.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return o, fmt.Errorf("observance has no DTSTART")
	}
	start, err := parseWallTime(startProp.Value)
	if err != nil {
		return o, err
	}
	o.start = start

	if o.offsetFrom, err = parseUTCOffset(comp.Props.Get("TZOFFSETFROM")); err != nil {
		return o, err
	}
	if o.offsetTo, err = parseUTCOffset(comp.Props.Get("TZOFFSETTO")); err != nil {
		return o, err
	}

	if opt, err := comp.Props.RecurrenceRule(); err == nil && opt != nil {
		opt.Dtstart = ruleAnchor(o.start)
		if rule, err := rrule.NewRRule(*opt); err == nil {
			o.rule = rule
		}
	}
	for _, prop := range comp.Props[ical.PropRecurrenceDates] {
		for _, value := range strings.Split(prop.Value, ",") {
			if t, err := parseWallTime(value); err == nil {
				o.rdates = append(o.rdates, t)
			}
		}
	}
	return o, nil
}

// Resolve returns the location a TZID names. For an embedded VTIMEZONE the
// result is a fixed-offset zone valid at wall, since Go cannot build a full
// time.Location without TZif data; callers converting a series of wall times
// must resolve each one.
func (tz *Timezones) Resolve(tzid string, wall time.Time) (*time.Location, error) {
	if tzid == "" {
		return time.UTC, nil
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, nil
	}
	// Some clients namespace the IANA name; the last two segments are it.
	if loc, err := time.LoadLocation(trimTZIDPrefix(tzid)); err == nil {
		return loc, nil
	}
	if offset, ok := tz.embeddedOffset(tzid, wall); ok {
		return time.FixedZone(tzid, offset), nil
	}
	return nil, fmt.Errorf("recurrence: unknown time zone %q", tzid)
}

// ToUTC interprets a wall time as being in the named zone and returns the
// corresponding instant.
func (tz *Timezones) ToUTC(tzid string, wall time.Time) (time.Time, error) {
	loc, err := tz.Resolve(tzid, wall)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(wall.Year(), wall.Month(), wall.Day(),
		wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), loc).UTC(), nil
}

// embeddedOffset finds the offset in force at a wall time. The wall time is
// first taken as if it were UTC to pick a candidate observance, then the answer
// is refined once against the resulting instant. Only a wall time within a few
// hours of a transition can disagree between the two passes, and iterating
// further cannot resolve a wall time that is genuinely ambiguous or skipped.
func (tz *Timezones) embeddedOffset(tzid string, wall time.Time) (int, bool) {
	observances, ok := tz.embedded[tzid]
	if !ok {
		return 0, false
	}

	offset, ok := offsetAt(observances, wall)
	if !ok {
		return 0, false
	}
	return offsetAt(observances, wall.Add(-time.Duration(offset)*time.Second))
}

// offsetAt returns the offset of the observance whose most recent transition
// falls at or before instant.
func offsetAt(observances []observance, instant time.Time) (int, bool) {
	type transition struct {
		at     time.Time
		offset int
	}

	// A window rather than a full expansion: VTIMEZONE rules are yearly, so a
	// few years either side is always enough to bracket the instant.
	from := instant.AddDate(-3, 0, 0)
	to := instant.AddDate(1, 0, 0)

	var transitions []transition
	earliest := transition{at: time.Time{}}
	for _, o := range observances {
		// A transition's DTSTART is a wall time in the *previous* offset.
		add := func(wall time.Time) {
			at := wall.Add(-time.Duration(o.offsetFrom) * time.Second)
			transitions = append(transitions, transition{at: at, offset: o.offsetTo})
			if earliest.at.IsZero() || at.Before(earliest.at) {
				earliest = transition{at: at, offset: o.offsetFrom}
			}
		}

		add(o.start)
		for _, rd := range o.rdates {
			add(rd)
		}
		if o.rule != nil {
			for _, occ := range o.rule.Between(from, to, true) {
				add(occ)
			}
		}
	}
	if len(transitions) == 0 {
		return 0, false
	}

	sort.Slice(transitions, func(i, j int) bool { return transitions[i].at.Before(transitions[j].at) })

	best := -1
	for i, t := range transitions {
		if t.at.After(instant) {
			break
		}
		best = i
	}
	if best < 0 {
		// Before the first transition the zone is still what it was coming in.
		return earliest.offset, true
	}
	return transitions[best].offset, true
}

func parseUTCOffset(prop *ical.Prop) (int, error) {
	if prop == nil {
		return 0, fmt.Errorf("observance is missing a UTC offset")
	}

	value := strings.TrimSpace(prop.Value)
	if len(value) < 5 {
		return 0, fmt.Errorf("malformed UTC offset %q", prop.Value)
	}

	sign := 1
	switch value[0] {
	case '-':
		sign = -1
	case '+':
	default:
		return 0, fmt.Errorf("malformed UTC offset %q", prop.Value)
	}

	hours, err := strconv.Atoi(value[1:3])
	if err != nil {
		return 0, fmt.Errorf("malformed UTC offset %q: %w", prop.Value, err)
	}
	minutes, err := strconv.Atoi(value[3:5])
	if err != nil {
		return 0, fmt.Errorf("malformed UTC offset %q: %w", prop.Value, err)
	}
	seconds := 0
	if len(value) >= 7 {
		if seconds, err = strconv.Atoi(value[5:7]); err != nil {
			return 0, fmt.Errorf("malformed UTC offset %q: %w", prop.Value, err)
		}
	}
	return sign * (hours*3600 + minutes*60 + seconds), nil
}

// ruleAnchor moves an observance's DTSTART forward to a year the rrule
// implementation will iterate. Microsoft writes VTIMEZONE observances starting
// in 1601, for which rrule-go silently yields no occurrences at all; the
// observance rule carries BYMONTH and BYDAY, so its pattern does not depend on
// the starting year and only the lower bound moves.
func ruleAnchor(t time.Time) time.Time {
	if t.Year() >= 1970 {
		return t
	}
	return time.Date(1970, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

func trimTZIDPrefix(tzid string) string {
	parts := strings.Split(strings.TrimPrefix(tzid, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return tzid
}
