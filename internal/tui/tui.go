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
		case modeConfirmChain:
			return m.updateConfirmChain(msg)
		case modeConfirmRemove:
			return m.updateConfirmRemove(msg)
		case modeConfirmRemoveProvider:
			return m.updateConfirmRemoveProvider(msg)
		case modeAddType:
			return m.updateAddType(msg)
		case modeAddName:
			return m.updateAddName(msg)
		case modeAddDir:
			return m.updateAddDir(msg)
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
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	idleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("57"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	headStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
)

func (m Model) View() string {
	switch m.mode {
	case modeAddType, modeAddName, modeAddDir:
		return m.viewAddProvider()
	case modeEdit, modeInput, modeConfirmChain, modeConfirmRemove, modeConfirmRemoveProvider:
		return m.viewEdit()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("⏰ Curfew") + "  ")

	if m.err != nil {
		b.WriteString(warnStyle.Render("daemon not running"))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render(m.err.Error()))
		b.WriteString("\n\n")
		b.WriteString("Press " + headStyle.Render("Enter") + " to install & start the background service, or run ")
		b.WriteString(headStyle.Render("curfew install") + ".\n")
		b.WriteString(dimStyle.Render("\nEnter install · Esc quit"))
		if m.flash != "" {
			b.WriteString("\n\n" + warnStyle.Render(m.flash))
		}
		return b.String()
	}

	s := m.status
	b.WriteString(dimStyle.Render(fmt.Sprintf("pid %d · %s · %s", s.PID, s.Timezone, s.Now.Format("Mon 15:04:05"))))
	b.WriteString("\n\n")

	// Providers table.
	b.WriteString(headStyle.Render(fmt.Sprintf("  %-10s %-10s %-22s %-22s", "PROVIDER", "STATE", "WINDOW", "NEXT ANCHOR")))
	b.WriteString("\n")
	for i, p := range s.Providers {
		cursor := "  "
		if i == m.sel {
			cursor = "▸ "
		}
		state := idleStyle.Render("idle")
		window := dimStyle.Render("—")
		if p.Active {
			state = activeStyle.Render("ACTIVE")
			window = fmt.Sprintf("%s→%s (%s left)",
				p.WindowStart.Local().Format("15:04"), p.WindowEnd.Local().Format("15:04"),
				short(time.Until(p.WindowEnd)))
		}
		next := dimStyle.Render("—")
		if !p.NextAnchor.IsZero() {
			next = fmt.Sprintf("%s → reset %s", p.NextAnchor.Local().Format("Mon 15:04"), p.NextReset.Local().Format("15:04"))
		}
		row := fmt.Sprintf("%s%-10s %-10s %-22s %-22s", cursor, p.Name, state, window, next)
		if i == m.sel {
			row = selStyle.Render(fmt.Sprintf("%s%-10s ", cursor, p.Name)) +
				fmt.Sprintf("%-10s %-22s %-22s", state, window, next)
		}
		b.WriteString(row + "\n")
	}
	// "Add provider" row.
	if m.sel == len(s.Providers) {
		b.WriteString(selStyle.Render("▸ ＋ Add provider") + "\n")
	} else {
		b.WriteString(dimStyle.Render("  ＋ Add provider") + "\n")
	}

	// Recent history.
	b.WriteString("\n" + headStyle.Render("  RECENT") + "\n")
	if len(s.Recent) == 0 {
		b.WriteString(dimStyle.Render("  (no events yet)\n"))
	}
	for i, e := range s.Recent {
		if i >= 8 {
			break
		}
		b.WriteString(fmt.Sprintf("  %s  %-10s %s %s\n",
			dimStyle.Render(e.Time.Format("01-02 15:04")),
			e.Provider, outcomeStyle(e.Outcome), dimStyle.Render(truncate(e.Detail, 48))))
	}

	if m.flash != "" {
		b.WriteString("\n" + warnStyle.Render(m.flash) + "\n")
	}
	b.WriteString(dimStyle.Render("\n↑/↓ select · Enter open · Esc quit"))
	return b.String()
}

func outcomeStyle(o model.Outcome) string {
	switch o {
	case model.Fired, model.Manual:
		return activeStyle.Render(string(o))
	case model.Skipped:
		return dimStyle.Render(string(o))
	default:
		return warnStyle.Render(string(o))
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
