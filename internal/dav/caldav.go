package dav

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/emersion/go-ical"

	"github.com/steveljko/edav/internal/dav/caldav"
	"github.com/steveljko/edav/internal/dav/internal"
	"github.com/steveljko/edav/internal/recurrence"
	"github.com/steveljko/edav/internal/storage"
)

// CalDAVBackend implements caldav.Backend on top of the SQLite storage layer.
type CalDAVBackend struct {
	DB    *sql.DB
	Paths Paths
}

var _ caldav.Backend = (*CalDAVBackend)(nil)

func (b *CalDAVBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return "", err
	}
	return b.Paths.Principal(u.Username), nil
}

func (b *CalDAVBackend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return "", err
	}
	return b.Paths.CalendarHome(u.Username), nil
}

// AddressBookHomeSetPath implements caldav.AddressBookHomeSetProvider, so a
// principal fetched through the CalDAV handler still points at the address
// books.
func (b *CalDAVBackend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return "", err
	}
	return b.Paths.AddressBookHome(u.Username), nil
}

// ResourceTypeAtPath implements caldav.ResourceTypeResolver.
func (b *CalDAVBackend) ResourceTypeAtPath(reqPath string) (caldav.ResourceType, bool) {
	res, err := b.Paths.Parse(reqPath)
	if err != nil {
		return 0, false
	}

	switch res.Kind {
	case KindRoot:
		return caldav.ResourceTypeRoot, true
	case KindPrincipal:
		return caldav.ResourceTypeUserPrincipal, true
	case KindCalendarHome:
		return caldav.ResourceTypeCalendarHomeSet, true
	case KindCalendar:
		return caldav.ResourceTypeCalendar, true
	case KindCalendarObject:
		return caldav.ResourceTypeCalendarObject, true
	default:
		return 0, false
	}
}

func (b *CalDAVBackend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return nil, err
	}

	collections, err := storage.ListCollections(ctx, b.DB, u.ID, storage.CollectionCalendar)
	if err != nil {
		return nil, err
	}

	calendars := make([]caldav.Calendar, 0, len(collections))
	for _, c := range collections {
		calendars = append(calendars, b.calendar(u.Username, c))
	}
	return calendars, nil
}

func (b *CalDAVBackend) GetCalendar(ctx context.Context, path string) (*caldav.Calendar, error) {
	u, c, err := b.resolveCalendar(ctx, path)
	if err != nil {
		return nil, err
	}
	cal := b.calendar(u.Username, c)
	return &cal, nil
}

func (b *CalDAVBackend) CreateCalendar(ctx context.Context, cal *caldav.Calendar) error {
	u, res, err := b.resolve(ctx, cal.Path)
	if err != nil {
		return err
	}
	if res.Kind != KindCalendar {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: %q is not a calendar path", cal.Path)
	}

	_, err = storage.CreateCollection(ctx, b.DB, &storage.Collection{
		OwnerID:     u.ID,
		Type:        storage.CollectionCalendar,
		URI:         res.Collection,
		DisplayName: cal.Name,
		Description: cal.Description,
	})
	if errors.Is(err, storage.ErrConflict) {
		return internal.HTTPErrorf(http.StatusMethodNotAllowed, "caldav: %q already exists", cal.Path)
	}
	return err
}

func (b *CalDAVBackend) GetCalendarObject(ctx context.Context, path string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if res.Kind != KindCalendarObject {
		return nil, internal.HTTPErrorf(http.StatusNotFound, "caldav: %q is not a calendar object", path)
	}

	c, err := b.collectionOfType(ctx, u.ID, res.Collection, storage.CollectionCalendar)
	if err != nil {
		return nil, err
	}
	obj, err := storage.ObjectByURI(ctx, b.DB, c.ID, res.Object)
	if err != nil {
		return nil, notFoundIfMissing(err, path)
	}

	co, err := b.calendarObject(u.Username, c, obj, req)
	if err != nil {
		return nil, err
	}
	return &co, nil
}

func (b *CalDAVBackend) ListCalendarObjects(ctx context.Context, path string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	u, c, err := b.resolveCalendar(ctx, path)
	if err != nil {
		return nil, err
	}

	objects, err := storage.ListObjects(ctx, b.DB, c.ID)
	if err != nil {
		return nil, err
	}
	return b.calendarObjects(u.Username, c, objects, req)
}

