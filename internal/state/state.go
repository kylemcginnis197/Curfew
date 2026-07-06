// Package state reconstructs a provider's current usage window from its local
// session logs, so the daemon can avoid firing a redundant anchor when a window
// is already active.
package state

import (
	"time"

	"github.com/kyle/curfew/internal/config"
)

// Window describes a usage window reconstructed from logs.
type Window struct {
	Start        time.Time // first message of the current window (the anchor time)
	End          time.Time // Start + provider window length (the reset time)
	LastActivity time.Time // most recent message seen
	Active       bool      // true when now is within [Start, End)
}

// Reader reports the current usage window for a provider.
type Reader interface {
	// Current returns the active window as of now. When no window is active (no
	// recent logs, or logs unreadable) it returns Window{Active: false} and a
	// nil error — absence of state is not an error.
	Current(now time.Time) (Window, error)
}

// For returns the appropriate Reader for a provider. Providers with a LogGlob
// use log-based detection; those without fall back to NoopReader (always
// "no active window"), so anchoring still works for tools we can't observe.
func For(p config.Provider) Reader {
	if p.LogGlob == "" {
		return NoopReader{}
	}
	return &LogReader{
		Glob:           config.ExpandTilde(p.LogGlob),
		TimestampField: p.TimestampField,
		Window:         time.Duration(p.WindowMinutes) * time.Minute,
	}
}

// NoopReader always reports no active window.
type NoopReader struct{}

// Current implements Reader.
func (NoopReader) Current(time.Time) (Window, error) { return Window{}, nil }
