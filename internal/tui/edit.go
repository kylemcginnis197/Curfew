package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kyle/curfew/internal/config"
)

// mode selects which screen/keymap is active.
type mode int

const (
	modeDashboard mode = iota
	modeEdit
	modeBarEdit
	modeEditCommand
	modeConfirmRemoveGroup
	modeConfirmRemoveProvider
	modeAddName
	modeAddCommand
)

// weekdayOrder is the Mon-first display order for the weekday toggle row.
var weekdayOrder = []time.Weekday{
	time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
	time.Friday, time.Saturday, time.Sunday,
}

var weekdayToken = map[time.Weekday]string{
	time.Monday: "Mon", time.Tuesday: "Tue", time.Wednesday: "Wed",
	time.Thursday: "Thu", time.Friday: "Fri", time.Saturday: "Sat", time.Sunday: "Sun",
}

// --- transitions ---

// enterEdit switches to the schedule editor for a provider, reloading config
// from disk so any external hand-edits are reflected.
func (m Model) enterEdit(provider string) Model {
	if cfg, err := config.Load(m.cfgPath); err == nil {
		m.cfg = cfg
	}
	m.mode = modeEdit
	m.editProvider = provider
	m.flash = ""
	// Start focus on the first group if any, else the "Add" item — never on
	// "Fire now", so an immediate Enter can't fire by accident.
	it := m.items()
	if it.groupCount > 0 {
		m.editSel = it.firstGroup
	} else {
		m.editSel = it.addIdx
	}
	return m
}

// enterBarEdit opens the bar editor on a working copy of one day group.
// schedIdx is an index into cfg.Schedules, or -1 to create a new group
// (prefilled Mon-Fri).
func (m Model) enterBarEdit(schedIdx int) Model {
	m.mode = modeBarEdit
	m.barGroup = schedIdx
	m.barFocus = 0
	m.barDaySel = 0
	m.barDeleteArmed = false
	m.flash = ""
	if schedIdx >= 0 && m.cfg != nil && schedIdx < len(m.cfg.Schedules) {
		m.barTimes, m.barDays = groupToWorking(m.cfg.Schedules[schedIdx])
	} else {
		m.barGroup = -1
		m.barTimes = nil
		m.barDays = [7]bool{true, true, true, true, true, false, false} // Mon-Fri
	}
	if len(m.barTimes) > 0 {
		m.barCursor = m.barTimes[0]
	} else {
		m.barCursor = 12 * 60
	}
	return m
}

// editItems describes the layout of the editor's flat, arrow-navigable list:
// [Fire now] · [Command] · group rows · [Add reset times] · [Remove provider].
type editItems struct {
	fireIdx    int
	cmdIdx     int
	firstGroup int
	groupCount int
	addIdx     int
	removeIdx  int
	count      int
}

func (m Model) items() editItems {
	g := len(providerGroups(m.cfg, m.editProvider))
	it := editItems{fireIdx: 0, cmdIdx: 1, firstGroup: 2, groupCount: g}
	it.addIdx = 2 + g
	it.removeIdx = it.addIdx + 1
	it.count = it.removeIdx + 1
	return it
}

// --- key handling ---

// updateEdit drives the flat item list with arrows + Enter + Esc. On a group
// row, x/d asks to delete the group.
func (m Model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	it := m.items()
	switch msg.String() {
	case "esc", "q":
		m.mode = modeDashboard
		m.flash = ""
		return m, fetch() // refresh dashboard anchors immediately
	case "up":
		if m.editSel > 0 {
			m.editSel--
		}
	case "down":
		if m.editSel < it.count-1 {
			m.editSel++
		}
	case "x", "d", "backspace":
		if i := m.focusedGroup(it); i >= 0 {
			m.barGroup = i
			m.mode = modeConfirmRemoveGroup
		}
	case "enter":
		return m.activate(it)
	}
	return m, nil
}

// focusedGroup returns the cfg.Schedules index of the focused group row, or -1.
func (m Model) focusedGroup(it editItems) int {
	groups := providerGroups(m.cfg, m.editProvider)
	if i := m.editSel - it.firstGroup; i >= 0 && i < len(groups) {
		return groups[i]
	}
	return -1
}

