// Package tui is Curfew's primary interface: a Bubble Tea dashboard that polls
// the daemon's localhost API to show each provider's window state, the next
// anchor, and recent history, and lets the user anchor a provider on demand.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kyle/curfew/internal/api"
	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/service"
)

const pollInterval = 2 * time.Second

// Run starts the TUI event loop.
func Run() error {
	_, err := tea.NewProgram(newModel(), tea.WithAltScreen()).Run()
	return err
}

type statusMsg struct {
	s   model.Status
	err error
}
type tickMsg time.Time
type flashMsg string

// Model is the dashboard + editor state.
type Model struct {
	status model.Status
	err    error
	sel    int
	flash  string
	width  int

	// Editor state.
	mode         mode
	cfg          *config.Config // authoritative for editing (loaded from disk)
	cfgPath      string
	editProvider string // provider being edited
	editSel      int    // cursor over the editor's focusable items
	input        textinput.Model

	// Bar editor (one day group's working copy).
	barGroup  int     // index into cfg.Schedules; -1 = new group
	barCursor int     // cursor minute-of-day, in 15-minute steps
	barTimes  []int   // working-copy reset minutes
	barDays   [7]bool // working-copy day mask, Mon-first (weekdayOrder)
	barFocus  int     // 0 = time bar, 1 = weekday row
	barDaySel int     // cursor within the weekday row
	// barDeleteArmed is set by a first Backspace in the bar editor; a second
	// deletes the group, any other key disarms.
	barDeleteArmed bool

	// Add-provider flow.
	addName string // chosen provider name
}

func newModel() Model {
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 48

	path, _ := config.ConfigPath()
	cfg, _ := config.Load(path) // nil-safe: editor guards on cfg == nil
	return Model{mode: modeDashboard, cfg: cfg, cfgPath: path, input: ti}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetch(), tick())
}

func fetch() tea.Cmd {
	return func() tea.Msg {
		c, err := api.Dial()
		if err != nil {
			return statusMsg{err: err}
		}
		s, err := c.Status()
		return statusMsg{s: s, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func fireCmd(provider string) tea.Cmd {
	return func() tea.Msg {
		c, err := api.Dial()
		if err != nil {
			return flashMsg("cannot fire: daemon not running")
		}
		if _, err := c.Fire(provider); err != nil {
			return flashMsg("fire failed: " + err.Error())
		}
		return flashMsg("anchoring " + provider + "… (running in background)")
	}
}

func installCmd() tea.Cmd {
	return func() tea.Msg {
		if err := service.EnsureRunning(); err != nil {
			return flashMsg("install failed: " + err.Error())
		}
		// The daemon needs a moment to bind its port and write the endpoint file;
		// the 2s tick re-fetches and the dashboard appears once it's up.
		return flashMsg("service started — connecting…")
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case statusMsg:
		m.status, m.err = msg.s, msg.err
		if n := len(m.status.Providers); n > 0 && m.sel >= n {
			m.sel = n - 1
		}
		return m, nil
	case tickMsg:
		// Keep the dashboard's bars and toggle tracking external config edits;
		// never reload mid-edit, which would clobber unsaved changes.
		if m.mode == modeDashboard && m.cfgPath != "" {
			if cfg, err := config.Load(m.cfgPath); err == nil {
				m.cfg = cfg
			}
		}
		return m, tea.Batch(fetch(), tick())
	case flashMsg:
		m.flash = string(msg)
		return m, fetch()
	case tea.KeyMsg:
		// Editor modes handle their own keys (esc means "back", not "quit").
		switch m.mode {
		case modeBarEdit:
			return m.updateBarEdit(msg)
		case modeEditCommand:
			return m.updateEditCommand(msg)
		case modeConfirmRemoveGroup:
			return m.updateConfirmRemoveGroup(msg)
		case modeConfirmRemoveProvider:
			return m.updateConfirmRemoveProvider(msg)
		case modeAddName:
			return m.updateAddName(msg)
		case modeAddCommand:
			return m.updateAddCommand(msg)
		case modeEdit:
			return m.updateEdit(msg)
		default:
			return m.updateDashboard(msg)
		}
	}
	return m, nil
}

// updateDashboard handles keys on the read-only dashboard. Navigation is
// arrows + Enter + Esc only.
func (m Model) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The provider list has one extra selectable row at the end: "Add provider".
	addIdx := len(m.status.Providers)
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "up":
		if m.sel > 0 {
			m.sel--
		}
	case "down":
		if m.sel < addIdx {
			m.sel++
		}
	case "a":
		return m.toggleAutoPrime()
	case "enter":
		// When the daemon is down, Enter installs & starts it; otherwise Enter
		// opens the selected provider's editor, or the add-provider flow when the
		// "Add provider" row is selected.
		if m.err != nil {
			m.flash = "installing service…"
			return m, installCmd()
		}
		if m.sel == addIdx {
			return m.startAddProvider(), nil
		}
		if p := m.selected(); p != "" {
			return m.enterEdit(p), nil
		}
	}
	return m, nil
}

// toggleAutoPrime flips the global auto-reset (primer) setting and saves it;
// the daemon picks the change up via its config watcher. The config is
// reloaded first so the toggle never clobbers external edits.
func (m Model) toggleAutoPrime() (tea.Model, tea.Cmd) {
	if m.cfgPath == "" {
		m.flash = "no config loaded"
		return m, nil
	}
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		m.flash = "load failed: " + err.Error()
		return m, nil
	}
	next := !cfg.General.AutoPrimeEnabled()
	cfg.General.AutoPrime = &next
	if err := cfg.Save(m.cfgPath); err != nil {
		m.flash = "save failed: " + err.Error()
		return m, nil
	}
	m.cfg = cfg
	if next {
		m.flash = "auto-reset on"
	} else {
		m.flash = "auto-reset off"
	}
	return m, fetch()
}

