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

- Dashboard: each provider's state and next reset, plus `+ add provider` (name it,
  then type the command to run).
- Provider view: fire now, edit the command, add/remove reset times (with their
  computed anchors shown), toggle weekdays, remove the provider.

## Config

`~/.config/curfew/config.toml`, written on first run and hot-reloaded on change.

```toml
[general]
timezone = "local"

[[provider]]
name    = "claude-1"
command = "claude -p 'curfew: anchor' --model haiku"
window_minutes = 300
log_glob = "~/.claude/projects/**/*.jsonl"   # optional, enables state detection

[[schedule]]
provider  = "claude-1"
resets_at = ["10:00", "15:00", "20:00"]
days      = ["Mon", "Tue", "Wed", "Thu", "Fri"]
```

`command` is a shell command line: whatever you'd type in a terminal, env
prefixes and pipes included.

**Two subscriptions.** Give each Claude provider its own config dir with
`CLAUDE_CONFIG_DIR` (it holds that account's credentials and logs). Log into each
once, then set the variable in the provider's command or its `[provider.env]`
table:

```sh
CLAUDE_CONFIG_DIR=~/.claude    claude   # subscription 1
CLAUDE_CONFIG_DIR=~/.claude-2  claude   # subscription 2
```

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
