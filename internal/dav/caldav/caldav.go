// Package caldav provides a client and server CalDAV implementation.
//
// CalDAV is defined in RFC 4791.
package caldav

import (
	"fmt"
	"time"

	"github.com/emersion/go-ical"
	"github.com/steveljko/edav/internal/dav/internal"
	"github.com/steveljko/edav/internal/dav/webdav"
)

var CapabilityCalendar = webdav.Capability("calendar-access")

func NewCalendarHomeSet(path string) webdav.BackendSuppliedHomeSet {
	return &calendarHomeSet{Href: internal.Href{Path: path}}
}

// ValidateCalendarObject checks the validity of a calendar object according to
// the contraints layed out in RFC 4791 section 4.1 and returns the only event
// type and UID occuring in this calendar, or an error if the calendar could
// not be validated.
func ValidateCalendarObject(cal *ical.Calendar) (eventType string, uid string, err error) {
	// Calendar object resources contained in calendar collections
	// MUST NOT specify the iCalendar METHOD property.
	if prop := cal.Props.Get(ical.PropMethod); prop != nil {
		return "", "", fmt.Errorf("calendar resource must not specify METHOD property")
	}

	for _, comp := range cal.Children {
		// Calendar object resources contained in calendar collections
		// MUST NOT contain more than one type of calendar component
		// (e.g., VEVENT, VTODO, VJOURNAL, VFREEBUSY, etc.) with the
		// exception of VTIMEZONE components, which MUST be specified
		// for each unique TZID parameter value specified in the
		// iCalendar object.
		if comp.Name != ical.CompTimezone {
			if eventType == "" {
				eventType = comp.Name
			}
			if eventType != comp.Name {
				return "", "", fmt.Errorf("conflicting event types in calendar: %s, %s", eventType, comp.Name)
			}
			// TODO check VTIMEZONE for each TZID?
		}

		// Calendar components in a calendar collection that have
		// different UID property values MUST be stored in separate
		// calendar object resources.
		compUID, err := comp.Props.Text(ical.PropUID)
		if err != nil {
			return "", "", fmt.Errorf("error checking component UID: %v", err)
		}
		if uid == "" {
			uid = compUID
		}
		if compUID != "" && uid != compUID {
			return "", "", fmt.Errorf("conflicting UID values in calendar: %s, %s", uid, compUID)
		}
	}
	return eventType, uid, nil
}

type Calendar struct {
	Path                  string
	Name                  string
	Description           string
	MaxResourceSize       int64
	SupportedComponentSet []string
	// SyncToken is the collection's current position in its own change
	// sequence, reported so a client can start syncing from now.
	SyncToken string
	// Timezone is a VCALENDAR carrying one VTIMEZONE, which a client applies
	// to floating times in this calendar. Empty when the calendar has none.
	Timezone string
}

// SyncQuery is a sync-collection request.
type SyncQuery struct {
	CompRequest CalendarCompRequest
	SyncToken   string
	Limit       int // <= 0 means unlimited
}

// SyncResponse carries the changes since a sync token, and the token to use
// next time.
type SyncResponse struct {
	SyncToken string
	Updated   []CalendarObject
	Deleted   []string
}

type CalendarCompRequest struct {
	Name string

	AllProps bool
	Props    []string

	AllComps bool
	Comps    []CalendarCompRequest

	Expand *CalendarExpandRequest
}

type CalendarExpandRequest struct {
	Start, End time.Time
}

type CompFilter struct {
	Name         string
	IsNotDefined bool
	Start, End   time.Time
	Props        []PropFilter
	Comps        []CompFilter
}

type ParamFilter struct {
	Name         string
	IsNotDefined bool
	TextMatch    *TextMatch
}

type PropFilter struct {
	Name         string
	IsNotDefined bool
	Start, End   time.Time
	TextMatch    *TextMatch
	ParamFilter  []ParamFilter
}

type TextMatch struct {
	Text            string
	NegateCondition bool
}

type CalendarQuery struct {
	CompRequest CalendarCompRequest
	CompFilter  CompFilter
}

type CalendarMultiGet struct {
	Paths       []string
	CompRequest CalendarCompRequest
}

type CalendarObject struct {
	Path          string
	ModTime       time.Time
	ContentLength int64
	ETag          string

	// Raw is the iCalendar object exactly as the client stored it, and is what
	// GET and an unrestricted calendar-data response return. Backends must set
	// it.
	Raw []byte

	// Data is a parsed view of Raw, needed to evaluate filters and component
	// restrictions. Backends may leave it nil when the request needs neither. A
	// response built from a restricted Data carries no Raw, since it no longer
	// represents the stored bytes.
	Data *ical.Calendar
}

// MaxResourceSize caps the size of a single iCalendar object accepted by PUT.
// Calendar objects run larger than contacts: a recurring event with many
// overrides and an embedded VTIMEZONE adds up.
const MaxResourceSize = 10 << 20
