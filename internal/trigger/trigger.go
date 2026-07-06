// Package trigger executes a provider's anchor command, verifies a fresh window
// actually started, and retries with backoff on failure.
package trigger

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/state"
)

// Trigger anchors a single provider.
type Trigger struct {
	Provider config.Provider
	Reader   state.Reader
	// Backoff delays between retries; len(Backoff)+1 total attempts.
	Backoff []time.Duration
	// VerifyWait is how long to poll logs for the new window after the command
	// exits before giving up on verification.
	VerifyWait time.Duration
	// Now allows tests to inject a clock.
	Now func() time.Time
}

// New builds a Trigger with sensible defaults for a provider.
func New(p config.Provider) *Trigger {
	// Only wait to verify a new window when the provider exposes logs to read;
	// otherwise verification can never confirm and would just stall.
	verify := 15 * time.Second
	if p.LogGlob == "" {
		verify = 0
	}
	return &Trigger{
		Provider:   p,
		Reader:     state.For(p),
		Backoff:    []time.Duration{30 * time.Second, 2 * time.Minute, 8 * time.Minute},
		VerifyWait: verify,
		Now:        time.Now,
	}
}

// Fire anchors the provider. When manual is false it first checks state and
// returns a Skipped event if a window is already active. It returns a fully
// populated Event describing the outcome.
func (t *Trigger) Fire(ctx context.Context, manual bool) model.Event {
	now := t.Now()
	ev := model.Event{Time: now, Provider: t.Provider.Name}
	if manual {
		ev.Outcome = model.Manual
	}

	// State-aware skip: a redundant anchor wastes nothing to avoid.
	if !manual {
		if w, err := t.Reader.Current(now); err == nil && w.Active {
			ev.Outcome = model.Skipped
			ev.Detail = fmt.Sprintf("window already active since %s (resets %s)",
				w.Start.Format("15:04"), w.End.Format("15:04"))
			ev.WindowStart = w.Start
			return ev
		}
	}

	fireStart := t.Now()
	var lastErr string
	attempts := len(t.Backoff) + 1
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				ev.Outcome = model.Failed
				ev.Detail = "cancelled: " + lastErr
				return ev
			case <-time.After(t.Backoff[i-1]):
			}
		}
		out, err := t.run(ctx)
		if err == nil {
			// Command succeeded; try to verify a new window appeared.
			ws, ok := t.verify(fireStart)
			if manual {
				ev.Outcome = model.Manual
			} else {
				ev.Outcome = model.Fired
			}
			if ok {
				ev.WindowStart = ws
			} else {
				ev.Detail = "started (window not yet visible in logs)"
			}
			return ev
		}
		lastErr = trim(err.Error() + ": " + out)
	}

	ev.Outcome = model.Failed
	ev.Detail = fmt.Sprintf("after %d attempts: %s", attempts, lastErr)
	return ev
}

// run executes the anchor command once, returning combined output.
func (t *Trigger) run(ctx context.Context) (string, error) {
	cmd := t.Provider.Command
	if len(cmd) == 0 {
		return "", fmt.Errorf("provider %s has no command", t.Provider.Name)
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Env = t.env()
	out, err := c.CombinedOutput()
	return string(out), err
}

// env merges the process environment with the provider's Env (expanding ~),
// which is how multiple subscriptions of one CLI are separated.
func (t *Trigger) env() []string {
	env := os.Environ()
	for k, v := range t.Provider.Env {
		env = append(env, k+"="+config.ExpandTilde(v))
	}
	return env
}

// verify polls the reader for a window whose start is at/after the fire time,
// confirming the anchor really started a fresh window.
func (t *Trigger) verify(fireStart time.Time) (time.Time, bool) {
	if t.VerifyWait <= 0 {
		return time.Time{}, false // provider has no observable state to verify
	}
	deadline := t.Now().Add(t.VerifyWait)
	slack := 2 * time.Minute // tolerate small clock/log skew
	for {
		if w, err := t.Reader.Current(t.Now()); err == nil && w.Active {
			if !w.Start.Before(fireStart.Add(-slack)) {
				return w.Start, true
			}
		}
		if t.Now().After(deadline) {
			return time.Time{}, false
		}
		time.Sleep(time.Second)
	}
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	const max = 500
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
