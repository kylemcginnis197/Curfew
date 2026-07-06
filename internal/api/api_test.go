package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kyle/curfew/internal/model"
)

// redirectDirs points config.CacheDir (used by EndpointPath) at a temp dir so
// the endpoint file round-trip never touches the user's real cache.
func redirectDirs(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestEndpointRoundTrip(t *testing.T) {
	redirectDirs(t)
	want := Endpoint{Port: 54321, PID: 4242, Addr: "127.0.0.1:54321"}
	if err := WriteEndpoint(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadEndpoint()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}
	// RemoveEndpoint should delete it; a subsequent read must fail.
	RemoveEndpoint()
	if _, err := ReadEndpoint(); err == nil {
		t.Fatal("expected read to fail after RemoveEndpoint")
	}
}

func TestReadEndpointMissing(t *testing.T) {
	redirectDirs(t)
	if _, err := ReadEndpoint(); err == nil {
		t.Fatal("expected error reading a missing endpoint file")
	}
}

// stubServer stands up an httptest server speaking the daemon's API contract and
// returns a Client pointed at it.
func stubServer(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// srv.URL is like "http://127.0.0.1:PORT"; Client wants the host:port.
	addr := srv.Listener.Addr().String()
	return &Client{addr: addr, http: srv.Client()}
}

func TestClientStatus(t *testing.T) {
	want := model.Status{
		Timezone: "America/New_York",
		PID:      99,
		Providers: []model.ProviderState{
			{Name: "claude-1", Active: true},
			{Name: "codex"},
		},
	}
	c := stubServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Errorf("path = %s, want /status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))

	got, err := c.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.PID != want.PID || got.Timezone != want.Timezone || len(got.Providers) != 2 {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	if !got.Providers[0].Active || got.Providers[0].Name != "claude-1" {
		t.Errorf("providers not decoded: %+v", got.Providers)
	}
}

func TestClientStatusNon200(t *testing.T) {
	c := stubServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	if _, err := c.Status(); err == nil {
		t.Fatal("expected error on non-200 status")
	}
}

func TestClientFire(t *testing.T) {
	c := stubServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/fire" {
			t.Errorf("got %s %s, want POST /fire", r.Method, r.URL.Path)
		}
		var body struct {
			Provider string `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Provider != "claude-1" {
			t.Errorf("provider = %q, want claude-1", body.Provider)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.Event{Provider: body.Provider, Outcome: model.Manual})
	}))

	ev, err := c.Fire("claude-1")
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if ev.Provider != "claude-1" || ev.Outcome != model.Manual {
		t.Fatalf("event = %+v, want manual claude-1", ev)
	}
}

func TestClientFireNon200(t *testing.T) {
	c := stubServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	if _, err := c.Fire("ghost"); err == nil {
		t.Fatal("expected error when fire returns non-200")
	}
}

func TestReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if !Reachable(addr) {
		t.Errorf("Reachable(%s) = false, want true (listener open)", addr)
	}
	ln.Close()
	if Reachable(addr) {
		t.Errorf("Reachable(%s) = true after close, want false", addr)
	}
}

func TestDialNoDaemon(t *testing.T) {
	redirectDirs(t) // no endpoint file present
	if _, err := Dial(); err == nil {
		t.Fatal("expected Dial to fail with no endpoint file")
	}
}

// TestDialRoundTrip writes an endpoint file pointing at a live stub server and
// confirms Dial finds and verifies it.
func TestDialRoundTrip(t *testing.T) {
	redirectDirs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.Status{PID: 7})
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()

	if err := WriteEndpoint(Endpoint{Addr: addr, PID: 7, Port: srv.Listener.Addr().(*net.TCPAddr).Port}); err != nil {
		t.Fatal(err)
	}
	c, err := Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	st, err := c.Status()
	if err != nil || st.PID != 7 {
		t.Fatalf("status after dial = %+v err=%v", st, err)
	}
}
