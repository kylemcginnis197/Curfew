package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	orig := Default()
	if err := orig.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Providers) != 2 || got.Providers[0].Name != "claude" {
		t.Fatalf("providers not preserved: %+v", got.Providers)
	}
	if got.Providers[0].WindowMinutes != 300 {
		t.Errorf("window_minutes = %d, want 300", got.Providers[0].WindowMinutes)
	}
	if got.Providers[0].Command != "claude -p 'curfew: anchor' --model haiku" {
		t.Errorf("command not preserved: %q", got.Providers[0].Command)
	}
	if len(got.Schedules) != 2 {
		t.Fatalf("schedules not preserved: %+v", got.Schedules)
	}
	if got.General.PrimeDelayMinutes != 1 {
		t.Errorf("prime_delay_minutes = %d, want 1", got.General.PrimeDelayMinutes)
	}
}

func TestPrimeDelayDefault(t *testing.T) {
	cases := []struct {
		set  int
		want int
	}{
		{0, 1},  // unset -> default
		{-3, 1}, // nonsense -> default
		{1, 1},
		{5, 5},
	}
	for _, tc := range cases {
		g := General{PrimeDelayMinutes: tc.set}
		if got := g.PrimeDelay(); got != tc.want {
			t.Errorf("PrimeDelay() with %d = %d, want %d", tc.set, got, tc.want)
		}
	}
}

func TestAutoPrimeEnabled(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		set  *bool
		want bool
	}{
		{nil, true}, // unset -> enabled (back-compat)
		{&yes, true},
		{&no, false},
	}
	for _, tc := range cases {
		g := General{AutoPrime: tc.set}
		if got := g.AutoPrimeEnabled(); got != tc.want {
			t.Errorf("AutoPrimeEnabled() with %v = %v, want %v", tc.set, got, tc.want)
		}
	}
}

func TestAutoPrimeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Default leaves auto_prime unset; a load of the saved file stays enabled.
	orig := Default()
	if err := orig.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.General.AutoPrimeEnabled() {
		t.Fatal("absent auto_prime should stay enabled")
	}

	// An explicit false survives the round trip.
	off := false
	orig.General.AutoPrime = &off
	if err := orig.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.General.AutoPrime == nil || *got.General.AutoPrime {
		t.Fatalf("auto_prime = %v, want explicit false", got.General.AutoPrime)
	}
	if got.General.AutoPrimeEnabled() {
		t.Fatal("explicit false should disable priming")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if len(got.Providers) == 0 {
		t.Fatal("expected default providers")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"ok", func(*Config) {}, false},
		{"unknown provider", func(c *Config) { c.Schedules[0].Provider = "ghost" }, true},
		{"bad reset time", func(c *Config) { c.Schedules[0].ResetsAt = []string{"25:00"} }, true},
		{"bad day", func(c *Config) { c.Schedules[0].Days = []string{"Funday"} }, true},
		{"empty resets", func(c *Config) { c.Schedules[0].ResetsAt = nil }, true},
		{"zero window", func(c *Config) { c.Providers[0].WindowMinutes = 0 }, true},
		{"dup provider", func(c *Config) { c.Providers = append(c.Providers, c.Providers[0]) }, true},
		{"bad tz", func(c *Config) { c.General.Timezone = "Mars/Olympus" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestWeekdays(t *testing.T) {
	s := Schedule{Days: []string{"Mon", "wed", "FRI"}}
	got := s.Weekdays()
	want := []time.Weekday{time.Monday, time.Wednesday, time.Friday}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if all := (Schedule{}).Weekdays(); len(all) != 7 {
		t.Fatalf("empty days should be all 7, got %d", len(all))
	}
}

func TestLocation(t *testing.T) {
	c := Default()
	if loc, err := c.Location(); err != nil || loc != time.Local {
		t.Fatalf("local: loc=%v err=%v", loc, err)
	}
	c.General.Timezone = "America/New_York"
	if _, err := c.Location(); err != nil {
		t.Fatalf("iana: %v", err)
	}
}
