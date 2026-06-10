# hunch

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Hunch is a shell companion that learns your command-line behavior and predicts what you're most likely to do next.

It builds a lightweight statistical model from your own command history — no AI, no cloud, no telemetry. Just fast, local suggestions that get better over time.

---

## Quick start

```bash
# Install
go install github.com/DerekCorniello/hunch@latest

# Get the source line for your shell
hunch init zsh
# Prints: source /home/user/.local/share/hunch/integrations/zsh/hunch.zsh

# Add it to your rc file, restart your shell, and you're done.
# Hunch learns from every command. Predictions appear as you type.

# Start using it — predictions appear as ghost text
git clone https://github.com/user/repo.git
# ghost text: cd repo          press Right or End to accept

After a few commands, Hunch learns your workflows:
```
git clone REPO → cd STR
cargo build    → cargo run
ssh STR        → ssh STR
```

---

## Installation

### From source

```bash
go install github.com/DerekCorniello/hunch@latest
```

The binary is built at `~/go/bin/hunch` (or wherever `$GOBIN` points). Make sure it's on your `PATH`.

Build with a version string:

```bash
go install -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=1.0.0" github.com/DerekCorniello/hunch@latest
```

Or from a local clone:

```bash
go install -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=$(git describe --tags --always)" .
```

### Pre-built binaries

Pre-built binaries are not yet available. See [Building from source](#from-source) above.

### Dependencies

Hunch requires no external runtime dependencies. The Go binary is fully static (SQLite is handled by [`modernc.org/sqlite`](https://modernc.org/sqlite), a pure-Go port — no CGO needed).

---

## Shell integration

Run `hunch init <shell>` to print the source line to add to your rc file:

```bash
# zsh — ghost text with Right/End accept
hunch init zsh
# Prints: source /path/to/hunch/integrations/zsh/hunch.zsh

# Add the printed line to ~/.zshrc, then restart your shell.

# bash — Tab-accept
hunch init bash

# fish
hunch init fish