// activate performs the action for the currently focused item.
func (m Model) activate(it editItems) (tea.Model, tea.Cmd) {
	switch {
	case m.editSel == it.fireIdx:
		m.flash = "anchoring " + m.editProvider + "…"
		return m, fireCmd(m.editProvider)
	case m.editSel == it.cmdIdx:
		m.mode = modeEditCommand
		m.input.Prompt = "command: "
		m.input.Placeholder = "claude -p 'curfew: anchor'"
		m.input.Width = 48
		m.input.CharLimit = 256
		m.input.SetValue(providerCommand(m.cfg, m.editProvider))
		m.input.CursorEnd()
		m.input.Focus()
		m.flash = ""
		return m, nil
	case m.editSel == it.addIdx:
		return m.enterBarEdit(-1), nil
	case m.editSel == it.removeIdx:
		m.mode = modeConfirmRemoveProvider
		return m, nil
	default: // a group row is focused → open the bar editor on it
		if i := m.focusedGroup(it); i >= 0 {
			return m.enterBarEdit(i), nil
		}
		return m, nil
	}
}

// updateBarEdit drives the bar editor: a time cursor over a 0:00→24:00 bar
// plus a weekday toggle row. Every change autosaves; Backspace twice removes
// the reset at the cursor.
func (m Model) updateBarEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	onDays := m.barFocus == 1
	if msg.String() != "backspace" && m.barDeleteArmed {
		m.barDeleteArmed = false
		m.flash = ""
	}
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		m.flash = ""
		return m, nil
	case "backspace":
		if onDays {
			return m, nil
		}
		i, hit := timeInSlot(m.barTimes, m.barCursor)
		if !hit {
			m.barDeleteArmed = false
			m.flash = "no reset at " + hhmm(m.barCursor)
			return m, nil
		}
		if !m.barDeleteArmed {
			m.barDeleteArmed = true
			m.flash = "backspace again to remove " + hhmm(m.barTimes[i])
			return m, nil
		}
		m.barDeleteArmed = false
		removed := m.barTimes[i]
		m.barTimes = append(m.barTimes[:i:i], m.barTimes[i+1:]...)
		return m.autosaveBar("removed " + hhmm(removed))
	case "up", "down":
		m.barFocus = 1 - m.barFocus
	case "left":
		if onDays {
			if m.barDaySel > 0 {
				m.barDaySel--
			}
		} else {
			m.barCursor = clampMinute(m.barCursor - 60)
		}
	case "right":
		if onDays {
			if m.barDaySel < len(weekdayOrder)-1 {
				m.barDaySel++
			}
		} else {
			m.barCursor = clampMinute(m.barCursor + 60)
		}
	// ctrl+arrows are the documented fine step; shift+arrows and H/L cover
	// terminals that swallow ctrl+arrow sequences.
	case "ctrl+left", "shift+left", "H":
		if !onDays {
			m.barCursor = clampMinute(m.barCursor - 15)
		}
	case "ctrl+right", "shift+right", "L":
		if !onDays {
			m.barCursor = clampMinute(m.barCursor + 15)
		}
	case "enter", " ":
		if onDays {
			// Refuse turning the last day off: a saved schedule needs one.
			if m.barDays[m.barDaySel] && countDays(m.barDays) == 1 {
				m.flash = "keep at least one day"
				return m, nil
			}
			m.barDays[m.barDaySel] = !m.barDays[m.barDaySel]
			return m.autosaveBar("days updated")
		}
		if _, hit := timeInSlot(m.barTimes, m.barCursor); hit {
			m.barTimes, _ = toggleTimeAt(m.barTimes, m.barCursor)
			return m.autosaveBar("removed " + hhmm(m.barCursor))
		}
		// A reset closer than the window length to a neighbor would land
		// mid-window: its anchor fires while the previous window is still
		// open and gets skipped, so the "reset" never happens.
		win := providerWindowMinutes(m.cfg, m.editProvider)
		if c, clash := conflictingTime(m.barTimes, m.barCursor, win); clash {
			m.flash = fmt.Sprintf("too close to %s — resets must be %s apart (the window length)",
				hhmm(c), short(time.Duration(win)*time.Minute))
			return m, nil
		}
		m.barTimes, _ = toggleTimeAt(m.barTimes, m.barCursor)
		return m.autosaveBar("added " + hhmm(m.barCursor))
	}
	return m, nil
}

