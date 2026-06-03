# Hunch: Phased Implementation Plan

This plan covers the five remaining packages plus the cross-cutting platform
abstraction and configuration. Build order follows the dependency chain so
each phase is testable in isolation before the next layer is added.

---

## v1 Scope

Hunch is a working shell companion on Linux, macOS, and Windows. It
records every command, learns workflows, and suggests the next likely
command as fish-style ghost text. Predictions appear within seconds of
having enough history.

**CLI is very thin.** Just three root commands:

- `hunch init <shell>` -- print the source line for the user's rc file
- `hunch daemon start|stop|status` -- manage the daemon lifecycle
- `hunch client <op>` -- IPC ops (record, predict, reset, export) used
  by shell scripts and for ad-hoc inspection

No `hunch top`, `hunch stats`, `hunch predict`, `hunch normalize`,
`hunch export`, `hunch reset`, or `hunch config` subcommands in v1. Dev
tooling is deferred to v1.1.

---

## Architecture

```
+----------+      +---------+      +-------------+
|  shell   | <--> | inte-   | <--> |   daemon    |
|  (zsh/   |      | gration | <--> |  (long-     |
|  bash/   |      | (thin)  |      |   running)  |
|  fish/   |      +---------+      +------+------+
|  pwsh)   |                             |
+----------+                             |
                                         v
                               +---------+---------+
                               |      core/        |
                               |                   |
                               |  normalize        |
                               |  graph    <-- pure|
                               |  predict   <--    |
                               +---------+---------+
                                         |
                                         v
                                    +----+----+
                                    | sqlite  |
                                    +---------+
```

The CLI tool talks to the daemon over IPC. The shell integration uses
the `hunch client` subcommand (which speaks the same IPC) to record
commands and request predictions.

---

## Design Decisions (locked in)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | State representation | Last 2 commands | Captures workflows like `git add -> commit -> push`; sweet spot between coverage and context. |
| 2 | State padding | Empty string sentinel | First command in a session: state is `["", cmd]`. Sentinel stabilizes the window-2 key space. |
| 3 | State in storage | Workflow state recorded on every `record` | Data is captured for future state-aware queries. |
| 4 | State in predict IPC | Sent in every predict request | Shell buffers last 2 normalized templates and sends with each predict. Workflow-aware from day 1. |
| 5 | Decay model | Time-based, 30-day half-life | Adapts to changing habits; keeps long-term signal. |
| 6 | Score formula | Additive-smoothed probability | Graceful cold start; bounded in (0, 1]. |
| 7 | Smoothing constant | `alpha = 0.5` default, configurable | Both `alpha` and `halfLife` exposed in config. |
| 8 | Outcome in scoring | Use all transitions, no filter | Failures are still signal. Locked. |
| 9 | Outcome representation | String: `success` or `failure` | Shell maps `$? == 0` to success, else failure. Simple, no nuance. |
| 10 | CWD in scoring | Never used | Locked decision, not a v1 limitation. `types.State.CWD` is captured for type completeness only. |
| 11 | IPC protocol | JSON-lines over Unix socket / Windows named pipe | Debuggable with `socat`; throughput is irrelevant at human speed. |
| 12 | IPC connection model | One request per connection | Simplest framing; no pipelining. Fine at human speed. |
| 13 | IPC record timestamp | Trust the client | Shell sends `at`; daemon trusts it. No clock-sync issues between processes. |
| 14 | IPC predict shape | `{op, state, prefix, at, limit}` | Server-side prefix filter; client sends workflow state and buffer prefix. |
| 15 | SQLite driver | `modernc.org/sqlite` (pure Go) | No CGO; cross-platform; perf is fine. |
| 16 | Storage paths | OS-specific | XDG_CACHE_HOME on Linux, ~/Library/Caches on macOS, %LocalAppData% on Windows. |
| 17 | Daemon lifecycle | Per-shell launch + file lock | Cross-platform; no service config; first command starts it. |
| 18 | Stale lock recovery | PID file + liveness check | On startup, if lock is held, read PID; alive -> exit 0, dead -> remove stale lock + PID, retry. Self-healing. |
| 19 | Daemon stop | SIGTERM (Windows: console ctrl event) | Standard POSIX. Daemon traps, flushes, exits. CLI polls socket for up to 5s. |
| 20 | CLI subcommands | `init`, `daemon`, `client` | Very thin. Init onboarding; daemon lifecycle; client IPC. |
| 21 | Seed format | JSON with metadata wrapper | Top-level `{version, source, generated_at, transitions: [...]}`. Forward-compat and provenance. |
| 22 | Seed merge | Counts additive, `lastSeen = max(existing, seed)` | Local data always wins freshness; seed is a floor for counts. |
| 23 | Seed import trigger | First-run only via `--seed` flag | DB has zero rows AND seed path is set. No `hunch seed` subcommand in v1. |
| 24 | Config file | TOML at XDG_CONFIG_HOME/hunch/config.toml | Human-editable, well-typed, version-stable. Read by daemon; not exposed via CLI. |
| 25 | Config precedence | defaults < config file < env < CLI flags | Standard layering. |
| 26 | Platform support | Linux + macOS + Windows | 4 shells: zsh, bash (4+), fish, PowerShell (7.4+). cmd.exe skipped (no hooks). |
| 27 | Platform shim | `daemon/osutil` package with build tags | Hides socket URL, file lock, path, signal differences. ~200-300 lines. |
| 28 | Prediction UX | Fish-style ghost text, fixed gray | Shown at prompt; user types to refine; right-arrow or End accepts. |
| 29 | Debounce | 100ms | Standard for zsh-autosuggestions and similar. |
| 30 | Accept key (zsh/fish/pwsh) | Right arrow + End | Fish default. User-configurable. |
| 31 | Accept key (bash) | Tab | Readline cannot do inline ghost text; accept-on-Tab is a clean fallback. |
| 32 | vi mode bindings | Both emacs and vi normal/insert modes | Bind accept key in both. Per-shell keymap differences. |
| 33 | PSReadLine | Disable native history prediction | Call `Set-PSReadLineOption -PredictionSource None` on first run. Our integration is the only predictor. |
| 34 | Secret filtering | None | Document in README that hunch is local-only storage. User is responsible for what they type. |
| 35 | Test isolation | `HUNCH_SOCKET` / `HUNCH_DB_PATH` env override | Tests `t.Setenv` to point at `t.TempDir()`. |
| 36 | Versioning | `git describe` via `-ldflags "-X main.Version=..."` | `hunch --version` prints it. |
| 37 | Distribution | `go install github.com/DerekCorniello/hunch@latest` | Documented in README. No CI, no Homebrew, no release automation in v1. |

