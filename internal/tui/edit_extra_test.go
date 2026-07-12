package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kyle/curfew/internal/config"
)

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

// testModel builds a Model in the bar editor for baseCfg's provider, saving to
// a temp config path.
func testModel(t *testing.T) Model {
	t.Helper()
	cfg := baseCfg()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return Model{
		mode:         modeEdit,
		cfg:          cfg,
		cfgPath:      path,
		editProvider: "claude-1",
		input:        textinput.New(),
	}
}

// press feeds one key through Model.Update.
func press(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return nm
}

func key(tp tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: tp} }
func rune_(r rune) tea.KeyMsg       { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestBarEditSessionSavesSchedule(t *testing.T) {
	m := testModel(t).enterBarEdit(-1) // new group, Mon-Fri, cursor 12:00

	// ←×2 = 10:00, add. ctrl+→ = 10:15... use: add 10:00, then move to 20:00 and add.
	m = press(t, m, key(tea.KeyLeft))
	m = press(t, m, key(tea.KeyLeft))
	m = press(t, m, key(tea.KeyEnter)) // add 10:00
	for i := 0; i < 10; i++ {
		m = press(t, m, key(tea.KeyRight))
	}
	m = press(t, m, key(tea.KeyCtrlRight)) // +15m -> 20:15
	m = press(t, m, key(tea.KeyEnter))     // add 20:15

	// Toggle Saturday on (days row, Mon-first index 5).
	m = press(t, m, key(tea.KeyUp)) // focus days
	for i := 0; i < 5; i++ {
		m = press(t, m, key(tea.KeyRight))
	}
	m = press(t, m, key(tea.KeyEnter)) // Sat on

	m = press(t, m, rune_('s')) // save
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit after save (flash: %q)", m.mode, m.flash)
	}

	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 2 {
		t.Fatalf("schedules = %+v, want the seeded one plus the new group", saved.Schedules)
	}
	got := saved.Schedules[1]
	if want := []string{"10:00", "20:15"}; len(got.ResetsAt) != 2 || got.ResetsAt[0] != want[0] || got.ResetsAt[1] != want[1] {
		t.Errorf("resets = %v, want %v", got.ResetsAt, want)
	}
	if want := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}; len(got.Days) != 6 || got.Days[5] != "Sat" {
		t.Errorf("days = %v, want %v", got.Days, want)
	}
}

func TestBarEditEscDiscards(t *testing.T) {
	m := testModel(t).enterBarEdit(-1)
	m = press(t, m, key(tea.KeyEnter)) // add 12:00
	m = press(t, m, key(tea.KeyEsc))
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit", m.mode)
	}
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 {
		t.Fatalf("esc must not persist: schedules = %+v", saved.Schedules)
	}
}

func TestBarEditEnterRemovesExisting(t *testing.T) {
	m := testModel(t).enterBarEdit(0) // seeded group: 15:00, cursor lands on it
	if m.barCursor != 15*60 {
		t.Fatalf("cursor = %d, want first time 15:00", m.barCursor)
	}
	m = press(t, m, key(tea.KeyEnter)) // toggle off
	if len(m.barTimes) != 0 {
		t.Fatalf("times = %v, want empty", m.barTimes)
	}
	// Saving an empty group is refused.
	m = press(t, m, rune_('s'))
	if m.mode != modeBarEdit {
		t.Fatal("empty group must not save")
	}
}

func TestBarEditRefusesZeroDays(t *testing.T) {
	m := testModel(t).enterBarEdit(-1)
	m = press(t, m, key(tea.KeyEnter)) // add a time so days are the failure
	m = press(t, m, key(tea.KeyUp))    // focus days
	for i := 0; i < 5; i++ {           // toggle Mon-Fri all off
		m = press(t, m, key(tea.KeyEnter))
		m = press(t, m, key(tea.KeyRight))
	}
	m = press(t, m, rune_('s'))
	if m.mode != modeBarEdit {
		t.Fatalf("zero days must not save (flash %q)", m.flash)
	}
}

func TestBarEditRefusesMidWindowReset(t *testing.T) {
	m := testModel(t).enterBarEdit(-1) // cursor 12:00, window 300m
	m = press(t, m, key(tea.KeyEnter)) // add 12:00
	m = press(t, m, key(tea.KeyRight)) // 13:00 — inside the 12:00 window
	m = press(t, m, key(tea.KeyEnter))
	if len(m.barTimes) != 1 {
		t.Fatalf("times = %v, want the mid-window add refused", m.barTimes)
	}
	if !strings.Contains(m.flash, "too close") {
		t.Errorf("flash = %q, want a too-close explanation", m.flash)
	}
	for i := 0; i < 4; i++ { // 17:00 — exactly one window later, allowed
		m = press(t, m, key(tea.KeyRight))
	}
	m = press(t, m, key(tea.KeyEnter))
	if len(m.barTimes) != 2 {
		t.Fatalf("times = %v, want 12:00 and 17:00", m.barTimes)
	}
	// Removal inside the window distance still works (same slot).
	m = press(t, m, key(tea.KeyEnter))
	if len(m.barTimes) != 1 {
		t.Fatalf("times = %v, want 17:00 removed", m.barTimes)
	}
}

