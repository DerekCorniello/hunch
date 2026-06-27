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

# Set up shell integration (auto-detects your shell, appends to rc file)
hunch init --auto

# Restart your shell, and you're done.
# Hunch learns from every command. Predictions appear as you type.

# Start using it — predictions appear as ghost text
git clone https://github.com/user/repo.git
# ghost text: cd repo          press Right or End to accept

After a few commands, Hunch learns your workflows:
```text
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

Run `hunch init <shell>` to print the source line to add to your rc file, or use `--auto` to append it automatically:

```bash
# Auto-detect shell and append source line to rc file
hunch init --auto

# Or specify shell explicitly
hunch init zsh --auto

# Without --auto, just prints the source line
hunch init zsh
# Prints: source /path/to/hunch/integrations/zsh/hunch.zsh

# bash
hunch init bash

# fish
hunch init fish

# PowerShell (add to your $PROFILE)
hunch init powershell
```

### Support matrix

Each shell gets the best experience it can support reliably. Inline ghost text
(suggestions as you type) needs a per-keystroke prediction path and a
ghost-text primitive; only zsh has both, so the other shells fall back to a dim
post-command hint showing the most likely next command.

| Shell | UX | Mechanism | Accept / cycle |
|-------|----|-----------|----------------|
| zsh | **Inline ghost text** as you type | persistent `serve` coprocess + `POSTDISPLAY` | Right/End to accept; Alt-n / Alt-p to cycle |
| bash | Post-command hint line | `client predict` in `PROMPT_COMMAND` | — (type or copy) |
| fish | Post-command hint line | `client predict` in `fish_postexec` | — (fish's own autosuggestions still apply) |
| PowerShell | Post-command hint line | `client predict` in a wrapped `prompt` | — |

> **Why not inline everywhere?** bash has no ghost-text primitive without a
> large add-on like ble.sh; fish's native autosuggestion engine owns inline
> text and resists external injection; PowerShell's native inline predictor
> (PSReadLine `ICommandPredictor`) requires a compiled binary module. A native
> PowerShell predictor module is possible future work. The post-command hint is
> robust everywhere and never fights the shell's own line editor.

All integrations:
- Auto-start the daemon when sourced
- Capture each command's exit code and working directory, feeding the
  location-affinity and outcome-weighting signals
- Send recorded commands to the daemon asynchronously (non-blocking)
- Use the `HUNCH_BIN` environment variable to locate the `hunch` binary (default: `hunch`)
- Honor `HUNCH_HINT=0` (bash/fish/PowerShell) to silence the hint line
- Silently degrade if the daemon is unavailable

---

## CLI reference

### `hunch init [shell]`

Set up shell integration. Auto-detects shell from `$SHELL` if not specified. Supported shells: `zsh`, `bash`, `fish`, `powershell`.

```
--auto               Automatically append source line to rc file
```

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
--at <timestamp>        ISO 8601 timestamp
```

#### `hunch client predict`

```
--state <prev1,prev2>   Previous 1–2 commands (comma-separated)
--prefix <text>         Current buffer text for filtering
--limit <n>             Max suggestions (default: 3)
```

### `hunch doctor`

Check hunch installation and daemon health. Verifies:
- Binary location and PATH
- Daemon status
- Database file
- Shell integration source line

Returns exit code 0 if all checks pass, non-zero otherwise.

### `hunch uninstall`

Remove hunch from your system. Stops the daemon, removes all data files (database, socket, logs, integrations, config), and removes the source line from all shell rc files.

```
--yes, -y            Skip confirmation prompt
```

### `hunch update`

Check for and install updates. Queries GitHub for the latest release, compares versions, and re-installs via `go install` if a newer version is available. Automatically restarts the daemon after updating.

### Shortcut commands

For convenience, these shortcuts wrap common `hunch client` operations:

| Command | Equivalent |
|---------|------------|
| `hunch stats` | `hunch client stats` |
| `hunch predict [flags]` | `hunch client predict [flags]` |
| `hunch reset` | `hunch client reset` |

Example:
```bash
hunch predict --state "git add,git commit" --limit 5
```

---

## Configuration

### Environment variables