---

## Configuration

### Precedence (lowest to highest)

1. Built-in defaults
2. Config file (`<ConfigDir>/hunch/config.toml`)
3. Environment variables (`HUNCH_*`)
4. CLI flags

### Config file location

- Linux:   `~/.config/hunch/config.toml` (`$XDG_CONFIG_HOME` honored)
- macOS:   `~/Library/Application Support/hunch/config.toml`
- Windows: `%AppData%\hunch\config.toml`

If the file is missing or unreadable, fall back to defaults. Missing
fields fall back to defaults (partial configs are fine).

### Schema (v1)

```toml
# Override IPC socket/pipe location.
# Unix default:    <CacheDir>/hunch.sock
# Windows default: \\.\pipe\hunch
socket = "/run/user/1000/hunch.sock"

# Override SQLite database location.
# Unix default:    <DataDir>/hunch.db
# Windows default: %LocalAppData%\hunch\hunch.db
db_path = "/var/lib/hunch/hunch.db"

# Keys that accept the current ghost-text suggestion.
# In zsh, fish, and PowerShell, all of these are wired up.
# In bash, only "tab" is used; the others are ignored.
accept_keys = ["right", "end"]  # or ["tab"] for bash-style

# Path to the daemon binary (used by shell integrations for auto-start).
# If unset, the integration searches $PATH.
daemon_bin = "/usr/local/bin/hunch"

# Half-life for decay, in hours. Default 720 (30 days).
half_life_hours = 720

# Smoothing constant for the predict score formula. Default 0.5.
alpha = 0.5

# Extra parent commands whose subcommand is preserved verbatim during
# normalization. Merged with the built-in DefaultParents.
extra_parents = ["mycli", "teamtool"]

# Log level: debug, info, warn, error. Default info.
log_level = "info"
```

### Environment overrides

Every config key has an env equivalent:

- `HUNCH_SOCKET`
- `HUNCH_DB_PATH`
- `HUNCH_ACCEPT_KEYS` (comma-separated)
- `HUNCH_DAEMON_BIN`
- `HUNCH_HALF_LIFE_HOURS`
- `HUNCH_ALPHA`
- `HUNCH_EXTRA_PARENTS` (comma-separated)
- `HUNCH_LOG_LEVEL`

---

## Platform Support and Abstraction

Daemon-internal OS differences are hidden behind a small platform shim
(build-tagged files in `daemon/`). The shim exposes:

```go
// Locker abstracts advisory file locking
// (flock on Unix, LockFileEx on Windows).
type Locker interface {
    Lock() error   // non-blocking; returns ErrLocked if held
    Unlock() error
    Close() error
}

// OpenLock creates/opens the lock file at path and returns a Locker.
func OpenLock(path string) (Locker, error)

// SocketURL returns the appropriate URL for the IPC listener.
// Unix:    "unix:///path/to/socket"
// Windows: "pipe://hunch"
func SocketURL(path string) string

// CacheDir returns the OS-specific cache directory for hunch.
func CacheDir() (string, error)

// DataDir returns the OS-specific data directory for hunch (for the DB).
func DataDir() (string, error)

// ConfigDir returns the OS-specific config directory for hunch.
func ConfigDir() (string, error)
```

