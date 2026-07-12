package tui

import (
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
)

// tlCfg builds a one-provider config with the given window and schedules.
func tlCfg(windowMin int, schedules ...config.Schedule) *config.Config {
	for i := range schedules {
		schedules[i].Provider = "p"
	}
	return &config.Config{
		Providers: []config.Provider{{Name: "p", Command: "x", WindowMinutes: windowMin}},
		Schedules: schedules,
	}
}

// monday is an arbitrary fixed Monday for weekday-sensitive tests.
var monday = time.Date(2026, 7, 6, 0, 0, 0, 0, time.Local)

func TestDayMinutesMidDayWindow(t *testing.T) {
	// Reset 15:00, 300m window -> active 10:00-15:00, tick at 15:00.
	cfg := tlCfg(300, config.Schedule{ResetsAt: []string{"15:00"}})
	mins := dayMinutes(cfg, "p", monday)
	cases := []struct {
		min  int
		want cellState
	}{
		{9*60 + 59, cellOff},
		{10 * 60, cellActive},
		{14*60 + 59, cellActive},
		{15 * 60, cellTick},
		{15*60 + 1, cellOff},
	}
	for _, tc := range cases {
		if got := mins[tc.min]; got != tc.want {
			t.Errorf("minute %d = %v, want %v", tc.min, got, tc.want)
		}
	}
}

func TestDayMinutesOvernightWindow(t *testing.T) {
	// Reset Tue 02:00, 300m window -> Mon 21:00-24:00 active on Monday's bar;
	// Tue 00:00-02:00 active + tick at 02:00 on Tuesday's bar.
	cfg := tlCfg(300, config.Schedule{ResetsAt: []string{"02:00"}, Days: []string{"Tue"}})

	mon := dayMinutes(cfg, "p", monday)
	if mon[20*60+59] != cellOff || mon[21*60] != cellActive || mon[23*60+59] != cellActive {
		t.Errorf("monday tail: 20:59=%v 21:00=%v 23:59=%v, want off/active/active",
			mon[20*60+59], mon[21*60], mon[23*60+59])
	}
	if mon[2*60] != cellOff {
		t.Errorf("monday 02:00 = %v, want off (reset is Tuesday's)", mon[2*60])
	}

	tue := dayMinutes(cfg, "p", monday.AddDate(0, 0, 1))
	if tue[0] != cellActive || tue[2*60-1] != cellActive || tue[2*60] != cellTick {
		t.Errorf("tuesday head: 00:00=%v 01:59=%v 02:00=%v, want active/active/tick",
			tue[0], tue[2*60-1], tue[2*60])
	}
	if tue[2*60+1] != cellOff {
		t.Errorf("tuesday 02:01 = %v, want off", tue[2*60+1])
	}
}

func TestDayMinutesDayFiltered(t *testing.T) {
	// A Mon-only schedule leaves Wednesday's bar empty.
	cfg := tlCfg(300, config.Schedule{ResetsAt: []string{"12:00"}, Days: []string{"Mon"}})
	wed := dayMinutes(cfg, "p", monday.AddDate(0, 0, 2))
	for i, st := range wed {
		if st != cellOff {
			t.Fatalf("wednesday minute %d = %v, want all off", i, st)
		}
	}
}

func TestDayMinutesMultipleGroups(t *testing.T) {
	// Two groups on Monday: 10:00 (Mon-only) and 20:00 (all days).
	cfg := tlCfg(300,
		config.Schedule{ResetsAt: []string{"10:00"}, Days: []string{"Mon"}},
		config.Schedule{ResetsAt: []string{"20:00"}},
	)
	mins := dayMinutes(cfg, "p", monday)
	if mins[10*60] != cellTick || mins[20*60] != cellTick {
		t.Errorf("expected ticks at both resets, got 10:00=%v 20:00=%v", mins[10*60], mins[20*60])
	}
	if mins[6*60] != cellActive || mins[16*60] != cellActive {
		t.Errorf("expected both windows active, got 06:00=%v 16:00=%v", mins[6*60], mins[16*60])
	}
}

func TestDayMinutesMidnightReset(t *testing.T) {
	// Reset 00:00 -> tick at minute 0; its window belongs to the previous day.
	cfg := tlCfg(300, config.Schedule{ResetsAt: []string{"00:00"}})
	mins := dayMinutes(cfg, "p", monday)
	if mins[0] != cellTick {
		t.Errorf("minute 0 = %v, want tick", mins[0])
	}
	// The 00:00 reset of the *next* day paints 19:00-24:00 today.
	if mins[19*60] != cellActive || mins[23*60+59] != cellActive {
		t.Errorf("tail = %v/%v, want active (next day's 00:00 reset)", mins[19*60], mins[23*60+59])
	}
}

func TestDayMinutesEmpty(t *testing.T) {
	mins := dayMinutes(tlCfg(300), "p", monday)
	for i, st := range mins {
		if st != cellOff {
			t.Fatalf("minute %d = %v, want off", i, st)
		}
	}
	// nil config must not panic.
	_ = dayMinutes(nil, "p", monday)
}

func TestToCellsTickWins(t *testing.T) {
	var mins [24 * 60]cellState
	mins[10] = cellActive
	mins[20] = cellTick // same first cell (0-29)
	cells := toCells(mins)
	if cells[0] != cellTick {
		t.Errorf("cell 0 = %v, want tick over active", cells[0])
	}
	if cells[1] != cellOff {
		t.Errorf("cell 1 = %v, want off", cells[1])
	}
}

func TestGroupCellsWraps(t *testing.T) {
	// Reset 02:00 with a 300m window wraps: tail of the bar (21:00+) active.
	cells := groupCells([]int{2 * 60}, 300)
	if cells[2*60/minutesPerCell] != cellTick {
		t.Errorf("expected tick at 02:00 cell")
	}
	if cells[0] != cellActive || cells[barCells-1] != cellActive {
		t.Errorf("expected wrap-around coverage, got head=%v tail=%v", cells[0], cells[barCells-1])
	}
	if cells[12*60/minutesPerCell] != cellOff {
		t.Errorf("expected midday off")
	}
}

func TestRenderBarScale(t *testing.T) {
	s := renderBarScale()
	if len(s) != barCells {
		t.Fatalf("scale width = %d, want %d", len(s), barCells)
	}
	if s[:4] != "0:00" || s[barCells-5:] != "24:00" {
		t.Errorf("scale ends wrong: %q", s)
	}
}
