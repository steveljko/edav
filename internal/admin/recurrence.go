package admin

import (
	"fmt"
	"strconv"
	"strings"
)

// repeatOption is one choice in the repeat menu offered when creating an
// event. Editing an existing rule is deliberately not offered: a rule with
// exceptions and moved occurrences cannot be shown in a menu, and a menu that
// silently replaced it would lose them.
type repeatOption struct {
	Value string
	Label string
}

var repeatOptions = []repeatOption{
	{Value: "", Label: "Does not repeat"},
	{Value: "FREQ=DAILY", Label: "Every day"},
	{Value: "FREQ=WEEKLY", Label: "Every week"},
	{Value: "FREQ=MONTHLY", Label: "Every month"},
	{Value: "FREQ=YEARLY", Label: "Every year"},
}

var frequencyNames = map[string]string{
	"SECONDLY": "second",
	"MINUTELY": "minute",
	"HOURLY":   "hour",
	"DAILY":    "day",
	"WEEKLY":   "week",
	"MONTHLY":  "month",
	"YEARLY":   "year",
}

var weekdayNames = map[string]string{
	"MO": "Monday", "TU": "Tuesday", "WE": "Wednesday", "TH": "Thursday",
	"FR": "Friday", "SA": "Saturday", "SU": "Sunday",
}

// describeRecurrence renders an RRULE as a sentence. It is for reading only:
// what cannot be described is reported as such rather than approximated, since
// a wrong description of a repeat is worse than none.
func describeRecurrence(rule string) string {
	parts := map[string]string{}
	for _, part := range strings.Split(rule, ";") {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			parts[strings.ToUpper(key)] = value
		}
	}

	freq, ok := frequencyNames[strings.ToUpper(parts["FREQ"])]
	if !ok {
		return "Repeats on a schedule this page cannot describe"
	}

	var b strings.Builder
	interval := 1
	if n, err := strconv.Atoi(parts["INTERVAL"]); err == nil && n > 1 {
		interval = n
	}

	switch interval {
	case 1:
		fmt.Fprintf(&b, "Every %s", freq)
	case 2:
		fmt.Fprintf(&b, "Every other %s", freq)
	default:
		fmt.Fprintf(&b, "Every %d %ss", interval, freq)
	}

	if days := parts["BYDAY"]; days != "" {
		var named []string
		for _, day := range strings.Split(days, ",") {
			// An ordinal prefix such as -1SU is a position in the month, which
			// this sentence does not try to render.
			trimmed := strings.TrimLeft(day, "+-0123456789")
			if name, ok := weekdayNames[strings.ToUpper(trimmed)]; ok && trimmed == day {
				named = append(named, name)
			}
		}
		if len(named) > 0 {
			fmt.Fprintf(&b, " on %s", joinWords(named))
		}
	}

	switch {
	case parts["COUNT"] != "":
		if n, err := strconv.Atoi(parts["COUNT"]); err == nil {
			fmt.Fprintf(&b, ", %d times", n)
		}
	case parts["UNTIL"] != "":
		if until := parts["UNTIL"]; len(until) >= 8 {
			fmt.Fprintf(&b, ", until %s-%s-%s", until[0:4], until[4:6], until[6:8])
		}
	}
	return b.String()
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	default:
		return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
	}
}