Build tags:

- `daemon/osutil_unix.go`:    `//go:build unix` (linux, darwin, *bsd)
- `daemon/osutil_windows.go`: `//go:build windows`

This adds ~200-300 lines of platform code (mostly trivial), keeps the
rest of the daemon 100% portable, and makes the Windows port a small
delta rather than a rewrite.

---

## Phase 1: `core/graph`

### Goal

Track state -> next-command transitions with counts and last-seen
timestamps. Pure logic, no IO, no shell awareness. This is the
foundation for everything.

### Files

- `core/graph/graph.go` -- public API
- `core/graph/seed.go` -- seed merge + export
- `core/graph/graph_test.go` -- tests

### Public API

```go
package graph

type Transition struct {
    State    []string  // last N templates, most recent last
    Next     string
    Count    int
    LastSeen time.Time
}

type Graph struct { /* unexported fields */ }

func New(windowSize int) *Graph
func (g *Graph) Record(state []string, next string, at time.Time)
func (g *Graph) Transitions(state []string) []Transition
func (g *Graph) Decay(at time.Time, halfLife time.Duration)
func (g *Graph) Merge(seed []Transition) error
func (g *Graph) All() []Transition   // for export; sorted by (state, next)
func (g *Graph) Size() int            // distinct transition count
```

### Design notes

- **State key**: internal `stateKey` is `strings.Join(state, "\x00")` --
  the null byte can't appear in normalized templates (tokens are
  space-separated identifiers), so it's a safe separator. Preserves
  order.
- **State padding**: callers pass the raw `Previous` slice from
  `types.State`, which is already padded to window size with `""`
  sentinels by the daemon. The graph doesn't pad; it uses what's passed.
- **Storage**: `map[stateKey]map[string]*entry` where `entry` is
  `{count int, lastSeen time.Time}`. Bounded by distinct transitions
  ever observed (for a single user, typically 10k-100k).
- **Concurrency**: `sync.RWMutex` on the whole graph. Write lock for
  `Record`/`Merge`/`Decay`; read lock for
  `Transitions`/`All`/`Size`. Lock contention is negligible at human
  command rates.
- **Decay on read vs. write**: storage is not compacted. `Transitions`
  returns raw counts; the predict package applies the decay formula
  when scoring. `Decay()` exists as a hook for future compaction
  (currently a no-op).
- **Window size**: passed to `New()`. The graph doesn't enforce that
  `Record` is called with the right number of tokens; it uses what's
  passed. Validation is the caller's job.
- **Merge semantics**:
  - `count += seed.Count` (additive; commutative)
  - `lastSeen = max(existing.LastSeen, seed.LastSeen)` (newer wins)
- **`All()` ordering**: sort by `(state, next)` lexicographically. Stable
  for export diffability.

### Test plan

- `Record` then `Transitions` returns the transition with count 1.
- Multiple `Record` of the same transition increments count and updates
  `LastSeen`.
- `Transitions` for an unknown state returns nil.
- Different states are tracked independently (no cross-contamination).
- `Merge` adds new transitions and increments existing ones.
- `Merge` takes the newer `lastSeen` when both exist.
- `All` returns every distinct transition in sorted order.
- `Size` reflects unique (state, next) pairs.
- Concurrent `Record` calls do not race (run with `-race`).
- `Decay` does not corrupt or lose data (current behavior: no-op).

---

## Phase 2: `core/predict`

### Goal

Score and rank transitions for a given state, returning a slice of
`types.Suggestion`. Pure logic; depends only on graph and types.

### Files

- `core/predict/predict.go` -- public API
- `core/predict/score.go` -- scoring formula
- `core/predict/predict_test.go` -- tests

### Public API

```go
package predict

type Predictor struct { /* unexported fields */ }

// New constructs a Predictor.
// halfLife: time for an observation to halve in effective weight.
// alpha:    additive smoothing constant.
func New(g *graph.Graph, halfLife time.Duration, alpha float64) *Predictor
func (p *Predictor) Predict(state types.State, at time.Time, limit int) []types.Suggestion
```

### Scoring formula

For each transition `t` in `graph.Transitions(state)`:

```
age      = at.Sub(t.LastSeen)
weight   = exp(-age / halfLife)
effCount = float64(t.Count) * weight
total    = sum of effCount for all transitions from this state
N        = number of distinct `next` values from this state
alpha    = smoothing constant (passed to New)

score(t) = (effCount + alpha) / (total + alpha * N)
```

Properties:
- Bounded in `(0, 1]`.
- More observations => higher score.
- Recent observations weighted more heavily than old ones.
- Cold start handled: a single observation gets a non-trivial score
  because `alpha` dominates when `total` is small.
- **CWD is never used in scoring** (locked decision; `state.CWD` is
  available but ignored).
