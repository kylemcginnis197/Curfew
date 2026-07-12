package tui

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
)

// TestViewSmoke prints the redesigned screens for manual inspection.
// Run with: go test ./internal/tui/ -run TestViewSmoke -v
func TestViewSmoke(t *testing.T) {
	cfg := config.Default()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m := Model{
		mode:    modeDashboard,
		cfg:     cfg,
		cfgPath: path,
		input:   textinput.New(),
		status: model.Status{
			Now: now,
			Providers: []model.ProviderState{
				{Name: "claude", Active: true, WindowEnd: now.Add(2 * time.Hour), NextReset: now.Add(2 * time.Hour)},
				{Name: "codex", NextReset: now.Add(5 * time.Hour)},
			},
		},
	}
	fmt.Println("=== DASHBOARD ===")
	fmt.Println(m.View())

	me := m.enterEdit("claude")
	fmt.Println("\n=== EDIT ===")
	fmt.Println(me.View())

	mb := me.enterBarEdit(-1)
	fmt.Println("\n=== BAR EDIT (bar focus) ===")
	fmt.Println(mb.View())

	mb.barFocus = 1
	fmt.Println("\n=== BAR EDIT (days focus) ===")
	fmt.Println(mb.View())
}
