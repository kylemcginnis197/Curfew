// Package config defines Curfew's on-disk configuration (config.toml): the
// general settings, provider definitions, and reset schedules. It also handles
// loading, saving, validation, and built-in provider presets.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the root of config.toml.
type Config struct {
	General   General    `toml:"general"`
	Providers []Provider `toml:"provider"`
	Schedules []Schedule `toml:"schedule"`
}

// General holds top-level settings.
type General struct {
	// Timezone is "local" or an IANA name (e.g. "America/New_York"). Anchor
	// times are computed in this zone so resets land on the wall-clock the user
	// expects, DST included.
	Timezone string `toml:"timezone"`
}

// Provider describes how to anchor and observe one CLI tool's usage window.
type Provider struct {
	// Name is the unique key referenced by schedules (e.g. "claude").
	Name string `toml:"name"`
	// Command is a shell command line executed (via the platform shell) to anchor
	// a fresh window — exactly what you'd type in a terminal, e.g.
	//   claude -p 'curfew: anchor' --model haiku
	// It may include quotes, environment prefixes, and pipes.
	Command string `toml:"command"`
	// Env sets extra environment variables for the anchor command. This is how
	// multiple subscriptions of the same CLI are separated: e.g. two Claude
	// providers each set CLAUDE_CONFIG_DIR to a different logged-in config dir.
	// Values may use ~ (expanded before exec).
	Env map[string]string `toml:"env"`
	// WindowMinutes is the rolling-window length used to back-compute anchor
	// times from reset times and to judge whether a window is currently active.
	WindowMinutes int `toml:"window_minutes"`
	// LogGlob points at the provider's local session logs for state detection.
	// Written with ~ for portability; expand with ExpandTilde before use.
	LogGlob string `toml:"log_glob"`
	// TimestampField is the JSON field holding each message's timestamp in the
	// provider's JSONL logs.
	TimestampField string `toml:"timestamp_field"`
}

// Schedule maps a set of desired reset boundaries onto a provider.
type Schedule struct {
	// Provider references a Provider.Name.
	Provider string `toml:"provider"`
	// ResetsAt is the list of "HH:MM" wall-clock times the user wants fresh
	// capacity. Curfew fires an anchor WindowMinutes earlier for each.
	ResetsAt []string `toml:"resets_at"`
	// Days limits the schedule to specific weekdays (Mon..Sun). Empty = all days.
	Days []string `toml:"days"`
}

var (
	hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)
	// validDays maps the accepted three-letter weekday tokens (case-insensitive).
	validDays = map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
		"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday,
		"sat": time.Saturday,
	}
)

// Provider returns the named provider, or false if absent.
func (c *Config) Provider(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// Location resolves General.Timezone to a *time.Location. "" or "local" yields
// time.Local.
func (c *Config) Location() (*time.Location, error) {
	tz := strings.TrimSpace(c.General.Timezone)
	if tz == "" || strings.EqualFold(tz, "local") {
		return time.Local, nil
	}
	return time.LoadLocation(tz)
}

// Validate checks referential integrity and field formats, returning the first
// problem found.
func (c *Config) Validate() error {
	if _, err := c.Location(); err != nil {
		return fmt.Errorf("general.timezone: %w", err)
	}
	seen := map[string]bool{}
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("provider[%d]: name is required", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("provider %q: duplicate name", p.Name)
		}
		seen[p.Name] = true
		if strings.TrimSpace(p.Command) == "" {
			return fmt.Errorf("provider %q: command is required", p.Name)
		}
		if p.WindowMinutes <= 0 {
			return fmt.Errorf("provider %q: window_minutes must be > 0", p.Name)
		}
	}
	for i, s := range c.Schedules {
		if _, ok := c.Provider(s.Provider); !ok {
			return fmt.Errorf("schedule[%d]: unknown provider %q", i, s.Provider)
		}
		if len(s.ResetsAt) == 0 {
			return fmt.Errorf("schedule[%d] (%s): resets_at is empty", i, s.Provider)
		}
		for _, r := range s.ResetsAt {
			if !hhmmRe.MatchString(r) {
				return fmt.Errorf("schedule[%d] (%s): invalid reset time %q, want HH:MM", i, s.Provider, r)
			}
		}
		for _, d := range s.Days {
			if _, ok := validDays[strings.ToLower(d)]; !ok {
				return fmt.Errorf("schedule[%d] (%s): invalid day %q", i, s.Provider, d)
			}
		}
	}
	return nil
}

// Weekdays converts a schedule's Days tokens to time.Weekday values. An empty
// list returns all seven days.
func (s Schedule) Weekdays() []time.Weekday {
	if len(s.Days) == 0 {
		return []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	}
	out := make([]time.Weekday, 0, len(s.Days))
	for _, d := range s.Days {
		out = append(out, validDays[strings.ToLower(d)])
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Load reads and validates config.toml from the given path. If the file does
// not exist it returns a Default() config without writing it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Save validates and atomically writes the config to path.
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("# Curfew configuration. Edit here or via the TUI.\n")
	sb.WriteString("# resets_at are the wall-clock times you want fresh capacity;\n")
	sb.WriteString("# Curfew fires an anchor window_minutes earlier for each.\n\n")
	enc := toml.NewEncoder(&sb)
	if err := enc.Encode(c); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