- **All transitions are scored, regardless of outcome** (locked
  decision).

### Design notes

- **Half-life and alpha**: both passed to `New()` so the daemon can
  surface them as config (`HUNCH_HALF_LIFE_HOURS`, `HUNCH_ALPHA`).
- **Limit**: `0` means "all suggestions"; positive values truncate to
  the top N.
- **Sort order**: descending by score, then descending by count as
  tie-breaker (deterministic when scores are very close).
- **Empty state**: `Predict` with an empty `Previous` and no graph data
  returns nil.

### Test plan

- `Predict` on empty graph returns nil.
- `Predict` with a single transition returns it with score > 0.
- Multiple transitions are ranked by score.
- Older transitions rank lower after time advance.
- `limit` truncates correctly.
- Smoothing: a single observation for transition A and a single
  observation for transition B in the same state with equal age tie
  (both `(1 + alpha) / (2 + 2*alpha)`). Confirm tie-breaker is
  deterministic.
- Different states produce different rankings (no cross-contamination).
- CWD on the state does not affect score (locked behavior).
- Outcomes on recorded transitions do not affect score (locked
  behavior).

---

## Phase 3: `daemon`

### Goal

Persist the graph to SQLite, serve IPC over a Unix socket (or Windows
named pipe), and own the single-instance lifecycle. Depends on
`core/*`, the platform shim, and SQLite.

### Files

- `daemon/daemon.go`         -- main loop, lifecycle
- `daemon/ipc.go`            -- protocol types, socket handling
- `daemon/store.go`          -- SQLite open/migrate/save/load
- `daemon/config.go`         -- TOML loader, env override, Options
- `daemon/osutil_unix.go`    -- `//go:build unix`
- `daemon/osutil_windows.go` -- `//go:build windows`
- `daemon/daemon_test.go`    -- integration tests

### SQLite schema

```sql
CREATE TABLE IF NOT EXISTS transitions (
    state     TEXT NOT NULL,    -- JSON array of templates
    next      TEXT NOT NULL,
    count     INTEGER NOT NULL,
    last_seen INTEGER NOT NULL, -- unix seconds
    PRIMARY KEY (state, next)
);
CREATE INDEX IF NOT EXISTS idx_state ON transitions(state);
```

State is stored as a JSON array (e.g.,
`["git add STR","git commit FLAG STR"]`). Debuggable, diffable, and
matches the seed format.

### IPC protocol (JSON-lines)

**Request: record**
```json
{"op":"record","state":["git add STR"],"next":"git commit FLAG STR","outcome":"success","cwd":"/x","at":"2025-12-01T10:00:00Z"}
```

**Request: predict**
```json
{"op":"predict","state":["git add STR","git commit FLAG STR"],"prefix":"git pu","at":"2025-12-01T10:00:00Z","limit":3}
```

**Request: reset**
```json
{"op":"reset"}
```

**Request: export**
```json
{"op":"export"}
```

**Response (success)**
```json
{"ok":true}
```

**Response (predict)**
```json
{"suggestions":[{"template":"git push origin main","score":0.42,"count":7}]}
```

**Response (export)**
```json
{"transitions":[{"state":["..."],"next":"...","count":42,"last_seen":"..."}]}
```

**Response (error)**
```json
{"error":"message"}
```

### Connection model

One request per connection. Client opens, writes one JSON line
(newline-terminated), reads one JSON line response, closes.

### Lifecycle

- `daemon.Run(ctx context.Context, opts Options) error` -- main loop,
  blocks until `ctx` is cancelled or a fatal error occurs.
- Paths (defaults; overridden by config / env):
  - Socket: `<CacheDir>/hunch.sock` (Unix) or `\\.\pipe\hunch` (Windows)
  - DB:     `<DataDir>/hunch.db`
  - Lock:   `<DataDir>/hunch.lock`
  - PID:    `<DataDir>/hunch.pid`
- Single instance + stale recovery:
  1. Try non-blocking `Locker.Lock()`.
  2. On `ErrLocked`:
     - Read PID file.
     - If process is alive: exit 0 (another instance is running).
     - If dead: log warning, remove stale lock + PID, retry once.
- Startup sequence:
  1. Acquire lock (with stale recovery above).
  2. Write PID file.
  3. Open SQLite, run migrations.
  4. Load all transitions into in-memory `*graph.Graph`.
  5. If `opts.SeedPath` is set AND DB has zero rows, load and merge
     the seed.
  6. Start IPC listener.
  7. On `ctx.Done()`: flush graph to SQLite, close socket, release
     lock, remove PID file.
- Record path:
  1. Update in-memory graph (acquire write lock).
  2. Increment a dirty counter.
  3. If dirty counter exceeds threshold (50) or a timer fires (5s),
     flush to SQLite in a single transaction.
