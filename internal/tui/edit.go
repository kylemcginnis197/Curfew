package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/schedule"
)

// mode selects which screen/keymap is active.
type mode int

const (
	modeDashboard mode = iota
	modeEdit
	modeInput
	modeConfirmChain
)

// earliestAnchorMin is the earliest wall-clock minute-of-day an auto-chained
// anchor may fall on; the chain stops backfilling once an earlier reset would
// require anchoring before this (05:00 by default).
const earliestAnchorMin = 5 * 60

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
	if path, err := config.ConfigPath(); err == nil {
		if cfg, err := config.Load(path); err == nil {
			m.cfg, m.cfgPath = cfg, path
		}
	}
	m.mode = modeEdit
	m.editProvider = provider
	m.resetSel = 0
	m.daysFocus = false
	m.daySel = 0
	m.flash = ""
	return m
}

// --- key handling ---

func (m Model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	resets := providerResets(m.cfg, m.editProvider)
	switch msg.String() {
	case "esc", "q":
		if m.daysFocus {
			m.daysFocus = false
			return m, nil
		}
		m.mode = modeDashboard
		m.flash = ""
		return m, fetch() // refresh dashboard anchors immediately
	case "w":
		m.daysFocus = !m.daysFocus
		return m, nil
	case "left", "h":
		if m.daysFocus && m.daySel > 0 {
			m.daySel--
		}
	case "right", "l":
		if m.daysFocus && m.daySel < len(weekdayOrder)-1 {
			m.daySel++
		}
	case "up", "k":
		if !m.daysFocus && m.resetSel > 0 {
			m.resetSel--
		}
	case "down", "j":
		if !m.daysFocus && m.resetSel < len(resets)-1 {
			m.resetSel++
		}
	case " ", "space":
		if m.daysFocus {
			if err := toggleDay(m.cfg, m.editProvider, weekdayOrder[m.daySel]); err != nil {
				m.flash = err.Error()
				return m, nil
			}
			return m.persist("days updated")
		}
	case "a":
		m.mode = modeInput
		m.input.SetValue("")
		m.input.Focus()
		m.flash = ""
		return m, nil
	case "d", "x":
		if !m.daysFocus && m.resetSel < len(resets) {
			removed := resets[m.resetSel]
			removeReset(m.cfg, m.editProvider, removed)
			if m.resetSel > 0 {
				m.resetSel--
			}
			return m.persist("removed " + removed)
		}
	}
	return m, nil
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		return m, nil
	case "enter":
		norm, ok := parseFlexibleTime(m.input.Value())
		if !ok {
			m.flash = "invalid time — try 8pm, 8:00pm, or 20:00"
			return m, nil
		}
		m.pendingReset = norm
		chain := chainResets(norm, m.providerWindow())
		if len(chain) > 1 {
			m.pendingChain = chain
			m.mode = modeConfirmChain
			return m, nil
		}
		addReset(m.cfg, m.editProvider, norm)
		return m.persist("added " + norm)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		for _, r := range m.pendingChain {
			addReset(m.cfg, m.editProvider, r)
		}
		n := len(m.pendingChain)
		m.pendingChain = nil
		return m.persist(fmt.Sprintf("added %d resets", n))
	case "n", "N", "enter":
		addReset(m.cfg, m.editProvider, m.pendingReset)
		one := m.pendingReset
		m.pendingChain = nil
		return m.persist("added " + one)
	case "esc":
		m.mode = modeEdit
		m.pendingChain = nil
		return m, nil
	}
	return m, nil
}

// persist saves the edited config (the daemon hot-reloads it) and returns to the
// edit screen. On a validation error it reloads from disk to discard the bad
// edit.
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
	// Clamp cursor after the list may have shrunk/grown.
	if n := len(providerResets(m.cfg, m.editProvider)); m.resetSel >= n && n > 0 {
		m.resetSel = n - 1
	}
	m.flash = flash
	return m, fetch()
}

// providerWindow returns the edited provider's window length in minutes
// (defaulting to 300 if the provider is somehow missing).
func (m Model) providerWindow() int {
	if m.cfg != nil {
		if p, ok := m.cfg.Provider(m.editProvider); ok && p.WindowMinutes > 0 {
			return p.WindowMinutes
		}
	}
	return 300
}

