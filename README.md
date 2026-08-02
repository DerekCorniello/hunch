# hunch

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Hunch predicts your next shell command from your own history. Local, statistical, zero telemetry.

It learns which commands tend to follow which, then shows the most likely next one as ghost text - accept it with a keystroke, or keep typing. No LLM, no cloud, no accounts, and it gets better the more you use it.

**zsh only, for now.** Hunch needs a per-keystroke prediction path and a ghost-text primitive; zsh's ZLE is the only shell scripting layer that gives a plugin both. See [Shell support](#shell-support) for why bash, fish, and PowerShell aren't there yet, and what it would take.

---

## Quick start

```bash
# Install
go install github.com/DerekCorniello/hunch@latest

# Set up the zsh integration (appends the source line to .zshrc)
hunch init --auto

# Restart your shell, and you're done.
# Hunch learns from every command. Predictions appear as you type.

# Start using it - predictions appear as ghost text
git clone https://github.com/user/repo.git
# ghost text: cd repo          press Right or End to accept
```

`hunch init` offers to import your existing `~/.zsh_history` so you don't
start from nothing, and prints a self-test accuracy number computed from that
same history right after:

```text
Self-test on your history: top-1 24%, top-3 31% (vs. 9% always guessing your most frequent command).
Run 'hunch eval' any time to recheck this against your live history.
```

After a few commands, Hunch learns your workflows:

```text
git clone REPO -> cd STR
cargo build    -> cargo run
ssh STR        -> ssh STR
```

