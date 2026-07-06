// Package service installs and controls Curfew's background daemon as a
// per-user OS service: a systemd --user unit on Linux, a LaunchAgent on macOS,
// and a Task Scheduler / service entry on Windows. It deliberately runs in the
// user session (not root/SYSTEM) so the anchor commands can reach the user's
// credentials/keychain.
package service

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kardianos/service"
	"github.com/kyle/curfew/internal/daemon"
)

// program adapts the daemon to the kardianos service lifecycle.
type program struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (p *program) Start(service.Service) error {
	// Start must not block; run the daemon in the background.
	go func() {
		if err := daemon.Run(p.ctx); err != nil {
			// service logger isn't wired here; stderr is captured by the manager.
			fmt.Fprintln(os.Stderr, "curfew daemon exited:", err)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	p.cancel()
	return nil
}

// build constructs the kardianos service for the current executable.
func build() (service.Service, *program, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	prog := &program{}
	prog.ctx, prog.cancel = context.WithCancel(context.Background())
	cfg := &service.Config{
		Name:        "curfew",
		DisplayName: "Curfew",
		Description: "Anchors AI usage windows so their resets land at convenient times.",
		Executable:  exe,
		Arguments:   []string{"daemon"},
		Option: service.KeyValue{
			"UserService": true, // systemd --user / macOS LaunchAgent
			"RunAtLoad":   true, // start at login
			"KeepAlive":   true, // restart if it exits
		},
	}
	s, err := service.New(prog, cfg)
	return s, prog, err
}

// RunDaemon is the entry point for `curfew daemon`. When launched by the service
// manager it hands control to kardianos (which drives Start/Stop); when run
// interactively it runs the daemon in the foreground with signal handling.
func RunDaemon() error {
	s, _, err := build()
	if err == nil && !service.Interactive() {
		return s.Run()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return daemon.Run(ctx)
}

// Install registers and starts the service.
func Install() error {
	s, _, err := build()
	if err != nil {
		return err
	}
	if err := s.Install(); err != nil {
		return err
	}
	return s.Start()
}

// Uninstall stops and removes the service.
func Uninstall() error {
	s, _, err := build()
	if err != nil {
		return err
	}
	_ = s.Stop() // ignore "not running"
	return s.Uninstall()
}

// Start starts the installed service.
func Start() error {
	s, _, err := build()
	if err != nil {
		return err
	}
	return s.Start()
}

// Stop stops the installed service.
func Stop() error {
	s, _, err := build()
	if err != nil {
		return err
	}
	return s.Stop()
}

// Status returns a human-readable service status.
func Status() (string, error) {
	s, _, err := build()
	if err != nil {
		return "", err
	}
	st, err := s.Status()
	if err != nil {
		return "unknown", err
	}
	switch st {
	case service.StatusRunning:
		return "running", nil
	case service.StatusStopped:
		return "stopped", nil
	default:
		return "not installed", nil
	}
}
