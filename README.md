# Curfew

Anchors your AI usage windows so they reset when you want them to.

Claude's usage limit runs on a rolling 5-hour window that starts with your first
message. Curfew sends a throwaway prompt at a set time to start that window
early, so it resets on a schedule you choose instead of whenever you happened to
start working. Works with Claude, Codex, or any CLI.

## How it works

You tell a provider when you want a fresh window (its reset times). Curfew fires
the anchor `window_length` earlier, so a 10:00 reset means a 05:00 anchor for
Claude's 5h window; chain them for resets at 10:00 / 15:00 / 20:00.

- **State-aware.** Before firing it reads the provider's local session logs and
  skips if a window is already open.
- **Primed.** A minute after each reset (`prime_delay_minutes`, default 1) it
  fires again, so the next window starts right at the boundary instead of
  whenever you send your first message. Toggle with `a` on the dashboard or
  `auto_prime = false`.
- **Verified.** The anchor runs your command, confirms a new window appeared,
  and retries with backoff on failure.
- **Background service.** A per-user service (systemd `--user`, launchd, or Task
  Scheduler) runs the schedule. If the machine is asleep at anchor time the miss
  is recorded, not retried.

## Install

Needs Go 1.26+.

```sh
go build -o ~/.local/bin/curfew ./cmd/curfew   # or scripts/build.sh for all platforms
curfew install                                 # install and start the service
curfew                                          # open the TUI
```

## TUI

`curfew` opens a dashboard. Navigation is arrow keys, Enter, and Esc.

- Dashboard: each provider's state, next reset, and a 0:00→24:00 timeline of
  today (green = window active, pale ticks = reset times); `a` toggles
  auto-reset; `+ add provider` (name it, then type the command to run).
- Provider view: fire now, edit the command, and the reset-time groups — each
  group is a set of times on a set of weekdays (e.g. 10/15/20:00 on Mon–Fri and
  a different set on weekends), shown as its own timeline bar.
- Bar editor: move the cursor with ←/→ (1 h) and ctrl+←/→ (15 min; shift+←/→ or
  H/L if your terminal swallows ctrl+arrows), Enter adds or removes a reset at
  the cursor, ↑/↓ switches to the weekday row, `s` saves.

## Config

`~/.config/curfew/config.toml`, written on first run and hot-reloaded on change.

```toml
[general]
timezone = "local"
prime_delay_minutes = 1   # fire again this long after each reset
auto_prime = true         # set false to disable the post-reset primers

[[provider]]
name    = "claude"
command = "claude -p 'curfew: anchor' --model haiku"
window_minutes = 300
log_glob = "~/.claude/projects/**/*.jsonl"   # optional, enables state detection

[[schedule]]
provider  = "claude"
resets_at = ["10:00", "15:00", "20:00"]
days      = ["Mon", "Tue", "Wed", "Thu", "Fri"]
```

`command` is a shell command line: whatever you'd type in a terminal, env
prefixes and pipes included. `claude` and `codex` ship as presets; add any other
tool by giving it a name and a command.

## Commands

```
curfew                     open the TUI
curfew status              provider state and recent history
curfew state               window detected from logs (for troubleshooting)
curfew fire <provider>     anchor now
curfew list                print the config
curfew install|uninstall   manage the service
curfew start|stop          control the service
```

## Layout

```
cmd/curfew         entry point
internal/config    config and presets
internal/state     window detection from logs
internal/schedule  reset -> anchor (cron, DST-aware)
internal/trigger   run command + verify + retry
internal/store     SQLite history
internal/daemon    scheduler + localhost API
internal/tui       Bubble Tea UI
internal/service   install and manage the OS service
```
