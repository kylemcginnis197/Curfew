package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeLog writes a JSONL file of {"timestamp": ...} records at the given times.
func writeLog(t *testing.T, path string, times ...time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, tm := range times {
		if err := enc.Encode(map[string]string{"timestamp": tm.UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	// Ensure ModTime is recent so the reader's cutoff doesn't skip it.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

func newReader(dir string) *LogReader {
	return &LogReader{
		Glob:           filepath.Join(dir, "projects", "**", "*.jsonl"),
		TimestampField: "timestamp",
		Window:         300 * time.Minute,
	}
}

func TestActiveWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	start := now.Add(-1 * time.Hour)
	writeLog(t, filepath.Join(dir, "projects", "a", "s.jsonl"), start, now.Add(-30*time.Minute))

	w, err := newReader(dir).Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Active {
		t.Fatal("expected active window")
	}
	if d := w.Start.Sub(start); d < -time.Second || d > time.Second {
		t.Errorf("start = %v, want ~%v", w.Start, start)
	}
	if d := w.End.Sub(start.Add(300 * time.Minute)); d < -time.Second || d > time.Second {
		t.Errorf("end = %v, want start+5h", w.End)
	}
}

func TestExpiredWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Last activity 6h ago -> window (5h) has reset; file modtime is recent so
	// it isn't skipped by the cutoff, exercising the real active/inactive math.
	writeLog(t, filepath.Join(dir, "projects", "a", "s.jsonl"), now.Add(-6*time.Hour))

	w, err := newReader(dir).Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if w.Active {
		t.Fatalf("expected inactive window, got %+v", w)
	}
}

func TestNewWindowAfterGap(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-10 * time.Hour)
	recent := now.Add(-45 * time.Minute)
	writeLog(t, filepath.Join(dir, "projects", "a", "s.jsonl"), old, recent)

	w, err := newReader(dir).Current(now)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Active {
		t.Fatal("expected active window from recent activity")
	}
	if d := w.Start.Sub(recent); d < -time.Second || d > time.Second {
		t.Errorf("window start = %v, want ~%v (post-gap)", w.Start, recent)
	}
}

func TestNoLogs(t *testing.T) {
	w, err := newReader(t.TempDir()).Current(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w.Active {
		t.Fatal("expected no active window with no logs")
	}
}

func TestBoundaryExactlyAtWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Start exactly Window ago: End == now, so now is NOT before End -> inactive.
	writeLog(t, filepath.Join(dir, "projects", "s.jsonl"), now.Add(-300*time.Minute))
	w, _ := newReader(dir).Current(now)
	if w.Active {
		t.Fatal("window starting exactly Window ago should be inactive")
	}
}

func TestGlobFilesRecursive(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, filepath.Join(dir, "projects", "x", "y", "deep.jsonl"), time.Now())
	writeLog(t, filepath.Join(dir, "projects", "top.jsonl"), time.Now())
	got, err := globFiles(filepath.Join(dir, "projects", "**", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(got), got)
	}
}
