package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kyle/curfew/internal/config"
)

// addProvider appends a new provider defined by a name and a shell command
// (what you'd type in a terminal to anchor a window). It errors on an empty or
// duplicate name, or an empty command.
func addProvider(cfg *config.Config, name, command string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name required")
	}
	if _, ok := cfg.Provider(name); ok {
		return fmt.Errorf("provider %q already exists", name)
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command required")
	}
	cfg.Providers = append(cfg.Providers, config.Provider{
		Name:          name,
		Command:       strings.TrimSpace(command),
		WindowMinutes: 300,
	})
	return nil
}

// providerCommand returns the shell command configured to anchor a provider.
func providerCommand(cfg *config.Config, name string) string {
	if cfg == nil {
		return ""
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			return cfg.Providers[i].Command
		}
	}
	return ""
}

// setProviderCommand updates a provider's anchor command in place. It errors on
// an empty command or an unknown provider.
func setProviderCommand(cfg *config.Config, name, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command required")
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			cfg.Providers[i].Command = command
			return nil
		}
	}
	return fmt.Errorf("unknown provider %q", name)
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

// --- add-provider flow (dashboard "+ add provider"): name -> command ---

// startAddProvider reloads config and prompts for the new provider's name.
func (m Model) startAddProvider() Model {
	if cfg, err := config.Load(m.cfgPath); err == nil {
		m.cfg = cfg
	}
	m.mode = modeAddName
	m.addName = ""
	m.input.SetValue("")
	m.input.Prompt = "name: "
	m.input.Placeholder = "claude-3"
	m.input.Width = 24
	m.input.CharLimit = 40
	m.input.Focus()
	m.flash = ""
	return m
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
		m.mode = modeAddCommand
		m.input.Prompt = "command: "
		m.input.Width = 48
		m.input.CharLimit = 256
		m.input.SetValue("claude -p 'curfew: anchor'")
		m.input.CursorEnd()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateAddCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeDashboard
		return m, nil
	case "enter":
		if err := addProvider(m.cfg, m.addName, m.input.Value()); err != nil {
			m.flash = err.Error()
			return m, nil
		}
		if err := m.cfg.Save(m.cfgPath); err != nil {
			m.flash = "save failed: " + err.Error()
			if cfg, err := config.Load(m.cfgPath); err == nil {
				m.cfg = cfg
			}
			m.mode = modeDashboard
			return m, nil
		}
		// Drop straight into the bar editor so the new provider gets its first
		// reset times; save/esc both land in the provider's editor.
		m.editProvider = m.addName
		return m.enterBarEdit(-1), fetch()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateEditCommand edits the focused provider's anchor command. Enter saves
// (the daemon hot-reloads), Esc cancels back to the editor.
func (m Model) updateEditCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		return m, nil
	case "enter":
		if err := setProviderCommand(m.cfg, m.editProvider, m.input.Value()); err != nil {
			m.flash = err.Error()
			return m, nil
		}
		return m.persist("command updated")
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

// viewAddProvider renders the two add-provider steps.
func (m Model) viewAddProvider() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("CURFEW") + dimStyle.Render(" · add provider") + "\n\n")
	switch m.mode {
	case modeAddName:
		b.WriteString("  " + dimStyle.Render("provider name") + "\n\n  " + m.input.View() + "\n")
		b.WriteString(faintStyle.Render("  enter next · esc cancel"))
	case modeAddCommand:
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("command to anchor %q (as you'd type it in a terminal)", m.addName)) +
			"\n\n  " + m.input.View() + "\n")
		b.WriteString(faintStyle.Render("  runs via your shell · enter create · esc cancel"))
	}
	if m.flash != "" {
		b.WriteString("\n\n  " + warnStyle.Render(m.flash))
	}
	return b.String()
}