// --- pure helpers (unit-tested) ---

// parseFlexibleTime normalizes a user-typed time to "HH:MM". It accepts 24-hour
// ("20:00", "9", "9:30") and 12-hour ("8pm", "8:00pm", "8 pm") forms.
func parseFlexibleTime(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return "", false
	}
	pm := strings.HasSuffix(s, "pm")
	am := strings.HasSuffix(s, "am")
	if pm || am {
		s = s[:len(s)-2]
	}
	var hStr, mStr string
	if i := strings.IndexByte(s, ':'); i >= 0 {
		hStr, mStr = s[:i], s[i+1:]
	} else {
		hStr, mStr = s, "0"
	}
	h, err1 := strconv.Atoi(hStr)
	mn, err2 := strconv.Atoi(mStr)
	if err1 != nil || err2 != nil || mn < 0 || mn > 59 {
		return "", false
	}
	if am || pm {
		if h < 1 || h > 12 {
			return "", false
		}
		switch {
		case am && h == 12:
			h = 0
		case pm && h != 12:
			h += 12
		}
	} else if h < 0 || h > 23 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", h, mn), true
}

// chainResets returns the reset time plus the earlier resets that chain back to
// it in windowMin steps, stopping once an earlier reset would anchor before
// earliestAnchorMin. The result is sorted ascending. A single-element result
// means there's nothing to backfill.
func chainResets(hhmm string, windowMin int) []string {
	total, ok := minutesOfDay(hhmm)
	if !ok {
		return []string{hhmm}
	}
	out := []int{total}
	for cur := total; ; {
		prev := cur - windowMin
		if prev-windowMin < earliestAnchorMin {
			break
		}
		out = append(out, prev)
		cur = prev
	}
	sort.Ints(out)
	res := make([]string, len(out))
	for i, v := range out {
		res[i] = fmt.Sprintf("%02d:%02d", v/60, v%60)
	}
	return res
}

// providerScheduleIdx returns the index of the provider's first schedule block,
// or -1.
func providerScheduleIdx(cfg *config.Config, provider string) int {
	if cfg == nil {
		return -1
	}
	for i := range cfg.Schedules {
		if cfg.Schedules[i].Provider == provider {
			return i
		}
	}
	return -1
}

// providerResets returns the provider's first schedule's reset times.
func providerResets(cfg *config.Config, provider string) []string {
	if i := providerScheduleIdx(cfg, provider); i >= 0 {
		return cfg.Schedules[i].ResetsAt
	}
	return nil
}

// addReset adds a reset time to the provider's first schedule (creating one if
// none exists), deduped and kept sorted.
func addReset(cfg *config.Config, provider, hhmm string) {
	i := providerScheduleIdx(cfg, provider)
	if i < 0 {
		cfg.Schedules = append(cfg.Schedules, config.Schedule{
			Provider: provider,
			ResetsAt: []string{hhmm},
			Days:     []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
		})
		return
	}
	for _, r := range cfg.Schedules[i].ResetsAt {
		if r == hhmm {
			return
		}
	}
	cfg.Schedules[i].ResetsAt = append(cfg.Schedules[i].ResetsAt, hhmm)
	sort.Strings(cfg.Schedules[i].ResetsAt)
}