- Predict path:
  1. Acquire read lock on graph.
  2. Call `predict.Predict` for the given state.
  3. Filter suggestions whose template starts with `prefix`.
  4. Return top N.
- Signal handling: `signal.Notify` on SIGTERM and SIGINT. On either,
  cancel the context, triggering the shutdown sequence above.

### Concurrency

- Graph has its own `sync.RWMutex` (Phase 1).
- SQLite writes are serialized via a single goroutine with a write
  channel to avoid concurrent-write contention.
- IPC requests handled by one goroutine per connection (standard
  `net.Listener` accept loop).

### Error handling

- Socket already in use + live process: exit 0.
- Stale lock + dead process: log warning, clean up, retry.
- SQLite open/migrate failure: log to stderr, exit 1.
- Per-request error: respond with `{"error":"..."}`, keep serving.
- Per-request panic: recover, respond with `{"error":"internal"}`,
  keep serving. Never let a bad message kill the daemon.

### Logging

- stderr only in v1.
- Format: `log/slog` JSON or text handler (configurable).
- Levels: debug, info, warn, error. Default level: info. Override via
  `HUNCH_LOG_LEVEL` (env) or `log_level` (config file).

### Test plan

- Record then predict roundtrip works end-to-end via the socket.
- Persistence: write, stop, restart, verify data is there.
- Single instance: second `daemon.Run` against the same lock fails
  cleanly.
- Stale lock recovery: simulate a dead PID, verify the next startup
  cleans up and proceeds.
- Concurrent records: no races, no lost writes.
- Reset clears the in-memory graph and the SQLite table.
- Export returns all transitions in a stable order.
- Predict filters by prefix server-side.
- Bad input (malformed JSON, unknown op) returns an error response,
  not a crash.
- Context cancellation triggers a clean shutdown and flush.
- SIGTERM triggers the same shutdown path.
- Config / env override: set `HUNCH_SOCKET` to a `t.TempDir()` path,
  verify the daemon uses it.

---

## Phase 4: `cli`

### Goal

Very thin developer/admin interface focused on setup and the IPC
client. The shell integration is the primary user; the CLI exists to
bootstrap the daemon and let users inspect state via the `client`
subcommand.

### Files

- `main.go` (repo root)    -- entry point, dispatches to subcommands
- `cli/root.go`            -- `--version`, `--help`, shared flags
- `cli/init.go`            -- `hunch init <shell>`
- `cli/daemon.go`          -- `hunch daemon start|stop|status`
- `cli/client.go`          -- `hunch client record|predict|reset|export`
- `cli/cli_test.go`        -- per-subcommand tests

### Subcommand behavior

| Subcommand | Talks to | Behavior |
|------------|----------|----------|
| `hunch init zsh\|bash\|fish\|pwsh` | none (offline) | Print the source line to add to the rc file. Path is resolved from the binary's own location. |
| `hunch daemon start` | direct (forks) | Re-exec self with `daemon run`; detach (Unix: `Setsid`; Windows: `CREATE_NEW_PROCESS_GROUP`); poll socket for up to 2s; print resolved socket path. |
| `hunch daemon stop` | direct (signal) | Read PID file, send SIGTERM (Windows: console ctrl); poll socket for up to 5s. |
| `hunch daemon status` | direct (filesystem) | Check socket + PID file. Print `running` / `stopped` / `stale lock`. |
| `hunch client record --state <t>... --next <t> --outcome <s\|f> --cwd <path> --at <rfc3339>` | daemon IPC | Send `record`. Exit 0 on success, non-zero on error. |
| `hunch client predict --state <t>... --prefix <text> --at <rfc3339> --limit N` | daemon IPC | Send `predict`. Print JSON suggestions to stdout. |
| `hunch client reset` | daemon IPC | Send `reset`. |
| `hunch client export` | daemon IPC | Send `export`. Print JSON to stdout. |
| `hunch --version` | none | Print version (from ldflags). |
| `hunch --help` | none | Print usage. |

### `hunch init` output

For `hunch init zsh`, prints:

```
Add this line to your ~/.zshrc, then restart your shell or run `source ~/.zshrc`:

    source /path/to/hunch/integrations/zsh/hunch.zsh
```

The path is resolved at runtime from `os.Executable()` + a known
relative offset to the integrations directory. For `go install`
installs, the integrations are co-located with the binary.

### `hunch daemon start` flow

1. Resolve the path to the current binary (`os.Executable()`).
2. Re-exec it as a child with `os/exec`, with args
   `[os.Args[0], "daemon", "run"]`, setting:
   - Unix: `&syscall.SysProcAttr{Setsid: true}`
   - Windows: `&syscall.SysProcAttr{CreationFlags: 0x00000200}` (CREATE_NEW_PROCESS_GROUP)
3. Stdout/stderr of the child are discarded (or piped to a log file
   in the data dir for postmortem; TBD at implementation time).