# PowerShell (add to your $PROFILE)
hunch init powershell
```

What each integration provides:

| Shell | UX | Accept | Record hook |
|-------|----|--------|-------------|
| zsh | Inline ghost text via `POSTDISPLAY` | Right arrow, End | `precmd` |
| bash | Tab inserts suggestion | Tab | `PROMPT_COMMAND` |
| fish | Ghost text via `commandline` manipulation | Right arrow, End | `fish_postexec` |
| PowerShell | Disables native prediction, shows suggestion on Right/End | Right arrow, End | `Invoke-HunchRecord` via key binding |

All integrations:
- Auto-start the daemon when sourced
- Send recorded commands to the daemon asynchronously (non-blocking)
- Use the `HUNCH_BIN` environment variable to locate the `hunch` binary (default: `hunch`)
- Silently degrade if the daemon is unavailable

---

## CLI reference

### `hunch init <shell>`

Print the shell integration source line. Supported shells: `zsh`, `bash`, `fish`, `powershell`.

When run interactively, `hunch init` detects your shell history and offers to import
it to jump-start predictions. In non-interactive contexts (piped, scripted), the
prompt is skipped.

### `hunch import-history <shell>`

Import shell command history as training data for predictions. Supports `zsh`, `bash`,
`fish`, and `powershell`.

```
--path <file>      History file path (defaults to the shell's standard location)
--threads <N>      Number of normalize worker threads (default: CPU count)
```

Processes history by parsing commands, normalizing them into templates, building
state transitions, and importing into the daemon as a seed.

### `hunch daemon <action>`

Manage the background daemon process.

| Action | Description |
|--------|-------------|
| `run`  | Run daemon in foreground (useful for debugging) |
| `start`| Detach and run daemon in background |
| `stop` | Stop the running daemon |
| `status`| Check if daemon is running (exit 0 = running) |

### `hunch client <op>`

Send an IPC operation to the running daemon.

| Op | Description |
|----|-------------|
| `record` | Record a command transition |
| `predict` | Get next-command predictions |
| `reset` | Wipe all learned data |
| `export` | Export the transition graph as JSON |
| `normalize`| Normalize a raw command to its template |
| `stats` | Show daemon stats (size, half-life, alpha) |
| `config` | Show active daemon configuration |
| `import` | Import a seed JSON file |

#### `hunch client record`

```
--state <prev1,prev2>   Previous 1–2 commands (comma-separated)
--next <command>        The command that was run
--outcome <type>        success or failure
--cwd <path>            Working directory
--at <timestamp>        ISO 8601 timestamp
```

#### `hunch client predict`

```
--state <prev1,prev2>   Previous 1–2 commands (comma-separated)
--prefix <text>         Current buffer text for filtering
--limit <n>             Max suggestions (default: 3)
```

---

## Configuration

### Environment variables

| Variable | Field | Default |
|----------|-------|---------|
| `HUNCH_BIN` | Binary path | `hunch` (from PATH) |
| `HUNCH_SOCKET` | Unix socket path | `~/.cache/hunch.sock` |
| `HUNCH_DB_PATH` | SQLite database path | `~/.local/share/hunch.db` |
| `HUNCH_ACCEPT_KEYS` | Accept key override | `right,end` |
| `HUNCH_DAEMON_BIN` | Daemon binary path | (same as `hunch`) |
| `HUNCH_HALF_LIFE_HOURS` | Decay half-life | `720` (30 days) |
| `HUNCH_ALPHA` | Additive smoothing | `0.5` |
| `HUNCH_EXTRA_PARENTS` | Extra parent commands | (none) |
| `HUNCH_LOG_LEVEL` | Log level | `info` |

### Config file

Hunch looks for `config.toml` in the XDG config directory:

| OS | Config path |
|----|-------------|
| Linux | `~/.config/hunch/config.toml` |
| macOS | `~/.config/hunch/config.toml` |
| Windows | `%AppData%\hunch\config.toml` |

```toml
socket = "/run/user/1000/hunch.sock"
db_path = "/var/lib/hunch/hunch.db"
half_life_hours = 720
alpha = 0.5
accept_keys = ["right", "end"]
extra_parents = ["mycli", "teamtool"]
log_level = "info"
```

Precedence (lowest to highest): built-in defaults → config file → env vars → CLI flags.

---

## Architecture

```
shell → integration (thin adapter) → daemon (background service) → core/ (logic)
                                          │
                                     SQLite (WAL)
```

- **core/** — Pure logic. `normalize` (two-phase: unwrap wrappers, classify tokens), `graph` (transition counts), `predict` (additive-smoothed exponential decay scoring). Deterministic and stateless.
- **daemon/** — Background service. Owns SQLite, receives IPC events, calls core to update and predict. One request per connection over a Unix socket.
- **cli/** — Admin interface. Routes to init/daemon/client subcommands. Links the full daemon package.
- **integrations/** — Shell-specific adapters. Minimal shims that shell out to `hunch client`. No learning logic.

See [AGENTS.md](AGENTS.md) for the full architecture and design decisions.

---

## Platform support

| Platform | Status |
|----------|--------|
| Linux (x86_64, aarch64) | ✅ Full support |
| macOS (x86_64, arm64) | ✅ Supported |
| Windows (x86_64) | ✅ Supported (Unix domain sockets) |
| Other Unix (FreeBSD, etc.) | ✅ Supported (flock, XDG paths) |

On Windows, you may need to exclude `%LocalAppData%\hunch\` from Windows Defender
real-time scanning to avoid lock contention with the SQLite database.

---

## Non-goals

- No AI/LLM — purely statistical learning
- No cloud sync or telemetry
- No distributed system
- No multi-user graph merging
- No complex shell grammar parsing
- No daemon-less mode (the daemon is required)

---

## License

MIT. See [LICENSE](LICENSE).
