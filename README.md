# Curfew

**Align your AI usage-window resets to convenient times.**

Claude (and similar tools) meter usage in a rolling window — for Claude, 5 hours
that starts on your *first* message. Because the *number* of windows is
unbounded, you can "anchor" a fresh window by firing a throwaway prompt at a
strategic time. Curfew does this automatically so your windows reset on
convenient boundaries — you get maximum usable capacity during focused work and
push the dead time to hours you don't code.

You tell Curfew **when you want fresh capacity** (reset boundaries); it
back-computes the anchor times, fires them state-aware (never double-firing when
a window is already open), and shows everything in a TUI dashboard.

```
⏰ Curfew   pid 2474558 · Local · Mon 00:22:41

  PROVIDER   STATE      WINDOW                 NEXT ANCHOR
  claude-1   idle       —                      Mon 05:00 → reset 10:00
  claude-2   idle       —                      Mon 07:30 → reset 12:30
▸ codex      ACTIVE     23:54→04:54 (4h left)  Mon 08:00 → reset 13:00

  RECENT
  07-06 00:14  codex      fired
  07-05 20:00  claude-1   skipped   window already active since 19:02

  ↑/↓ select · f fire now · r refresh · q quit
```

## How it works

- **Reset → anchor:** a window that should reset at `R` must start at
  `R − window_length`. For a 10:00 reset with Claude's 5h window, Curfew fires an
  anchor at 05:00.
- **State-aware:** at each anchor time Curfew reads the provider's local session
  logs. If a window is already active, it skips — anchoring would be redundant.
- **Reliable:** a per-user background service (systemd `--user` on Linux,
  LaunchAgent on macOS, Task Scheduler on Windows) runs an internal cron
  scheduler, so timing is identical on every OS. It runs in your **user session**
  so the anchor commands can reach your credentials.
- **Verify + retry:** after firing, Curfew confirms a fresh window appeared in
  the logs and retries with backoff on failure.
- **Asleep at anchor time?** Curfew records the miss (visible in the TUI) rather
  than waking the machine.

## Install

Requires Go 1.24+ to build from source.

```sh
go build -o ~/.local/bin/curfew ./cmd/curfew   # or: ./scripts/build.sh for all platforms
curfew install                                  # install & start the background service
curfew                                          # open the TUI
```

## Commands

| Command | Purpose |
|---|---|
| `curfew` | launch the TUI dashboard |
| `curfew status` | one-shot status of providers + recent history |
| `curfew state` | show each provider's currently-detected window (troubleshooting) |
| `curfew list` | show configured providers and schedules |
| `curfew fire <provider>` | anchor a window now |
| `curfew install` / `uninstall` | manage the background service |
| `curfew start` / `stop` | control the installed service |
| `curfew validate` / `path` / `init` | config helpers |

## Configuration

Config lives at your OS config dir (`~/.config/curfew/config.toml` on Linux).
A default is written on first run.

```toml
[general]
timezone = "local"          # or an IANA name, e.g. "America/New_York"

[[provider]]
name    = "claude-1"
command = ["claude", "-p", "curfew: anchor", "--model", "haiku"]
window_minutes = 300
log_glob = "~/.claude/projects/**/*.jsonl"
timestamp_field = "timestamp"
[provider.env]
CLAUDE_CONFIG_DIR = "~/.claude"

[[schedule]]
provider  = "claude-1"
resets_at = ["10:00", "15:00", "20:00"]   # when you want fresh capacity
days      = ["Mon", "Tue", "Wed", "Thu", "Fri"]
```

Edits are hot-reloaded — no restart needed.

### Editing from the TUI

Everything above is editable in the TUI without touching the file, using only
arrow keys / Enter / Esc:

- **Dashboard** — `↑/↓` select a provider, `Enter` open it, or select
  `＋ Add provider` to create one (pick `claude`/`codex`, name it, and set its
  config directory — which is how you point it at a specific account/subscription).
- **Provider editor** — a navigable list: `Fire now` · reset times (each showing
  its computed anchor) · `＋ Add reset time` · a `Days:` row (`←/→` + `Enter` to
  toggle) · `🗑 Remove provider`. Adding an evening reset offers to auto-fill the
  earlier chained resets.

### Two Claude subscriptions (claude-1 / claude-2)

Curfew separates two Claude subscriptions by pointing each provider at a
different Claude config directory via `CLAUDE_CONFIG_DIR`. That directory holds
both the logged-in credentials and the session logs, so each provider anchors
and observes an independent subscription. Log in to each once:

```sh
CLAUDE_CONFIG_DIR=~/.claude    claude   # /login  -> subscription 1  (claude-1)
CLAUDE_CONFIG_DIR=~/.claude-2  claude   # /login  -> subscription 2  (claude-2)
```

Codex is separated the same way via `CODEX_HOME`. Any other CLI works too — add
a `[[provider]]` with its `command`, `window_minutes`, and (optionally) a
`log_glob`/`timestamp_field` for state detection. Providers without a `log_glob`
still anchor; they just always fire on schedule (no active-window skipping).

## Architecture

One Go binary, two roles selected by subcommand:

- `curfew daemon` — the scheduler + a localhost JSON API, run by the service.
- everything else — a client of that API (the TUI and CLI verbs).

```
internal/
  config/    TOML config, provider presets, validation
  state/     reconstruct the current window from session logs
  schedule/  reset times -> anchor cron entries (DST-aware)
  trigger/   execute anchor command + verify + retry
  store/     SQLite history of outcomes
  daemon/    cron loop + state-aware fire decision + localhost API
  api/       endpoint discovery + HTTP client
  service/   install/manage the per-user OS service
  tui/       Bubble Tea dashboard
```
