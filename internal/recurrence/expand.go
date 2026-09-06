package recurrence

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// maxOccurrences caps a single expansion. An unbounded RRULE with a wide query
// range would otherwise generate instances until the query bound, and a
// malicious FREQ=SECONDLY rule would do so very quickly.
const maxOccurrences = 10000

// expansionSlack widens the wall-clock window used to drive rrule, which works
// in floating time while queries arrive as instants. A day either side covers
// every real UTC offset, and instances are filtered exactly afterwards.
const expansionSlack = 26 * time.Hour

// Instance is one occurrence of a component, in absolute time.
type Instance struct {
	Start time.Time
	End   time.Time
	// RecurrenceID identifies which occurrence of a recurring series this is.
	// Zero for a component that does not recur.
	RecurrenceID time.Time
	// Override is true when this instance came from a component carrying
	// RECURRENCE-ID rather than from expanding the master.
	Override bool
}

// Bounds summarises an object for the storage index columns. End is nil when
// the object recurs without an end, since no finite value describes it.
type Bounds struct {
	Start         *time.Time
	End           *time.Time
	Recurring     bool
	UID           string
	ComponentType string
	Summary       string
}

// Expander turns a parsed calendar object into concrete instances. It is the
// only thing that knows about RRULE, EXDATE and RECURRENCE-ID; query handling
// asks it questions and does not interpret recurrence itself.
type Expander struct {
	timezones *Timezones
}

// NewExpander prepares an expander for one calendar object, collecting the
// VTIMEZONE definitions it carries.
func NewExpander(cal *ical.Calendar) *Expander {
	return &Expander{timezones: NewTimezones(cal)}
}

// Expand returns every instance of the object that overlaps [from, to),
// ordered by start. Recurrence overrides replace the instances they name, and
// EXDATE removes them.
func (e *Expander) Expand(cal *ical.Calendar, from, to time.Time) ([]Instance, error) {
	master, overrides, err := split(cal)
	if err != nil {
		return nil, err
	}
	if master == nil && len(overrides) == 0 {
		return nil, nil
	}

	byRecurrenceID := make(map[time.Time]Instance, len(overrides))
	for _, comp := range overrides {
		recurrenceID, err := e.componentTime(comp, ical.PropRecurrenceID)
		if err != nil {
			return nil, err
		}
		inst, err := e.instanceOf(comp)
		if err != nil {
			return nil, err
		}
		inst.RecurrenceID = recurrenceID
		inst.Override = true
		byRecurrenceID[recurrenceID] = inst
	}

	var instances []Instance
	if master != nil {
		expanded, err := e.expandMaster(master, from, to)
		if err != nil {
			return nil, err
		}
		for _, inst := range expanded {
			if override, ok := byRecurrenceID[inst.RecurrenceID]; ok {
				// The override may have moved the occurrence out of range.
				delete(byRecurrenceID, inst.RecurrenceID)
				if overlaps(override, from, to) {
					instances = append(instances, override)
				}
				continue
			}
			instances = append(instances, inst)
		}
	}

	// An override whose RECURRENCE-ID is not in the series is still a real
	// occurrence; clients move an instance and the rule stops generating it.
	for _, override := range byRecurrenceID {
		if overlaps(override, from, to) {
			instances = append(instances, override)
		}
	}

	sort.Slice(instances, func(i, j int) bool { return instances[i].Start.Before(instances[j].Start) })
	return instances, nil
}

// Overlaps reports whether any instance of the object falls in [from, to). It
// stops at the first match rather than expanding the whole series.
func (e *Expander) Overlaps(cal *ical.Calendar, from, to time.Time) (bool, error) {
	instances, err := e.Expand(cal, from, to)
	if err != nil {
		return false, err
	}
	return len(instances) > 0, nil
}

// Summarise derives the values indexed on write. For a recurring object the
// start is the first occurrence and the end is the last, or nil when the rule
// never ends.
func (e *Expander) Summarise(cal *ical.Calendar) (Bounds, error) {
	var b Bounds

	master, overrides, err := split(cal)
	if err != nil {
		return b, err
	}
	comp := master
	if comp == nil {
		if len(overrides) == 0 {
			return b, nil
		}
		comp = overrides[0]
	}

	b.ComponentType = comp.Name
	if uid, err := comp.Props.Text(ical.PropUID); err == nil {
		b.UID = uid
	}
	if summary, err := comp.Props.Text(ical.PropSummary); err == nil {
		b.Summary = summary
	}

	base, err := e.instanceOf(comp)
	if err != nil {
		return b, err
	}
	start, end := base.Start, base.End
	b.Start, b.End = &start, &end

	if master == nil {
		return b, nil
	}

	rule, err := e.recurrenceSet(master, base)
	if err != nil {
		return b, err
	}
	if rule == nil {
		// Overrides can still push the object's bounds outward.
		for _, comp := range overrides {
			inst, err := e.instanceOf(comp)
			if err != nil {
				return b, err
			}
			b.widen(inst)
		}
		return b, nil
	}

	b.Recurring = true
	if !rule.bounded {
		b.End = nil
		return b, nil
	}

	duration := base.End.Sub(base.Start)
	for _, wall := range rule.set.All() {
		inst, err := e.absolute(master, wall, duration)
		if err != nil {
			return b, err
		}
		b.widen(inst)
	}
	for _, comp := range overrides {
		inst, err := e.instanceOf(comp)
		if err != nil {
			return b, err
		}
		b.widen(inst)
	}
	return b, nil
}