| Variable | Field | Default |
|----------|-------|---------|
| `HUNCH_BIN` | Binary path | `hunch` (from PATH) |
| `HUNCH_SOCKET` | Unix socket path | `~/.cache/hunch.sock` |
| `HUNCH_DB_PATH` | SQLite database path | `~/.local/share/hunch.db` |
| `HUNCH_DAEMON_BIN` | Daemon binary path | (same as `hunch`) |
| `HUNCH_HALF_LIFE_HOURS` | Decay half-life | `720` (30 days) |
| `HUNCH_ALPHA` | Additive smoothing | `0.5` |
| `HUNCH_BETA` | CWD-affinity boost strength | `0.75` |
| `HUNCH_GAMMA` | Failure-rate suppression strength | `0.5` |
| `HUNCH_DELTA` | Prior-outcome boost strength | `0.5` |
| `HUNCH_EPSILON` | Confirmed-acceptance boost strength | `0.5` |
| `HUNCH_EXTRA_PARENTS` | Extra parent commands (comma-separated) | (none) |
| `HUNCH_IGNORE` | Extra regexes for sensitive commands to never record (comma-separated) | (none) |
| `HUNCH_LOG_LEVEL` | Log level | `info` |
| `HUNCH_HINT` | Set to `0` to silence the post-command hint (bash/fish/PowerShell) | `1` |

Each scoring strength (`beta`/`gamma`/`delta`/`epsilon`) is a soft,
multiplicative adjustment that is the identity when its signal is absent; set
any to `0` to disable that signal. Sensitive commands matching a built-in or
`HUNCH_IGNORE` pattern are never recorded (neither the transition nor the raw
command), so secrets are not persisted or suggested back.

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
beta = 0.75    # CWD-affinity boost
gamma = 0.5    # failure-rate suppression
delta = 0.5    # prior-outcome boost
epsilon = 0.5  # confirmed-acceptance boost
accept_keys = ["right", "end"]
extra_parents = ["mycli", "teamtool"]
ignore = ['(?i)--api-token']  # extra sensitive-command patterns to never record
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

## Boot persistence

The daemon starts automatically the first time you open a terminal after boot
(your shell rc file runs `hunch daemon start`). It then stays alive as a
detached background process across terminal sessions. This is sufficient for
normal interactive use.

If you need the daemon running without an interactive shell (e.g. tmux sessions
that auto-start, CI, SSH invocations), install a user service for your platform:

**Linux (systemd):**
```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/hunch-daemon.service << 'EOF'
[Unit]
Description=Hunch daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/go/bin/hunch daemon run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
systemctl --user enable --now hunch-daemon
```

**macOS (launchd):**
```bash
mkdir -p ~/Library/LaunchAgents
cat > ~/Library/LaunchAgents/com.user.hunch-daemon.plist << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.user.hunch-daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>PATH/TO/hunch</string>
        <string>daemon</string>
        <string>run</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
EOF
launchctl load ~/Library/LaunchAgents/com.user.hunch-daemon.plist
```

**Windows (Task Scheduler):**
```powershell
$action = New-ScheduledTaskAction -Execute "hunch.exe" -Argument "daemon run"
$trigger = New-ScheduledTaskTrigger -AtLogOn
Register-ScheduledTask -TaskName HunchDaemon -Action $action -Trigger $trigger
```

---

## Non-goals

- No AI/LLM — purely statistical learning
- No cloud sync or telemetry
- No distributed system
- No multi-user graph merging
- No complex shell grammar parsing
- No daemon-less mode (the daemon is required)

---

## Troubleshooting

### No predictions appear

1. Check if the daemon is running:
   ```bash
   hunch daemon status
   ```

2. If not running, start it:
   ```bash
   hunch daemon start
   ```

3. Verify shell integration is loaded:
   ```bash
   hunch doctor
   ```

4. Check that your rc file sources the hunch integration script.

### Daemon won't start

1. Check for stale socket file:
   ```bash
   ls -la ~/.cache/hunch.sock
   ```

2. Remove it if the daemon isn't running:
   ```bash
   rm ~/.cache/hunch.sock
   hunch daemon start
   ```

3. Check the log file for errors:
   ```bash
   tail -f ~/.local/share/hunch/hunch.log
   ```

### `hunch: command not found`

The binary isn't on your PATH. Either:
```bash
# Install globally
go install github.com/DerekCorniello/hunch@latest

# Or add to PATH
export PATH="$PATH:$(go env GOPATH)/bin"
```

### Predictions are wrong or not useful

- Hunch needs time to learn your patterns. Run `hunch import-history` to jump-start from your shell history.
- Check the graph size: `hunch stats`
- Reset and start fresh: `hunch reset`

### Shell integration conflicts

If you use `zsh-autosuggestions` or similar plugins, Hunch uses the `zle-line-pre-redraw` hook which should compose correctly. If you see issues, ensure Hunch is sourced **after** other plugins in your rc file.

### Windows-specific issues

- Exclude `%LocalAppData%\hunch\` from Windows Defender real-time scanning to avoid SQLite lock contention.
- Ensure you're using Windows 10 version 1803 or later for Unix domain socket support.

### Getting more help

Run `hunch doctor` for a comprehensive health check, or check the logs at `~/.local/share/hunch/hunch.log`.

---

## License

MIT. See [LICENSE](LICENSE).