// autosaveBar persists the working copy after every bar-editor change, staying
// in the editor. An empty group is removed from the config (or simply not
// created yet); the first added time creates the schedule entry.
func (m Model) autosaveBar(flash string) (tea.Model, tea.Cmd) {
	if m.cfg == nil {
		m.flash = "no config loaded"
		return m, nil
	}
	if len(m.barTimes) == 0 {
		if m.barGroup < 0 {
			m.flash = flash // nothing persisted yet
			return m, nil
		}
		deleteGroup(m.cfg, m.barGroup)
		m.barGroup = -1
	} else {
		sched := workingToSchedule(m.editProvider, m.barTimes, m.barDays)
		setGroup(m.cfg, m.barGroup, sched)
		if m.barGroup < 0 {
			m.barGroup = len(m.cfg.Schedules) - 1
		}
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.flash = "save failed: " + err.Error()
		if cfg, err := config.Load(m.cfgPath); err == nil {
			m.cfg = cfg // discard the invalid change
		}
		return m, nil
	}
	m.flash = flash
	return m, fetch()
}

// countDays reports how many weekdays are selected.
func countDays(days [7]bool) int {
	n := 0
	for _, on := range days {
		if on {
			n++
		}
	}
	return n
}

// updateConfirmRemoveGroup deletes the pending group on Enter or a second
// Backspace, cancels on Esc.
func (m Model) updateConfirmRemoveGroup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "backspace":
		deleteGroup(m.cfg, m.barGroup)
		return m.persist("removed schedule")
	case "esc":
		m.mode = modeEdit
		return m, nil
	}
	return m, nil
}

// persist saves the edited config (the daemon hot-reloads it) and returns to the
// edit screen. On a validation error it reloads from disk to discard the bad
// edit. It clamps the item cursor after the list changes size.
func (m Model) persist(flash string) (tea.Model, tea.Cmd) {
	m.mode = modeEdit
	if m.cfg == nil {
		m.flash = "no config loaded"
		return m, nil
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.flash = "save failed: " + err.Error()
		if cfg, err := config.Load(m.cfgPath); err == nil {
			m.cfg = cfg // discard the invalid change
		}
		return m, nil
	}
	if it := m.items(); m.editSel >= it.count {
		m.editSel = it.count - 1
	}
	m.flash = flash
	return m, fetch()
}

// --- pure helpers (unit-tested) ---

// providerGroups returns the cfg.Schedules indices belonging to a provider,
// in config order. Each schedule entry is one editable day group.
func providerGroups(cfg *config.Config, provider string) []int {
	if cfg == nil {
		return nil
	}
	var out []int
	for i := range cfg.Schedules {
		if cfg.Schedules[i].Provider == provider {
			out = append(out, i)
		}
	}
	return out
}

// groupToWorking converts a schedule into the bar editor's working copy:
// reset minutes plus a Mon-first day mask.
func groupToWorking(s config.Schedule) ([]int, [7]bool) {
	var times []int
	for _, r := range s.ResetsAt {
		if v, ok := minutesOfDay(r); ok {
			times = append(times, v)
		}
	}
	sort.Ints(times)
	var days [7]bool
	on := map[time.Weekday]bool{}
	for _, d := range s.Weekdays() { // empty Days => all seven
		on[d] = true
	}
	for i, d := range weekdayOrder {
		days[i] = on[d]
	}
	return times, days
}

// workingToSchedule builds a schedule from the working copy, sorting times and
// storing all-seven-days as an empty Days list (the config's canonical form).
func workingToSchedule(provider string, times []int, days [7]bool) config.Schedule {
	sorted := append([]int(nil), times...)
	sort.Ints(sorted)
	resets := make([]string, len(sorted))
	for i, v := range sorted {
		resets[i] = hhmm(v)
	}
	var tokens []string
	for i, d := range weekdayOrder {
		if days[i] {
			tokens = append(tokens, weekdayToken[d])
		}
	}
	if len(tokens) == 7 {
		tokens = nil
	}
	return config.Schedule{Provider: provider, ResetsAt: resets, Days: tokens}
}

// setGroup replaces the schedule at schedIdx, or appends when schedIdx is -1.
func setGroup(cfg *config.Config, schedIdx int, s config.Schedule) {
	if schedIdx >= 0 && schedIdx < len(cfg.Schedules) {
		cfg.Schedules[schedIdx] = s
		return
	}
	cfg.Schedules = append(cfg.Schedules, s)
}

// deleteGroup removes the schedule at schedIdx.
func deleteGroup(cfg *config.Config, schedIdx int) {
	if cfg == nil || schedIdx < 0 || schedIdx >= len(cfg.Schedules) {
		return
	}
	cfg.Schedules = append(cfg.Schedules[:schedIdx], cfg.Schedules[schedIdx+1:]...)
}