func (b *Bounds) widen(inst Instance) {
	if b.Start == nil || inst.Start.Before(*b.Start) {
		start := inst.Start
		b.Start = &start
	}
	if b.End != nil && inst.End.After(*b.End) {
		end := inst.End
		b.End = &end
	}
}

func (e *Expander) expandMaster(master *ical.Component, from, to time.Time) ([]Instance, error) {
	base, err := e.instanceOf(master)
	if err != nil {
		return nil, err
	}

	rule, err := e.recurrenceSet(master, base)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		if !overlaps(base, from, to) {
			return nil, nil
		}
		return []Instance{base}, nil
	}

	duration := base.End.Sub(base.Start)
	// rrule works in the floating wall clock the series was written in, so the
	// query bounds are converted to that frame, widened, and the results are
	// filtered exactly once they are absolute again.
	windowFrom := from.Add(-duration - expansionSlack)
	windowTo := to.Add(expansionSlack)

	var instances []Instance
	next := rule.set.Iterator()
	for count := 0; count < maxOccurrences; count++ {
		wall, ok := next()
		if !ok {
			break
		}
		if wall.After(windowTo) {
			break
		}
		if wall.Before(windowFrom) {
			continue
		}

		inst, err := e.absolute(master, wall, duration)
		if err != nil {
			return nil, err
		}
		if overlaps(inst, from, to) {
			instances = append(instances, inst)
		}
	}
	return instances, nil
}

type recurrenceSet struct {
	set     *rrule.Set
	bounded bool
}

// recurrenceSet assembles RRULE, RDATE and EXDATE into one iterable set, all in
// the floating wall clock of DTSTART. It returns nil when the component does
// not recur.
func (e *Expander) recurrenceSet(comp *ical.Component, base Instance) (*recurrenceSet, error) {
	opt, err := comp.Props.RecurrenceRule()
	if err != nil {
		return nil, fmt.Errorf("recurrence: parse RRULE: %w", err)
	}
	rdates := e.dateList(comp, ical.PropRecurrenceDates)
	if opt == nil && len(rdates) == 0 {
		return nil, nil
	}

	startWall, err := e.wallTime(comp, ical.PropDateTimeStart)
	if err != nil {
		return nil, err
	}

	set := &rrule.Set{}
	set.DTStart(startWall)

	bounded := true
	if opt != nil {
		opt.Dtstart = startWall
		if !opt.Until.IsZero() {
			// UNTIL is defined in UTC; the set works in wall time.
			until, err := e.toWall(comp, opt.Until)
			if err != nil {
				return nil, err
			}
			opt.Until = until
		}
		if opt.Count == 0 && opt.Until.IsZero() {
			bounded = false
		}
		rule, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, fmt.Errorf("recurrence: build RRULE: %w", err)
		}
		set.RRule(rule)
	}

	for _, t := range rdates {
		set.RDate(t)
	}
	for _, t := range e.dateList(comp, ical.PropExceptionDates) {
		set.ExDate(t)
	}
	return &recurrenceSet{set: set, bounded: bounded}, nil
}