If a suggestion looks wrong, `hunch why` explains the scoring behind it
instead of asking you to trust it (see [`hunch why`](#hunch-why)).

---

## Installation

### Pre-built binaries

No Go toolchain required. Download the binary for your platform from the
[latest release](https://github.com/DerekCorniello/hunch/releases/latest),
make it executable, and put it on your `PATH`:

```bash
# Linux (x86_64) - substitute your platform below
curl -L -o hunch https://github.com/DerekCorniello/hunch/releases/latest/download/hunch-linux-amd64
chmod +x hunch
sudo mv hunch /usr/local/bin/
```

Available builds: `hunch-linux-amd64`, `hunch-linux-arm64`,
`hunch-darwin-amd64`, `hunch-darwin-arm64`, `hunch-windows-amd64.exe`,
`hunch-windows-arm64.exe`.

On macOS, Gatekeeper will block the binary on first run because it is
unsigned. Clear the quarantine attribute with
`xattr -d com.apple.quarantine /usr/local/bin/hunch`.

To upgrade later, run `hunch update`. It downloads the current release for
your platform and replaces the running binary in place, so no Go toolchain
is needed.

### From source

```bash
go install github.com/DerekCorniello/hunch@latest
```

The binary is built at `~/go/bin/hunch` (or wherever `$GOBIN` points). Make sure it's on your `PATH`.

Build with a version string:

```bash
go install -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=v0.1.0" github.com/DerekCorniello/hunch@latest
```

Or from a local clone:

```bash
go install -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=$(git describe --tags --always)" .
```

### Dependencies

Hunch requires no external runtime dependencies. The Go binary is fully static (SQLite is handled by [`modernc.org/sqlite`](https://modernc.org/sqlite), a pure-Go port - no CGO needed).

---

## Shell integration

```bash
# Set up zsh integration and append the source line to .zshrc
hunch init --auto

# Without --auto, just prints the source line to add yourself
hunch init
# Prints: source /path/to/hunch/integrations/zsh/hunch.zsh
```

The integration:
- Auto-starts the daemon when sourced
- Shows inline ghost text as you type, via a persistent `serve` coprocess and zsh's `POSTDISPLAY` - accept with Right/End, cycle candidates with Alt-n / Alt-p
- Captures each command's exit code and working directory, feeding the location-affinity and outcome-weighting signals
- Sends recorded commands to the daemon asynchronously (non-blocking)
- Uses the `HUNCH_BIN` environment variable to locate the `hunch` binary (default: `hunch`)
- Silently degrades if the daemon is unavailable
- Composes with `zsh-autosuggestions` and similar plugins regardless of load order (see [Shell integration conflicts](#shell-integration-conflicts))

### Shell support

Hunch is zsh-only right now. This isn't an arbitrary limitation - it's that
inline ghost text (a suggestion appearing after your cursor as you type, which
you accept with a keystroke or type over) needs a shell scripting layer that
exposes two things: a per-keystroke hook, and a place to draw text the shell
itself won't try to execute. Whether a shell offers that varies a lot:

| Shell | Can a plugin do this? | Why / why not |
|-------|------------------------|----------------|
| **zsh** | Yes - this is what hunch uses | ZLE exposes both a per-keystroke hook and `POSTDISPLAY`, a variable built for exactly this. `zsh-autosuggestions` uses the same mechanism. |
| **bash** | No, not without a large add-on | Bash's line editor (GNU Readline) has no inline-suggestion concept at all. The only way in is [ble.sh](https://github.com/akinomyoga/ble.sh), which replaces Readline entirely with a pure-bash reimplementation - a heavy, non-default dependency. |
| **fish** | Not from outside fish | Fish already ships its own built-in autosuggestions, better than either of the above out of the box - but it doesn't expose a supported way for a third-party tool to feed suggestions into that mechanism. |
| **PowerShell** | Not from a script | PSReadLine has `ICommandPredictor`, an official, well-designed extension point for exactly this (used by Microsoft's own predictors) - but it requires a compiled .NET module, not a `.ps1` profile script. This is the one gap that's genuinely just unbuilt, not blocked by the shell. |

Given that, hunch focuses on zsh doing this well - inline suggestions, ranked
cycling, acceptance feedback, all backed by real regression coverage - rather
than spreading thin across four shells with three different, lesser
experiences. A PowerShell `ICommandPredictor` module is the most plausible
next platform if there's demand; bash and fish would need their own
ecosystems to open a door that isn't open today.

If you're on bash, fish, or PowerShell: `hunch daemon`, `hunch client`,
`hunch stats`, `hunch eval`, and `hunch why` all still work from the CLI, you
just won't get the shell-integrated ghost text.

---

## CLI reference

### `hunch init`

Set up the zsh integration.

```
--auto               Automatically append the source line to .zshrc
```

When run interactively, `hunch init` detects your `~/.zsh_history` and offers to import
it to jump-start predictions, then prints a self-test accuracy summary computed
from that same history (see `hunch eval` below). In non-interactive contexts
(piped, scripted), the prompt is skipped.

### `hunch import-history`

Import `~/.zsh_history` as training data for predictions.

```
--path <file>      History file path (default: ~/.zsh_history)
--threads <N>      Number of normalize worker threads (default: CPU count)
```

Processes history by parsing commands, normalizing them into templates, building
state transitions, and importing into the daemon as a seed.

Safe to re-run: counts combine by maximum, so importing the same history twice
leaves the graph unchanged rather than doubling it. Worth doing after an upgrade
that changes how transitions are recorded, since the import backfills context
that older data does not have.

### `hunch eval`

Measure how well hunch predicts your own history. Replays your shell history in
order, predicting each command using only the commands before it, which is how
the daemon sees your session as you work.

```
--path <file>      History file path (default: ~/.zsh_history)
--warmup <n>       Commands to learn from before scoring begins (default: 50)
```

Reports top-1, top-3, and top-5 hit rates, how often any suggestion was
offered, and a baseline: the hit rate you would get by always guessing your
single most frequent command. The baseline is the number worth comparing
against, since a shell history dominated by one command can make a weak model
look strong.

### `hunch why`

Explain the scoring behind a prediction instead of asking you to trust it.
Runs the same fallback ladder the daemon uses for a real suggestion (exact
directory, then ancestor directory, then no directory, then shorter history),
reports which level answered, and breaks down each candidate's score into its
components: time-decay weight, CWD affinity, prior-outcome affinity,
acceptance rate, and failure rate.

```
--state <prev1,prev2>   Prior commands (default: last 2 from ~/.zsh_history)
--cwd <dir>             Directory to explain (default: current directory)
--prior-outcome <s>     success, failure, or empty
--limit <n>             Max candidates to show (default: 5)
```

With no flags, `hunch why` explains "what would hunch suggest right now, in
this directory, given what I just ran" - so after seeing a suggestion you
don't understand, running it plain is usually enough:

```text
$ hunch why

hunch why

context matched: exact directory
  cwd:   /home/you/project
  state: git add PATH -> git commit FLAG STR

gates: a suggestion needs count >= 2 always; a fallback context also needs score >= 0.10

#  SCORE  COUNT  DECAY  CWD  PRIOR  ACCEPT  FAIL  NEXT
1  0.342  12     0.91   80%  -      -       5%    git push
2  0.201  4      0.75   -    -      -       -     git status
```

`-` means that boost is either disabled or has no data for this candidate, so
it left the score unchanged rather than pulling it up or down.

Scoring is only half the picture: a template like `cd PATH` still has to be
filled in with an actual directory, and sometimes more than one is plausible
(you've `cd`'d into two different repos recently, say). When that hydration
is genuinely ambiguous, `hunch why` shows what was considered:

```text
  cd PATH:
    1. cd hunch                             100%
    2. cd my-other-project                  74%
```

The same ambiguity is what live cycling (Alt-n/Alt-p) pages through in the
shell - if hunch is confident about the shape but unsure which directory,
you're not stuck with its first guess.

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
| `explain` | Get the scoring breakdown behind a prediction (JSON; see `hunch why` for a formatted view) |
| `reset` | Wipe all learned data |
| `export` | Export the transition graph as JSON |
| `normalize`| Normalize a raw command to its template |
| `stats` | Show daemon stats (size, half-life, alpha) |
| `config` | Show active daemon configuration |
| `import` | Import a seed JSON file |

#### `hunch client record`

```
--state <prev1,prev2>   Previous 1-2 commands (comma-separated)
--next <command>        The command that was run
--at <timestamp>        ISO 8601 timestamp
```

#### `hunch client predict`

```
--state <prev1,prev2>   Previous 1-2 commands (comma-separated)
--prefix <text>         Current buffer text for filtering
--limit <n>             Max suggestions (default: 3)
```

#### `hunch client explain`

```
--state <prev1,prev2>   Previous 1-2 commands (comma-separated)
--cwd <dir>             Current working directory
--prior-outcome <s>     success, failure, or empty
--limit <n>             Max candidates to break down (default: 5)
```

Same context flags as `predict`, but returns the full `ExplainResponse` JSON
(level, gating thresholds, per-candidate breakdown) instead of just the
ranked suggestions. `hunch why` is this same request with human-formatted
output.

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

Check for and install updates. Queries GitHub for the latest release and, if a
newer version exists, downloads the binary built for your platform, verifies it
against the release's `SHA256SUMS`, and replaces the running executable in
place. No Go toolchain is required. The daemon is restarted automatically
afterwards.

Verification fails closed: if the checksum file is missing or the digest does
not match, the download is discarded and nothing is installed.

The new binary is downloaded next to the current one and moved into place, so
the directory holding `hunch` must be writable. If it is not (for example
`/usr/local/bin` owned by root), re-run with elevated privileges. Platforms
with no published binary are told to reinstall from source instead.

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
| `HUNCH_MAX_IDLE_DAYS` | Hard cutoff: forget a transition untouched for this many days, regardless of count | `90` (`0` disables) |
| `HUNCH_ALPHA` | Additive smoothing | `0.5` |
| `HUNCH_BETA` | CWD-affinity boost strength | `0.75` |
| `HUNCH_GAMMA` | Failure-rate suppression strength | `0.5` |
| `HUNCH_DELTA` | Prior-outcome boost strength | `0.5` |
| `HUNCH_EPSILON` | Confirmed-acceptance boost strength | `0.5` |
| `HUNCH_MIN_CONFIDENCE` | Score a generalized match must reach to be shown | `0.10` |
| `HUNCH_MIN_COUNT` | Times a command must have been seen before it is suggested | `2` |
| `HUNCH_EXTRA_PARENTS` | Extra parent commands (comma-separated) | (none) |
| `HUNCH_IGNORE` | Extra regexes for sensitive commands to never record (comma-separated) | (none) |
| `HUNCH_LOG_LEVEL` | Log level | `info` |

Each scoring strength (`beta`/`gamma`/`delta`/`epsilon`) is a soft,
multiplicative adjustment that is the identity when its signal is absent; set
any to `0` to disable that signal.

`half_life_hours` and `max_idle_days` control two different things and are
intentionally separate. `half_life_hours` governs how fast a *still-alive*
transition's ranking influence fades - a command you ran 100 times legitimately
keeps some pull longer than a one-off, and that's by design. `max_idle_days` is
a flat, evidence-independent backstop: once you haven't touched something in
that many days, it's forgotten outright, no matter how much of a habit it used
to be. Set `max_idle_days = 0` to disable the hard cutoff and rely on
half-life decay alone.

`min_count` is how many times you must have run a command in a given context
before hunch will suggest it. The default of `2` means one-off commands are
never suggested: hunch predicts habits, and a habit is repeated by definition.
This matters more than it sounds, because a command run once in a context you
never revisit is the only candidate for that state, so it scores as the most
confident suggestion possible on the least evidence possible. Set it to `1` to
be suggested everything, including things you ran once by accident.

`min_confidence` controls how readily hunch generalizes. An exact-context match
is always shown. When there is no exact match, hunch widens the context (drop
the directory, then drop the oldest command) and shows the result only if it
scores at least this high. Lower it to see suggestions more often at the cost
of more wrong ones; set it to `1` to suppress generalized suggestions entirely
and only ever show exact-context matches. Sensitive commands matching a built-in or
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
max_idle_days = 90     # forget anything untouched this long, regardless of count
alpha = 0.5
beta = 0.75    # CWD-affinity boost
gamma = 0.5    # failure-rate suppression
delta = 0.5    # prior-outcome boost
epsilon = 0.5  # confirmed-acceptance boost
min_confidence = 0.10  # bar for suggestions from a generalized context
min_count = 2          # ignore commands you have only run once
accept_keys = ["right", "end"]
extra_parents = ["mycli", "teamtool"]
ignore = ['(?i)--api-token']  # extra sensitive-command patterns to never record
log_level = "info"
```

Precedence (lowest to highest): built-in defaults -> config file -> env vars -> CLI flags.

---

## Architecture

```
shell -> integration (thin adapter) -> daemon (background service) -> core/ (logic)
                                          |
                                     SQLite (WAL)
```

- **core/** - Pure logic. `normalize` (two-phase: unwrap wrappers, classify tokens), `graph` (transition counts), `predict` (additive-smoothed exponential decay scoring). Deterministic and stateless.
- **daemon/** - Background service. Owns SQLite, receives IPC events, calls core to update and predict. One request per connection over a Unix socket.
- **cli/** - Admin interface. Routes to init/daemon/client subcommands. Links the full daemon package.
- **integrations/** - Shell-specific adapters. Minimal shims that shell out to `hunch client`. No learning logic.

See [AGENTS.md](AGENTS.md) for the full architecture and design decisions.

---

## Platform support

| Platform | Status |
|----------|--------|
| Linux (x86_64, aarch64) | Full support |
| macOS (x86_64, arm64) | Supported |
| Windows (x86_64) | Daemon/CLI supported (Unix domain sockets); shell integration needs zsh (e.g. via WSL) |
| Other Unix (FreeBSD, etc.) | Supported (flock, XDG paths) |

The daemon, CLI, and `hunch client` commands build and run everywhere Go
cross-compiles. The shell integration itself is zsh, so on native Windows
(PowerShell/cmd) you get the CLI and daemon but not inline ghost text unless
you're running zsh under WSL. See [Shell support](#shell-support).

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

## How this compares

Hunch overlaps with tools you may already run. It is designed to sit alongside
them rather than replace them.

| Tool | What it answers | Relationship to hunch |
|------|-----------------|-----------------------|
| `zsh-autosuggestions` / fish autosuggestions | "What did I type that started this way?" | Prefix search over history. It needs you to start typing; hunch suggests before you type anything, based on what you just ran. They compose. |
| `atuin` | "What did I run before, and where?" | A searchable history database with sync. It is recall on demand; hunch is a prediction offered unprompted. Different moments. |
| `fzf` history widget | "Let me hunt through history interactively." | Explicit search you invoke. hunch never requires an invocation. |
| `thefuck` | "That command failed, what did I mean?" | Corrects the command you just ran. hunch proposes the next one. |

The distinguishing idea is that hunch models the *sequence*: it learns that
`cargo build` tends to be followed by `cargo run`, and conditions that on the
directory you are in and whether the last command succeeded. Prefix-matching
tools have no notion of what typically comes next.

Two honest caveats. Predictions are templates hydrated with a concrete
past command, so hunch suggests things you have run before, not novel commands.
And it needs history to be useful: run `hunch import-history` to start from
your existing history rather than from nothing.

Measure it on your own history with `hunch eval` rather than taking
any of this on faith.

## Non-goals

- No AI/LLM - purely statistical learning
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

Hunch registers its zle hooks through `add-zle-hook-widget` (zsh 5.3+), which
keeps a list of widgets per hook and runs all of them. It composes with
`zsh-autosuggestions` and similar plugins regardless of load order, and adopts
any widget a plugin bound directly. On zsh older than 5.3 it falls back to
chaining a single previous widget.

Sourcing hunch **after** other plugins is still recommended: when two plugins
both want to draw ghost text, the one that runs last is the one you see.

### Windows-specific issues

- Exclude `%LocalAppData%\hunch\` from Windows Defender real-time scanning to avoid SQLite lock contention.
- Ensure you're using Windows 10 version 1803 or later for Unix domain socket support.

### Getting more help

Run `hunch doctor` for a comprehensive health check, or check the logs at `~/.local/share/hunch/hunch.log`.

---

## License

MIT. See [LICENSE](LICENSE).
