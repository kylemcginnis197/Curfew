package trigger

import (
	"context"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/state"
)

// stubReader lets tests control the reported window state.
type stubReader struct{ w state.Window }

func (s stubReader) Current(time.Time) (state.Window, error) { return s.w, nil }

func echoProvider() config.Provider {
	return config.Provider{Name: "t", Command: "echo hi"}
}

func TestSkipWhenActive(t *testing.T) {
	tr := &Trigger{
		Provider: echoProvider(),
		Reader:   stubReader{w: state.Window{Active: true, Start: time.Now(), End: time.Now().Add(time.Hour)}},
		Now:      time.Now,
	}
	ev := tr.Fire(context.Background(), false)
	if ev.Outcome != model.Skipped {
		t.Fatalf("outcome = %s, want skipped", ev.Outcome)
	}
}

func TestManualBypassesSkip(t *testing.T) {
	tr := &Trigger{
		Provider: echoProvider(),
		Reader:   stubReader{w: state.Window{Active: true}},
		Now:      time.Now,
	}
	ev := tr.Fire(context.Background(), true)
	if ev.Outcome != model.Manual {
		t.Fatalf("manual fire outcome = %s, want manual", ev.Outcome)
	}
}

func TestFireSuccessUnverified(t *testing.T) {
	tr := &Trigger{
		Provider:   echoProvider(),
		Reader:     stubReader{}, // never active -> verify can't confirm
		VerifyWait: 0,
		Now:        time.Now,
	}
	ev := tr.Fire(context.Background(), false)
	if ev.Outcome != model.Fired {
		t.Fatalf("outcome = %s, want fired", ev.Outcome)
	}
	if ev.Detail == "" {
		t.Errorf("expected an 'unverified' detail note")
	}
}

func TestFireVerified(t *testing.T) {
	start := time.Now()
	tr := &Trigger{
		Provider:   echoProvider(),
		Reader:     stubReader{w: state.Window{Active: true, Start: start, End: start.Add(time.Hour)}},
		VerifyWait: time.Second,
		Now:        time.Now,
	}
	ev := tr.Fire(context.Background(), true)
	if ev.Outcome != model.Manual {
		t.Fatalf("outcome = %s, want manual", ev.Outcome)
	}
	if ev.WindowStart.IsZero() {
		t.Errorf("expected verified window start to be set")
	}
}

func TestFireFailure(t *testing.T) {
	tr := &Trigger{
		Provider: config.Provider{Name: "bad", Command: "this-binary-does-not-exist-xyz"},
		Reader:   stubReader{},
		Backoff:  nil, // single attempt, no waiting
		Now:      time.Now,
	}
	ev := tr.Fire(context.Background(), false)
	if ev.Outcome != model.Failed {
		t.Fatalf("outcome = %s, want failed", ev.Outcome)
	}
}

func TestEnvExpansion(t *testing.T) {
	tr := &Trigger{Provider: config.Provider{
		Name: "e", Command: "true",
		Env: map[string]string{"CLAUDE_CONFIG_DIR": "~/.claude-2"},
	}}
	env := tr.env()
	found := false
	for _, kv := range env {
		if kv == "CLAUDE_CONFIG_DIR="+config.ExpandTilde("~/.claude-2") {
			found = true
		}
	}
	if !found {
		t.Fatal("provider env not merged/expanded into command environment")
	}
}
