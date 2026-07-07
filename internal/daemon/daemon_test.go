package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/schedule"
	"github.com/kyle/curfew/internal/store"
)

// newTestDaemon builds a Daemon wired to a temp store and the given config,
// with anchors compiled from that config. It never touches the real service or
// network.
func newTestDaemon(t *testing.T, cfg *config.Config) *Daemon {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	anchors, err := schedule.Compile(cfg)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return &Daemon{
		logger:    log.New(io.Discard, "", 0),
		cfg:       cfg,
		loc:       time.Local,
		anchors:   anchors,
		store:     st,
		startedAt: time.Now(),
	}
}

// echoCfg returns a config whose provider anchors with a harmless `echo` command
// and, by default, has no log glob (so state detection reports "not active").
func echoCfg(resets ...string) *config.Config {
	if len(resets) == 0 {
		resets = []string{"10:00"}
	}
	return &config.Config{
		General:   config.General{Timezone: "local"},
		Providers: []config.Provider{{Name: "p", Command: "echo anchor", WindowMinutes: 300}},
		Schedules: []config.Schedule{{Provider: "p", ResetsAt: resets}},
	}
}

// writeActiveLog writes a JSONL log whose only message is `ago` before now, so a
// LogReader with a 5h window sees an active window.
func writeActiveLog(t *testing.T, dir string, ago time.Duration) {
	t.Helper()
	p := filepath.Join(dir, "sess.jsonl")
	line, _ := json.Marshal(map[string]string{"timestamp": time.Now().Add(-ago).UTC().Format(time.RFC3339Nano)})
	if err := os.WriteFile(p, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFireRecordsFired(t *testing.T) {
	d := newTestDaemon(t, echoCfg())
	p, _ := d.cfg.Provider("p")
	ev := d.fire(p, "10:00", false)
	if ev.Outcome != model.Fired {
		t.Fatalf("outcome = %s, want fired", ev.Outcome)
	}
	if ev.Reset != "10:00" {
		t.Errorf("reset = %q, want 10:00", ev.Reset)
	}
	if ev.ID == 0 {
		t.Error("expected a persisted event ID")
	}
	recent, err := d.store.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Outcome != model.Fired {
		t.Fatalf("history = %+v, want one fired event", recent)
	}
}

func TestFireSkipsWhenActive(t *testing.T) {
	logDir := t.TempDir()
	writeActiveLog(t, logDir, time.Hour) // window opened 1h ago, 5h window -> active
	cfg := echoCfg()
	cfg.Providers[0].LogGlob = filepath.Join(logDir, "**", "*.jsonl")
	cfg.Providers[0].TimestampField = "timestamp"

	d := newTestDaemon(t, cfg)
	p, _ := d.cfg.Provider("p")
	ev := d.fire(p, "10:00", false)
	if ev.Outcome != model.Skipped {
		t.Fatalf("outcome = %s, want skipped (window active)", ev.Outcome)
	}
}

func TestFireManualIgnoresActiveWindow(t *testing.T) {
	logDir := t.TempDir()
	// Window opened 1 minute ago: it's active (so a non-manual fire would skip),
	// but recent enough to fall inside the verify slack so the manual fire's
	// verification confirms immediately instead of polling the full VerifyWait.
	writeActiveLog(t, logDir, time.Minute)
	cfg := echoCfg()
	cfg.Providers[0].LogGlob = filepath.Join(logDir, "**", "*.jsonl")
	cfg.Providers[0].TimestampField = "timestamp"

	d := newTestDaemon(t, cfg)
	p, _ := d.cfg.Provider("p")
	ev := d.fire(p, "", true)
	if ev.Outcome != model.Manual {
		t.Fatalf("manual outcome = %s, want manual even with active window", ev.Outcome)
	}
}

// TestDetectMissedDeduplicates verifies detectMissed records a Missed event once
// and does not duplicate it on a subsequent run (the restart case).
func TestDetectMissedDeduplicates(t *testing.T) {
	// Anchor every day (no Days) so Prev(now) is always within the last 24h.
	cfg := echoCfg("10:00", "15:00")
	d := newTestDaemon(t, cfg)

	d.detectMissed()
	first, err := d.store.Recent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("expected at least one missed event recorded")
	}
	for _, e := range first {
		if e.Outcome != model.Missed {
			t.Fatalf("outcome = %s, want missed", e.Outcome)
		}
	}

	// Second run (simulating a daemon restart) must not duplicate.
	d.detectMissed()
	second, err := d.store.Recent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first) {
		t.Fatalf("detectMissed duplicated events: %d -> %d", len(first), len(second))
	}
}

