// Package model holds the domain types shared across the store, trigger,
// daemon, API, and TUI. It is a leaf package with no internal dependencies to
// keep those consumers free of import cycles.
package model

import "time"

// Outcome is the result of an anchor attempt.
type Outcome string

const (
	// Fired means the anchor command ran successfully.
	Fired Outcome = "fired"
	// Failed means the command errored on every retry.
	Failed Outcome = "failed"
	// Skipped means a window was already active, so no anchor was needed.
	Skipped Outcome = "skipped"
	// Missed means the anchor time passed while the daemon wasn't running
	// (machine asleep/off) and was detected late.
	Missed Outcome = "missed"
	// Manual marks an anchor fired on demand via "fire now".
	Manual Outcome = "manual"
)

// Event is one recorded anchor attempt, persisted to history.
type Event struct {
	ID          int64     `json:"id"`
	Time        time.Time `json:"time"`
	Provider    string    `json:"provider"`
	Reset       string    `json:"reset,omitempty"`   // reset boundary served (HH:MM)
	Outcome     Outcome   `json:"outcome"`
	Detail      string    `json:"detail,omitempty"`  // error or skip reason
	WindowStart time.Time `json:"window_start,omitempty"`
}

// ProviderState is the live state of one provider for the dashboard.
type ProviderState struct {
	Name        string    `json:"name"`
	Active      bool      `json:"active"`
	WindowStart time.Time `json:"window_start,omitempty"`
	WindowEnd   time.Time `json:"window_end,omitempty"`
	NextAnchor  time.Time `json:"next_anchor,omitempty"`
	NextReset   time.Time `json:"next_reset,omitempty"`
}

// Status is the full snapshot the daemon serves to the TUI.
type Status struct {
	Now       time.Time       `json:"now"`
	Timezone  string          `json:"timezone"`
	PID       int             `json:"pid"`
	Providers []ProviderState `json:"providers"`
	Recent    []Event         `json:"recent"`
}