func (m Model) selected() string {
	if m.sel >= 0 && m.sel < len(m.status.Providers) {
		return m.status.Providers[m.sel].Name
	}
	return ""
}

// --- styles ---

var (
	// Minimal / monochrome palette: grayscale text with a single accent.
	accentColor = lipgloss.Color("212") // the one accent (soft magenta)

	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor)
	brightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	midStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	faintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	activeStyle = accentStyle
	idleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("236"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("174")) // muted red
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("71"))  // green
	headStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	// Timeline bar cells. Active windows use a muted sage (reads "glass" over
	// dark bg), reset ticks are a dark notch cut into them, the editor cursor
	// is a white block so it reads over any cell, and the dashboard now-marker
	// is a quiet steel so it doesn't fight the rest of the bar.
	barActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("65"))  // muted sage
	barTickStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("235"))
	barCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	barNowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("109")) // dusty teal
)

// healthGlyph is the per-provider health dot shown at the start of each row.
const healthGlyph = "●"

// padTo pads (or truncates) s to n display columns, counting runes so that
// multibyte glyphs like → and — don't break column alignment.
func padTo(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}

// rowWidth is the content width of the edit screens: a 2-column indent plus a
// 48-cell bar. The header clock and selection highlights align to it.
const rowWidth = 2 + barCells

// Dashboard column layout: health dot, name, next reset, then the timeline
// bar. dashWidth is the full row width used for the header clock.
const (
	dashNameW  = 11
	dashResetW = 12
	dashBarCol = 2 + dashNameW + dashResetW
	dashWidth  = dashBarCol + barCells
)