// dateList reads a repeatable date property. Each property can itself hold a
// comma-separated list, which clients do use and which go-ical's own helpers
// do not split.
func (e *Expander) dateList(comp *ical.Component, name string) []time.Time {
	var out []time.Time
	for _, prop := range comp.Props[name] {
		for _, value := range strings.Split(prop.Value, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if t, err := parseWallTime(value); err == nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// instanceOf resolves a component's own start and end in absolute time.
func (e *Expander) instanceOf(comp *ical.Component) (Instance, error) {
	start, err := e.componentTime(comp, ical.PropDateTimeStart)
	if err != nil {
		// A VTODO need not have DTSTART; DUE alone still places it in time.
		if comp.Name == ical.CompToDo {
			due, dueErr := e.componentTime(comp, "DUE")
			if dueErr == nil {
				return Instance{Start: due, End: due}, nil
			}
		}
		return Instance{}, err
	}

	end, err := e.endTime(comp, start)
	if err != nil {
		return Instance{}, err
	}
	return Instance{Start: start, End: end}, nil
}

func (e *Expander) endTime(comp *ical.Component, start time.Time) (time.Time, error) {
	endProp := ical.PropDateTimeEnd
	if comp.Name == ical.CompToDo {
		endProp = "DUE"
	}
	if comp.Props.Get(endProp) != nil {
		return e.componentTime(comp, endProp)
	}

	if prop := comp.Props.Get("DURATION"); prop != nil {
		d, err := prop.Duration()
		if err != nil {
			return time.Time{}, fmt.Errorf("recurrence: parse DURATION: %w", err)
		}
		return start.Add(d), nil
	}

	// RFC 5545 3.6.1: with neither DTEND nor DURATION, a date-valued DTSTART
	// spans the whole day and a date-time-valued one has no duration.
	if isDateOnly(comp.Props.Get(ical.PropDateTimeStart)) {
		return start.AddDate(0, 0, 1), nil
	}
	return start, nil
}

// componentTime resolves a property to an absolute instant, honouring TZID,
// a trailing Z, and floating times.
func (e *Expander) componentTime(comp *ical.Component, name string) (time.Time, error) {
	prop := comp.Props.Get(name)
	if prop == nil {
		return time.Time{}, fmt.Errorf("recurrence: %s has no %s", comp.Name, name)
	}

	wall, err := parseWallTime(prop.Value)
	if err != nil {
		return time.Time{}, fmt.Errorf("recurrence: parse %s: %w", name, err)
	}
	if strings.HasSuffix(prop.Value, "Z") {
		return wall, nil
	}
	return e.timezones.ToUTC(prop.Params.Get(ical.PropTimezoneID), wall)
}

// wallTime reads a property as a floating wall time, discarding its zone. This
// is the frame recurrence rules are evaluated in.
func (e *Expander) wallTime(comp *ical.Component, name string) (time.Time, error) {
	prop := comp.Props.Get(name)
	if prop == nil {
		return time.Time{}, fmt.Errorf("recurrence: %s has no %s", comp.Name, name)
	}
	return parseWallTime(prop.Value)
}

// absolute converts one expanded wall time back into an instant in the
// component's zone. Each occurrence is resolved separately, so a weekly 09:00
// meeting stays at 09:00 across a daylight saving change.
func (e *Expander) absolute(comp *ical.Component, wall time.Time, duration time.Duration) (Instance, error) {
	prop := comp.Props.Get(ical.PropDateTimeStart)
	if prop == nil {
		return Instance{}, fmt.Errorf("recurrence: %s has no DTSTART", comp.Name)
	}

	var start time.Time
	if strings.HasSuffix(prop.Value, "Z") {
		start = wall
	} else {
		var err error
		start, err = e.timezones.ToUTC(prop.Params.Get(ical.PropTimezoneID), wall)
		if err != nil {
			return Instance{}, err
		}
	}
	return Instance{Start: start, End: start.Add(duration), RecurrenceID: wall}, nil
}

// toWall converts a UTC instant into the floating wall clock of a component's
// zone, for comparing UNTIL against an expansion that runs in wall time.
func (e *Expander) toWall(comp *ical.Component, utc time.Time) (time.Time, error) {
	prop := comp.Props.Get(ical.PropDateTimeStart)
	if prop == nil || strings.HasSuffix(prop.Value, "Z") {
		return utc, nil
	}

	loc, err := e.timezones.Resolve(prop.Params.Get(ical.PropTimezoneID), utc)
	if err != nil {
		return time.Time{}, err
	}
	local := utc.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(),
		local.Hour(), local.Minute(), local.Second(), 0, time.UTC), nil
}

// split separates the master component from its recurrence overrides. VTIMEZONE
// and VALARM are not schedulable components and are ignored.
func split(cal *ical.Calendar) (master *ical.Component, overrides []*ical.Component, err error) {
	if cal == nil {
		return nil, nil, nil
	}

	for _, comp := range cal.Children {
		switch comp.Name {
		case ical.CompEvent, ical.CompToDo, ical.CompJournal, ical.CompFreeBusy:
		default:
			continue
		}

		if comp.Props.Get(ical.PropRecurrenceID) != nil {
			overrides = append(overrides, comp)
			continue
		}
		if master == nil {
			master = comp
		}
	}
	return master, overrides, nil
}

// overlaps implements the RFC 4791 9.9 time-range test: a component matches
// when it starts before the range ends and ends after the range starts. A
// zero-length instance matches when the range contains its start.
func overlaps(inst Instance, from, to time.Time) bool {
	if inst.Start.Equal(inst.End) {
		return !inst.Start.Before(from) && inst.Start.Before(to)
	}
	return inst.Start.Before(to) && inst.End.After(from)
}

func isDateOnly(prop *ical.Prop) bool {
	if prop == nil {
		return false
	}
	if prop.Params.Get("VALUE") == "DATE" {
		return true
	}
	return len(prop.Value) == len("20060102")
}

// parseWallTime reads an iCalendar date or date-time as a naive wall time,
// carried in UTC. Any zone the value names is applied by the caller.
func parseWallTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	switch len(value) {
	case len("20060102"):
		return time.ParseInLocation("20060102", value, time.UTC)
	case len("20060102T150405"):
		return time.ParseInLocation("20060102T150405", value, time.UTC)
	case len("20060102T150405Z"):
		return time.ParseInLocation("20060102T150405Z", value, time.UTC)
	default:
		return time.Time{}, fmt.Errorf("cannot parse date-time %q", value)
	}
}