// toggleTimeAt removes the first time sharing the cursor's 15-minute slot, or
// inserts the cursor minute (kept sorted) when the slot is empty. The bool
// reports whether a time was removed.
func toggleTimeAt(times []int, cursor int) ([]int, bool) {
	if i, hit := timeInSlot(times, cursor); hit {
		return append(times[:i:i], times[i+1:]...), true
	}
	out := append(append([]int(nil), times...), cursor)
	sort.Ints(out)
	return out, false
}

// timeInSlot returns the index of the time sharing the cursor's 15-minute
// slot, if any.
func timeInSlot(times []int, cursor int) (int, bool) {
	for i, t := range times {
		if t/15 == cursor/15 {
			return i, true
		}
	}
	return -1, false
}

// conflictingTime returns an existing time strictly closer than windowMin to
// t. Two such resets can't both work: the later one's anchor fires inside the
// earlier one's window and is skipped.
func conflictingTime(times []int, t, windowMin int) (int, bool) {
	for _, e := range times {
		d := t - e
		if d < 0 {
			d = -d
		}
		if d < windowMin {
			return e, true
		}
	}
	return 0, false
}

// clampMinute keeps a bar cursor within 0:00–23:45.
func clampMinute(v int) int {
	if v < 0 {
		return 0
	}
	if v > 24*60-15 {
		return 24*60 - 15
	}
	return v
}

// hhmm renders a minute-of-day as "HH:MM".
func hhmm(v int) string {
	return fmt.Sprintf("%02d:%02d", v/60, v%60)
}

// groupDaysLabel summarizes a schedule's days for group rows.
func groupDaysLabel(s config.Schedule) string {
	if len(s.Days) == 0 {
		return "every day"
	}
	on := map[time.Weekday]bool{}
	for _, d := range s.Weekdays() {
		on[d] = true
	}
	var tokens []string
	for _, d := range weekdayOrder {
		if on[d] {
			tokens = append(tokens, weekdayToken[d])
		}
	}
	return strings.Join(tokens, " ")
}

func minutesOfDay(hhmm string) (int, bool) {
	i := strings.IndexByte(hhmm, ':')
	if i < 0 {
		return 0, false
	}
	h, err1 := strconv.Atoi(hhmm[:i])
	mn, err2 := strconv.Atoi(hhmm[i+1:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || mn < 0 || mn > 59 {
		return 0, false
	}
	return h*60 + mn, true
}

// --- views ---

func (m Model) viewEdit() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("CURFEW") + dimStyle.Render(" · edit · ") + brightStyle.Render(m.editProvider) + "\n\n")

	if m.cfg == nil {
		b.WriteString("  " + warnStyle.Render("no config loaded") + "\n")
		b.WriteString(faintStyle.Render("\n  esc back"))
		return b.String()
	}

	it := m.items()
	win := providerWindowMinutes(m.cfg, m.editProvider)
	groups := providerGroups(m.cfg, m.editProvider)

	// item highlights a focused row as a subtle full-width bar.
	item := func(idx int, label string) string {
		if m.editSel == idx {
			return selStyle.Render(padTo("  "+label, rowWidth))
		}
		return "  " + label
	}

	// fire now
	if m.editSel == it.fireIdx {
		b.WriteString(item(it.fireIdx, "fire now") + "\n")
	} else {
		b.WriteString("  " + midStyle.Render("fire now") + faintStyle.Render("   anchor now") + "\n")
	}

	// command (the shell command run to anchor this provider's limit)
	cmd := providerCommand(m.cfg, m.editProvider)
	if m.editSel == it.cmdIdx {
		b.WriteString(item(it.cmdIdx, "command  "+clip(cmd, 33)) + "\n\n")
	} else {
		b.WriteString("  " + midStyle.Render("command") + faintStyle.Render("   "+clip(cmd, 32)) + "\n\n")
	}

	// current reset times, one bar per day group
	b.WriteString(dimStyle.Render("  current reset times") + "\n")
	if len(groups) == 0 {
		b.WriteString("  " + faintStyle.Render("none yet") + "\n")
	}
	for gi, si := range groups {
		s := m.cfg.Schedules[si]
		times, _ := groupToWorking(s)
		labels := make([]string, len(times))
		for i, t := range times {
			labels[i] = hhmm(t)
		}
		if m.editSel == it.firstGroup+gi {
			label := groupDaysLabel(s) + " · " + strings.Join(labels, " ")
			b.WriteString(item(it.firstGroup+gi, clip(label, rowWidth-2)) + "\n")
		} else {
			b.WriteString("  " + midStyle.Render(groupDaysLabel(s)) +
				dimStyle.Render(" · "+strings.Join(labels, " ")) + "\n")
		}
		b.WriteString("  " + renderBar(groupCells(times, win), -1, -1) + "\n")
	}

	// add group
	b.WriteString("\n")
	if m.editSel == it.addIdx {
		b.WriteString(item(it.addIdx, "+ add reset times") + "\n")
	} else {
		b.WriteString("  " + accentStyle.Render("+") + dimStyle.Render(" add reset times") + "\n")
	}

	// remove provider
	b.WriteString("\n")
	if m.editSel == it.removeIdx {
		b.WriteString(item(it.removeIdx, "× remove provider") + "\n")
	} else {
		b.WriteString("  " + dimStyle.Render("× remove provider") + "\n")
	}

	// Mode-specific prompt + contextual footer.
	switch m.mode {
	case modeEditCommand:
		b.WriteString("\n  " + dimStyle.Render("command to anchor "+m.editProvider+" (as you'd type it in a terminal)") +
			"\n\n  " + m.input.View() + "\n")
		b.WriteString(faintStyle.Render("  runs via your shell · enter save · esc cancel"))
	case modeConfirmRemoveGroup:
		desc := ""
		if m.barGroup >= 0 && m.barGroup < len(m.cfg.Schedules) {
			desc = groupDaysLabel(m.cfg.Schedules[m.barGroup])
		}
		b.WriteString("\n  " + warnStyle.Render("remove the "+desc+" reset times?") + "\n")
		b.WriteString(faintStyle.Render("  backspace/enter remove · esc cancel"))
	case modeConfirmRemoveProvider:
		b.WriteString("\n  " + warnStyle.Render("remove provider "+m.editProvider+"?") + "\n")
		b.WriteString(faintStyle.Render("  enter remove · esc cancel"))
	default:
		hint := "↑/↓ move · enter open · ⌫ remove · esc back"
		b.WriteString(faintStyle.Render("\n  " + hint))
	}

	if m.flash != "" {
		b.WriteString("\n\n  " + warnStyle.Render(m.flash))
	}
	return b.String()
}

