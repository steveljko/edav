package recurrence

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func load(t *testing.T, name string) *ical.Calendar {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return cal
}

func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		panic(err)
	}
	return t
}

// starts renders instances as RFC 3339 UTC for readable failures.
func starts(instances []Instance) []string {
	out := make([]string, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst.Start.UTC().Format(time.RFC3339))
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func expand(t *testing.T, file string, from, to time.Time) []Instance {
	t.Helper()
	cal := load(t, file)
	instances, err := NewExpander(cal).Expand(cal, from, to)
	if err != nil {
		t.Fatalf("Expand(%s) = %v", file, err)
	}
	return instances
}

func TestExpandStarts(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		from, to time.Time
		want     []string
	}{
		{
			name: "one-off inside range",
			file: "simple.ics",
			from: utc("2026-03-01T00:00:00Z"), to: utc("2026-04-01T00:00:00Z"),
			want: []string{"2026-03-15T09:00:00Z"},
		},
		{
			name: "one-off outside range",
			file: "simple.ics",
			from: utc("2026-04-01T00:00:00Z"), to: utc("2026-05-01T00:00:00Z"),
			want: nil,
		},
		{
			name: "daily with COUNT",
			file: "daily_count.ics",
			from: utc("2026-01-01T00:00:00Z"), to: utc("2026-12-31T00:00:00Z"),
			want: []string{
				"2026-03-01T09:00:00Z", "2026-03-02T09:00:00Z", "2026-03-03T09:00:00Z",
				"2026-03-04T09:00:00Z", "2026-03-05T09:00:00Z",
			},
		},
		{
			name: "daily clipped to a narrow range",
			file: "daily_count.ics",
			from: utc("2026-03-02T12:00:00Z"), to: utc("2026-03-04T12:00:00Z"),
			want: []string{"2026-03-03T09:00:00Z", "2026-03-04T09:00:00Z"},
		},
		{
			name: "multi-value EXDATE removes both",
			file: "exdate_multi.ics",
			from: utc("2026-01-01T00:00:00Z"), to: utc("2026-12-31T00:00:00Z"),
			want: []string{"2026-03-01T09:00:00Z", "2026-03-02T09:00:00Z", "2026-03-04T09:00:00Z"},
		},
		{
			name: "RDATE adds an occurrence outside the rule",
			file: "rdate.ics",
			from: utc("2026-01-01T00:00:00Z"), to: utc("2026-12-31T00:00:00Z"),
			want: []string{"2026-03-01T09:00:00Z", "2026-03-02T09:00:00Z", "2026-03-20T09:00:00Z"},
		},
		{
			name: "RECURRENCE-ID override replaces one instance",
			file: "override.ics",
			from: utc("2026-01-01T00:00:00Z"), to: utc("2026-12-31T00:00:00Z"),
			want: []string{
				"2026-03-01T09:00:00Z", "2026-03-02T09:00:00Z",
				"2026-03-03T14:00:00Z", "2026-03-04T09:00:00Z",
			},
		},
		{
			// The overridden instance must not appear at its original time.
			name: "override moved months away leaves a gap",
			file: "override_moved.ics",
			from: utc("2026-03-01T00:00:00Z"), to: utc("2026-03-31T00:00:00Z"),
			want: []string{"2026-03-01T09:00:00Z", "2026-03-03T09:00:00Z"},
		},
		{
			name: "override moved months away appears at its new time",
			file: "override_moved.ics",
			from: utc("2026-06-01T00:00:00Z"), to: utc("2026-06-30T00:00:00Z"),
			want: []string{"2026-06-10T09:00:00Z"},
		},
		{
			// 09:00 Berlin is 08:00Z in winter and 07:00Z after the last
			// Sunday in March. The wall time must stay at 09:00 local.
			name: "IANA zone keeps local time across DST",
			file: "dst_iana.ics",
			from: utc("2026-03-01T00:00:00Z"), to: utc("2026-05-01T00:00:00Z"),
			want: []string{
				"2026-03-23T08:00:00Z", "2026-03-30T07:00:00Z", "2026-04-06T07:00:00Z",
			},
		},
		{
			name: "embedded VTIMEZONE keeps local time across DST",
			file: "dst_vtimezone.ics",
			from: utc("2026-03-01T00:00:00Z"), to: utc("2026-05-01T00:00:00Z"),
			want: []string{
				"2026-03-23T08:00:00Z", "2026-03-30T07:00:00Z", "2026-04-06T07:00:00Z",
			},
		},
		{
			name: "namespaced TZID resolves to the IANA zone",
			file: "tzid_prefixed.ics",
			from: utc("2026-03-01T00:00:00Z"), to: utc("2026-04-01T00:00:00Z"),
			want: []string{"2026-03-15T08:00:00Z"},
		},
		{
			// UNTIL is an instant in UTC while the series runs in local time,
			// and RFC 5545 3.3.10 makes it inclusive: the occurrence landing
			// exactly on it is the last one kept.
			name: "UNTIL in UTC bounds a local-time series inclusively",
			file: "until_utc.ics",
			from: utc("2026-01-01T00:00:00Z"), to: utc("2026-12-31T00:00:00Z"),
			want: []string{
				"2026-03-02T08:00:00Z", "2026-03-03T08:00:00Z", "2026-03-04T08:00:00Z",
			},
		},
		{
			name: "all-day event spans its whole day",
			file: "allday.ics",
			from: utc("2026-03-15T20:00:00Z"), to: utc("2026-03-16T00:00:00Z"),
			want: []string{"2026-03-15T00:00:00Z"},
		},
		{
			name: "all-day event does not leak into the next day",
			file: "allday.ics",
			from: utc("2026-03-16T00:00:00Z"), to: utc("2026-03-17T00:00:00Z"),
			want: nil,
		},
		{
			name: "VTODO with DUE and no DTSTART",
			file: "todo_due.ics",
			from: utc("2026-03-15T00:00:00Z"), to: utc("2026-03-16T00:00:00Z"),
			want: []string{"2026-03-15T17:00:00Z"},
		},
		{
			name: "infinite series is clipped to the query range",
			file: "infinite.ics",
			from: utc("2026-02-01T00:00:00Z"), to: utc("2026-03-01T00:00:00Z"),
			want: []string{
				"2026-02-02T09:00:00Z", "2026-02-09T09:00:00Z",
				"2026-02-16T09:00:00Z", "2026-02-23T09:00:00Z",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := starts(expand(t, tt.file, tt.from, tt.to))
			if !equal(got, tt.want) {
				t.Errorf("starts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpandDurations(t *testing.T) {
	tests := []struct {
		name string
		file string
		want time.Duration
	}{
		{"explicit DTEND", "simple.ics", time.Hour},
		{"DURATION property", "duration.ics", 90 * time.Minute},
		{"all-day defaults to one day", "allday.ics", 24 * time.Hour},
		{"VTODO with only DUE has no duration", "todo_due.ics", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instances := expand(t, tt.file, utc("2026-01-01T00:00:00Z"), utc("2026-12-31T00:00:00Z"))
			if len(instances) == 0 {
				t.Fatal("no instances")
			}
			if got := instances[0].End.Sub(instances[0].Start); got != tt.want {
				t.Errorf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}

// RFC 4791 9.9: a component overlaps when it starts before the range ends and
// ends after the range starts. The boundaries are where servers get it wrong.
func TestTimeRangeBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		from, to time.Time
		want     bool
	}{
		{"range ends exactly at event start", utc("2026-03-15T08:00:00Z"), utc("2026-03-15T09:00:00Z"), false},
		{"range starts exactly at event end", utc("2026-03-15T10:00:00Z"), utc("2026-03-15T11:00:00Z"), false},
		{"range ends one second after event start", utc("2026-03-15T08:00:00Z"), utc("2026-03-15T09:00:01Z"), true},
		{"range starts one second before event end", utc("2026-03-15T09:59:59Z"), utc("2026-03-15T11:00:00Z"), true},
		{"range exactly equals the event", utc("2026-03-15T09:00:00Z"), utc("2026-03-15T10:00:00Z"), true},
		{"range strictly inside the event", utc("2026-03-15T09:15:00Z"), utc("2026-03-15T09:45:00Z"), true},
	}

	cal := load(t, "simple.ics")
	e := NewExpander(cal)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Overlaps(cal, tt.from, tt.to)
			if err != nil {
				t.Fatalf("Overlaps() = %v", err)
			}
			if got != tt.want {
				t.Errorf("Overlaps() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSummarise(t *testing.T) {
	tests := []struct {
		name          string
		file          string
		wantUID       string
		wantComp      string
		wantSummary   string
		wantStart     string
		wantEnd       string // empty means nil, for an unbounded series
		wantRecurring bool
	}{
		{
			name: "one-off", file: "simple.ics",
			wantUID: "simple-1", wantComp: "VEVENT", wantSummary: "One-off meeting",
			wantStart: "2026-03-15T09:00:00Z", wantEnd: "2026-03-15T10:00:00Z",
		},
		{
			name: "bounded series spans first to last", file: "daily_count.ics",
			wantUID: "daily-1", wantComp: "VEVENT", wantSummary: "Five daily standups",
			wantStart: "2026-03-01T09:00:00Z", wantEnd: "2026-03-05T10:00:00Z",
			wantRecurring: true,
		},
		{
			name: "unbounded series has no end", file: "infinite.ics",
			wantUID: "infinite-1", wantComp: "VEVENT", wantSummary: "Never ends",
			wantStart: "2026-01-01T09:00:00Z", wantEnd: "",
			wantRecurring: true,
		},
		{
			name: "override widens the bounds", file: "override_moved.ics",
			wantUID: "moved-1", wantComp: "VEVENT", wantSummary: "Daily",
			wantStart: "2026-03-01T09:00:00Z", wantEnd: "2026-06-10T10:00:00Z",
			wantRecurring: true,
		},
		{
			name: "VTODO indexes its DUE", file: "todo_due.ics",
			wantUID: "todo-1", wantComp: "VTODO", wantSummary: "File the tax return",
			wantStart: "2026-03-15T17:00:00Z", wantEnd: "2026-03-15T17:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := load(t, tt.file)
			got, err := NewExpander(cal).Summarise(cal)
			if err != nil {
				t.Fatalf("Summarise() = %v", err)
			}

			if got.UID != tt.wantUID {
				t.Errorf("UID = %q, want %q", got.UID, tt.wantUID)
			}
			if got.ComponentType != tt.wantComp {
				t.Errorf("ComponentType = %q, want %q", got.ComponentType, tt.wantComp)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if got.Recurring != tt.wantRecurring {
				t.Errorf("Recurring = %v, want %v", got.Recurring, tt.wantRecurring)
			}
			if got.Start == nil {
				t.Fatal("Start = nil")
			}
			if s := got.Start.UTC().Format(time.RFC3339); s != tt.wantStart {
				t.Errorf("Start = %s, want %s", s, tt.wantStart)
			}
			switch {
			case tt.wantEnd == "":
				if got.End != nil {
					t.Errorf("End = %s, want nil for an unbounded series", got.End.UTC().Format(time.RFC3339))
				}
			case got.End == nil:
				t.Errorf("End = nil, want %s", tt.wantEnd)
			default:
				if s := got.End.UTC().Format(time.RFC3339); s != tt.wantEnd {
					t.Errorf("End = %s, want %s", s, tt.wantEnd)
				}
			}
		})
	}
}

func TestExpandIsDeterministic(t *testing.T) {
	from, to := utc("2026-01-01T00:00:00Z"), utc("2026-12-31T00:00:00Z")
	first := starts(expand(t, "override.ics", from, to))
	for i := 0; i < 5; i++ {
		if got := starts(expand(t, "override.ics", from, to)); !equal(got, first) {
			t.Fatalf("run %d = %v, want %v", i, got, first)
		}
	}
}

func TestExpandInstancesAreOrdered(t *testing.T) {
	instances := expand(t, "override_moved.ics", utc("2026-01-01T00:00:00Z"), utc("2026-12-31T00:00:00Z"))
	for i := 1; i < len(instances); i++ {
		if instances[i].Start.Before(instances[i-1].Start) {
			t.Errorf("instance %d starts before %d: %v", i, i-1, starts(instances))
			break
		}
	}
}

func TestOverrideIsMarked(t *testing.T) {
	instances := expand(t, "override.ics", utc("2026-01-01T00:00:00Z"), utc("2026-12-31T00:00:00Z"))

	var overrides int
	for _, inst := range instances {
		if inst.Override {
			overrides++
			if !inst.Start.Equal(utc("2026-03-03T14:00:00Z")) {
				t.Errorf("override starts at %v, want 2026-03-03T14:00:00Z", inst.Start)
			}
		}
	}
	if overrides != 1 {
		t.Errorf("overrides = %d, want 1", overrides)
	}
}
