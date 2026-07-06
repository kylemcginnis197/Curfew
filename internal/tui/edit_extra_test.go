package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
)

func TestDayEnabledEveryDay(t *testing.T) {
	// Empty Days means "every day": all weekdays should read as enabled.
	c := &config.Config{
		Providers: []config.Provider{{Name: "p", Command: []string{"x"}, WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: []string{"10:00"}}}, // no Days
	}
	for _, d := range weekdayOrder {
		if !dayEnabled(c, "p", d) {
			t.Errorf("dayEnabled(%v) = false, want true for every-day schedule", d)
		}
	}
	// Unknown provider -> not enabled, no panic.
	if dayEnabled(c, "ghost", time.Monday) {
		t.Error("dayEnabled for unknown provider should be false")
	}
}

func TestToggleDayFromEveryDayTurnsOneOff(t *testing.T) {
	c := &config.Config{
		Providers: []config.Provider{{Name: "p", Command: []string{"x"}, WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: []string{"10:00"}}}, // every day
	}
	if err := toggleDay(c, "p", time.Sunday); err != nil {
		t.Fatal(err)
	}
	i := providerScheduleIdx(c, "p")
	// Now six explicit days remain, Sunday off.
	if len(c.Schedules[i].Days) != 6 {
		t.Fatalf("days = %v, want 6 explicit days", c.Schedules[i].Days)
	}
	if dayEnabled(c, "p", time.Sunday) {
		t.Error("Sunday should be disabled after toggling off from every-day")
	}
}

func TestToggleDayNoSchedule(t *testing.T) {
	c := &config.Config{Providers: []config.Provider{{Name: "p", Command: []string{"x"}, WindowMinutes: 300}}}
	if err := toggleDay(c, "p", time.Monday); err == nil {
		t.Fatal("toggleDay with no schedule should error (add a reset first)")
	}
}

func TestProviderHelpersNilSafe(t *testing.T) {
	if providerScheduleIdx(nil, "p") != -1 {
		t.Error("providerScheduleIdx(nil) should be -1")
	}
	if providerResets(nil, "p") != nil {
		t.Error("providerResets(nil) should be nil")
	}
}

func TestAddResetDefaultDays(t *testing.T) {
	c := &config.Config{Providers: []config.Provider{{Name: "codex", Command: []string{"x"}, WindowMinutes: 300}}}
	addReset(c, "codex", "13:00")
	i := providerScheduleIdx(c, "codex")
	if i < 0 {
		t.Fatal("schedule not created")
	}
	if want := []string{"Mon", "Tue", "Wed", "Thu", "Fri"}; !reflect.DeepEqual(c.Schedules[i].Days, want) {
		t.Errorf("default days = %v, want %v", c.Schedules[i].Days, want)
	}
}

func TestRemoveResetUnknownProvider(t *testing.T) {
	c := baseCfg()
	before := len(c.Schedules)
	removeReset(c, "ghost", "10:00") // no-op, must not panic or alter
	if len(c.Schedules) != before {
		t.Error("removeReset on unknown provider should be a no-op")
	}
}

func TestChainResetsUnparseable(t *testing.T) {
	if got := chainResets("nonsense", 300); !reflect.DeepEqual(got, []string{"nonsense"}) {
		t.Errorf("chainResets(bad) = %v, want single passthrough", got)
	}
}

func TestMinutesOfDay(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"00:00", 0, true},
		{"05:30", 330, true},
		{"23:59", 1439, true},
		{"24:00", 0, false},
		{"noon", 0, false},
		{"12:60", 0, false},
	}
	for _, tc := range cases {
		got, ok := minutesOfDay(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("minutesOfDay(%q) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