// QueryCalendarObjects answers calendar-query. A time-range filter is narrowed
// in SQL from the indexed start and end, then confirmed by expanding each
// candidate's recurrence rule: the index knows an object's overall span, not
// whether an occurrence actually lands in the range.
func (b *CalDAVBackend) QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	u, c, err := b.resolveCalendar(ctx, path)
	if err != nil {
		return nil, err
	}

	var req *caldav.CalendarCompRequest
	if query != nil {
		req = &query.CompRequest
	}

	from, to, ranged := timeRangeOf(query)
	var objects []*storage.Object
	if ranged {
		objects, err = storage.ObjectsInTimeRange(ctx, b.DB, c.ID, from, to)
	} else {
		objects, err = storage.ListObjects(ctx, b.DB, c.ID)
	}
	if err != nil {
		return nil, err
	}

	if ranged {
		matched := objects[:0]
		for _, obj := range objects {
			ok, err := occursInRange(obj.Raw, from, to)
			if err != nil {
				// A stored object we cannot parse should not fail the whole
				// query for every other object in the calendar.
				continue
			}
			if ok {
				matched = append(matched, obj)
			}
		}
		objects = matched
	}

	cos, err := b.calendarObjects(u.Username, c, objects, req)
	if err != nil {
		return nil, err
	}
	return caldav.Filter(stripTimeRange(query, ranged), cos)
}

// stripTimeRange removes the component time-range filters once they have
// already been applied. The generic matcher would evaluate them again through
// go-ical, which resolves TZID only against the IANA database and so fails
// outright on an Outlook zone name, and which expands recurrence without
// honouring RECURRENCE-ID overrides.
func stripTimeRange(query *caldav.CalendarQuery, applied bool) *caldav.CalendarQuery {
	if query == nil || !applied {
		return query
	}
	clone := *query
	clone.CompFilter = stripCompRange(query.CompFilter)
	return &clone
}

func stripCompRange(filter caldav.CompFilter) caldav.CompFilter {
	filter.Start, filter.End = time.Time{}, time.Time{}
	if len(filter.Comps) > 0 {
		comps := make([]caldav.CompFilter, len(filter.Comps))
		for i, c := range filter.Comps {
			comps[i] = stripCompRange(c)
		}
		filter.Comps = comps
	}
	return filter
}

func (b *CalDAVBackend) PutCalendarObject(ctx context.Context, path string, raw []byte, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if res.Kind != KindCalendarObject {
		return nil, internal.HTTPErrorf(http.StatusForbidden, "caldav: %q is not a calendar object path", path)
	}

	c, err := b.collectionOfType(ctx, u.ID, res.Collection, storage.CollectionCalendar)
	if err != nil {
		return nil, err
	}

	existing, err := storage.ObjectByURI(ctx, b.DB, c.ID, res.Object)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if err := checkCalendarPreconditions(opts, existing); err != nil {
		return nil, err
	}

	bounds, err := calendarIndex(raw)
	if err != nil {
		return nil, internal.HTTPErrorf(http.StatusBadRequest, "caldav: %v", err)
	}

	obj, err := storage.PutObject(ctx, b.DB, &storage.Object{
		CollectionID:  c.ID,
		URI:           res.Object,
		Raw:           raw,
		UID:           bounds.UID,
		ComponentType: bounds.ComponentType,
		DisplayName:   bounds.Summary,
		StartAt:       bounds.Start,
		EndAt:         bounds.End,
		Recurring:     bounds.Recurring,
	})
	if err != nil {
		return nil, err
	}

	co, err := b.calendarObject(u.Username, c, obj, nil)
	if err != nil {
		return nil, err
	}
	return &co, nil
}

func (b *CalDAVBackend) DeleteCalendarObject(ctx context.Context, path string) error {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return err
	}

	switch res.Kind {
	case KindCalendar:
		c, err := b.collectionOfType(ctx, u.ID, res.Collection, storage.CollectionCalendar)
		if err != nil {
			return err
		}
		return storage.DeleteCollection(ctx, b.DB, c.ID)
	case KindCalendarObject:
		c, err := b.collectionOfType(ctx, u.ID, res.Collection, storage.CollectionCalendar)
		if err != nil {
			return err
		}
		return notFoundIfMissing(storage.DeleteObject(ctx, b.DB, c.ID, res.Object), path)
	default:
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: %q cannot be deleted", path)
	}
}

func (b *CalDAVBackend) calendar(username string, c *storage.Collection) caldav.Calendar {
	return caldav.Calendar{
		Path:            b.Paths.Calendar(username, c.URI),
		Name:            c.DisplayName,
		Description:     c.Description,
		MaxResourceSize: caldav.MaxResourceSize,
		SupportedComponentSet: []string{
			ical.CompEvent, ical.CompToDo,
		},
	}
}

func (b *CalDAVBackend) calendarObjects(username string, c *storage.Collection, objects []*storage.Object, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	cos := make([]caldav.CalendarObject, 0, len(objects))
	for _, obj := range objects {
		co, err := b.calendarObject(username, c, obj, req)
		if err != nil {
			return nil, err
		}
		cos = append(cos, co)
	}
	return cos, nil
}