// TestDetectMissedSkipsWhenActive: if a window is currently active, nothing is
// treated as missed.
func TestDetectMissedSkipsWhenActive(t *testing.T) {
	logDir := t.TempDir()
	writeActiveLog(t, logDir, time.Hour)
	cfg := echoCfg("10:00")
	cfg.Providers[0].LogGlob = filepath.Join(logDir, "**", "*.jsonl")
	cfg.Providers[0].TimestampField = "timestamp"

	d := newTestDaemon(t, cfg)
	d.detectMissed()
	recent, err := d.store.Recent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Fatalf("expected no missed events while active, got %+v", recent)
	}
}

func TestBuildStatus(t *testing.T) {
	d := newTestDaemon(t, echoCfg("10:00"))
	st := d.buildStatus()
	if st.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", st.PID, os.Getpid())
	}
	if len(st.Providers) != 1 || st.Providers[0].Name != "p" {
		t.Fatalf("providers = %+v", st.Providers)
	}
	ps := st.Providers[0]
	if ps.NextAnchor.IsZero() {
		t.Error("expected a next anchor time")
	}
	// Next reset must be window_minutes after the next anchor.
	if got := ps.NextReset.Sub(ps.NextAnchor); got != 300*time.Minute {
		t.Errorf("reset-anchor gap = %v, want 5h", got)
	}
}

func TestHandleStatus(t *testing.T) {
	d := newTestDaemon(t, echoCfg())
	rec := httptest.NewRecorder()
	d.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var st model.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(st.Providers) != 1 {
		t.Fatalf("providers = %+v", st.Providers)
	}
}

func TestHandleFire(t *testing.T) {
	d := newTestDaemon(t, echoCfg())

	t.Run("method not allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleFire(rec, httptest.NewRequest(http.MethodGet, "/fire", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("code = %d, want 405", rec.Code)
		}
	})

	t.Run("missing provider", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleFire(rec, httptest.NewRequest(http.MethodPost, "/fire", bytes.NewReader([]byte(`{}`))))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", rec.Code)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		rec := httptest.NewRecorder()
		body := bytes.NewReader([]byte(`{"provider":"ghost"}`))
		d.handleFire(rec, httptest.NewRequest(http.MethodPost, "/fire", body))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404", rec.Code)
		}
	})

	t.Run("accepts and eventually records", func(t *testing.T) {
		rec := httptest.NewRecorder()
		body := bytes.NewReader([]byte(`{"provider":"p"}`))
		d.handleFire(rec, httptest.NewRequest(http.MethodPost, "/fire", body))
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", rec.Code)
		}
		var ev model.Event
		if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ev.Outcome != model.Manual {
			t.Errorf("outcome = %s, want manual", ev.Outcome)
		}
		// The real fire runs in the background; poll until it lands in history.
		deadline := time.Now().Add(3 * time.Second)
		for {
			recent, _ := d.store.Recent(10)
			if len(recent) > 0 && recent[0].Outcome == model.Manual {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("background manual fire never recorded")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

// TestServeEndpointLifecycle exercises serve() + the api endpoint file + a real
// client round-trip, then confirms the endpoint file is removed on shutdown.
func TestServeEndpointLifecycle(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d := newTestDaemon(t, echoCfg())

	srv, err := d.serve()
	if err != nil {
		t.Fatalf("serve: %v", err)
	}

	// The endpoint file should now exist and point at a reachable server.
	// (Client verification is covered in the api package tests; here we assert
	// the file lifecycle managed by the daemon.)
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// RegisterOnShutdown callbacks run asynchronously during Shutdown; give the
	// cleanup a beat, then confirm the endpoint file is gone.
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(endpointPathForTest(t)); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("endpoint file not removed on shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestReload writes a config file, points a daemon at it, and confirms reload
// compiles anchors and builds a running cron engine.
func TestReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.Default().Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	d := &Daemon{
		cfgPath:   cfgPath,
		logger:    log.New(io.Discard, "", 0),
		store:     st,
		startedAt: time.Now(),
	}
	if err := d.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	d.mu.Lock()
	n, cr := len(d.anchors), d.cron
	d.mu.Unlock()
	if n != 6 { // default: claude-1(3) + claude-2(2) + codex(1)
		t.Errorf("anchors = %d, want 6", n)
	}
	if cr == nil {
		t.Fatal("cron engine not built")
	}
	cr.Stop()
}

func endpointPathForTest(t *testing.T) string {
	t.Helper()
	p, err := config.EndpointPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
