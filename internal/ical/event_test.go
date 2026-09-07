package ical

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)

// An event as a client would store it: an alarm, attendees, vendor properties
// and a recurrence rule, none of which a form models.
const phoneEvent = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Apple Inc.//iOS 17.0//EN\r\n" +
	"CALSCALE:GREGORIAN\r\n" +
	"BEGIN:VTIMEZONE\r\n" +
	"TZID:Europe/Berlin\r\n" +
	"BEGIN:DAYLIGHT\r\n" +
	"TZOFFSETFROM:+0100\r\n" +
	"TZOFFSETTO:+0200\r\n" +
	"DTSTART:19700329T020000\r\n" +
	"END:DAYLIGHT\r\n" +
	"END:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:evt-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20260401T090000\r\n" +
	"DTEND;TZID=Europe/Berlin:20260401T100000\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=6\r\n" +
	"SUMMARY:Standup\r\n" +
	"LOCATION:Room 3\r\n" +
	"SEQUENCE:2\r\n" +
	"ATTENDEE;CN=Ada;PARTSTAT=ACCEPTED:mailto:ada@example.com\r\n" +
	"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n" +
	"BEGIN:VALARM\r\n" +
	"ACTION:DISPLAY\r\n" +
	"DESCRIPTION:Reminder\r\n" +
	"TRIGGER:-PT15M\r\n" +
	"END:VALARM\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestUntouchedObjectIsByteIdentical(t *testing.T) {
	o, err := Parse([]byte(phoneEvent))
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}
	if got := string(o.Bytes()); got != phoneEvent {
		t.Errorf("re-emitting an unedited object changed it:\n in: %q\nout: %q", phoneEvent, got)
	}
}

func TestReadEvent(t *testing.T) {
	e, err := ReadEvent([]byte(phoneEvent))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}

	if e.UID != "evt-1" || e.Summary != "Standup" || e.Location != "Room 3" {
		t.Errorf("event = %+v", e)
	}
	if e.Timezone != "Europe/Berlin" {
		t.Errorf("Timezone = %q", e.Timezone)
	}
	if e.AllDay {
		t.Error("a timed event was read as all day")
	}
	if got := e.Start.Format(dateTimeLayout); got != "20260401T090000" {
		t.Errorf("Start = %s", got)
	}
	if got := e.End.Format(dateTimeLayout); got != "20260401T100000" {
		t.Errorf("End = %s", got)
	}
	if e.Recurrence != "FREQ=WEEKLY;COUNT=6" {
		t.Errorf("Recurrence = %q", e.Recurrence)
	}
}

// The reason this package exists. An alarm, attendees and a recurrence rule
// are not the form's to discard.
func TestApplyPreservesEverythingElse(t *testing.T) {
	e, err := ReadEvent([]byte(phoneEvent))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	e.Summary = "Morning standup"

	out, err := Apply([]byte(phoneEvent), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "SUMMARY:Morning standup\r\n") {
		t.Errorf("the title was not updated:\n%s", got)
	}
	for _, want := range []string{
		"PRODID:-//Apple Inc.//iOS 17.0//EN\r\n",
		"CALSCALE:GREGORIAN\r\n",
		"BEGIN:VTIMEZONE\r\n",
		"TZID:Europe/Berlin\r\n",
		"RRULE:FREQ=WEEKLY;COUNT=6\r\n",
		"ATTENDEE;CN=Ada;PARTSTAT=ACCEPTED:mailto:ada@example.com\r\n",
		"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n",
		"BEGIN:VALARM\r\n",
		"TRIGGER:-PT15M\r\n",
		"UID:evt-1\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("editing the title lost %q:\n%s", want, got)
		}
	}
}

// The alarm carries its own DESCRIPTION, which must not be mistaken for the
// event's.
func TestApplyDoesNotReachIntoTheAlarm(t *testing.T) {
	e, err := ReadEvent([]byte(phoneEvent))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	if e.Description != "" {
		t.Fatalf("Description = %q, want empty: the alarm's was read as the event's", e.Description)
	}

	e.Description = "Daily sync"
	out, err := Apply([]byte(phoneEvent), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "DESCRIPTION:Reminder\r\n") {
		t.Errorf("the alarm's description was overwritten:\n%s", got)
	}
	if !strings.Contains(got, "DESCRIPTION:Daily sync\r\n") {
		t.Errorf("the event's description was not written:\n%s", got)
	}
}

