package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
)

// TestModtimeCutoffSkipsStaleFile verifies that a file last modified before the
// cutoff (older than Window+slack) is ignored, even if it contains a timestamp
// that would otherwise look active.
func TestModtimeCutoffSkipsStaleFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := filepath.Join(dir, "projects", "a", "s.jsonl")
	// The content claims a message 1h ago (would be "active"), but we backdate the
	// file's modtime well beyond the cutoff so the reader skips it.
	writeLog(t, path, now.Add(-time.Hour))
	old := now.Add(-8 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	w, err := newReader(dir).Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if w.Active {
		t.Fatalf("stale-modtime file should be skipped -> inactive, got %+v", w)
	}
}

// TestMultipleFilesMerged confirms timestamps from several files are merged and
// sorted so the reconstructed window spans them.
func TestMultipleFilesMerged(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	writeLog(t, filepath.Join(dir, "projects", "a", "one.jsonl"), start)
	writeLog(t, filepath.Join(dir, "projects", "b", "two.jsonl"), now.Add(-30*time.Minute))

	w, err := newReader(dir).Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Active {
		t.Fatal("expected active window merged across files")
	}
	if d := w.Start.Sub(start); d < -time.Second || d > time.Second {
		t.Errorf("start = %v, want ~%v (earliest across files)", w.Start, start)
	}
	if d := w.LastActivity.Sub(now.Add(-30 * time.Minute)); d < -time.Second || d > time.Second {
		t.Errorf("last activity = %v, want ~30m ago", w.LastActivity)
	}
}

// TestCustomTimestampField confirms a non-default field name is honored.
func TestCustomTimestampField(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := filepath.Join(dir, "projects", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"ts":"` + now.Add(-time.Hour).UTC().Format(time.RFC3339Nano) + `"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &LogReader{Glob: filepath.Join(dir, "projects", "**", "*.jsonl"), TimestampField: "ts", Window: 300 * time.Minute}
	w, err := r.Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Active {
		t.Fatalf("expected active window from custom field, got %+v", w)
	}
}

// TestReadTimestampsSkipsBadLines confirms non-JSON and mismatched lines are
// tolerated while valid ones are still read.
func TestReadTimestampsSkipsBadLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := filepath.Join(dir, "projects", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "not json\n" +
		`{"timestamp":"not-a-time"}` + "\n" +
		`{"other":"field"}` + "\n" +
		`{"timestamp":"` + now.Add(-time.Hour).UTC().Format(time.RFC3339Nano) + `"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := newReader(dir).Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Active {
		t.Fatalf("expected the one valid line to yield an active window, got %+v", w)
	}
}

func TestParseTimeLayouts(t *testing.T) {
	cases := []string{
		"2026-07-06T12:00:00Z",
		"2026-07-06T12:00:00.123456789Z",
		"2026-07-06T12:00:00-04:00",
	}
	for _, s := range cases {
		if _, ok := parseTime(s); !ok {
			t.Errorf("parseTime(%q) failed, want success", s)
		}
	}
	if _, ok := parseTime("07/06/2026"); ok {
		t.Error("parseTime should reject non-RFC3339 input")
	}
}

func TestNoopReader(t *testing.T) {
	w, err := NoopReader{}.Current(time.Now())
	if err != nil || w.Active {
		t.Fatalf("noop = %+v err=%v, want inactive/no error", w, err)
	}
}

func TestForSelectsReader(t *testing.T) {
	if _, ok := For(config.Provider{Name: "x"}).(NoopReader); !ok {
		t.Error("provider without LogGlob should get a NoopReader")
	}
	if _, ok := For(config.Provider{Name: "x", LogGlob: "~/logs/*.jsonl", WindowMinutes: 300}).(*LogReader); !ok {
		t.Error("provider with LogGlob should get a *LogReader")
	}
}
