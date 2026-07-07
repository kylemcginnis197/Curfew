package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
)

func TestAnchorForResetMultiDayShift(t *testing.T) {
	// A 25h window with a 00:00 reset anchors two calendar days earlier at 23:00.
	a, err := AnchorForReset(25*60, "00:00")
	if err != nil {
		t.Fatal(err)
	}
	if a.Hour != 23 || a.Min != 0 {
		t.Errorf("anchor = %02d:%02d, want 23:00", a.Hour, a.Min)
	}
	if a.DayShift != -2 {
		t.Errorf("dayShift = %d, want -2", a.DayShift)
	}
}

func TestAnchorForResetInvalid(t *testing.T) {
	if _, err := AnchorForReset(300, "9:99"); err == nil {
		t.Fatal("expected error for bad reset time")
	}
}

func TestMultiDayWeekdayShift(t *testing.T) {
	// 25h window, reset 00:00 Monday -> anchor 23:00 two days earlier = Saturday.
	c := &config.Config{
		Providers: []config.Provider{{Name: "p", Command: "x", WindowMinutes: 25 * 60}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: []string{"00:00"}, Days: []string{"Mon"}}},
	}
	got, err := Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Weekdays) != 1 || got[0].Weekdays[0] != time.Saturday {
		t.Fatalf("weekday = %v, want [Saturday]", got[0].Weekdays)
	}
	if spec := got[0].CronSpec(); spec != "0 23 * * 6" {
		t.Errorf("cron = %q, want %q", spec, "0 23 * * 6")
	}
}

func TestCompileUnknownProvider(t *testing.T) {
	c := &config.Config{
		Schedules: []config.Schedule{{Provider: "ghost", ResetsAt: []string{"10:00"}}},
	}
	if _, err := Compile(c); err == nil {
		t.Fatal("expected error compiling schedule for unknown provider")
	}
}

func TestNextAndPrev(t *testing.T) {
	// Daily 05:00 anchor.
	a := Anchor{Hour: 5, Min: 0, Weekdays: []time.Weekday{0, 1, 2, 3, 4, 5, 6}}
	loc := time.UTC
	at := time.Date(2026, 7, 6, 12, 0, 0, 0, loc) // Mon noon

	next, err := a.Next(at)
	if err != nil {
		t.Fatal(err)
	}
	// Next 05:00 strictly after Mon noon is Tue 05:00.
	if want := time.Date(2026, 7, 7, 5, 0, 0, 0, loc); !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}

	prev, err := a.Prev(at)
	if err != nil {
		t.Fatal(err)
	}
	// Most recent 05:00 at/before Mon noon is Mon 05:00.
	if want := time.Date(2026, 7, 6, 5, 0, 0, 0, loc); !prev.Equal(want) {
		t.Errorf("prev = %v, want %v", prev, want)
	}
}

func TestPrevOutOfRange(t *testing.T) {
	// Anchor only fires on Sunday; a Wednesday query is >25h from the last fire,
	// so Prev returns the zero time (documented look-back limitation).
	a := Anchor{Hour: 5, Min: 0, Weekdays: []time.Weekday{time.Sunday}}
	at := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) // Wednesday
	prev, err := a.Prev(at)
	if err != nil {
		t.Fatal(err)
	}
	if !prev.IsZero() {
		t.Errorf("prev = %v, want zero (out of 25h range)", prev)
	}
}

func TestDescribe(t *testing.T) {
	same := Anchor{Provider: "p", Hour: 5, Min: 0, DayShift: 0, Reset: "10:00"}
	if got := same.Describe(); !strings.Contains(got, "same day") {
		t.Errorf("describe = %q, want to mention same day", got)
	}
	prev := Anchor{Provider: "p", Hour: 21, Min: 0, DayShift: -1, Reset: "02:00"}
	if got := prev.Describe(); !strings.Contains(got, "prev day") {
		t.Errorf("describe = %q, want to mention prev day", got)
	}
}