func TestApplyBumpsSequenceAndStamps(t *testing.T) {
	e, err := ReadEvent([]byte(phoneEvent))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	e.Summary = "Changed"

	out, err := Apply([]byte(phoneEvent), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "SEQUENCE:3\r\n") {
		t.Errorf("SEQUENCE did not advance from 2:\n%s", got)
	}
	if !strings.Contains(got, "DTSTAMP:20260315T093000Z\r\n") {
		t.Errorf("DTSTAMP was not restamped:\n%s", got)
	}
}

func TestApplyMovesTheEvent(t *testing.T) {
	e, err := ReadEvent([]byte(phoneEvent))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	e.Start = time.Date(2026, 4, 1, 14, 0, 0, 0, time.UTC)
	e.End = time.Date(2026, 4, 1, 15, 30, 0, 0, time.UTC)

	out, err := Apply([]byte(phoneEvent), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "DTSTART;TZID=Europe/Berlin:20260401T140000\r\n") {
		t.Errorf("DTSTART was not moved:\n%s", got)
	}
	if !strings.Contains(got, "DTEND;TZID=Europe/Berlin:20260401T153000\r\n") {
		t.Errorf("DTEND was not moved:\n%s", got)
	}
}

// An event written with DURATION keeps that shape rather than gaining a second
// way to say when it ends.
func TestApplyKeepsDurationShape(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\n" +
		"UID:d-1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260401T090000Z\r\n" +
		"DURATION:PT1H30M\r\nSUMMARY:Sized by duration\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	e, err := ReadEvent([]byte(raw))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	if got := e.End.Sub(e.Start); got != 90*time.Minute {
		t.Errorf("duration read as %v, want 1h30m", got)
	}

	e.End = e.Start.Add(2 * time.Hour)
	out, err := Apply([]byte(raw), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "DURATION:PT2H\r\n") {
		t.Errorf("DURATION was not updated:\n%s", got)
	}
	if strings.Contains(got, "DTEND") {
		t.Errorf("a DTEND was added alongside DURATION:\n%s", got)
	}
}

func TestAllDayEvent(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\n" +
		"UID:a-1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;VALUE=DATE:20260401\r\n" +
		"DTEND;VALUE=DATE:20260402\r\nSUMMARY:Holiday\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	e, err := ReadEvent([]byte(raw))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	if !e.AllDay {
		t.Error("a date-valued event was not read as all day")
	}

	e.Summary = "Long weekend"
	e.End = e.Start.AddDate(0, 0, 3)
	out, err := Apply([]byte(raw), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "DTSTART;VALUE=DATE:20260401\r\n") {
		t.Errorf("DTSTART lost its date form:\n%s", got)
	}
	if !strings.Contains(got, "DTEND;VALUE=DATE:20260404\r\n") {
		t.Errorf("DTEND was not extended:\n%s", got)
	}
}

func TestNewEvent(t *testing.T) {
	e := &Event{
		Summary:     "Dentist",
		Location:    "High Street",
		Description: "Bring the referral",
		Start:       time.Date(2026, 5, 4, 11, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 5, 4, 11, 45, 0, 0, time.UTC),
		Timezone:    "Europe/Berlin",
	}

	out, err := New(e, testNow)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	got := string(out)
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"BEGIN:VEVENT\r\n",
		"SUMMARY:Dentist\r\n",
		"LOCATION:High Street\r\n",
		"DESCRIPTION:Bring the referral\r\n",
		"DTSTART;TZID=Europe/Berlin:20260504T110000\r\n",
		"DTEND;TZID=Europe/Berlin:20260504T114500\r\n",
		"END:VEVENT\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("new object is missing %q:\n%s", want, got)
		}
	}

	back, err := ReadEvent(out)
	if err != nil {
		t.Fatalf("the generated object does not read back: %v", err)
	}
	if back.UID == "" {
		t.Error("no UID was generated")
	}
	if back.Summary != "Dentist" || back.Timezone != "Europe/Berlin" {
		t.Errorf("round trip = %+v", back)
	}
}