func TestBackspaceBackspaceDeletesGroup(t *testing.T) {
	m := testModel(t)
	m.editSel = m.items().firstGroup // focus the seeded group row
	m = press(t, m, key(tea.KeyBackspace))
	if m.mode != modeConfirmRemoveGroup {
		t.Fatalf("mode = %v, want confirm after first backspace", m.mode)
	}
	m = press(t, m, key(tea.KeyBackspace))
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit after confirming", m.mode)
	}
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 0 {
		t.Fatalf("schedules = %+v, want the group deleted", saved.Schedules)
	}
}

func TestBackspaceEscCancels(t *testing.T) {
	m := testModel(t)
	m.editSel = m.items().firstGroup
	m = press(t, m, key(tea.KeyBackspace))
	m = press(t, m, key(tea.KeyEsc))
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit after cancel", m.mode)
	}
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 {
		t.Fatalf("schedules = %+v, want the group kept", saved.Schedules)
	}
}

func TestBarEditDoubleBackspaceDeletesGroup(t *testing.T) {
	m := testModel(t).enterBarEdit(0) // editing the seeded group
	m = press(t, m, key(tea.KeyBackspace))
	if m.mode != modeBarEdit || !m.barDeleteArmed {
		t.Fatalf("first backspace should arm deletion (mode %v armed %v)", m.mode, m.barDeleteArmed)
	}
	m = press(t, m, key(tea.KeyBackspace))
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit after delete", m.mode)
	}
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 0 {
		t.Fatalf("schedules = %+v, want the group deleted", saved.Schedules)
	}
}

func TestBarEditBackspaceDisarmsOnOtherKey(t *testing.T) {
	m := testModel(t).enterBarEdit(0)
	m = press(t, m, key(tea.KeyBackspace))
	m = press(t, m, key(tea.KeyLeft)) // any other key disarms
	if m.barDeleteArmed {
		t.Fatal("moving the cursor should disarm deletion")
	}
	m = press(t, m, key(tea.KeyBackspace)) // arms again, does not delete
	if m.mode != modeBarEdit {
		t.Fatalf("mode = %v, want still editing", m.mode)
	}
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 {
		t.Fatalf("schedules = %+v, want the group kept", saved.Schedules)
	}
}

func TestBarEditDoubleBackspaceOnNewGroupDiscards(t *testing.T) {
	m := testModel(t).enterBarEdit(-1) // unsaved group (add-provider flow)
	m = press(t, m, key(tea.KeyEnter)) // add a time so there's something to discard
	m = press(t, m, key(tea.KeyBackspace))
	m = press(t, m, key(tea.KeyBackspace))
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit after discard", m.mode)
	}
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 {
		t.Fatalf("schedules = %+v, want only the seeded group (nothing persisted)", saved.Schedules)
	}
}

func TestSaveRefusesTooCloseTimes(t *testing.T) {
	m := testModel(t).enterBarEdit(-1)
	m.barTimes = []int{10 * 60, 12 * 60} // hand-edited config scenario
	m = press(t, m, rune_('s'))
	if m.mode != modeBarEdit {
		t.Fatalf("too-close times must not save (flash %q)", m.flash)
	}
	if !strings.Contains(m.flash, "closer than") {
		t.Errorf("flash = %q, want a spacing explanation", m.flash)
	}
}

func TestDashboardToggleAutoPrime(t *testing.T) {
	m := testModel(t)
	m.mode = modeDashboard
	m = press(t, m, rune_('a'))
	saved, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.General.AutoPrimeEnabled() {
		t.Fatal("first toggle should turn auto-prime off")
	}
	m = press(t, m, rune_('a'))
	saved, err = config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.General.AutoPrimeEnabled() {
		t.Fatal("second toggle should turn auto-prime back on")
	}
}

func TestCtrlAndFallbackSteps(t *testing.T) {
	m := testModel(t).enterBarEdit(-1) // cursor 12:00
	m = press(t, m, key(tea.KeyCtrlLeft))
	if m.barCursor != 11*60+45 {
		t.Errorf("ctrl+left: cursor = %d, want 11:45", m.barCursor)
	}
	m = press(t, m, key(tea.KeyShiftLeft))
	if m.barCursor != 11*60+30 {
		t.Errorf("shift+left: cursor = %d, want 11:30", m.barCursor)
	}
	m = press(t, m, rune_('L'))
	if m.barCursor != 11*60+45 {
		t.Errorf("L: cursor = %d, want 11:45", m.barCursor)
	}
	m = press(t, m, rune_('H'))
	if m.barCursor != 11*60+30 {
		t.Errorf("H: cursor = %d, want 11:30", m.barCursor)
	}
}
