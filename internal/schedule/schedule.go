// Package schedule decides when a campaign sends. A rule is plain data chosen by
// a person, so it is validated like any other input and turned into the next
// moment to run by one pure function.
package schedule

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // zones must resolve in a minimal container image
)

// The kinds of rule.
const (
	KindOnce     = "once"
	KindInterval = "interval"
	KindWeekly   = "weekly"

	maxEveryHours = 720
	maxAhead      = 366 * 24 * time.Hour
)

// Whom later runs of a repeating rule reach. ReachNew, the default, leaves out
// everyone an earlier run reached.
const (
	ReachNew      = "new"
	ReachAfter    = "after"
	ReachEveryone = "everyone"

	maxReachAfterDays = 365
)

var weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Spec is a sending rule.
type Spec struct {
	Kind string `json:"kind"`
	// At is when a one-time send happens.
	At *time.Time `json:"at,omitempty"`
	// EveryHours and StartsAt describe an interval. Runs fall on a fixed grid
	// counted from StartsAt.
	EveryHours int        `json:"every_hours,omitempty"`
	StartsAt   *time.Time `json:"starts_at,omitempty"`
	// Days, Time (HH:MM) and Timezone describe a weekly rule, in local time.
	Days     []string `json:"days,omitempty"`
	Time     string   `json:"time,omitempty"`
	Timezone string   `json:"timezone,omitempty"`
	// EndsAt optionally stops a repeating rule, inclusive.
	EndsAt *time.Time `json:"ends_at,omitempty"`
	// Reach says whom a repeating rule's later runs may message again. Within one run
	// nobody gets the message twice, whatever it says.
	Reach string `json:"reach,omitempty"`
	// ReachAfterDays is how long someone rests before ReachAfter lets them in again.
	ReachAfterDays int `json:"reach_after_days,omitempty"`
}

// InvalidError says what is wrong with a rule, in words for the person choosing it.
type InvalidError struct {
	Message string
}

func (e *InvalidError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &InvalidError{fmt.Sprintf(format, args...)}
}

// Validate checks a rule against the current time.
func (s Spec) Validate(now time.Time) error {
	switch s.Kind {
	case KindOnce:
		if s.At == nil {
			return invalid("Choose when to send.")
		}
		if !s.At.After(now) {
			return invalid("The send time must be in the future.")
		}
		if s.At.After(now.Add(maxAhead)) {
			return invalid("The send time can be at most a year ahead.")
		}
	case KindInterval:
		if s.EveryHours < 1 || s.EveryHours > maxEveryHours {
			return invalid("The interval must be between 1 hour and 30 days.")
		}
		if s.StartsAt != nil && s.EndsAt != nil && !s.EndsAt.After(*s.StartsAt) {
			return invalid("The end must be after the first send.")
		}
	case KindWeekly:
		if len(s.Days) == 0 {
			return invalid("Choose at least one day.")
		}
		for _, day := range s.Days {
			if !slices.Contains(weekdays, day) {
				return invalid("%q is not a day of the week.", day)
			}
		}
		if _, _, ok := clock(s.Time); !ok {
			return invalid("Choose a time of day as HH:MM.")
		}
		if _, err := time.LoadLocation(s.Timezone); err != nil || s.Timezone == "" {
			return invalid("%q is not a known time zone.", s.Timezone)
		}
	default:
		return invalid("Choose how this campaign should be scheduled.")
	}
	switch s.Reach {
	case "", ReachNew, ReachEveryone:
		if s.ReachAfterDays != 0 {
			return invalid("A waiting time only goes with reaching people again after some days.")
		}
	case ReachAfter:
		if s.ReachAfterDays < 1 || s.ReachAfterDays > maxReachAfterDays {
			return invalid("The waiting time must be between 1 and 365 days.")
		}
	default:
		return invalid("Choose whom later sends may reach again.")
	}
	if s.Kind == KindOnce && s.Reach != "" && s.Reach != ReachNew {
		return invalid("A one-time send has no later sends.")
	}
	if s.EndsAt != nil && !s.EndsAt.After(now) {
		return invalid("The end must be in the future.")
	}
	return nil
}