// viewBarEdit renders the bar editor: weekday toggles, the editable timeline,
// and the working times list.
func (m Model) viewBarEdit() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("CURFEW") + dimStyle.Render(" · edit · ") + brightStyle.Render(m.editProvider) +
		dimStyle.Render(" · reset times") + "\n\n")

	// Weekday toggle row. Every token renders 5 columns wide so the row never
	// shifts as the day cursor moves.
	b.WriteString(" ")
	for i, d := range weekdayOrder {
		cell := " " + weekdayToken[d] + " "
		switch {
		case m.barFocus == 1 && i == m.barDaySel:
			b.WriteString(selStyle.Render(cell))
		case m.barDays[i]:
			b.WriteString(accentStyle.Render(cell))
		default:
			b.WriteString(faintStyle.Render(cell))
		}
	}
	b.WriteString("\n\n")

	// Scale + editable bar.
	cursorCell := -1
	if m.barFocus == 0 {
		cursorCell = m.barCursor / minutesPerCell
	}
	win := providerWindowMinutes(m.cfg, m.editProvider)
	b.WriteString("  " + headStyle.Render(renderBarScale()) + "\n")
	b.WriteString("  " + renderBar(groupCells(m.barTimes, win), cursorCell, -1) + "\n\n")

	// Cursor + working times.
	b.WriteString("  " + dimStyle.Render("cursor ") + brightStyle.Render(hhmm(m.barCursor)))
	if len(m.barTimes) > 0 {
		labels := make([]string, len(m.barTimes))
		for i, t := range m.barTimes {
			labels[i] = hhmm(t)
		}
		b.WriteString(dimStyle.Render("   resets ") + midStyle.Render(strings.Join(labels, " ")))
	} else {
		b.WriteString(dimStyle.Render("   no reset times yet"))
	}
	b.WriteString("\n")

	hint := "←/→ 1h · ctrl+←/→ 15m · enter add/remove · ↑/↓ days · s save · ⌫⌫ delete · esc cancel"
	if m.barFocus == 1 {
		hint = "←/→ pick day · enter toggle · ↑/↓ back to bar · s save · ⌫⌫ delete · esc cancel"
	}
	b.WriteString(faintStyle.Render("\n  " + hint))

	if m.flash != "" {
		b.WriteString("\n\n  " + warnStyle.Render(m.flash))
	}
	return b.String()
}

// clip truncates s to n display runes, appending an ellipsis when it cuts.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
