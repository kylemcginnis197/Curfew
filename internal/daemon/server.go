package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/kyle/curfew/internal/api"
	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/state"
)

// serve binds the localhost API to a random high port, writes the endpoint file
// so clients can find it, and starts serving in the background.
func (d *Daemon) serve() (*http.Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/status", d.handleStatus)
	mux.HandleFunc("/fire", d.handleFire)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.logger.Printf("api server: %v", err)
		}
	}()

	if err := api.WriteEndpoint(api.Endpoint{
		Port: ln.Addr().(*net.TCPAddr).Port,
		PID:  os.Getpid(),
		Addr: addr,
	}); err != nil {
		return nil, err
	}
	d.logger.Printf("api listening on %s", addr)
	// Clean up the endpoint file when the server shuts down.
	srv.RegisterOnShutdown(api.RemoveEndpoint)
	return srv, nil
}

func (d *Daemon) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, d.buildStatus())
}

func (d *Daemon) handleFire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" {
		http.Error(w, "provider required", http.StatusBadRequest)
		return
	}
	d.mu.Lock()
	p, ok := d.cfg.Provider(body.Provider)
	d.mu.Unlock()
	if !ok {
		http.Error(w, fmt.Sprintf("unknown provider %q", body.Provider), http.StatusNotFound)
		return
	}
	// Fire in the background: anchoring can run for seconds (command + verify)
	// or minutes (retries), so we don't block the HTTP response. The final
	// outcome is written to history and surfaces in the next status poll.
	go d.fire(p, "", true)
	writeJSON(w, model.Event{
		Provider: p.Name,
		Outcome:  model.Manual,
		Time:     time.Now(),
		Detail:   "anchoring (running in background; check status for the result)",
	})
}

// buildStatus assembles the live snapshot: per-provider window state and next
// anchor/reset, plus recent history.
func (d *Daemon) buildStatus() model.Status {
	d.mu.Lock()
	cfg, loc, anchors := d.cfg, d.loc, d.anchors
	d.mu.Unlock()

	now := time.Now()
	st := model.Status{
		Now:      now,
		Timezone: loc.String(),
		PID:      os.Getpid(),
	}
	for _, p := range cfg.Providers {
		ps := model.ProviderState{Name: p.Name}
		if w, err := state.For(p).Current(now); err == nil && w.Active {
			ps.Active = true
			ps.WindowStart = w.Start
			ps.WindowEnd = w.End
		}
		// Next anchor across this provider's anchors, in the config timezone.
		next := time.Time{}
		for _, a := range anchors {
			if a.Provider != p.Name {
				continue
			}
			if t, err := a.Next(now.In(loc)); err == nil {
				if next.IsZero() || t.Before(next) {
					next = t
				}
			}
		}
		if !next.IsZero() {
			ps.NextAnchor = next
			ps.NextReset = next.Add(time.Duration(p.WindowMinutes) * time.Minute)
		}
		st.Providers = append(st.Providers, ps)
	}
	if rec, err := d.store.Recent(50); err == nil {
		st.Recent = rec
	}
	return st
}

// stateActive reports whether a provider currently has an active window.
func stateActive(p config.Provider, now time.Time) (bool, error) {
	w, err := state.For(p).Current(now)
	return w.Active, err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