// calendarObject builds the protocol view of a stored object. Like its CardDAV
// counterpart it parses only when a component restriction needs it; otherwise
// the stored bytes answer the request unchanged.
func (b *CalDAVBackend) calendarObject(username string, c *storage.Collection, obj *storage.Object, req *caldav.CalendarCompRequest) (caldav.CalendarObject, error) {
	co := caldav.CalendarObject{
		Path:          b.Paths.CalendarObject(username, c.URI, obj.URI),
		ModTime:       obj.UpdatedAt,
		ContentLength: int64(len(obj.Raw)),
		ETag:          obj.ETag,
		Raw:           obj.Raw,
	}

	if req != nil {
		cal, err := ical.NewDecoder(bytes.NewReader(obj.Raw)).Decode()
		if err != nil {
			return caldav.CalendarObject{}, fmt.Errorf("dav: parse stored calendar object %q: %w", obj.URI, err)
		}
		co.Data = cal
	}
	return co, nil
}

func (b *CalDAVBackend) resolveCalendar(ctx context.Context, path string) (*storage.User, *storage.Collection, error) {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	if res.Kind != KindCalendar {
		return nil, nil, internal.HTTPErrorf(http.StatusNotFound, "caldav: %q is not a calendar", path)
	}

	c, err := b.collectionOfType(ctx, u.ID, res.Collection, storage.CollectionCalendar)
	if err != nil {
		return nil, nil, err
	}
	return u, c, nil
}

func (b *CalDAVBackend) collectionOfType(ctx context.Context, ownerID int64, uri string, typ storage.CollectionType) (*storage.Collection, error) {
	c, err := storage.CollectionByURI(ctx, b.DB, ownerID, uri)
	if err != nil {
		return nil, notFoundIfMissing(err, uri)
	}
	if c.Type != typ {
		return nil, internal.HTTPErrorf(http.StatusNotFound, "dav: %q is not a %s", uri, typ)
	}
	return c, nil
}

func (b *CalDAVBackend) resolve(ctx context.Context, path string) (*storage.User, Resource, error) {
	return (&CardDAVBackend{DB: b.DB, Paths: b.Paths}).resolve(ctx, path)
}

// calendarIndex derives the columns stored alongside an object.
func calendarIndex(raw []byte) (recurrence.Bounds, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return recurrence.Bounds{}, fmt.Errorf("parse iCalendar: %w", err)
	}

	bounds, err := recurrence.NewExpander(cal).Summarise(cal)
	if err != nil {
		return recurrence.Bounds{}, err
	}
	if bounds.UID == "" {
		return recurrence.Bounds{}, fmt.Errorf("calendar object has no UID")
	}
	return bounds, nil
}

func occursInRange(raw []byte, from, to time.Time) (bool, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return false, fmt.Errorf("dav: parse stored calendar object: %w", err)
	}
	return recurrence.NewExpander(cal).Overlaps(cal, from, to)
}

// timeRangeOf finds the time-range filter of a calendar-query, if it has one.
// CalDAV nests filters by component, so the range can sit on the VCALENDAR
// filter or on any component filter beneath it.
func timeRangeOf(query *caldav.CalendarQuery) (from, to time.Time, ok bool) {
	if query == nil {
		return time.Time{}, time.Time{}, false
	}
	return compFilterRange(&query.CompFilter)
}

func compFilterRange(filter *caldav.CompFilter) (from, to time.Time, ok bool) {
	if !filter.Start.IsZero() || !filter.End.IsZero() {
		from, to = filter.Start, filter.End
		if from.IsZero() {
			from = time.Unix(0, 0)
		}
		if to.IsZero() {
			// An open-ended range: far enough out to cover any real calendar.
			to = from.AddDate(100, 0, 0)
		}
		return from, to, true
	}

	for i := range filter.Comps {
		if from, to, ok := compFilterRange(&filter.Comps[i]); ok {
			return from, to, ok
		}
	}
	return time.Time{}, time.Time{}, false
}

func checkCalendarPreconditions(opts *caldav.PutCalendarObjectOptions, existing *storage.Object) error {
	if opts == nil {
		return nil
	}

	if opts.IfNoneMatch.IsSet() {
		if existing != nil {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "caldav: resource already exists")
		}
		return nil
	}

	if opts.IfMatch.IsSet() {
		if existing == nil {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "caldav: resource does not exist")
		}
		ok, err := opts.IfMatch.MatchETag(existing.ETag)
		if err != nil {
			return internal.HTTPErrorf(http.StatusBadRequest, "caldav: malformed If-Match: %v", err)
		}
		if !ok {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "caldav: ETag does not match")
		}
	}
	return nil
}
