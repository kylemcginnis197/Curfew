package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestRecordFillsZeroTime confirms Record stamps a time when none is supplied,
// and leaves a zero WindowStart zero on read-back.
func TestRecordFillsZeroTime(t *testing.T) {
	s := openTestStore(t)
	before := time.Now().Add(-time.Second)
	rec, err := s.Record(model.Event{Provider: "p", Outcome: model.Fired}) // no Time
	if err != nil {
		t.Fatal(err)
	}
	if rec.Time.Before(before) {
		t.Errorf("Record did not fill Time: %v", rec.Time)
	}
	got, err := s.Recent(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Time.IsZero() {
		t.Fatalf("stored event has zero time: %+v", got)
	}
	if !got[0].WindowStart.IsZero() {
		t.Errorf("window_start = %v, want zero", got[0].WindowStart)
	}
}

// TestRecordEmptyResetAndDetail confirms empty reset/detail (stored via NULLable
// columns) round-trip back to empty strings, not errors.
func TestRecordEmptyResetAndDetail(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Record(model.Event{Time: time.Now(), Provider: "p", Outcome: model.Missed}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Recent(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].Reset != "" || got[0].Detail != "" {
		t.Errorf("empty reset/detail not preserved: %+v", got[0])
	}
	if got[0].Outcome != model.Missed {
		t.Errorf("outcome = %s, want missed", got[0].Outcome)
	}
}

// TestRecentDefaultLimit confirms a non-positive limit falls back to the default.
func TestRecentDefaultLimit(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Record(model.Event{Time: time.Now().Add(time.Duration(i) * time.Second), Provider: "p", Outcome: model.Fired}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Recent(0) // <= 0 -> default 50
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("default limit should return all 3, got %d", len(got))
	}
}

// TestRecentEmpty confirms an empty DB returns no rows and no error.
func TestRecentEmpty(t *testing.T) {
	s := openTestStore(t)
	got, err := s.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no events, got %d", len(got))
	}
}
