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
	editProvider string         // provider being edited
	editSel      int            // cursor over the editor's focusable items
	daySel       int            // cursor within the weekday row (when it's focused)
	chainSel     int            // 0 = add whole chain, 1 = add just the one
	input        textinput.Model
	pendingReset string   // reset time awaiting the add/chain/remove decision
	pendingChain []string // full chain offered for the pending reset

	// Add-provider flow.
	addTypeSel int    // cursor over providerKinds
	addKind    string // chosen kind ("claude"/"codex")
	addName    string // chosen provider name
}

func newModel() Model {
	ti := textinput.New()
	ti.Placeholder = "e.g. 8pm or 20:00"
	ti.Prompt = "add reset time: "
	ti.CharLimit = 8
	ti.Width = 12

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
		if err := service.Install(); err != nil {
			return flashMsg("install failed: " + err.Error())
		}
		return flashMsg("service installed & started")
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
		return m, tea.Batch(fetch(), tick())
	case flashMsg:
		m.flash = string(msg)
		return m, fetch()
	case tea.KeyMsg:
		// Editor modes handle their own keys (esc means "back", not "quit").
		switch m.mode {
		case modeInput:
			return m.updateInput(msg)
		case modeEditCommand:
			return m.updateEditCommand(msg)
		case modeConfirmChain:
			return m.updateConfirmChain(msg)
		case modeConfirmRemove:
			return m.updateConfirmRemove(msg)
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
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114")) // soft green
	headStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
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

// rowWidth is the content width used for the header clock alignment and the
// selection highlight bar.
const rowWidth = 46

func (m Model) View() string {
	switch m.mode {
	case modeAddName, modeAddCommand:
		return m.viewAddProvider()
	case modeEdit, modeInput, modeEditCommand, modeConfirmChain, modeConfirmRemove, modeConfirmRemoveProvider:
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

	// Header: wordmark left, clock right-aligned.
	clock := s.Now.Local().Format("Mon 15:04")
	gap := rowWidth - len("CURFEW") - len(clock)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(titleStyle.Render("CURFEW") + strings.Repeat(" ", gap) + dimStyle.Render(clock) + "\n\n")

	// Providers.
	for i, p := range s.Providers {
		stateWord := "idle"
		detail := ""
		if p.Active {
			stateWord = "active"
		} else if !p.NextAnchor.IsZero() {
			detail = "next " + p.NextAnchor.Local().Format("15:04")
		}
		hs := healthStyle(providerHealth(p.Name, s.Recent))
		if i == m.sel {
			plain := padTo(p.Name, 11) + padTo(stateWord, 8)
			if p.Active {
				plain += fmt.Sprintf("resets %s   %s left", p.WindowEnd.Local().Format("15:04"), short(time.Until(p.WindowEnd)))
			} else {
				plain += detail
			}
			dot := selStyle.Foreground(hs.GetForeground()).Render(healthGlyph)
			b.WriteString(dot + selStyle.Render(padTo(" "+plain, rowWidth-1)) + "\n")
			continue
		}
		name := brightStyle.Render(padTo(p.Name, 11))
		var st, det string
		if p.Active {
			st = accentStyle.Render(padTo(stateWord, 8))
			det = midStyle.Render("resets "+p.WindowEnd.Local().Format("15:04")) +
				dimStyle.Render("   "+short(time.Until(p.WindowEnd))+" left")
		} else {
			st = dimStyle.Render(padTo(stateWord, 8))
			det = dimStyle.Render(detail)
		}
		b.WriteString(hs.Render(healthGlyph) + " " + name + st + det + "\n")
	}
	// Add-provider row.
	if m.sel == len(s.Providers) {
		b.WriteString(selStyle.Render(padTo("  + add provider", rowWidth)) + "\n")
	} else {
		b.WriteString("  " + accentStyle.Render("+") + dimStyle.Render(" add provider") + "\n")
	}

	// Health legend.
	b.WriteString("\n  " + okStyle.Render(healthGlyph) + dimStyle.Render(" resetting on time   ") +
		warnStyle.Render(healthGlyph) + dimStyle.Render(" missed a reset") + "\n")

	if m.flash != "" {
		b.WriteString("\n  " + warnStyle.Render(m.flash) + "\n")
	}
	b.WriteString(faintStyle.Render("\n  ↑/↓ select · enter open · esc quit"))
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
		case model.Fired, model.Skipped:
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
