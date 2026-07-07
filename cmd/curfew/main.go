// Command curfew anchors provider usage windows so their resets land on
// convenient boundaries. Running it with no arguments launches the TUI; the
// daemon and management verbs are available as subcommands.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kyle/curfew/internal/api"
	"github.com/kyle/curfew/internal/config"
	"github.com/kyle/curfew/internal/model"
	"github.com/kyle/curfew/internal/service"
	"github.com/kyle/curfew/internal/state"
	"github.com/kyle/curfew/internal/trigger"
	"github.com/kyle/curfew/internal/tui"
)

const usage = `curfew — align your Claude/Codex usage-window resets to convenient times

Usage:
  curfew                 launch the TUI dashboard (default)
  curfew daemon          run the scheduler in the foreground (used by the service)
  curfew list            show configured providers and schedules
  curfew validate        check config.toml for errors
  curfew path            print the config file path
  curfew init            write a default config.toml if none exists
  curfew install         install the background service (user session)
  curfew uninstall       remove the background service
  curfew start|stop|status   control the installed service
  curfew fire <provider>     anchor a window now (ignores schedule)
  curfew help            show this help
`

func main() {
	args := os.Args[1:]
	cmd := "tui"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "list":
		err = cmdList()
	case "validate":
		err = cmdValidate()
	case "path":
		err = cmdPath()
	case "init":
		err = cmdInit()
	case "daemon":
		err = cmdDaemon()
	case "status":
		err = cmdStatus()
	case "state":
		err = cmdState()
	case "fire":
		err = cmdFire(args)
	case "install":
		err = cmdService("install")
	case "uninstall":
		err = cmdService("uninstall")
	case "start":
		err = cmdService("start")
	case "stop":
		err = cmdService("stop")
	case "tui":
		err = tui.Run()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// loadConfig resolves the config path and loads it (returning defaults if the
// file is absent).
func loadConfig() (*config.Config, string, error) {
	path, err := config.ConfigPath()
	if err != nil {
		return nil, "", err
	}
	c, err := config.Load(path)
	return c, path, err
}

func cmdPath() error {
	path, err := config.ConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func cmdInit() error {
	path, err := config.ConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("config already exists: %s\n", path)
		return nil
	}
	if err := config.Default().Save(path); err != nil {
		return err
	}
	fmt.Printf("wrote default config: %s\n", path)
	return nil
}

func cmdValidate() error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	fmt.Printf("%s: ok (%d providers, %d schedules)\n", path, len(c.Providers), len(c.Schedules))
	return nil
}

func cmdDaemon() error {
	return service.RunDaemon()
}

func cmdService(action string) error {
	var err error
	switch action {
	case "install":
		err = service.Install()
	case "uninstall":
		err = service.Uninstall()
	case "start":
		err = service.Start()
	case "stop":
		err = service.Stop()
	}
	if err != nil {
		return err
	}
	st, _ := service.Status()
	fmt.Printf("service %sed — status: %s\n", action, st)
	return nil
}

// cmdState reports each provider's currently-detected window straight from the
// logs (bypassing the daemon), useful for troubleshooting state detection.
func cmdState() error {
	c, _, err := loadConfig()
	if err != nil {
		return err
	}
	now := time.Now()
	for _, p := range c.Providers {
		w, err := state.For(p).Current(now)
		if err != nil {
			fmt.Printf("  %-10s error: %v\n", p.Name, err)
			continue
		}
		if !w.Active && w.LastActivity.IsZero() {
			fmt.Printf("  %-10s no recent logs (glob %s)\n", p.Name, config.ExpandTilde(p.LogGlob))
			continue
		}
		fmt.Printf("  %-10s active=%-5v window %s→%s  last activity %s\n",
			p.Name, w.Active, w.Start.Local().Format("15:04"), w.End.Local().Format("15:04"), w.LastActivity.Local().Format("15:04"))
	}
	return nil
}

func cmdStatus() error {
	c, err := api.Dial()
	if err != nil {
		return err
	}
	s, err := c.Status()
	if err != nil {
		return err
	}
	fmt.Printf("daemon pid %d, timezone %s, now %s\n\n", s.PID, s.Timezone, s.Now.Format("15:04:05"))
	for _, p := range s.Providers {
		win := "idle"
		if p.Active {
			win = fmt.Sprintf("ACTIVE, resets %s", p.WindowEnd.Local().Format("Mon 15:04"))
		}
		next := "—"
		if !p.NextAnchor.IsZero() {
			next = fmt.Sprintf("%s (reset %s)", p.NextAnchor.Local().Format("Mon 15:04"), p.NextReset.Local().Format("15:04"))
		}
		fmt.Printf("  %-10s %-28s next anchor: %s\n", p.Name, win, next)
	}
	if len(s.Recent) > 0 {
		fmt.Println("\nRecent:")
		for i, e := range s.Recent {
			if i >= 8 {
				break
			}
			fmt.Printf("  %s  %-10s %-8s %s\n", e.Time.Format("01-02 15:04"), e.Provider, e.Outcome, e.Detail)
		}
	}
	return nil
}

func cmdFire(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: curfew fire <provider>")
	}
	provider := args[0]
	// Prefer the running daemon so the event is recorded and state-aware; fall
	// back to a direct in-process fire if the daemon isn't up.
	if c, err := api.Dial(); err == nil {
		ev, err := c.Fire(provider)
		if err != nil {
			return err
		}
		printEvent(ev)
		fmt.Println("run 'curfew status' to see the recorded outcome")
		return nil
	}
	c, _, err := loadConfig()
	if err != nil {
		return err
	}
	p, ok := c.Provider(provider)
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	fmt.Printf("daemon not running — firing %s directly...\n", provider)
	ev := trigger.New(p).Fire(context.Background(), true)
	printEvent(ev)
	return nil
}

func printEvent(e model.Event) {
	fmt.Printf("%s: %s", e.Provider, e.Outcome)
	if e.Detail != "" {
		fmt.Printf(" (%s)", e.Detail)
	}
	if !e.WindowStart.IsZero() {
		fmt.Printf(" — window started %s", e.WindowStart.Format("15:04"))
	}
	fmt.Println()
}

func cmdList() error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("config: %s\ntimezone: %s\n\n", path, c.General.Timezone)
	fmt.Println("Providers:")
	for _, p := range c.Providers {
		fmt.Printf("  %-10s window=%dm  cmd=%s\n", p.Name, p.WindowMinutes, p.Command)
	}
	fmt.Println("\nSchedules:")
	for _, s := range c.Schedules {
		days := "every day"
		if len(s.Days) > 0 {
			days = strings.Join(s.Days, ",")
		}
		fmt.Printf("  %-10s resets@ %-25s %s\n", s.Provider, strings.Join(s.ResetsAt, " "), days)
	}
	return nil
}
