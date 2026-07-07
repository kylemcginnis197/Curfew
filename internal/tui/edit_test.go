package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
)

func TestParseFlexibleTime(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"8pm", "20:00", true},
		{"8:00pm", "20:00", true},
		{"8:30 pm", "20:30", true},
		{"12pm", "12:00", true},
		{"12am", "00:00", true},
		{"12:15am", "00:15", true},
		{"20:00", "20:00", true},
		{"9", "09:00", true},
		{"9:30", "09:30", true},
		{"00:00", "00:00", true},
		{"23:59", "23:59", true},
		{"  7 AM ", "07:00", true},
		{"25:00", "", false},
		{"20:99", "", false},
		{"13pm", "", false},
		{"0pm", "", false},
		{"bogus", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := parseFlexibleTime(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseFlexibleTime(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestChainResets(t *testing.T) {
	cases := []struct {
		in   string
		win  int
		want []string
	}{
		{"20:00", 300, []string{"10:00", "15:00", "20:00"}},
		{"21:00", 300, []string{"11:00", "16:00", "21:00"}},
		{"09:00", 300, []string{"09:00"}}, // nothing earlier to backfill
		{"10:00", 300, []string{"10:00"}}, // anchor 05:00; earlier would be <05:00
	}
	for _, tc := range cases {
		got := chainResets(tc.in, tc.win)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("chainResets(%q,%d) = %v, want %v", tc.in, tc.win, got, tc.want)
		}
	}
}

func baseCfg() *config.Config {
	return &config.Config{
		Providers: []config.Provider{{Name: "claude-1", Command: "x", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "claude-1", ResetsAt: []string{"15:00"}, Days: []string{"Mon", "Tue"}}},
	}
}

func TestAddResetDedupeSort(t *testing.T) {
	c := baseCfg()
	addReset(c, "claude-1", "20:00")
	addReset(c, "claude-1", "10:00")
	addReset(c, "claude-1", "15:00") // dupe, ignored
	got := providerResets(c, "claude-1")
	want := []string{"10:00", "15:00", "20:00"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resets = %v, want %v", got, want)
	}
}

func TestAddResetCreatesSchedule(t *testing.T) {
	c := &config.Config{Providers: []config.Provider{{Name: "codex", Command: "x", WindowMinutes: 300}}}
	addReset(c, "codex", "13:00")
	if got := providerResets(c, "codex"); !reflect.DeepEqual(got, []string{"13:00"}) {
		t.Fatalf("resets = %v, want [13:00]", got)
	}
}

func TestRemoveResetDropsEmptySchedule(t *testing.T) {
	c := baseCfg()
	removeReset(c, "claude-1", "15:00")
	if idx := providerScheduleIdx(c, "claude-1"); idx != -1 {
		t.Fatalf("schedule should be dropped when last reset removed, idx=%d", idx)
	}
	// And the resulting config must still validate.
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid after drop: %v", err)
	}
}

func TestRemoveResetKeepsOthers(t *testing.T) {
	c := baseCfg()
	addReset(c, "claude-1", "20:00")
	removeReset(c, "claude-1", "15:00")
	if got := providerResets(c, "claude-1"); !reflect.DeepEqual(got, []string{"20:00"}) {
		t.Fatalf("resets = %v, want [20:00]", got)
	}
}

func TestToggleDay(t *testing.T) {
	c := baseCfg() // Days = [Mon, Tue]
	// Turn on Wednesday.
	if err := toggleDay(c, "claude-1", time.Wednesday); err != nil {
		t.Fatal(err)
	}
	if !dayEnabled(c, "claude-1", time.Wednesday) {
		t.Fatal("Wednesday should be enabled")
	}
	// Turn off Mon and Tue and Wed -> only-Wed then removing Wed should fail (last day).
	toggleDay(c, "claude-1", time.Monday)
	toggleDay(c, "claude-1", time.Tuesday)
	if err := toggleDay(c, "claude-1", time.Wednesday); err == nil {
		t.Fatal("removing the last day should error")
	}
	if !dayEnabled(c, "claude-1", time.Wednesday) {
		t.Fatal("Wednesday should remain enabled after refused removal")
	}
}

func TestToggleDayAllSevenStoresEmpty(t *testing.T) {
	c := baseCfg() // [Mon, Tue]
	for _, d := range []time.Weekday{time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday} {
		if err := toggleDay(c, "claude-1", d); err != nil {
			t.Fatal(err)
		}
	}
	i := providerScheduleIdx(c, "claude-1")
	if len(c.Schedules[i].Days) != 0 {
		t.Fatalf("all-seven should store empty Days (canonical every-day), got %v", c.Schedules[i].Days)
	}
}
