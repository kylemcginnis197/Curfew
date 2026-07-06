package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/model"
)

func TestRecordAndRecent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	ws := base.Add(5 * time.Minute)
	if _, err := s.Record(model.Event{Time: base, Provider: "claude-1", Reset: "10:00", Outcome: model.Fired, WindowStart: ws}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(model.Event{Time: base.Add(time.Minute), Provider: "claude-2", Outcome: model.Skipped, Detail: "already active"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d", len(got))
	}
	// Newest first.
	if got[0].Provider != "claude-2" || got[0].Outcome != model.Skipped {
		t.Errorf("order/fields wrong: %+v", got[0])
	}
	if got[1].Provider != "claude-1" || got[1].Reset != "10:00" {
		t.Errorf("fields not preserved: %+v", got[1])
	}
	if !got[1].WindowStart.Equal(ws) {
		t.Errorf("window_start = %v, want %v", got[1].WindowStart, ws)
	}
	if got[1].ID == 0 {
		t.Error("expected assigned ID")
	}
}

func TestRecentLimit(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "h.db"))
	defer s.Close()
	for i := 0; i < 5; i++ {
		s.Record(model.Event{Time: time.Now().Add(time.Duration(i) * time.Second), Provider: "p", Outcome: model.Fired})
	}
	got, _ := s.Recent(3)
	if len(got) != 3 {
		t.Fatalf("limit not honored: got %d", len(got))
	}
}
