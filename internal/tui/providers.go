package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kyle/curfew/internal/config"
)

// providerKinds are the provider types the TUI can create.
var providerKinds = []string{"claude", "codex"}

// defaultDirFor suggests a config/home directory for a new provider.
func defaultDirFor(name string) string { return "~/." + name }

// makeProvider builds a provider of the given kind bound to dir.
func makeProvider(kind, name, dir string) config.Provider {
	if kind == "codex" {
		return config.NewCodexProvider(name, dir)
	}
	return config.NewClaudeProvider(name, dir)
}

// addProvider appends a new provider (deduped by name). It errors on an empty or
// duplicate name; an empty dir falls back to the default for the name.
func addProvider(cfg *config.Config, kind, name, dir string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name required")
	}
	if _, ok := cfg.Provider(name); ok {
		return fmt.Errorf("provider %q already exists", name)
	}
	if strings.TrimSpace(dir) == "" {
		dir = defaultDirFor(name)
	}
	cfg.Providers = append(cfg.Providers, makeProvider(kind, name, strings.TrimSpace(dir)))
	return nil
}

// removeProvider deletes a provider and any schedules that reference it.
func removeProvider(cfg *config.Config, name string) {
	keptP := cfg.Providers[:0:0]
	for _, p := range cfg.Providers {
		if p.Name != name {
			keptP = append(keptP, p)
		}
	}
	cfg.Providers = keptP
	keptS := cfg.Schedules[:0:0]
	for _, s := range cfg.Schedules {
		if s.Provider != name {
			keptS = append(keptS, s)
		}
	}
	cfg.Schedules = keptS
}

// --- add-provider flow (dashboard "＋ Add provider") ---

// startAddProvider reloads config and enters the type-picker.
func (m Model) startAddProvider() Model {
	if path, err := config.ConfigPath(); err == nil {
		if cfg, err := config.Load(path); err == nil {
			m.cfg, m.cfgPath = cfg, path
		}
	}
	m.mode = modeAddType
	m.addTypeSel = 0
	m.flash = ""
	return m
}

func (m Model) updateAddType(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "down":
		m.addTypeSel = (m.addTypeSel + 1) % len(providerKinds)
	case "enter":
		m.addKind = providerKinds[m.addTypeSel]
		m.mode = modeAddName
		m.input.SetValue("")
		m.input.Prompt = "name: "
		m.input.Placeholder = m.addKind + "-2"
		m.input.Width = 24
		m.input.CharLimit = 40
		m.input.Focus()
	case "esc":
		m.mode = modeDashboard
	}
	return m, nil
}

func (m Model) updateAddName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeDashboard
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.input.Value())
		if name == "" {
			m.flash = "name required"
			return m, nil
		}
		if _, ok := m.cfg.Provider(name); ok {
			m.flash = "provider " + name + " already exists"
			return m, nil
		}
		m.addName = name
		m.mode = modeAddDir
		m.input.Prompt = "config dir: "
		m.input.Width = 40 // paths can be long (e.g. ~/.claude-personal)
		m.input.CharLimit = 128
		m.input.SetValue(defaultDirFor(name))
		m.input.CursorEnd()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateAddDir(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeDashboard
		return m, nil
	case "enter":
		dir := strings.TrimSpace(m.input.Value())
		if err := addProvider(m.cfg, m.addKind, m.addName, dir); err != nil {
			m.flash = err.Error()
			return m, nil
		}
		m.input.Prompt = "add reset time: " // restore for the schedule editor
		// Save and open the new provider's editor so the user can add resets.
		if err := m.cfg.Save(m.cfgPath); err != nil {
			m.flash = "save failed: " + err.Error()
			if cfg, err := config.Load(m.cfgPath); err == nil {
				m.cfg = cfg
			}
			m.mode = modeDashboard
			return m, nil
		}
		return m.enterEdit(m.addName), fetch()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateConfirmRemoveProvider deletes the edited provider on Enter.
func (m Model) updateConfirmRemoveProvider(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := m.editProvider
		removeProvider(m.cfg, name)
		if err := m.cfg.Save(m.cfgPath); err != nil {
			m.flash = "save failed: " + err.Error()
			if cfg, err := config.Load(m.cfgPath); err == nil {
				m.cfg = cfg
			}
			m.mode = modeEdit
			return m, nil
		}
		m.mode = modeDashboard
		m.sel = 0
		m.flash = "removed provider " + name
		return m, fetch()
	case "esc":
		m.mode = modeEdit
		return m, nil
	}
	return m, nil
}

// viewAddProvider renders the add-provider screens.
func (m Model) viewAddProvider() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("CURFEW") + dimStyle.Render(" · add provider") + "\n\n")
	switch m.mode {
	case modeAddType:
		b.WriteString("  " + dimStyle.Render("provider type") + "\n")
		for i, k := range providerKinds {
			b.WriteString("  " + chainOpt(i == m.addTypeSel, k) + "\n")
		}
		b.WriteString(faintStyle.Render("\n  ↑/↓ choose · enter next · esc cancel"))
	case modeAddName:
		b.WriteString("  " + dimStyle.Render("type "+m.addKind) + "\n\n  " + m.input.View() + "\n")
		b.WriteString(faintStyle.Render("  enter next · esc cancel"))
	case modeAddDir:
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%s provider %q", m.addKind, m.addName)) + "\n\n  " + m.input.View() + "\n")
		envKey := "CLAUDE_CONFIG_DIR"
		if m.addKind == "codex" {
			envKey = "CODEX_HOME"
		}
		b.WriteString(faintStyle.Render("  sets " + envKey + " + log location · enter create · esc cancel"))
	}
	if m.flash != "" {
		b.WriteString("\n\n  " + warnStyle.Render(m.flash))
	}
	return b.String()
}