// removeReset removes a reset time; if that empties a schedule's reset list, the
// schedule entry is dropped (an empty resets_at would fail validation).
func removeReset(cfg *config.Config, provider, hhmm string) {
	i := providerScheduleIdx(cfg, provider)
	if i < 0 {
		return
	}
	kept := cfg.Schedules[i].ResetsAt[:0:0]
	for _, r := range cfg.Schedules[i].ResetsAt {
		if r != hhmm {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		cfg.Schedules = append(cfg.Schedules[:i], cfg.Schedules[i+1:]...)
		return
	}
	cfg.Schedules[i].ResetsAt = kept
}

// toggleDay flips a weekday on the provider's first schedule. It refuses to
// remove the last remaining day. When all seven end up selected it stores an
// empty Days list (the config's canonical "every day").
func toggleDay(cfg *config.Config, provider string, wd time.Weekday) error {
	i := providerScheduleIdx(cfg, provider)
	if i < 0 {
		return fmt.Errorf("add a reset time first")
	}
	cur := map[time.Weekday]bool{}
	for _, d := range cfg.Schedules[i].Weekdays() { // empty Days => all seven
		cur[d] = true
	}
	cur[wd] = !cur[wd]

	var selected []time.Weekday
	for _, d := range weekdayOrder {
		if cur[d] {
			selected = append(selected, d)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("keep at least one day")
	}
	if len(selected) == 7 {
		cfg.Schedules[i].Days = nil
		return nil
	}
	tokens := make([]string, len(selected))
	for j, d := range selected {
		tokens[j] = weekdayToken[d]
	}
	cfg.Schedules[i].Days = tokens
	return nil
}

// dayEnabled reports whether a schedule currently runs on wd (for rendering).
func dayEnabled(cfg *config.Config, provider string, wd time.Weekday) bool {
	i := providerScheduleIdx(cfg, provider)
	if i < 0 {
		return false
	}
	for _, d := range cfg.Schedules[i].Weekdays() {
		if d == wd {
			return true
		}
	}
	return false
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

// --- view ---

func (m Model) viewEdit() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("⏰ Curfew · edit schedule") + "  ")
	b.WriteString(dimStyle.Render(m.editProvider) + "\n\n")

	if m.cfg == nil {
		b.WriteString(warnStyle.Render("no config loaded") + "\n")
		b.WriteString(dimStyle.Render("\nesc back"))
		return b.String()
	}

	win := m.providerWindow()
	resets := providerResets(m.cfg, m.editProvider)
	b.WriteString(headStyle.Render(fmt.Sprintf("  %-8s   %s", "RESET", "ANCHOR")) + "\n")
	if len(resets) == 0 {
		b.WriteString(dimStyle.Render("  (none — press 'a' to add)\n"))
	}
	for i, r := range resets {
		cursor := "  "
		if !m.daysFocus && i == m.resetSel {
			cursor = "▸ "
		}
		anchor := dimStyle.Render("?")
		if a, err := schedule.AnchorForReset(win, r); err == nil {
			label := fmt.Sprintf("%02d:%02d", a.Hour, a.Min)
			if a.DayShift < 0 {
				label += " (prev day)"
			}
			anchor = label
		}
		line := fmt.Sprintf("%s%-8s → %s", cursor, r, anchor)
		if !m.daysFocus && i == m.resetSel {
			line = selStyle.Render(fmt.Sprintf("%s%-8s ", cursor, r)) + fmt.Sprintf("→ %s", anchor)
		}
		b.WriteString(line + "\n")
	}

	// Weekday row.
	b.WriteString("\n" + headStyle.Render("  DAYS") + "  ")
	if m.daysFocus {
		b.WriteString(dimStyle.Render("(←/→ move · space toggle · w/esc back)"))
	}
	b.WriteString("\n  ")
	for i, d := range weekdayOrder {
		tok := weekdayToken[d]
		cell := "  " + tok + "  "
		on := dayEnabled(m.cfg, m.editProvider, d)
		switch {
		case m.daysFocus && i == m.daySel:
			cell = selStyle.Render(" " + tok + " ")
		case on:
			cell = activeStyle.Render(" " + tok + " ")
		default:
			cell = dimStyle.Render(" " + tok + " ")
		}
		b.WriteString(cell + " ")
	}
	b.WriteString("\n")

	// Mode-specific prompt lines.
	switch m.mode {
	case modeInput:
		b.WriteString("\n  " + m.input.View() + "\n")
		b.WriteString(dimStyle.Render("  enter confirm · esc cancel"))
	case modeConfirmChain:
		b.WriteString("\n  " + warnStyle.Render("Also add the earlier chained resets? ") +
			strings.Join(m.pendingChain, ", ") + "\n")
		b.WriteString(dimStyle.Render("  y add all · n just " + m.pendingReset + " · esc cancel"))
	default:
		b.WriteString(dimStyle.Render("\n  a add · d remove · w edit days · esc back"))
	}

	if m.flash != "" {
		b.WriteString("\n\n  " + warnStyle.Render(m.flash))
	}
	return b.String()
}