func (m Model) View() string {
	switch m.mode {
	case modeAddName, modeAddCommand:
		return m.viewAddProvider()
	case modeBarEdit:
		return m.viewBarEdit()
	case modeEdit, modeEditCommand, modeConfirmRemoveGroup, modeConfirmRemoveProvider:
		return m.viewEdit()
	}
	var b strings.Builder

	if m.err != nil {
		b.WriteString(titleStyle.Render("CURFEW") + "\n\n")
		b.WriteString("  " + warnStyle.Render("daemon not running") + "\n\n")
		b.WriteString("  " + dimStyle.Render(m.err.Error()) + "\n\n")
		b.WriteString("  press " + accentStyle.Render("enter") + dimStyle.Render(" to install & start the service") + "\n")
		if m.flash != "" {
			b.WriteString("\n  " + warnStyle.Render(m.flash) + "\n")
		}
		b.WriteString(faintStyle.Render("\n  enter install · esc quit"))
		return b.String()
	}

	s := m.status

	// Header: wordmark left, clock right-aligned over the bar column's edge.
	clock := s.Now.Local().Format("Mon 15:04")
	gap := dashWidth - len("CURFEW") - len(clock)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(titleStyle.Render("CURFEW") + strings.Repeat(" ", gap) + dimStyle.Render(clock) + "\n\n")

	// Column headers with the 0:00 → 24:00 ruler over the bar column.
	b.WriteString(headStyle.Render("  "+padTo("model", dashNameW)+padTo("next reset", dashResetW)) +
		headStyle.Render(renderBarScale()) + "\n")

	// Providers: one row each — name, next reset, today's timeline. The accent
	// cell on the bar marks the current time.
	today := s.Now.Local()
	nowCell := (today.Hour()*60 + today.Minute()) / minutesPerCell
	for i, p := range s.Providers {
		reset := ""
		if p.Active {
			reset = p.WindowEnd.Local().Format("15:04")
		} else if !p.NextReset.IsZero() {
			reset = p.NextReset.Local().Format("15:04")
		}
		hs := healthStyle(providerHealth(p.Name, s.Recent))
		bar := renderBar(toCells(dayMinutes(m.cfg, p.Name, today)), -1, nowCell)
		var row string
		if i == m.sel {
			row = selStyle.Foreground(hs.GetForeground()).Render(healthGlyph) +
				selStyle.Render(" "+padTo(p.Name, dashNameW)+padTo(reset, dashResetW-1)) + " "
		} else {
			rs := dimStyle
			if p.Active {
				rs = accentStyle
			}
			row = hs.Render(healthGlyph) + " " + brightStyle.Render(padTo(p.Name, dashNameW)) +
				rs.Render(padTo(reset, dashResetW))
		}
		b.WriteString(row + bar + "\n")
	}
	// Add-provider row.
	b.WriteString("\n")
	if m.sel == len(s.Providers) {
		b.WriteString(selStyle.Render(padTo("  + add provider", dashBarCol)) + "\n")
	} else {
		b.WriteString("  " + accentStyle.Render("+") + dimStyle.Render(" add provider") + "\n")
	}

	// Auto-reset toggle (global; flipped with the `a` key).
	check, cs := "[ ]", dimStyle
	if m.cfg != nil && m.cfg.General.AutoPrimeEnabled() {
		check, cs = "[x]", brightStyle
	}
	b.WriteString("\n  " + cs.Render(check) + dimStyle.Render(" automatically reset limit when available") + "\n")

	// Health legend.
	b.WriteString("\n  " + okStyle.Render(healthGlyph) + dimStyle.Render(" resetting on time   ") +
		warnStyle.Render(healthGlyph) + dimStyle.Render(" missed a reset") + "\n")

	if m.flash != "" {
		b.WriteString("\n  " + warnStyle.Render(m.flash) + "\n")
	}
	b.WriteString(faintStyle.Render("\n  ↑/↓ select · enter open · a auto-reset · esc quit"))
	return b.String()
}

// health is a provider's reset-health at a glance.
type health int

const (
	healthUnknown health = iota
	healthOK
	healthBad
)

// providerHealth reports whether a provider's most recent *scheduled* reset was
// served — i.e. a window was active around the reset boundary. events are
// newest-first (store.Recent orders ts DESC). Manual fires (Reset == "") are
// ignored: they say nothing about scheduled resets.
func providerHealth(name string, events []model.Event) health {
	for _, e := range events {
		if e.Provider != name || e.Reset == "" {
			continue
		}
		switch e.Outcome {
		case model.Fired, model.Skipped, model.Primed:
			return healthOK
		case model.Failed, model.Missed:
			return healthBad
		}
		return healthUnknown
	}
	return healthUnknown
}

func healthStyle(h health) lipgloss.Style {
	switch h {
	case healthOK:
		return okStyle
	case healthBad:
		return warnStyle
	default:
		return faintStyle
	}
}

func short(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
