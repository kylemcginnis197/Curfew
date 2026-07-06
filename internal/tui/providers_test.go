package tui

import (
	"testing"

	"github.com/kyle/curfew/internal/config"
)

func cfgWithProviders() *config.Config {
	return &config.Config{
		Providers: []config.Provider{
			config.NewClaudeProvider("claude-1", "~/.claude"),
			config.NewCodexProvider("codex", "~/.codex"),
		},
		Schedules: []config.Schedule{
			{Provider: "claude-1", ResetsAt: []string{"10:00"}, Days: []string{"Mon"}},
			{Provider: "codex", ResetsAt: []string{"13:00"}, Days: []string{"Mon"}},
		},
	}
}

func TestAddProviderClaude(t *testing.T) {
	c := cfgWithProviders()
	if err := addProvider(c, "claude", "claude-2", "~/.claude-personal"); err != nil {
		t.Fatal(err)
	}
	p, ok := c.Provider("claude-2")
	if !ok {
		t.Fatal("claude-2 not added")
	}
	if p.Env["CLAUDE_CONFIG_DIR"] != "~/.claude-personal" {
		t.Errorf("config dir = %q", p.Env["CLAUDE_CONFIG_DIR"])
	}
	if p.LogGlob != "~/.claude-personal/projects/**/*.jsonl" {
		t.Errorf("log_glob = %q", p.LogGlob)
	}
	if p.Command[0] != "claude" {
		t.Errorf("command = %v", p.Command)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("config invalid after add: %v", err)
	}
}

func TestAddProviderCodexDefaultsDir(t *testing.T) {
	c := cfgWithProviders()
	if err := addProvider(c, "codex", "codex-2", ""); err != nil {
		t.Fatal(err)
	}
	p, _ := c.Provider("codex-2")
	if p.Env["CODEX_HOME"] != "~/.codex-2" {
		t.Errorf("empty dir should default to ~/.codex-2, got %q", p.Env["CODEX_HOME"])
	}
	if p.LogGlob != "~/.codex-2/sessions/**/*.jsonl" {
		t.Errorf("log_glob = %q", p.LogGlob)
	}
}

func TestAddProviderErrors(t *testing.T) {
	c := cfgWithProviders()
	if err := addProvider(c, "claude", "", "~/x"); err == nil {
		t.Error("empty name should error")
	}
	if err := addProvider(c, "claude", "claude-1", "~/x"); err == nil {
		t.Error("duplicate name should error")
	}
}

func TestRemoveProvider(t *testing.T) {
	c := cfgWithProviders()
	removeProvider(c, "codex")
	if _, ok := c.Provider("codex"); ok {
		t.Fatal("codex provider not removed")
	}
	// Its schedule must be gone too, or the config won't validate.
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

func TestDefaultDirFor(t *testing.T) {
	if got := defaultDirFor("claude-3"); got != "~/.claude-3" {
		t.Errorf("defaultDirFor = %q", got)
	}
}