4. Poll the socket path (or named pipe) for up to 2 seconds.
5. On success: print `hunch daemon started (socket: <path>)`. Exit 0.
6. On timeout: print error, exit 1.

### `hunch daemon stop` flow

1. Read the PID file.
2. If absent: print `hunch daemon is not running`, exit 0.
3. Send SIGTERM (Unix) or `GenerateConsoleCtrlEvent` to the process
   group (Windows).
4. Poll the socket for up to 5 seconds.
5. On socket removal: print `hunch daemon stopped`, exit 0.
6. On timeout: print `hunch daemon did not stop in 5s`, exit 1.

### `hunch daemon status` flow

1. Check if the socket file exists.
2. Read the PID file.
3. If both exist and the PID is alive: print `running (pid: <pid>,
   socket: <path>)`, exit 0.
4. If socket exists but PID is dead: print `stale lock`, exit 1.
5. Otherwise: print `stopped`, exit 1 (so scripts can check exit code).

### `hunch client` flow

Thin wrapper: each subcommand builds the JSON request, opens a
connection to the daemon socket, writes the request, reads the
response, prints the result. Same code path the shell integration
uses internally.

### Design notes

- **Argument parsing**: one `flag.FlagSet` per subcommand. No
  third-party CLI library.
- **Daemon not running (for `client` and `daemon stop`/`status`)**:
  print a clear error and exit non-zero. Do not auto-start.
- **No config subcommand in v1**: the daemon reads the config file
  directly. Users edit the TOML by hand.
- **No stats / top / predict / export / reset CLI subcommands in v1**:
  all available via `hunch client` IPC ops.

### Test plan

- Each subcommand produces the expected output for representative
  inputs.
- `hunch init` prints the correct source line.
- `hunch daemon start` + `status` + `stop` roundtrip works.
- `hunch client record|predict|reset|export` work end-to-end.
- Subcommands fail with a clear error when the daemon is not running.
- `--version` prints a non-empty version string.
- `hunch export` output round-trips: export, reset, import (via seed)
  restores the same data.

---

## Phase 5: `integrations/{zsh,bash,fish,powershell}`

### Goal

Thin shell adapters that capture commands, query the daemon for
predictions, and render suggestions as ghost text. **No learning
logic, no normalization in the shell.** The shell only does IO; all
intelligence lives in the daemon/core.

### Files

- `integrations/zsh/hunch.zsh`         -- sourced from `.zshrc`
- `integrations/bash/hunch.bash`       -- sourced from `.bashrc`
- `integrations/fish/hunch.fish`       -- dropped into `~/.config/fish/conf.d/`
- `integrations/powershell/hunch.ps1`  -- dot-sourced from `$PROFILE`

The shell scripts shell out to `hunch client` for all IPC.

### Prediction model

**Capture (after each command):**

1. Compute the current template (the shell integration may do this
   client-side via a small helper, or use the `hunch client normalize`
   op if we add one in implementation; for v1, the shell buffers raw
   commands and uses the templates returned by record responses).
2. Build the state from the last 2 cached templates (or `["", ""]` for
   the first command of a session).
3. Run `hunch client record --state ... --next ... --outcome
   success|failure --cwd "$PWD" --at <rfc3339>` with a 50ms timeout.
4. Push the just-recorded template into the buffer (drop the oldest).

**Predict (on every keystroke, 100ms debounce):**

1. Read the current buffer (e.g. `$LBUFFER` in zsh, `$READLINE_LINE`
   in bash, `commandline` in fish, `$PSReadLine` buffer in PowerShell).
2. Build the state from the last 2 cached templates.
3. Run `hunch client predict --state ... --prefix <buffer> --at
   <rfc3339> --limit 3` with a 50ms timeout.
4. Parse the JSON response (with `jq` if available, fall back to
   `grep`/`sed` otherwise).
5. Take the top suggestion, render as ghost text.

**Auto-start:** on source, if the socket/pipe doesn't exist, spawn
`hunch daemon start` in the background. Silent on failure.

**Failure isolation:** every IPC call has a 50ms timeout. If the daemon
is slow or dead, the shell must not hang. Failures are silent.

### zsh integration (`hunch.zsh`)

- **Capture**: add `_hunch_record` to `precmd_functions`.
- **Prediction**: a ZLE widget (`_hunch_predict`) bound to
  `zle-line-init` and `zle-keymap-select`. Uses
  `region_highlight` to render the suggestion as ghost text.
- **Debounce**: 100ms via `zsh/sched`.
- **Accept key**:
  - Right arrow: `bindkey '^[[C' _hunch_accept`
  - End: `bindkey '^[[F' _hunch_accept`
  - In vi mode: `bindkey -M vicmd '^[[C' _hunch_accept_vi` and End
- **Ghost text color**: fixed gray (terminal color 8, bright black).

### bash integration (`hunch.bash`)

- **Bash 4+ required.** Detected at source time; on bash 3.x (macOS
  default), the integration prints a one-time warning to stderr and
  no-ops.
