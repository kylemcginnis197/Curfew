package tui

import (
	"reflect"
	"testing"

	"github.com/kyle/curfew/internal/config"
)

func baseCfg() *config.Config {
	return &config.Config{
		Providers: []config.Provider{{Name: "claude-1", Command: "x", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "claude-1", ResetsAt: []string{"15:00"}, Days: []string{"Mon", "Tue"}}},
	}
}

func TestToggleTimeAtSlotSemantics(t *testing.T) {
	times, removed := toggleTimeAt(nil, 13*60+45)
	if removed || !reflect.DeepEqual(times, []int{13*60 + 45}) {
		t.Fatalf("add: times=%v removed=%v", times, removed)
	}
	// Same 15-minute slot -> removes.
	times, removed = toggleTimeAt(times, 13*60+45)
	if !removed || len(times) != 0 {
		t.Fatalf("remove: times=%v removed=%v", times, removed)
	}
	// A different slot (13:30) does not remove 13:45.
	times, _ = toggleTimeAt(nil, 13*60+45)
	times, removed = toggleTimeAt(times, 13*60+30)
	if removed || !reflect.DeepEqual(times, []int{13*60 + 30, 13*60 + 45}) {
		t.Fatalf("adjacent slot: times=%v removed=%v", times, removed)
	}
}

func TestToggleTimeAtKeepsSorted(t *testing.T) {
	var times []int
	for _, v := range []int{20 * 60, 8 * 60, 12 * 60} {
		times, _ = toggleTimeAt(times, v)
	}
	if !reflect.DeepEqual(times, []int{8 * 60, 12 * 60, 20 * 60}) {
		t.Fatalf("times = %v, want sorted", times)
	}
}

func TestGroupToWorkingRoundTrip(t *testing.T) {
	s := config.Schedule{Provider: "p", ResetsAt: []string{"20:00", "08:30"}, Days: []string{"Mon", "Fri"}}
	times, days := groupToWorking(s)
	if !reflect.DeepEqual(times, []int{8*60 + 30, 20 * 60}) {
		t.Fatalf("times = %v", times)
	}
	if days != [7]bool{true, false, false, false, true, false, false} {
		t.Fatalf("days = %v", days)
	}
	back := workingToSchedule("p", times, days)
	if !reflect.DeepEqual(back.ResetsAt, []string{"08:30", "20:00"}) {
		t.Fatalf("resets = %v", back.ResetsAt)
	}
	if !reflect.DeepEqual(back.Days, []string{"Mon", "Fri"}) {
		t.Fatalf("days = %v", back.Days)
	}
}

func TestGroupToWorkingEveryDay(t *testing.T) {
	_, days := groupToWorking(config.Schedule{ResetsAt: []string{"10:00"}}) // no Days
	for i, on := range days {
		if !on {
			t.Fatalf("day %d should be on for an every-day schedule", i)
		}
	}
}

func TestWorkingToScheduleAllSevenStoresEmpty(t *testing.T) {
	s := workingToSchedule("p", []int{10 * 60}, [7]bool{true, true, true, true, true, true, true})
	if len(s.Days) != 0 {
		t.Fatalf("all-seven should store empty Days (canonical every-day), got %v", s.Days)
	}
}

func TestProviderGroups(t *testing.T) {
	c := &config.Config{
		Providers: []config.Provider{
			{Name: "a", Command: "x", WindowMinutes: 300},
			{Name: "b", Command: "x", WindowMinutes: 300},
		},
		Schedules: []config.Schedule{
			{Provider: "a", ResetsAt: []string{"10:00"}},
			{Provider: "b", ResetsAt: []string{"11:00"}},
			{Provider: "a", ResetsAt: []string{"20:00"}, Days: []string{"Sat", "Sun"}},
		},
	}
	if got := providerGroups(c, "a"); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("groups(a) = %v, want [0 2]", got)
	}
	if got := providerGroups(c, "ghost"); got != nil {
		t.Fatalf("groups(ghost) = %v, want nil", got)
	}
	if got := providerGroups(nil, "a"); got != nil {
		t.Fatalf("groups(nil cfg) = %v, want nil", got)
	}
}

func TestSetGroupReplaceAndAppend(t *testing.T) {
	c := baseCfg()
	setGroup(c, 0, config.Schedule{Provider: "claude-1", ResetsAt: []string{"09:00"}})
	if !reflect.DeepEqual(c.Schedules[0].ResetsAt, []string{"09:00"}) {
		t.Fatalf("replace failed: %v", c.Schedules[0])
	}
	setGroup(c, -1, config.Schedule{Provider: "claude-1", ResetsAt: []string{"21:00"}, Days: []string{"Sat"}})
	if len(c.Schedules) != 2 || c.Schedules[1].ResetsAt[0] != "21:00" {
		t.Fatalf("append failed: %v", c.Schedules)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
}

func TestDeleteGroup(t *testing.T) {
	c := baseCfg()
	deleteGroup(c, 0)
	if len(c.Schedules) != 0 {
		t.Fatalf("schedules = %v, want empty", c.Schedules)
	}
	// A provider without schedules must still validate.
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid after delete: %v", err)
	}
	deleteGroup(c, 0)   // out of range, no panic
	deleteGroup(nil, 0) // nil cfg, no panic
}

func TestConflictingTime(t *testing.T) {
	times := []int{10 * 60, 20 * 60}
	cases := []struct {
		t         int
		wantClash bool
		wantWith  int
	}{
		{12 * 60, true, 10 * 60}, // 2h after 10:00, inside its 5h window
		{8 * 60, true, 10 * 60},  // 2h before 10:00
		{15 * 60, false, 0},      // exactly 5h from both — allowed
		{15*60 + 15, true, 20 * 60},
		{3 * 60, false, 0},
	}
	for _, tc := range cases {
		got, clash := conflictingTime(times, tc.t, 300)
		if clash != tc.wantClash || (clash && got != tc.wantWith) {
			t.Errorf("conflictingTime(%d) = (%d,%v), want (%d,%v)", tc.t, got, clash, tc.wantWith, tc.wantClash)
		}
	}
}

func TestClampMinute(t *testing.T) {
	cases := []struct{ in, want int }{
		{-15, 0},
		{0, 0},
		{720, 720},
		{24 * 60, 24*60 - 15},
	}
	for _, tc := range cases {
		if got := clampMinute(tc.in); got != tc.want {
			t.Errorf("clampMinute(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
