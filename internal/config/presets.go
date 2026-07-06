package config

// Built-in provider presets. Users can override any field in config.toml, or
// add their own providers for arbitrary CLIs.
//
// Two Claude subscriptions are supported by pointing each at a different Claude
// config directory via the CLAUDE_CONFIG_DIR environment variable. That dir
// holds both the logged-in credentials and the session logs (projects/), so
// each provider anchors and observes an independent subscription. Log in to
// each separately, e.g.:
//
//	CLAUDE_CONFIG_DIR=~/.claude    claude   # then /login  -> subscription 1
//	CLAUDE_CONFIG_DIR=~/.claude-2  claude   # then /login  -> subscription 2

// NewClaudeProvider builds a Claude provider bound to a specific config dir.
// The dir holds the logged-in credentials and the session logs (projects/), so
// each Claude provider anchors and observes an independent subscription. This is
// exported so the TUI can add providers pointed at any account directory.
func NewClaudeProvider(name, configDir string) Provider {
	return Provider{
		Name:          name,
		Command:       []string{"claude", "-p", "curfew: anchor", "--model", "haiku"},
		Env:           map[string]string{"CLAUDE_CONFIG_DIR": configDir},
		WindowMinutes: 300, // Claude's 5-hour rolling window
		LogGlob:       configDir + "/projects/**/*.jsonl",
		// Claude session JSONL lines carry an ISO-8601 "timestamp" field.
		TimestampField: "timestamp",
	}
}

// NewCodexProvider builds a Codex provider bound to a specific CODEX_HOME dir.
func NewCodexProvider(name, home string) Provider {
	return Provider{
		Name:           name,
		Command:        []string{"codex", "exec", "curfew: anchor"},
		Env:            map[string]string{"CODEX_HOME": home},
		WindowMinutes:  300,
		LogGlob:        home + "/sessions/**/*.jsonl",
		TimestampField: "timestamp",
	}
}

// claudeProvider is the internal alias retained for the built-in presets.
func claudeProvider(name, configDir string) Provider { return NewClaudeProvider(name, configDir) }

// Claude1Preset is the primary subscription, using the standard ~/.claude dir.
func Claude1Preset() Provider { return claudeProvider("claude-1", "~/.claude") }

// Claude2Preset is a second subscription in an alternate config dir.
func Claude2Preset() Provider { return claudeProvider("claude-2", "~/.claude-2") }

// CodexPreset anchors an OpenAI Codex window using ~/.codex. If the logs can't
// be read, state detection falls back to "assume no active window".
func CodexPreset() Provider { return NewCodexProvider("codex", "~/.codex") }

// Presets returns all built-in provider presets keyed by name.
func Presets() map[string]Provider {
	return map[string]Provider{
		"claude-1": Claude1Preset(),
		"claude-2": Claude2Preset(),
		"codex":    CodexPreset(),
	}
}

// Default returns a starter config: the three presets plus example schedules
// with resets aligned to a typical workday, which the user can edit.
func Default() *Config {
	work := []string{"Mon", "Tue", "Wed", "Thu", "Fri"}
	return &Config{
		General: General{Timezone: "local"},
		Providers: []Provider{
			Claude1Preset(),
			Claude2Preset(),
			CodexPreset(),
		},
		Schedules: []Schedule{
			// Stagger the two Claude subscriptions so fresh capacity lands on
			// alternating boundaries through the workday.
			{Provider: "claude-1", ResetsAt: []string{"10:00", "15:00", "20:00"}, Days: work},
			{Provider: "claude-2", ResetsAt: []string{"12:30", "17:30"}, Days: work},
			{Provider: "codex", ResetsAt: []string{"13:00"}, Days: work},
		},
	}
}
