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
	"time"

	"github.com/kardianos/service"
	"github.com/kyle/curfew/internal/daemon"
)

// program adapts the daemon to the kardianos service lifecycle.
type program struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the daemon goroutine has fully shut down
}

func (p *program) Start(service.Service) error {
	// Start must not block; run the daemon in the background.
	go func() {
		defer close(p.done)
		if err := daemon.Run(p.ctx); err != nil {
			// service logger isn't wired here; stderr is captured by the manager.
			fmt.Fprintln(os.Stderr, "curfew daemon exited:", err)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	p.cancel()
	// Wait for daemon.Run to finish its shutdown (which removes the endpoint
	// file) before returning: kardianos returns from Run — and the process
	// exits — as soon as Stop returns, so without this wait the cleanup
	// goroutine loses the race and leaves a stale endpoint behind. Bounded so a
	// wedged shutdown can't hang the service manager.
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// build constructs the kardianos service for the current executable.
func build() (service.Service, *program, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	prog := &program{done: make(chan struct{})}
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
			// kardianos' default systemd template hardcodes
			// WantedBy=multi-user.target, which the *user* systemd instance never
			// reaches at login — so a --user service enabled that way never
			// auto-starts. Override the template to target default.target (and
			// restart faster). Only the systemd backend reads this key; it is inert
			// on macOS/Windows.
			"SystemdScript": systemdScript,
		},
	}
	s, err := service.New(prog, cfg)
	return s, prog, err
}

// systemdScript is kardianos' default --user unit template with two changes:
// WantedBy=default.target (so it auto-starts at login) and a shorter RestartSec.
// The cmd/cmdEscape template funcs and the .Restart/.Path/.Arguments/.EnvVars
// fields are provided by kardianos when it executes the template.
const systemdScript = `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}}
{{$dep}} {{end}}

[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{end}}
{{if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:{{.LogDirectory}}/{{.Name}}.out
StandardError=file:{{.LogDirectory}}/{{.Name}}.err
{{- end}}
{{if gt .LimitNOFILE -1 }}LimitNOFILE={{.LimitNOFILE}}{{end}}
{{if .Restart}}Restart={{.Restart}}{{end}}
{{if .SuccessExitStatus}}SuccessExitStatus={{.SuccessExitStatus}}{{end}}
RestartSec=5
EnvironmentFile=-/etc/sysconfig/{{.Name}}

{{range $k, $v := .EnvVars -}}
Environment={{$k}}={{$v}}
{{end -}}

[Install]
WantedBy=default.target
`

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

// EnsureRunning brings the daemon up regardless of the current install state,
// and is what the TUI's "press enter to install & start" and `curfew install`
// invoke. kardianos' Install() errors ("Init already exists") if the unit file
// is present, so a plain Install can't recover a service that's installed but
// stopped. When already installed we uninstall then reinstall: this both starts
// it and lands the current unit template (repairing units written by older
// versions with the wrong WantedBy).
func EnsureRunning() error {
	s, _, err := build()
	if err != nil {
		return err
	}
	// Best-effort teardown first so Install never trips "Init already exists".
	// This is a no-op (errors ignored) when nothing is installed, and repairs a
	// stale/failed/wrong-template unit when something is. The reinstall below is
	// authoritative.
	_ = s.Stop()
	_ = s.Uninstall()
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