func clock(value string) (hour, minute int, ok bool) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, false
	}
	return parsed.Hour(), parsed.Minute(), true
}

// Next returns the first run strictly after the given moment, or false when the
// rule has none left. Runs missed during an outage are not made up one by one:
// the next run reaches everyone they would have.
func (s Spec) Next(after time.Time) (time.Time, bool) {
	var next time.Time
	switch s.Kind {
	case KindOnce:
		if s.At == nil || !s.At.After(after) {
			return time.Time{}, false
		}
		next = *s.At
	case KindInterval:
		next = s.nextInterval(after)
	case KindWeekly:
		next = s.nextWeekly(after)
	}
	if next.IsZero() || (s.EndsAt != nil && next.After(*s.EndsAt)) {
		return time.Time{}, false
	}
	return next, true
}

func (s Spec) nextInterval(after time.Time) time.Time {
	every := time.Duration(s.EveryHours) * time.Hour
	if every <= 0 {
		return time.Time{}
	}
	start := after
	if s.StartsAt != nil {
		start = *s.StartsAt
	}
	if start.After(after) {
		return start
	}
	return start.Add((after.Sub(start)/every + 1) * every)
}

func (s Spec) nextWeekly(after time.Time) time.Time {
	location, err := time.LoadLocation(s.Timezone)
	hour, minute, ok := clock(s.Time)
	if err != nil || !ok {
		return time.Time{}
	}
	local := after.In(location)
	for offset := range 8 {
		candidate := time.Date(local.Year(), local.Month(), local.Day()+offset, hour, minute, 0, 0, location)
		if candidate.After(after) && slices.Contains(s.Days, weekdays[candidate.Weekday()]) {
			return candidate
		}
	}
	return time.Time{}
}

var dayNames = map[string]string{
	"mon": "Monday", "tue": "Tuesday", "wed": "Wednesday", "thu": "Thursday",
	"fri": "Friday", "sat": "Saturday", "sun": "Sunday",
}

// Describe says what the rule does, for people.
func (s Spec) Describe() string {
	when := s.describeWhen()
	switch {
	case when == "" || s.Kind == KindOnce:
		return when
	case s.Reach == ReachAfter:
		return when + ", reaching people again after " + plural(s.ReachAfterDays, "day")
	case s.Reach == ReachEveryone:
		return when + ", reaching everyone who matches each time"
	}
	return when + ", reaching only people not reached before"
}

// ReachesAgainAfter is how long since their last message someone in a later run
// must have waited: zero for no wait, and false when nobody is reached twice.
func (s Spec) ReachesAgainAfter() (time.Duration, bool) {
	switch s.Reach {
	case ReachAfter:
		return time.Duration(s.ReachAfterDays) * 24 * time.Hour, true
	case ReachEveryone:
		return 0, true
	}
	return 0, false
}

func plural(count int, unit string) string {
	if count == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(count) + " " + unit + "s"
}

func (s Spec) describeWhen() string {
	switch s.Kind {
	case KindOnce:
		if s.At != nil {
			return "Once on " + s.At.Format("2 Jan 2006 at 15:04 (-07:00)")
		}
	case KindInterval:
		switch {
		case s.EveryHours == 1:
			return "Every hour"
		case s.EveryHours == 24:
			return "Every day"
		case s.EveryHours%24 == 0:
			return "Every " + strconv.Itoa(s.EveryHours/24) + " days"
		}
		return "Every " + strconv.Itoa(s.EveryHours) + " hours"
	case KindWeekly:
		return s.describeDays() + " at " + s.Time + " (" + s.Timezone + ")"
	}
	return ""
}

func (s Spec) describeDays() string {
	names := []string{}
	for _, day := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
		if slices.Contains(s.Days, day) {
			names = append(names, dayNames[day])
		}
	}
	if len(names) == 7 {
		return "Every day"
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
