package schedule

import (
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
	"github.com/robfig/cron/v3"
)

func TestAnchorSameDay(t *testing.T) {
	// reset 10:00, 300m window -> anchor 05:00 same day, plus primer 10:01.
	c := &config.Config{
		Providers: []config.Provider{{Name: "claude-1", Command: "x", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "claude-1", ResetsAt: []string{"10:00"}, Days: []string{"Mon"}}},
	}
	got, err := Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want anchor + primer, got %d entries", len(got))
	}
	a := got[0]
	if a.Primer {
		t.Error("first entry should be the anchor, not the primer")
	}
	if a.Hour != 5 || a.Min != 0 || a.DayShift != 0 {
		t.Errorf("anchor = %02d:%02d shift %d, want 05:00 shift 0", a.Hour, a.Min, a.DayShift)
	}
	if spec := a.CronSpec(); spec != "0 5 * * 1" {
		t.Errorf("cron = %q, want %q", spec, "0 5 * * 1")
	}
	pr := got[1]
	if !pr.Primer {
		t.Fatal("second entry should be the primer")
	}
	if pr.Hour != 10 || pr.Min != 1 || pr.DayShift != 0 {
		t.Errorf("primer = %02d:%02d shift %d, want 10:01 shift 0", pr.Hour, pr.Min, pr.DayShift)
	}
	if spec := pr.CronSpec(); spec != "1 10 * * 1" {
		t.Errorf("primer cron = %q, want %q", spec, "1 10 * * 1")
	}
}

func TestPrimerCrossesMidnight(t *testing.T) {
	// reset 23:59 Mon, delay 2 -> primer 00:01 Tue (next day).
	c := &config.Config{
		General:   config.General{PrimeDelayMinutes: 2},
		Providers: []config.Provider{{Name: "p", Command: "x", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: []string{"23:59"}, Days: []string{"Mon"}}},
	}
	got, err := Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	pr := got[1]
	if !pr.Primer {
		t.Fatal("second entry should be the primer")
	}
	if pr.Hour != 0 || pr.Min != 1 || pr.DayShift != 1 {
		t.Errorf("primer = %02d:%02d shift %d, want 00:01 shift +1", pr.Hour, pr.Min, pr.DayShift)
	}
	if len(pr.Weekdays) != 1 || pr.Weekdays[0] != time.Tuesday {
		t.Errorf("primer weekday = %v, want [Tuesday]", pr.Weekdays)
	}
}

func TestAnchorCrossesMidnight(t *testing.T) {
	// reset 02:00 Mon, 300m window -> anchor 21:00 Sun (prev day).
	c := &config.Config{
		Providers: []config.Provider{{Name: "p", Command: "x", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: []string{"02:00"}, Days: []string{"Mon"}}},
	}
	got, _ := Compile(c)
	a := got[0]
	if a.Hour != 21 || a.Min != 0 || a.DayShift != -1 {
		t.Errorf("anchor = %02d:%02d shift %d, want 21:00 shift -1", a.Hour, a.Min, a.DayShift)
	}
	if len(a.Weekdays) != 1 || a.Weekdays[0] != time.Sunday {
		t.Errorf("weekday = %v, want [Sunday]", a.Weekdays)
	}
	if spec := a.CronSpec(); spec != "0 21 * * 0" {
		t.Errorf("cron = %q, want %q", spec, "0 21 * * 0")
	}
}

func TestAnchorHalfHour(t *testing.T) {
	// reset 12:30, 300m -> 07:30.
	c := &config.Config{
		Providers: []config.Provider{{Name: "p", Command: "x", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: []string{"12:30"}}},
	}
	got, _ := Compile(c)
	if got[0].Hour != 7 || got[0].Min != 30 {
		t.Errorf("anchor = %02d:%02d, want 07:30", got[0].Hour, got[0].Min)
	}
	// No days specified -> all seven weekdays.
	if len(got[0].Weekdays) != 7 {
		t.Errorf("weekdays = %d, want 7", len(got[0].Weekdays))
	}
}

func TestAnchorForReset(t *testing.T) {
	cases := []struct {
		reset            string
		win              int
		wantH, wantM, ds int
		wantErr          bool
	}{
		{"20:00", 300, 15, 0, 0, false},  // same day
		{"10:00", 300, 5, 0, 0, false},   // same day
		{"12:30", 300, 7, 30, 0, false},  // half hour
		{"02:00", 300, 21, 0, -1, false}, // wraps to prev day
		{"00:00", 300, 19, 0, -1, false}, // midnight reset
		{"25:00", 300, 0, 0, 0, true},    // invalid
		{"bogus", 300, 0, 0, 0, true},    // invalid
	}
	for _, tc := range cases {
		a, err := AnchorForReset(tc.win, tc.reset)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", tc.reset)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.reset, err)
			continue
		}
		if a.Hour != tc.wantH || a.Min != tc.wantM || a.DayShift != tc.ds {
			t.Errorf("%s: got %02d:%02d shift %d, want %02d:%02d shift %d",
				tc.reset, a.Hour, a.Min, a.DayShift, tc.wantH, tc.wantM, tc.ds)
		}
	}
}

func TestMultipleResetsAndProviders(t *testing.T) {
	got, err := Compile(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	// Default: claude (3 resets) + codex (1) = 4 anchors, each with a primer.
	if len(got) != 8 {
		t.Fatalf("want 8 entries (4 anchors + 4 primers), got %d", len(got))
	}
}

func TestAutoPrimeOff(t *testing.T) {
	off := false
	c := config.Default()
	c.General.AutoPrime = &off
	got, err := Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	// Same schedules as TestMultipleResetsAndProviders, but no primers.
	if len(got) != 4 {
		t.Fatalf("want 4 anchors (no primers), got %d", len(got))
	}
	for _, a := range got {
		if a.Primer {
			t.Errorf("unexpected primer with auto_prime off: %s", a.Describe())
		}
	}
}

// TestCronDSTWallClock verifies that the compiled cron spec, evaluated in a
// zone with DST, fires at the intended wall-clock time on both sides of a DST
// transition (robfig/cron owns the DST handling; we just feed it the spec).
func TestCronDSTWallClock(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	a := Anchor{Provider: "p", Hour: 5, Min: 0, Weekdays: []time.Weekday{0, 1, 2, 3, 4, 5, 6}}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(a.CronSpec())
	if err != nil {
		t.Fatal(err)
	}
	// US DST began 2024-03-10. Check the next 05:00 fire before and after it.
	for _, from := range []time.Time{
		time.Date(2024, 3, 8, 12, 0, 0, 0, ny),  // before DST
		time.Date(2024, 3, 12, 12, 0, 0, 0, ny), // after DST
	} {
		next := sched.Next(from)
		if h, m := next.In(ny).Hour(), next.In(ny).Minute(); h != 5 || m != 0 {
			t.Errorf("from %v: next fire %v is %02d:%02d wall, want 05:00", from, next.In(ny), h, m)
		}
	}
}
