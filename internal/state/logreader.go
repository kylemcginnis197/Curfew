package state

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LogReader reconstructs the current window from JSONL session logs matched by a
// glob that may contain a "**" recursive segment (e.g. ".../projects/**/*.jsonl").
type LogReader struct {
	Glob           string        // tilde already expanded
	TimestampField string        // JSON field carrying each message timestamp
	Window         time.Duration // provider window length
}

// Current implements Reader. It reads timestamps from recently-modified log
// files, groups them into windows the way usage tools do (a new window begins
// when more than Window has elapsed since the current window's start or since
// the previous message), and reports the last window's state.
func (r *LogReader) Current(now time.Time) (Window, error) {
	field := r.TimestampField
	if field == "" {
		field = "timestamp"
	}
	files, err := globFiles(r.Glob)
	if err != nil {
		return Window{}, err
	}

	// Only files touched within the last Window (plus slack) can contain
	// messages belonging to the currently-active window. This bounds the work
	// on large log directories.
	cutoff := now.Add(-r.Window - time.Minute)
	var stamps []time.Time
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		ts, err := readTimestamps(f, field)
		if err != nil {
			continue // unreadable file: skip, don't fail the whole read
		}
		stamps = append(stamps, ts...)
	}
	if len(stamps) == 0 {
		return Window{}, nil
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].Before(stamps[j]) })

	// Walk forward, restarting a window when the gap since its start or since
	// the previous message exceeds Window. The last window is the current one.
	winStart := stamps[0]
	last := stamps[0]
	for _, t := range stamps[1:] {
		if t.Sub(winStart) >= r.Window || t.Sub(last) >= r.Window {
			winStart = t
		}
		last = t
	}

	w := Window{
		Start:        winStart,
		End:          winStart.Add(r.Window),
		LastActivity: last,
	}
	// The limit window resets Window after it starts, regardless of whether
	// activity continued, so activeness depends only on now vs [Start, End).
	w.Active = !now.Before(w.Start) && now.Before(w.End)
	return w, nil
}

// readTimestamps extracts the timestamp field from every JSON line in a file.
func readTimestamps(path, field string) ([]time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // some log lines are large
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		// Decode only the timestamp field to keep parsing cheap.
		var rec map[string]json.RawMessage
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		raw, ok := rec[field]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if t, ok := parseTime(s); ok {
			out = append(out, t)
		}
	}
	return out, sc.Err()
}

// parseTime accepts the timestamp formats these logs use.
func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// globFiles resolves a glob that may contain a single "**" recursive segment,
// which the stdlib filepath.Glob does not support. Patterns without "**" are
// passed straight through to filepath.Glob.
func globFiles(pattern string) ([]string, error) {
	idx := strings.Index(pattern, "**")
	if idx < 0 {
		return filepath.Glob(pattern)
	}
	// Split into a literal root before "**" and a trailing basename pattern
	// after it (e.g. root=".../projects", tail="*.jsonl").
	root := filepath.Dir(pattern[:idx])
	tail := filepath.Base(pattern)

	var matches []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees
		}
		if d.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(tail, d.Name()); ok {
			matches = append(matches, p)
		}
		return nil
	})
	return matches, nil
}
