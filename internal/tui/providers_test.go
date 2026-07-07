package tui

import (
	"testing"

	"github.com/kyle/curfew/internal/config"
)

func cfgWithProviders() *config.Config {
	return &config.Config{
		Providers: []config.Provider{
			{Name: "claude-1", Command: "claude -p 'x'", WindowMinutes: 300},
			{Name: "codex", Command: "codex exec 'x'", WindowMinutes: 300},
		},
		Schedules: []config.Schedule{
			{Provider: "claude-1", ResetsAt: []string{"10:00"}, Days: []string{"Mon"}},
			{Provider: "codex", ResetsAt: []string{"13:00"}, Days: []string{"Mon"}},
		},
	}
}

func TestAddProvider(t *testing.T) {
	c := cfgWithProviders()
	if err := addProvider(c, "claude-2", "  claude -p 'hi' --model haiku  "); err != nil {
		t.Fatal(err)
	}
	p, ok := c.Provider("claude-2")
	if !ok {
		t.Fatal("claude-2 not added")
	}
	if p.Command != "claude -p 'hi' --model haiku" {
		t.Errorf("command = %q (should be trimmed)", p.Command)
	}
	if p.WindowMinutes != 300 {
		t.Errorf("window = %d, want 300", p.WindowMinutes)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid after add: %v", err)
	}
}

func TestSetProviderCommand(t *testing.T) {
	c := cfgWithProviders()
	if got := providerCommand(c, "claude-1"); got != "claude -p 'x'" {
		t.Fatalf("providerCommand = %q, want the seeded command", got)
	}
	if err := setProviderCommand(c, "claude-1", "  claude -p 'new' --model haiku  "); err != nil {
		t.Fatal(err)
	}
	if got := providerCommand(c, "claude-1"); got != "claude -p 'new' --model haiku" {
		t.Errorf("command = %q (should be updated and trimmed)", got)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid after edit: %v", err)
	}
}

func TestSetProviderCommandErrors(t *testing.T) {
	c := cfgWithProviders()
	if err := setProviderCommand(c, "claude-1", "   "); err == nil {
		t.Error("empty command should error")
	}
	if err := setProviderCommand(c, "nope", "claude -p x"); err == nil {
		t.Error("unknown provider should error")
	}
}

func TestAddProviderErrors(t *testing.T) {
	c := cfgWithProviders()
	if err := addProvider(c, "", "claude -p x"); err == nil {
		t.Error("empty name should error")
	}
	if err := addProvider(c, "claude-1", "claude -p x"); err == nil {
		t.Error("duplicate name should error")
	}
	if err := addProvider(c, "new", "   "); err == nil {
		t.Error("empty command should error")
	}
}

func TestRemoveProvider(t *testing.T) {
	c := cfgWithProviders()
	removeProvider(c, "codex")
	if _, ok := c.Provider("codex"); ok {
		t.Fatal("codex provider not removed")
	}
	for _, s := range c.Schedules {
		if s.Provider == "codex" {
			t.Fatal("codex schedule not removed")
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid after remove: %v", err)
	}
}

func TestRemoveProviderKeepsOthers(t *testing.T) {
	c := cfgWithProviders()
	removeProvider(c, "codex")
	if _, ok := c.Provider("claude-1"); !ok {
		t.Fatal("claude-1 should remain")
	}
	if len(c.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(c.Providers))
	}
}
