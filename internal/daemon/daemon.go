// Package daemon runs Curfew's scheduler: it registers compiled anchors with an
// internal cron engine, fires them state-aware, records outcomes, and serves a
// localhost API for the TUI/CLI. Config changes are hot-reloaded.
package daemon

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kyle/curfew/internal/api"
	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/schedule"
	"github.com/kyle/curfew/internal/store"
	"github.com/kyle/curfew/internal/trigger"
	"github.com/robfig/cron/v3"
)

// Daemon owns the running scheduler and its shared state.
type Daemon struct {
	cfgPath string
	dbPath  string
	logger  *log.Logger

	mu      sync.Mutex
	cfg     *config.Config
	loc     *time.Location
	anchors []schedule.Anchor
	cron    *cron.Cron

	store     *store.Store
	startedAt time.Time
}

// Run starts the daemon and blocks until ctx is cancelled, then shuts down
// cleanly (stops cron, closes the API server and store, removes the endpoint
// file).
func Run(ctx context.Context) error {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return err
	}
	dbPath, err := config.DBPath()
	if err != nil {
		return err
	}
	// Write a default config on first run so the user has something to edit.
	if _, statErr := os.Stat(cfgPath); os.IsNotExist(statErr) {
		if err := config.Default().Save(cfgPath); err != nil {
			return err
		}
	}

	d := &Daemon{
		cfgPath:   cfgPath,
		dbPath:    dbPath,
		logger:    log.New(os.Stderr, "curfew: ", log.LstdFlags),
		startedAt: time.Now(),
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	d.store = st
	defer d.store.Close()

	if err := d.reload(); err != nil {
		return err
	}
	d.logger.Printf("loaded %d anchors from %s", len(d.anchors), cfgPath)

	// Catch up on anchors missed while the daemon wasn't running.
	d.detectMissed()

	// Serve the localhost API; this also writes the endpoint file.
	srv, err := d.serve()
	if err != nil {
		return err
	}

	// Hot-reload on config changes.
	stopWatch := d.watchConfig()

	<-ctx.Done()
	d.logger.Println("shutting down")

	stopWatch()
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	// Remove the endpoint file synchronously: srv.RegisterOnShutdown runs its
	// callback in a goroutine that can lose the race with process exit, leaving a
	// stale daemon.json that makes the TUI report a dead port.
	api.RemoveEndpoint()
	d.mu.Lock()
	if d.cron != nil {
		d.cron.Stop()
	}
	d.mu.Unlock()
	return nil
}

// reload (re)loads config, recompiles anchors, and rebuilds the cron engine.
func (d *Daemon) reload() error {
	cfg, err := config.Load(d.cfgPath)
	if err != nil {
		return err
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	anchors, err := schedule.Compile(cfg)
	if err != nil {
		return err
	}

	c := cron.New(cron.WithLocation(loc))
	for _, a := range anchors {
		p, ok := cfg.Provider(a.Provider)
		if !ok {
			continue
		}
		a, p := a, p // capture
		if _, err := c.AddFunc(a.CronSpec(), func() { d.fire(p, a.Reset, false, a.Primer) }); err != nil {
			return err
		}
	}

	d.mu.Lock()
	old := d.cron
	d.cfg, d.loc, d.anchors, d.cron = cfg, loc, anchors, c
	d.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	c.Start()
	return nil
}

// fire anchors a provider, tags the event with the reset it serves, records it,
// and returns the event. primer marks a post-reset fire, recorded as Primed.
func (d *Daemon) fire(p config.Provider, reset string, manual, primer bool) model.Event {
	tr := trigger.New(p)
	ev := tr.Fire(context.Background(), manual)
	ev.Reset = reset
	if primer && ev.Outcome == model.Fired {
		ev.Outcome = model.Primed
	}
	if rec, err := d.store.Record(ev); err == nil {
		ev = rec
	} else {
		d.logger.Printf("record event: %v", err)
	}
	d.logger.Printf("%s %s (reset %s): %s", ev.Provider, ev.Outcome, reset, ev.Detail)
	return ev
}

// watchConfig reloads the daemon when config.toml changes. Returns a stop func.
func (d *Daemon) watchConfig() func() {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		d.logger.Printf("config watch disabled: %v", err)
		return func() {}
	}
	// Watch the directory (editors often replace the file, which drops a
	// file-level watch).
	dir, _ := config.ConfigDir()
	_ = w.Add(dir)

	done := make(chan struct{})
	go func() {
		var debounce <-chan time.Time
		for {
			select {
			case <-done:
				return
			case ev := <-w.Events:
				if ev.Name == d.cfgPath {
					debounce = time.After(300 * time.Millisecond)
				}
			case <-debounce:
				if err := d.reload(); err != nil {
					d.logger.Printf("reload failed (keeping previous config): %v", err)
				} else {
					d.logger.Println("config reloaded")
				}
			case err := <-w.Errors:
				d.logger.Printf("watch error: %v", err)
			}
		}
	}()
	return func() { close(done); w.Close() }
}

// detectMissed records a Missed event for any anchor whose most recent
// scheduled time passed with no corresponding history entry and no active
// window — i.e. the machine was asleep/off (surfaced in the TUI, not retried).
func (d *Daemon) detectMissed() {
	d.mu.Lock()
	anchors, loc := d.anchors, d.loc
	cfg := d.cfg
	d.mu.Unlock()

	now := time.Now().In(loc)
	recent, err := d.store.Recent(200)
	if err != nil {
		return
	}
	for _, a := range anchors {
		// A missed primer protects nothing: the next real message simply starts
		// the window then, so only real anchors are worth surfacing.
		if a.Primer {
			continue
		}
		prev, err := a.Prev(now)
		if err != nil || prev.IsZero() {
			continue
		}
		if recordedSince(recent, a.Provider, prev) {
			continue
		}
		p, ok := cfg.Provider(a.Provider)
		if !ok {
			continue
		}
		// If a window is currently active for this provider, nothing was missed.
		if w, err := stateActive(p, time.Now()); err == nil && w {
			continue
		}
		_, _ = d.store.Record(model.Event{
			Time: prev, Provider: a.Provider, Reset: a.Reset,
			Outcome: model.Missed, Detail: "daemon not running at anchor time",
		})
		d.logger.Printf("missed anchor %s reset %s at %s", a.Provider, a.Reset, prev.Format(time.RFC3339))
	}
}

func recordedSince(events []model.Event, provider string, since time.Time) bool {
	for _, e := range events {
		if e.Provider == provider && !e.Time.Before(since) {
			return true
		}
	}
	return false
}
