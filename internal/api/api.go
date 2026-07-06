// Package api defines the localhost contract between the daemon (server) and the
// TUI/CLI (client): the endpoint discovery file and a thin HTTP client.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
)

// Endpoint is written by the daemon to a file the client reads to locate it.
type Endpoint struct {
	Port int    `json:"port"`
	PID  int    `json:"pid"`
	Addr string `json:"addr"` // e.g. "127.0.0.1:53411"
}

// WriteEndpoint persists the daemon's endpoint to the well-known path.
func WriteEndpoint(e Endpoint) error {
	path, err := config.EndpointPath()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(e, "", "  ")
	return os.WriteFile(path, data, 0o600)
}

// RemoveEndpoint deletes the endpoint file (best effort, on shutdown).
func RemoveEndpoint() {
	if path, err := config.EndpointPath(); err == nil {
		_ = os.Remove(path)
	}
}

// ReadEndpoint loads the endpoint file.
func ReadEndpoint() (Endpoint, error) {
	var e Endpoint
	path, err := config.EndpointPath()
	if err != nil {
		return e, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(data, &e)
}

// Client talks to a running daemon over its localhost API.
type Client struct {
	addr string
	http *http.Client
}

// Dial locates the daemon via its endpoint file and verifies it is reachable.
// It returns an error (that the caller can surface as "daemon not running") if
// no live daemon is found.
func Dial() (*Client, error) {
	e, err := ReadEndpoint()
	if err != nil {
		return nil, fmt.Errorf("daemon not running (no endpoint file): %w", err)
	}
	c := &Client{addr: e.Addr, http: &http.Client{Timeout: 5 * time.Second}}
	if _, err := c.Status(); err != nil {
		return nil, fmt.Errorf("daemon not reachable at %s: %w", e.Addr, err)
	}
	return c, nil
}

// Reachable reports whether an addr accepts TCP connections quickly.
func Reachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Status fetches the current daemon snapshot.
func (c *Client) Status() (model.Status, error) {
	var s model.Status
	resp, err := c.http.Get("http://" + c.addr + "/status")
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return s, fmt.Errorf("status %d", resp.StatusCode)
	}
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// Fire asks the daemon to anchor a provider now. It returns the recorded event.
func (c *Client) Fire(provider string) (model.Event, error) {
	var ev model.Event
	body, _ := json.Marshal(map[string]string{"provider": provider})
	resp, err := c.http.Post("http://"+c.addr+"/fire", "application/json", bytes.NewReader(body))
	if err != nil {
		return ev, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ev, fmt.Errorf("fire failed: status %d", resp.StatusCode)
	}
	return ev, json.NewDecoder(resp.Body).Decode(&ev)
}
