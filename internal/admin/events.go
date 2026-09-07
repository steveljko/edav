package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/steveljko/edav/internal/dav"
	"github.com/steveljko/edav/internal/ical"
	"github.com/steveljko/edav/internal/storage"
)

// eventsPerPage is what one page of a calendar shows.
const eventsPerPage = 200

// eventRow is an event as the calendar listing shows it, built from the
// indexed columns rather than by parsing every stored object.
type eventRow struct {
	URI       string
	Summary   string
	When      string
	AllDay    bool
	Recurring bool
	Past      bool
}

func (s *Server) eventRows(r *http.Request, collectionID int64) ([]eventRow, int, int, error) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page := max(atoiOr(r.URL.Query().Get("page"), 1), 1)

	objects, total, err := storage.SearchObjects(r.Context(), s.DB, collectionID,
		query, eventsPerPage, (page-1)*eventsPerPage)
	if err != nil {
		return nil, 0, 0, err
	}

	now := time.Now()
	rows := make([]eventRow, 0, len(objects))
	for _, o := range objects {
		summary := o.DisplayName
		if summary == "" {
			summary = o.URI
		}
		row := eventRow{
			URI:       o.URI,
			Summary:   summary,
			Recurring: o.Recurring,
			When:      "No date",
		}
		if o.StartAt != nil {
			row.AllDay = o.EndAt != nil && o.EndAt.Sub(*o.StartAt)%(24*time.Hour) == 0 &&
				o.StartAt.Hour() == 0 && o.StartAt.Minute() == 0
			row.When = formatWhen(*o.StartAt, o.EndAt, row.AllDay)
			row.Past = o.EndAt != nil && o.EndAt.Before(now)
		}
		rows = append(rows, row)
	}
	return rows, total, page, nil
}

// formatWhen renders an event's span the way a person reads it, in UTC. The
// stored columns are instants; the event's own zone is shown when it is opened.
func formatWhen(start time.Time, end *time.Time, allDay bool) string {
	if allDay {
		if end != nil && end.Sub(start) > 24*time.Hour {
			return start.Format("2 Jan 2006") + " – " + end.AddDate(0, 0, -1).Format("2 Jan 2006")
		}
		return start.Format("2 Jan 2006")
	}

	when := start.Format("2 Jan 2006, 15:04")
	if end == nil {
		return when
	}
	if end.YearDay() == start.YearDay() && end.Year() == start.Year() {
		return when + " – " + end.Format("15:04")
	}
	return when + " – " + end.Format("2 Jan 2006, 15:04")
}

// calendarOf resolves a collection that must be a calendar, since the event
// routes are meaningless for an address book.
func (s *Server) calendarOf(w http.ResponseWriter, r *http.Request) (*storage.Collection, bool) {
	c, ok := s.collection(w, r)
	if !ok {
		return nil, false
	}
	if c.Type != storage.CollectionCalendar {
		http.NotFound(w, r)
		return nil, false
	}
	return c, true
}

func (s *Server) newEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.calendarOf(w, r)
	if !ok {
		return
	}

	// A new event starts at the next whole hour, which is what someone adding
	// one almost always wants to adjust rather than retype.
	start := time.Now().In(time.Local).Truncate(time.Hour).Add(time.Hour)

	data, err := s.eventPage(r, c, nil, &ical.Event{
		Start:    start,
		End:      start.Add(time.Hour),
		Timezone: localZoneName(),
	})
	if err != nil {
		s.fail(w, r, "new event", err)
		return
	}
	s.render(w, r, "event.html", data)
}

func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.calendarOf(w, r)
	if !ok {
		return
	}

	event, err := eventFromForm(r)
	if err != nil {
		s.rejectEvent(w, r, c, nil, event, err.Error())
		return
	}

	raw, err := ical.New(event, time.Now())
	if err != nil {
		s.rejectEvent(w, r, c, nil, event, humaniseICal(err))
		return
	}

	uri := ical.Filename(event.UID)
	if uri == ".ics" {
		uri = ical.Filename(ical.NewUID())
	}
	if _, err := s.storeEvent(r, c, uri, raw); err != nil {
		s.fail(w, r, "create event", err)
		return
	}

	slog.Info("admin created event", "collection", c.URI, "uri", uri)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

func (s *Server) showEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.calendarOf(w, r)
	if !ok {
		return
	}

	obj, err := storage.ObjectByURI(r.Context(), s.DB, c.ID, r.PathValue("uri"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	event, err := ical.ReadEvent(obj.Raw)
	if err != nil {
		// An object this server cannot read is still the client's data, so it
		// is reported rather than replaced with something editable.
		data, pageErr := s.collectionPage(r, c, pageData{})
		if pageErr != nil {
			s.fail(w, r, "show collection", pageErr)
			return
		}
		data.Error = fmt.Sprintf("%q cannot be edited here: %v. It is unchanged, and a DAV client can still read it.", obj.URI, err)
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "collection.html", data)
		return
	}

	data, err := s.eventPage(r, c, obj, event)
	if err != nil {
		s.fail(w, r, "show event", err)
		return
	}
	s.render(w, r, "event.html", data)
}

