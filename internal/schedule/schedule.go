// Package schedule compiles user-facing reset boundaries into concrete anchor
// jobs. A window that should reset at R must start at R - window_length, so each
// desired reset time yields an anchor cron entry that fires that much earlier
// (possibly on the previous calendar day).
package schedule

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kyle/curfew/internal/config"
	"github.com/robfig/cron/v3"
)

// cronParser parses the 5-field specs produced by CronSpec.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Anchor is one compiled trigger: fire the provider's anchor command at
// Hour:Min on each of Weekdays, so a window resets Reset later.
type Anchor struct {
	Provider string         // provider name to anchor
	Reset    string         // "HH:MM" reset boundary this serves (for display)
	Hour     int            // anchor hour (0-23), local to the config timezone
	Min      int            // anchor minute (0-59)
	DayShift int            // -1/+1 if the fire time falls on the prev/next calendar day
	Weekdays []time.Weekday // fire weekdays (already shifted from the reset day)
	// Primer marks an entry that fires just after Reset (rather than
	// window_minutes before it) so the next window starts on schedule.
	Primer bool
}

// CronSpec renders the anchor as a standard 5-field cron expression
// ("min hour dom month dow") for robfig/cron, evaluated in the config timezone.
func (a Anchor) CronSpec() string {
	days := make([]string, len(a.Weekdays))
	for i, d := range a.Weekdays {
		days[i] = strconv.Itoa(int(d))
	}
	sort.Strings(days)
	dow := "*"
	if len(days) > 0 {
		dow = strings.Join(days, ",")
	}
	return fmt.Sprintf("%d %d * * %s", a.Min, a.Hour, dow)
}

// Next returns the next time this anchor fires strictly after `after`. The
// weekday/hour/minute are interpreted in after's location, so callers pass a
// time already in the configured timezone for DST-correct results.
func (a Anchor) Next(after time.Time) (time.Time, error) {
	s, err := cronParser.Parse(a.CronSpec())
	if err != nil {
		return time.Time{}, err
	}
	return s.Next(after), nil
}

// Prev returns the most recent time this anchor fired at or before `at`, found
// by scanning forward from a day earlier. Returns zero time if none in range.
func (a Anchor) Prev(at time.Time) (time.Time, error) {
	s, err := cronParser.Parse(a.CronSpec())
	if err != nil {
		return time.Time{}, err
	}
	var prev time.Time
	for t := s.Next(at.Add(-25 * time.Hour)); !t.After(at); t = s.Next(t) {
		prev = t
	}
	return prev, nil
}

// Describe returns a human-readable summary for the TUI/CLI.
func (a Anchor) Describe() string {
	when := "same day"
	switch {
	case a.DayShift < 0:
		when = "prev day"
	case a.DayShift > 0:
		when = "next day"
	}
	if a.Primer {
		return fmt.Sprintf("%s: prime %02d:%02d (%s) after reset %s", a.Provider, a.Hour, a.Min, when, a.Reset)
	}
	return fmt.Sprintf("%s: anchor %02d:%02d (%s) -> reset %s", a.Provider, a.Hour, a.Min, when, a.Reset)
}

// Compile turns every schedule in the config into anchor jobs. Each reset time
// yields the anchor that starts its window plus, when auto-prime is enabled, a
// primer that fires just after the reset so the next window starts on schedule.
func Compile(c *config.Config) ([]Anchor, error) {
	var out []Anchor
	for _, s := range c.Schedules {
		p, ok := c.Provider(s.Provider)
		if !ok {
			return nil, fmt.Errorf("schedule references unknown provider %q", s.Provider)
		}
		for _, r := range s.ResetsAt {
			a, err := anchorFor(s, -p.WindowMinutes, r)
			if err != nil {
				return nil, err
			}
			out = append(out, a)

			if c.General.AutoPrimeEnabled() {
				pr, err := anchorFor(s, c.General.PrimeDelay(), r)
				if err != nil {
					return nil, err
				}
				pr.Primer = true
				out = append(out, pr)
			}
		}
	}
	return out, nil
}

// AnchorForReset computes the anchor time (hour/minute and day-shift) for a
// single reset time and window length, independent of any weekday context. It's
// the core reset→anchor math, handy for displaying "reset 20:00 → anchor 15:00"
// in the editor. DayShift is -1 when the anchor wraps to the previous day.
func AnchorForReset(windowMinutes int, reset string) (Anchor, error) {
	return offsetFromReset(-windowMinutes, reset)
}

// offsetFromReset computes the fire time offsetMin minutes after (negative:
// before) a reset time, wrapping DayShift by whole days as needed.
func offsetFromReset(offsetMin int, reset string) (Anchor, error) {
	h, m, err := parseHHMM(reset)
	if err != nil {
		return Anchor{}, fmt.Errorf("reset %q: %w", reset, err)
	}
	fireMin := h*60 + m + offsetMin
	dayShift := 0
	for fireMin < 0 {
		fireMin += 24 * 60
		dayShift--
	}
	for fireMin >= 24*60 {
		fireMin -= 24 * 60
		dayShift++
	}
	return Anchor{
		Reset:    reset,
		Hour:     fireMin / 60,
		Min:      fireMin % 60,
		DayShift: dayShift,
	}, nil
}

// anchorFor computes the fire time offsetMin from a reset time within a
// schedule, shifting the schedule's weekdays when the fire time wraps past
// midnight in either direction.
func anchorFor(s config.Schedule, offsetMin int, reset string) (Anchor, error) {
	a, err := offsetFromReset(offsetMin, reset)
	if err != nil {
		return Anchor{}, fmt.Errorf("provider %s: %w", s.Provider, err)
	}
	a.Provider = s.Provider
	for _, d := range s.Weekdays() {
		a.Weekdays = append(a.Weekdays, time.Weekday((int(d)+a.DayShift+7)%7))
	}
	return a, nil
}

func parseHHMM(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want HH:MM")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("bad hour")
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("bad minute")
	}
	return h, m, nil
}
