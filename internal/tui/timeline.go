package tui

import (
	"strings"
	"time"

	"github.com/kyle/curfew/internal/config"
)

// The dashboard and editor render each day as a fixed-width bar: 48 cells of
// 30 minutes covering 0:00 → 24:00.
const (
	barCells       = 48
	minutesPerCell = 24 * 60 / barCells
)

// cellState is one bar cell's content, in increasing display priority: a reset
// tick always wins over active coverage.
type cellState uint8

const (
	cellOff cellState = iota
	cellActive
	cellTick
)

// dayMinutes marks, minute by minute, when the provider's limit window is
// active on the calendar day `day`, with resets on that day overlaid as ticks.
// Each reset r on day d spans the window [d+r − window, d+r], so a window can
// begin on the previous day: resets scheduled for day+1 are considered too and
// their tails clipped to `day`. Ticks only mark resets on `day` itself.
func dayMinutes(cfg *config.Config, provider string, day time.Time) [24 * 60]cellState {
	var out [24 * 60]cellState
	if cfg == nil {
		return out
	}
	win := providerWindowMinutes(cfg, provider)
	for _, s := range cfg.Schedules {
		if s.Provider != provider {
			continue
		}
		// dayOffset 0 = `day`, 1 = the following day (whose windows may reach
		// back across midnight into `day`).
		for dayOffset := 0; dayOffset <= 1; dayOffset++ {
			wd := day.AddDate(0, 0, dayOffset).Weekday()
			if !scheduleOnDay(s, wd) {
				continue
			}
			for _, r := range s.ResetsAt {
				rm, ok := minutesOfDay(r)
				if !ok {
					continue
				}
				reset := dayOffset*24*60 + rm
				for t := max(reset-win, 0); t < min(reset, 24*60); t++ {
					if out[t] == cellOff {
						out[t] = cellActive
					}
				}
				if dayOffset == 0 {
					out[rm] = cellTick
				}
			}
		}
	}
	return out
}

// groupCells renders a single day group's reset times (minutes of day) as bar
// cells, independent of any calendar day: windows that would start before 0:00
// wrap around to the end of the bar, since the group repeats on each of its
// days.
func groupCells(times []int, windowMin int) [barCells]cellState {
	var minutes [24 * 60]cellState
	for _, rm := range times {
		if rm < 0 || rm >= 24*60 {
			continue
		}
		for i := 1; i <= windowMin && i <= 24*60; i++ {
			t := ((rm-i)%(24*60) + 24*60) % (24 * 60)
			if minutes[t] == cellOff {
				minutes[t] = cellActive
			}
		}
	}
	for _, rm := range times {
		if rm >= 0 && rm < 24*60 {
			minutes[rm] = cellTick
		}
	}
	return toCells(minutes)
}

// toCells reduces the minute array to bar cells; a cell shows the highest-
// priority state among its minutes.
func toCells(minutes [24 * 60]cellState) [barCells]cellState {
	var out [barCells]cellState
	for i, st := range minutes {
		c := i / minutesPerCell
		if st > out[c] {
			out[c] = st
		}
	}
	return out
}

// scheduleOnDay reports whether the schedule runs on the given weekday.
func scheduleOnDay(s config.Schedule, wd time.Weekday) bool {
	for _, d := range s.Weekdays() {
		if d == wd {
			return true
		}
	}
	return false
}

// providerWindowMinutes returns the provider's window length, defaulting to
// 300 when the provider is missing or unset.
func providerWindowMinutes(cfg *config.Config, provider string) int {
	if cfg != nil {
		if p, ok := cfg.Provider(provider); ok && p.WindowMinutes > 0 {
			return p.WindowMinutes
		}
	}
	return 300
}

// renderBar draws the cells as one styled line. cursorCell >= 0 draws the
// editor cursor as a white block on that cell; nowCell >= 0 marks the current
// time with barNowStyle (dashboard). Pass -1 to omit either.
func renderBar(cells [barCells]cellState, cursorCell, nowCell int) string {
	var b strings.Builder
	for i, c := range cells {
		glyph, style := "·", faintStyle
		switch c {
		case cellActive:
			glyph, style = "█", barActiveStyle
		case cellTick:
			glyph, style = "█", barTickStyle
		}
		switch i {
		case cursorCell:
			glyph, style = "█", barCursorStyle
		case nowCell:
			glyph, style = "█", barNowStyle
		}
		b.WriteString(style.Render(glyph))
	}
	return b.String()
}

// renderBarScale is the shared 0:00 / 12:00 / 24:00 ruler above the bars.
func renderBarScale() string {
	line := []byte(strings.Repeat(" ", barCells))
	copy(line[0:], "0:00")
	copy(line[barCells/2-2:], "12:00")
	copy(line[barCells-5:], "24:00")
	return string(line)
}