- **Capture**: append `_hunch_record` to `PROMPT_COMMAND` (using an
  array form for bash 4+).
- **Prediction**: bash readline cannot do inline ghost text reliably.
  v1 uses accept-on-Tab: bind Tab to `_hunch_accept` which calls the
  daemon and inserts the top suggestion.
- **Ghost text**: not rendered (readline limitation).
- **Accept key**: Tab only.

### fish integration (`hunch.fish`)

- **Capture**: bind to `fish_postexec` event.
- **Prediction**: override `fish_mode_prompt` or use `bind` to call
  `_hunch_predict` on each prompt render. Render ghost text using
  `set_color 808080` and `commandline -a` (or `-i` for insert).
- **Debounce**: 100ms via `sleep 0.1` in the predict function (or
  fish's built-in event coalescing).
- **Accept key**:
  - Right arrow: `bind right _hunch_accept`
  - End: `bind end _hunch_accept`
  - In vi mode: `bind --mode viins right _hunch_accept` and End

### PowerShell integration (`hunch.ps1`)

- **PowerShell 7.4+** (PSReadLine 2.3+) required. Document in
  README.
- **Capture**: hook into `PSReadLine`. Register a function on
  `PSReadLine` events (e.g., a custom `OnCommandLineAccepted` or
  similar) that calls `hunch client record` after each command.
- **Prediction**: use `PSReadLine`'s `PredictionView` or a custom key
  handler. v1 binds a function to Right arrow (and End) that calls
  `hunch client predict` and inserts the top suggestion via
  `[Microsoft.PowerShell.PSConsoleReadLine]::Insert()`.
- **PSReadLine conflict**: on first run, call
  `Set-PSReadLineOption -PredictionSource None` to disable PSReadLine's
  built-in history prediction. Our integration is the only predictor.
- **Accept key**: Right arrow + End. In vi mode: register handlers
  in both emacs and vi mode via `Set-PSReadLineKeyHandler -ViMode`.

### Design notes

- **No shell-specific logic in the Go code**: `hunch client` is
  shell-agnostic. All shell quirks (zle widgets, readline, fish
  events, PSReadLine) live in the shell scripts.
- **No persistence in the shell**: the shell holds a tiny buffer of
  the last 2 normalized templates (used for predict state). Everything
  else is in the daemon.
- **No secret filtering**: hunch records every command. README will
  state clearly that hunch stores all commands locally in
  `<DataDir>/hunch.db`; users who handle secrets on the command line
  are responsible for the implications.
- **`hunch client` discovery**: shell scripts look up the binary via
  `command -v hunch` (Unix) or `Get-Command hunch` (PowerShell).
  Override via `HUNCH_BIN` env var or `daemon_bin` config.

### Test plan

- `hunch client` round-trips with the daemon: record, predict, verify.
- `hunch client` exits non-zero and prints to stderr on daemon
  unreachable.
- Shell scripts: manual testing is the primary path. Lint with
  `shellcheck` for the bash script. The zsh, fish, and PowerShell
  scripts can be syntax-checked with `zsh -n`, `fish -n`, and the
  PowerShell parser respectively.

---

## End-to-end flow

1. User starts a zsh session. `hunch.zsh` is sourced, which checks
   for the socket. If absent, it spawns `hunch daemon start` in the
   background.
2. User types `git add .`. ZLE calls `_hunch_predict` on line init
   and on each keystroke (with 100ms debounce). The widget calls
   `hunch client predict` with the last 2 cached templates as state
   and the current buffer as prefix.
3. The daemon scores transitions from the given state, filters to
   those starting with the prefix, returns the top match.
4. ZLE renders the suggestion as ghost text via `region_highlight`.
5. User accepts with Right arrow (or keeps typing to refine).
6. User presses Enter; the command runs.
7. `precmd` fires; `_hunch_record` calls `hunch client record` with
   the executed command, its outcome, and the current CWD.
8. The daemon updates the in-memory graph; SQLite is flushed within
   5 seconds.
9. Repeat.

---

## Build order checklist

- [ ] **Phase 1**: `core/graph`
  - [ ] `graph.go` with New/Record/Transitions/All/Size/Merge/Decay
  - [ ] `seed.go` with Seed type (wrapper with metadata)
  - [ ] `graph_test.go` with race detector
  - [ ] No new dependencies

- [ ] **Phase 2**: `core/predict`
  - [ ] `predict.go` with New/Predict
  - [ ] `score.go` with the smoothed formula
  - [ ] `predict_test.go` with decay + ranking cases
  - [ ] No new dependencies

- [ ] **Phase 3**: `daemon`
  - [ ] `daemon.go` with Run/lifecycle + signal handling
  - [ ] `ipc.go` with protocol types and socket handling
  - [ ] `store.go` with SQLite open/migrate/save/load
  - [ ] `config.go` with TOML loader + env override
  - [ ] `osutil_unix.go` and `osutil_windows.go` with the platform
    shim
  - [ ] `daemon_test.go` with IPC roundtrip, persistence, stale-lock
    recovery
  - [ ] New dep: `modernc.org/sqlite`
  - [ ] New dep: `github.com/pelletier/go-toml/v2`

- [ ] **Phase 4**: `cli`
  - [ ] `main.go` dispatches to subcommands
  - [ ] `init.go` (print source line)
  - [ ] `daemon.go` (start|stop|status)
  - [ ] `client.go` (record|predict|reset|export)
  - [ ] `root.go` (--version, --help)
  - [ ] `cli_test.go` per subcommand
  - [ ] No new dependencies

- [ ] **Phase 5**: `integrations`
  - [ ] `integrations/zsh/hunch.zsh` (ghost text + right/End accept
    in emacs and vi modes)
  - [ ] `integrations/bash/hunch.bash` (Tab-accept, no ghost text,
    bash 4+ required)
  - [ ] `integrations/fish/hunch.fish` (ghost text + right/End accept
    in default and vi modes)
  - [ ] `integrations/powershell/hunch.ps1` (PSReadLine hook, right
    accept, disable native prediction)
  - [ ] Manual test in each shell
  - [ ] No new dependencies

---

## Resolved questions (decisions made, not deferred)

- **State padding**: empty string sentinel `""`.
- **State in predict IPC**: shell sends last 2 cached templates.
- **Seed merge with conflicting `lastSeen`**: take newer (max).
- **Seed JSON shape**: wrapper with `{version, source, generated_at,
  transitions}`.
- **Seed import trigger**: first-run only via `--seed` daemon flag.
- **CWD in scoring**: **never** used. Permanent decision.
- **Outcome in scoring**: use all transitions. No outcome filter.
- **Outcome type**: string `success`/`failure`.
- **Storage paths**: OS-specific (XDG / ~/Library/Caches /
  %LocalAppData%).
- **Daemon stop**: SIGTERM.
- **Stale lock recovery**: PID file + liveness check.
- **IPC connection model**: one request per connection.
- **IPC record timestamp**: trust the client.
- **CLI subcommands**: very thin -- `init`, `daemon`, `client` only.
- **Prediction UX**: fish-style ghost text, fixed gray, 100ms debounce.
- **Accept key**: Right + End (zsh/fish/pwsh), Tab (bash).
- **vi mode**: bind in both emacs and vi modes.
- **PSReadLine**: disable native history prediction.
- **Secret filtering**: none. Documented as local-only in README.
- **Config file**: TOML at XDG_CONFIG_HOME/hunch/config.toml.
- **Test isolation**: env var override via `t.Setenv`.
- **Platform support**: Linux + macOS + Windows, with `osutil` shim.
  Shells: zsh, bash, fish, PowerShell. cmd.exe skipped.
- **Versioning**: git describe via ldflags.
- **Distribution**: `go install` documented in README.

## Deferred to v1.1+ (intentional)

- **More CLI subcommands**: `top`, `stats`, `predict`, `normalize`,
  `export`, `reset`, `config show/path`. The shell integration is
  the v1 user; dev tooling comes in v1.1.
- **State fallback**: if no transitions exist for `[A, B]`, try `[B]`
  alone.
- **Compaction**: `graph.Decay` is currently a no-op. Storage grows
  with the number of distinct transitions ever observed. For a single
  user, this is bounded (~100k).
- **Multi-window**: currently hardcoded to last 2.
- **Seed on populated DB**: only first-run imports in v1.
- **Shell history bootstrap**: hunch is independent of shell history
  files.
- **CWD-aware scoring**: locked-out by decision. State.CWD stays in
  the type for completeness.
- **Outcome-nuanced scoring**: locked-out by decision. All outcomes
  count equally.
- **CI / release automation**: no GitHub Actions, no Homebrew tap, no
  release workflow. `go install` is the install path.
- **Dashboard / TUI**: no in-terminal dashboard in v1.

## Open at implementation time (small, defensible defaults)

These are small enough that I will pick a sensible default at
implementation time and document. Flag any that you want to lock in
upfront:

- **`hunch client normalize` op**: needed for shells that don't
  buffer raw commands. Will add if it turns out to be cleaner.
- **Integration file install path**: `~/.local/share/hunch/integrations/`
  on Unix (XDG_DATA_HOME), `%ProgramData%\hunch\integrations\` on
  Windows. `hunch init` resolves to this path.
- **Daemon log file**: stderr in foreground; if started detached via
  `hunch daemon start`, redirect to `<DataDir>/hunch.log`. TBD.
- **README structure**: I'll write it when the implementation is done.

---

## End

When all five phases are done, hunch is a working shell companion on
Linux, macOS, and Windows: install the binary, source the shell init,
and the daemon starts learning from the first command. Predictions
appear as ghost text within seconds of having enough history.