func TestNewEventWithARepeat(t *testing.T) {
	out, err := New(&Event{
		Summary:    "Standup",
		Start:      time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 5, 4, 9, 15, 0, 0, time.UTC),
		Recurrence: "FREQ=WEEKLY;COUNT=10",
	}, testNow)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if !strings.Contains(string(out), "RRULE:FREQ=WEEKLY;COUNT=10\r\n") {
		t.Errorf("the repeat was not written:\n%s", out)
	}
}

func TestEventValidation(t *testing.T) {
	start := time.Date(2026, 5, 4, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		event Event
	}{
		{"no title", Event{Start: start, End: start}},
		{"blank title", Event{Summary: "   ", Start: start, End: start}},
		{"no start", Event{Summary: "x"}},
		{"ends before it starts", Event{Summary: "x", Start: start, End: start.Add(-time.Hour)}},
		{"unknown zone", Event{Summary: "x", Start: start, End: start, Timezone: "Mars/Olympus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(&tt.event, testNow); err == nil {
				t.Error("New() = nil, want an error")
			}
		})
	}
}

func TestHasOverrides(t *testing.T) {
	if o, _ := Parse([]byte(phoneEvent)); o.HasOverrides() {
		t.Error("a plain recurring event was reported as having overrides")
	}

	withOverride := strings.Replace(phoneEvent, "END:VCALENDAR\r\n",
		"BEGIN:VEVENT\r\nUID:evt-1\r\nDTSTAMP:20260101T000000Z\r\n"+
			"RECURRENCE-ID;TZID=Europe/Berlin:20260408T090000\r\n"+
			"DTSTART;TZID=Europe/Berlin:20260408T140000\r\n"+
			"SUMMARY:Moved\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", 1)

	o, err := Parse([]byte(withOverride))
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}
	if !o.HasOverrides() {
		t.Error("an override was not detected")
	}
}

// Editing the master must leave an override alone.
func TestApplyLeavesOverridesAlone(t *testing.T) {
	withOverride := strings.Replace(phoneEvent, "END:VCALENDAR\r\n",
		"BEGIN:VEVENT\r\nUID:evt-1\r\nDTSTAMP:20260101T000000Z\r\n"+
			"RECURRENCE-ID;TZID=Europe/Berlin:20260408T090000\r\n"+
			"DTSTART;TZID=Europe/Berlin:20260408T140000\r\n"+
			"SUMMARY:Moved\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", 1)

	e, err := ReadEvent([]byte(withOverride))
	if err != nil {
		t.Fatalf("ReadEvent() = %v", err)
	}
	e.Summary = "Renamed"

	out, err := Apply([]byte(withOverride), e, testNow)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "SUMMARY:Renamed\r\n") {
		t.Error("the master was not renamed")
	}
	if !strings.Contains(got, "SUMMARY:Moved\r\n") {
		t.Errorf("the override was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "RECURRENCE-ID;TZID=Europe/Berlin:20260408T090000\r\n") {
		t.Errorf("the override lost its recurrence id:\n%s", got)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not a calendar", "hello there"},
		{"no VCALENDAR", "BEGIN:VEVENT\r\nUID:x\r\nEND:VEVENT\r\n"},
		{"no VEVENT", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.raw)); err == nil {
				t.Error("Parse() = nil, want an error")
			}
		})
	}
}

func TestDurationRoundTrip(t *testing.T) {
	tests := []struct {
		text string
		want time.Duration
	}{
		{"PT1H", time.Hour},
		{"PT30M", 30 * time.Minute},
		{"PT1H30M", 90 * time.Minute},
		{"P1D", 24 * time.Hour},
		{"P1DT2H", 26 * time.Hour},
		{"PT45S", 45 * time.Second},
		{"P1W", 7 * 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, err := parseDuration(tt.text)
			if err != nil {
				t.Fatalf("parseDuration(%q) = %v", tt.text, err)
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.text, got, tt.want)
			}
			if back, err := parseDuration(formatDuration(got)); err != nil || back != got {
				t.Errorf("formatDuration round trip = %q -> %v", formatDuration(got), back)
			}
		})
	}
}

func TestInstantResolvesTheZone(t *testing.T) {
	e := &Event{Timezone: "Europe/Berlin"}
	// 09:00 Berlin on 1 April is 07:00Z, summer time having started.
	got := e.Instant(time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC))
	if want := "2026-04-01T07:00:00Z"; got.Format(time.RFC3339) != want {
		t.Errorf("Instant() = %s, want %s", got.Format(time.RFC3339), want)
	}
}
