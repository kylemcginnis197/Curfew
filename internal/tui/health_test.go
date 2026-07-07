package tui

import (
	"testing"

	"github.com/kyle/curfew/internal/model"
)

func TestProviderHealth(t *testing.T) {
	ev := func(provider, reset string, o model.Outcome) model.Event {
		return model.Event{Provider: provider, Reset: reset, Outcome: o}
	}

	tests := []struct {
		name   string
		who    string
		events []model.Event // newest-first, as store.Recent returns
		want   health
	}{
		{
			name:   "fired scheduled reset is healthy",
			who:    "claude-1",
			events: []model.Event{ev("claude-1", "10:00", model.Fired)},
			want:   healthOK,
		},
		{
			name:   "skipped (already active) is healthy",
			who:    "claude-1",
			events: []model.Event{ev("claude-1", "10:00", model.Skipped)},
			want:   healthOK,
		},
		{
			name:   "failed scheduled reset is unhealthy",
			who:    "claude-1",
			events: []model.Event{ev("claude-1", "10:00", model.Failed)},
			want:   healthBad,
		},
		{
			name:   "missed scheduled reset is unhealthy",
			who:    "claude-1",
			events: []model.Event{ev("claude-1", "10:00", model.Missed)},
			want:   healthBad,
		},
		{
			name:   "no history is unknown",
			who:    "claude-1",
			events: nil,
			want:   healthUnknown,
		},
		{
			name:   "manual fires (no reset) are ignored -> unknown",
			who:    "claude-1",
			events: []model.Event{ev("claude-1", "", model.Manual)},
			want:   healthUnknown,
		},
		{
			name: "newest scheduled event wins over older",
			who:  "claude-1",
			events: []model.Event{
				ev("claude-1", "10:00", model.Fired),  // newest
				ev("claude-1", "10:00", model.Missed), // older
			},
			want: healthOK,
		},
		{
			name: "manual fire before the last scheduled reset is skipped",
			who:  "claude-1",
			events: []model.Event{
				ev("claude-1", "", model.Manual),      // newest, ignored
				ev("claude-1", "10:00", model.Missed), // most recent scheduled
			},
			want: healthBad,
		},
		{
			name:   "only other providers' events -> unknown",
			who:    "claude-1",
			events: []model.Event{ev("codex", "13:00", model.Fired)},
			want:   healthUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerHealth(tt.who, tt.events); got != tt.want {
				t.Errorf("providerHealth(%q) = %v, want %v", tt.who, got, tt.want)
			}
		})
	}
}
