package config

// Built-in provider presets for the common CLIs. A provider is otherwise just a
// shell command, so any tool (or a second account) can be added by hand or from
// the TUI.

// ClaudePreset anchors a Claude usage window using the default ~/.claude, on the
// cheapest model. Claude session JSONL lines carry an ISO-8601 "timestamp".
func ClaudePreset() Provider {
	return Provider{
		Name:           "claude",
		Command:        "claude -p 'curfew: anchor' --model haiku",
		WindowMinutes:  300, // Claude's 5-hour rolling window
		LogGlob:        "~/.claude/projects/**/*.jsonl",
		TimestampField: "timestamp",
	}
}

// CodexPreset anchors an OpenAI Codex window using the default ~/.codex. If the
// logs can't be read, state detection falls back to "assume no active window".
func CodexPreset() Provider {
	return Provider{
		Name:           "codex",
		Command:        "codex exec 'curfew: anchor'",
		WindowMinutes:  300,
		LogGlob:        "~/.codex/sessions/**/*.jsonl",
		TimestampField: "timestamp",
	}
}

// Presets returns the built-in provider presets keyed by name.
func Presets() map[string]Provider {
	return map[string]Provider{
		"claude": ClaudePreset(),
		"codex":  CodexPreset(),
	}
}

// Default returns a starter config: the claude and codex presets plus example
// workday schedules the user can edit.
func Default() *Config {
	work := []string{"Mon", "Tue", "Wed", "Thu", "Fri"}
	return &Config{
		General: General{Timezone: "local", PrimeDelayMinutes: 1},
		Providers: []Provider{
			ClaudePreset(),
			CodexPreset(),
		},
		Schedules: []Schedule{
			{Provider: "claude", ResetsAt: []string{"10:00", "15:00", "20:00"}, Days: work},
			{Provider: "codex", ResetsAt: []string{"13:00"}, Days: work},
		},
	}
}