func (s *Server) updateEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.calendarOf(w, r)
	if !ok {
		return
	}

	obj, err := storage.ObjectByURI(r.Context(), s.DB, c.ID, r.PathValue("uri"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	event, err := eventFromForm(r)
	if err != nil {
		s.rejectEvent(w, r, c, obj, event, err.Error())
		return
	}

	// The edit is applied to the stored bytes, so alarms, attendees and the
	// recurrence rule survive it.
	raw, err := ical.Apply(obj.Raw, event, time.Now())
	if err != nil {
		s.rejectEvent(w, r, c, obj, event, humaniseICal(err))
		return
	}

	if _, err := s.storeEvent(r, c, obj.URI, raw); err != nil {
		s.fail(w, r, "update event", err)
		return
	}

	slog.Info("admin updated event", "collection", c.URI, "uri", obj.URI)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

func (s *Server) confirmDeleteEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.calendarOf(w, r)
	if !ok {
		return
	}

	obj, err := storage.ObjectByURI(r.Context(), s.DB, c.ID, r.PathValue("uri"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	name := obj.DisplayName
	if name == "" {
		name = obj.URI
	}

	body := "This removes the event from the calendar and from every client that syncs it. It cannot be undone."
	if obj.Recurring {
		body = "This removes every occurrence of this repeating event, from the calendar and from every client that syncs it. It cannot be undone."
	}

	data := s.page(r, "Delete "+name, "users", pageData{
		Confirm: confirmation{
			Title:  "Delete " + name + "?",
			Body:   body,
			Verb:   "Delete this event",
			Action: fmt.Sprintf("/admin/collections/%d/events/%s/delete", c.ID, obj.URI),
			Cancel: fmt.Sprintf("/admin/collections/%d", c.ID),
		},
	})
	s.render(w, r, "confirm.html", data)
}

func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.calendarOf(w, r)
	if !ok {
		return
	}

	uri := r.PathValue("uri")
	err := storage.DeleteObject(r.Context(), s.DB, c.ID, uri)
	if errors.Is(err, storage.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, "delete event", err)
		return
	}

	slog.Info("admin deleted event", "collection", c.URI, "uri", uri)
	s.redirect(w, r, fmt.Sprintf("/admin/collections/%d", c.ID))
}

// storeEvent writes through the same path a DAV client uses, deriving the
// index columns from the object exactly as a client PUT would.
func (s *Server) storeEvent(r *http.Request, c *storage.Collection, uri string, raw []byte) (*storage.Object, error) {
	bounds, err := dav.CalendarIndex(raw)
	if err != nil {
		return nil, err
	}

	return storage.PutObject(r.Context(), s.DB, &storage.Object{
		CollectionID:  c.ID,
		URI:           uri,
		Raw:           raw,
		UID:           bounds.UID,
		ComponentType: bounds.ComponentType,
		DisplayName:   bounds.Summary,
		StartAt:       bounds.Start,
		EndAt:         bounds.End,
		Recurring:     bounds.Recurring,
	})
}

func (s *Server) eventPage(r *http.Request, c *storage.Collection, obj *storage.Object, event *ical.Event) (pageData, error) {
	owner, err := storage.UserByID(r.Context(), s.DB, c.OwnerID)
	if err != nil {
		return pageData{}, err
	}

	data := pageData{
		Collection: c,
		Subject:    owner,
		Event:      event,
		Repeats:    repeatOptions,
	}

	title := "New event"
	if obj != nil {
		data.EventURI = obj.URI
		data.EventAction = fmt.Sprintf("/admin/collections/%d/events/%s", c.ID, obj.URI)
		title = event.Summary

		if event.Recurrence != "" {
			data.RepeatSummary = describeRecurrence(event.Recurrence)
		}
		if o, err := ical.Parse(obj.Raw); err == nil {
			data.HasOverrides = o.HasOverrides()
		}
	} else {
		data.EventAction = fmt.Sprintf("/admin/collections/%d/events", c.ID)
	}
	return s.page(r, title, "users", data), nil
}

func (s *Server) rejectEvent(w http.ResponseWriter, r *http.Request, c *storage.Collection, obj *storage.Object, event *ical.Event, message string) {
	data, err := s.eventPage(r, c, obj, event)
	if err != nil {
		s.fail(w, r, "show event", err)
		return
	}
	data.Error = message
	s.renderStatus(w, r, http.StatusUnprocessableEntity, "event.html", data)
}

func eventFromForm(r *http.Request) (*ical.Event, error) {
	get := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }

	event := &ical.Event{
		UID:         get("uid"),
		Summary:     get("summary"),
		Location:    get("location"),
		Description: get("description"),
		Timezone:    get("timezone"),
		AllDay:      get("all_day") == "1",
		Recurrence:  get("recurrence"),
	}

	start, err := parseFormTime(get("start_date"), get("start_time"), event.AllDay)
	if err != nil {
		return event, fmt.Errorf("The start is not a valid date and time.")
	}
	event.Start = start

	end, err := parseFormTime(get("end_date"), get("end_time"), event.AllDay)
	if err != nil {
		return event, fmt.Errorf("The end is not a valid date and time.")
	}
	// A whole-day event ends the day after the last one it covers.
	if event.AllDay {
		end = end.AddDate(0, 0, 1)
	}
	event.End = end

	if event.AllDay {
		event.Timezone = ""
	}
	return event, nil
}

func parseFormTime(date, clock string, allDay bool) (time.Time, error) {
	if date == "" {
		return time.Time{}, fmt.Errorf("no date")
	}
	if allDay || clock == "" {
		return time.Parse("2006-01-02", date)
	}
	return time.Parse("2006-01-02T15:04", date+"T"+clock)
}

// localZoneName is the server's own zone, which for a self-hosted calendar is
// usually the one whoever is adding an event is sitting in.
func localZoneName() string {
	name := time.Local.String()
	if name == "" || name == "Local" {
		return ""
	}
	return name
}

func humaniseICal(err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "ical: "); ok {
		msg = rest
	}
	if msg == "" {
		return "That event could not be saved."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}
