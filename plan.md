# Hunch: Phased Implementation Plan

This plan covers the five remaining packages, from foundation to user-facing
product. Build order follows the dependency chain so each phase is testable in
isolation before the next layer is added.

---

## Architecture

```
+----------+      +---------+      +-------------+
|  shell   | <--> | inte-   | <--> |   daemon    |
|  (zsh/   |      | gration |      |  (long-     |
|  bash/   |      | (thin)  |      |   running)  |
|  fish)   |      +---------+      +------+------+
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

The CLI tool (`hunch <subcommand>`) talks either to the daemon over IPC
(runtime commands: `top`, `predict`, `stats`, `reset`) or directly to the
core packages (offline commands: `normalize`, `export`).

---

## Design Decisions (locked in)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | State representation | Last 2 commands | Captures workflows like `git add -> commit -> push`; sweet spot between coverage and context. |
| 2 | Decay model | Time-based, 30-day half-life | Adapts to changing habits; keeps long-term signal. |
| 3 | Score formula | Additive-smoothed probability | Graceful cold start; bounded in (0, 1]. |
| 4 | IPC protocol | JSON-lines over Unix socket | Debuggable with `socat`; throughput is irrelevant at human speed. |
| 5 | SQLite driver | `modernc.org/sqlite` (pure Go) | No CGO; cross-platform; perf is fine. |
| 6 | Daemon lifecycle | Per-shell launch + `flock` | Cross-platform; no service config; first command starts it. |
| 7 | CLI subcommands | `normalize`, `top`, `predict`, `stats`, `export`, `reset`, `daemon` | Covers all the AGENTS.md responsibilities. |
| 8 | Seed format | JSON | Human-readable, diffable, reviewable for community "workflow packs". |

---

## Phase 1: `core/graph`

### Goal

Track state -> next-command transitions with counts and last-seen timestamps.
Pure logic, no IO, no shell awareness. This is the foundation for everything.

### Files

- `core/graph/graph.go` -- public API
- `core/graph/graph_test.go` -- tests
- `core/graph/seed.go` -- seed merge + export (kept separate for clarity)

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
func (g *Graph) All() []Transition   // for export
func (g *Graph) Size() int            // distinct transition count
```

### Design notes

- **State key**: internal `stateKey` is `strings.Join(state, "\x00")` -- the null
  byte can't appear in normalized templates (tokens are space-separated
  identifiers), so it's a safe separator. Preserves order.
- **Storage**: `map[stateKey]map[string]*entry` where `entry` is
  `{count int, lastSeen time.Time}`. Bounded by distinct transitions ever
  observed (for a single user, typically 10k-100k).
- **Concurrency**: `sync.RWMutex` on the whole graph. Write lock for
  `Record`/`Merge`/`Decay`; read lock for `Transitions`/`All`/`Size`. Lock
  contention is negligible at human command rates.
- **Decay on read vs. write**: storage is not compacted. `Transitions` returns
  raw counts; the predict package applies the decay formula when scoring.
  This keeps the graph simple and lets predict own the scoring semantics.
  `Decay()` exists as a hook for future compaction (currently a no-op
  reserved for storage-size control).
- **Window size**: passed to `New()`. The graph doesn't enforce that
  `Record` is called with the right number of tokens; it just uses whatever
  is passed. Validation is the caller's job.
- **Merge semantics**: `Merge` adds seed counts to existing entries (seed
  count + existing count). If the user has local data, local wins in
  practice because the daemon writes local data on top of the seed.

### Test plan

- `Record` then `Transitions` returns the transition with count 1.
- Multiple `Record` of the same transition increments count and updates
  `LastSeen`.
- `Transitions` for an unknown state returns nil.
- Different states are tracked independently.
- `Merge` adds new transitions and increments existing ones.
- `All` returns every distinct transition.
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

func New(g *graph.Graph, alpha float64) *Predictor
func (p *Predictor) Predict(state types.State, at time.Time, limit int) []types.Suggestion
```

### Scoring formula

For each transition `t` in `graph.Transitions(state)`:

```
age      = at.Sub(t.LastSeen)
weight   = exp(-age / halfLife)        // halfLife fixed at 30 days
effCount = float64(t.Count) * weight
total    = sum of effCount for all transitions from this state
N        = number of distinct `next` values from this state
alpha    = smoothing constant (default 0.5)

score(t) = (effCount + alpha) / (total + alpha * N)
```

Properties:
- Bounded in `(0, 1]` (never zero due to additive smoothing).
- More observations => higher score.
- Recent observations weighted more heavily than old ones.
- Cold start handled: even a single observation gets a non-trivial score
  because `alpha` dominates when `total` is small.

### Design notes

- **Half-life**: hardcoded to 30 days in this phase. Could become a
  `Predictor` field later if needed; YAGNI for now.
- **CWD handling**: `state.CWD` is available but unused in scoring for v1.
  The predict package can grow a CWD-aware boost later. For now, the graph
  stores CWD-less transitions, so CWD can't differentiate. This is a known
  v1 limitation; the type is in place for the future.
- **Limit**: `0` means "all suggestions" (used by `hunch top`); positive
  values truncate to the top N (used by integrations).
- **Sort order**: descending by score, then descending by count as
  tie-breaker (deterministic ordering when scores are very close).
- **Empty state**: `Predict` with an empty `Previous` and no graph data
  returns nil. The integrations handle this gracefully (no ghost text).

### Test plan

- `Predict` on empty graph returns nil.
- `Predict` with a single transition returns it with score > 0.
- Multiple transitions are ranked by score.
- Older transitions rank lower after `Decay`-equivalent time advance.
- `limit` truncates correctly.
- Smoothing: a single observation outscores a single observation for a
  different transition in the same state with equal age (they tie --
  both get `(1 + alpha) / (2 + 2*alpha)`). Confirm tie-breaker is
  deterministic.
- Different states produce different rankings (no cross-contamination).

---

## Phase 3: `daemon`

### Goal

Persist the graph to SQLite, serve IPC over a Unix socket, and own the
single-instance lifecycle. Depends on `core/*` plus SQLite.

### Files

- `daemon/daemon.go` -- main loop, lifecycle
- `daemon/ipc.go` -- protocol types, socket handling
- `daemon/store.go` -- SQLite open/migrate/save/load
- `daemon/daemon_test.go` -- integration tests

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

State is stored as a JSON array (e.g., `["git add STR","git commit FLAG STR"]`).
This is debuggable, diffable, and matches the seed format.

### IPC protocol (JSON-lines over Unix socket)

**Request: record**
```json
{"op":"record","state":["git add STR"],"next":"git commit FLAG STR","outcome":"success","cwd":"/x","at":"2025-12-01T10:00:00Z"}
```

**Request: predict**
```json
{"op":"predict","state":["git add STR","git commit FLAG STR"],"cwd":"/x","at":"2025-12-01T10:00:00Z","limit":3}
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
{"suggestions":[{"template":"...","score":0.42,"count":7}]}
```

**Response (export)**
```json
{"transitions":[{"state":["..."],"next":"...","count":42,"last_seen":"..."}]}
```

**Response (error)**
```json
{"error":"message"}
```

### Lifecycle

- `daemon.Run(ctx context.Context, opts Options) error` -- main loop, blocks
  until `ctx` is cancelled or a fatal error occurs.
- Socket path: `~/.cache/hunch/hunch.sock` (portable default; can be
  overridden via env or flag).
- Single instance: `flock(2)` on `~/.cache/hunch/hunch.lock`. If lock fails,
  exit cleanly (another instance is already running).
- Startup sequence:
  1. Acquire file lock (exit if held).
  2. Open SQLite, run migrations.
  3. Load all transitions into in-memory `*graph.Graph`.
  4. If `opts.SeedPath` is set and the DB is empty, load and merge the seed.
  5. Start IPC listener.
  6. On `ctx.Done()`: flush graph to SQLite, close socket, release lock.
- Record path:
  1. Update in-memory graph (acquire write lock).
  2. Increment a dirty counter.
  3. If dirty counter exceeds threshold (e.g., 50) or a timer fires
     (e.g., 5 seconds), flush to SQLite in a single transaction.
- Predict path:
  1. Acquire read lock on graph.
  2. Call `predict.Predict`.
  3. Return suggestions.

### Concurrency

- Graph has its own `sync.RWMutex` (Phase 1).
- SQLite writes are serialized via a single goroutine with a write channel
  to avoid concurrent-write contention.
- IPC requests handled by one goroutine per connection (standard `net.Listener`
  accept loop).

### Error handling

- Socket already in use: another instance is running. Exit 0.
- SQLite open/migrate failure: log to stderr, exit 1.
- Per-request error: respond with `{"error":"..."}`, keep serving.
- Per-request panic: recover, respond with `{"error":"internal"}`, keep
  serving. Never let a bad message kill the daemon.

### Test plan

- Record then predict roundtrip works end-to-end via the socket.
- Persistence: write, stop, restart, verify data is there.
- Single instance: second `daemon.Run` against the same lock fails cleanly.
- Concurrent records: no races, no lost writes.
- Reset clears the in-memory graph and the SQLite table.
- Export returns all transitions in a stable order.
- Bad input (malformed JSON, unknown op) returns an error response, not a
  crash.
- Context cancellation triggers a clean shutdown and flush.

---

## Phase 4: `cli`

### Goal

Developer/admin interface for inspecting and managing hunch. Built on the
`core/*` packages and the daemon IPC.

### Files

- `main.go` (repo root) -- entry point, dispatches to subcommands
- `cli/normalize.go` -- `hunch normalize <cmd>`
- `cli/top.go` -- `hunch top [--limit N]`
- `cli/predict.go` -- `hunch predict --previous <tmpl>...`
- `cli/stats.go` -- `hunch stats`
- `cli/export.go` -- `hunch export`
- `cli/reset.go` -- `hunch reset`
- `cli/daemon.go` -- `hunch daemon start|stop|status`
- `cli/cli_test.go` -- per-subcommand tests

### Subcommand behavior

| Subcommand | Talks to | Output | Notes |
|------------|----------|--------|-------|
| `normalize <cmd>` | none (offline) | the normalized template | debug normalization |
| `top [--limit N]` | daemon IPC | tab-separated: `count  template` | most frequent transitions |
| `predict --previous <t>...` | daemon IPC | JSON suggestions | test prediction for a state |
| `stats` | daemon IPC | key/value lines | total commands, distinct templates, db size |
| `export` | daemon IPC | JSON to stdout | matches seed format |
| `reset [--yes]` | daemon IPC | confirmation | wipes everything |
| `daemon start\|stop\|status` | direct (start) / signal (stop) | status text | manages the long-running process |

### Design notes

- **Daemon-aware vs offline**: `normalize` runs locally (no daemon needed).
  Everything else assumes a running daemon and connects via the same socket
  the integrations use.
- **Daemon not running**: the CLI should print a clear error
  ("hunch daemon is not running; start it with `hunch daemon start`") and
  exit non-zero. Don't auto-start the daemon from the CLI (separation of
  concerns: the shell integration is the right place to auto-start).
- **Output format**: human-readable text for interactive commands
  (`top`, `stats`, `status`); JSON for machine-readable commands
  (`export`, `predict`). Tab-separated for `top` so it's easy to grep/cut.
- **Argument parsing**: stdlib `flag` package. No third-party CLI library;
  the surface is small enough that `flag` is sufficient.
- **Daemon start**: `hunch daemon start` spawns the daemon as a detached
  process (or uses `setsid`/equivalent), waits briefly for the socket
  to appear, then exits. `hunch daemon stop` sends a signal (or
  connects and sends `{"op":"shutdown"}` if we add one) and waits for
  the socket to disappear. `status` checks for the socket file.

### Test plan

- Each subcommand produces the expected output for representative inputs.
- `normalize` works with no daemon running.
- Other subcommands fail with a clear error when the daemon is not running.
- `export` output round-trips: export, reset, import (via seed) restores
  the same data.

---

## Phase 5: `integrations/{zsh,bash,fish}`

### Goal

Thin shell adapters that capture commands, query the daemon for predictions,
and render suggestions as ghost text. **No learning logic, no normalization
in the shell.** The shell only does IO; all intelligence lives in the
daemon/core.

### Files

- `integrations/zsh/hunch.zsh` -- sourced from `.zshrc`
- `integrations/bash/hunch.bash` -- sourced from `.bashrc`
- `integrations/fish/hunch.fish` -- dropped into `~/.config/fish/conf.d/`
- `cmd/hunch-client/main.go` -- tiny Go binary that speaks IPC from the shell

### hunch-client binary

Shell can't speak JSON-lines over Unix sockets ergonomically. So we provide
a small `hunch-client` binary that wraps the protocol:

```
hunch-client record --state "git add PATH" --next "git commit FLAG STR" --cwd /x
hunch-client predict --state "git add PATH" --state "git commit FLAG STR" --cwd /x --limit 3
hunch-client reset
hunch-client export
```

Output: one JSON object per line on stdout. The shell scripts parse with
`jq` or shell builtins. Errors go to stderr with a non-zero exit code.

If `jq` isn't available, the shell scripts fall back to grep/sed parsing.
Document the requirement: `jq` is the recommended path.

### zsh integration (`hunch.zsh`)

- **Capture**: add `_hunch` to `precmd_functions`. After each command, send
  the last command and its outcome to the daemon.
- **Prediction**: a ZLE widget (`_hunch_predict`) that runs on
  `zle-line-init` and `zle-keymap-select`. Sends the current `LBUFFER`
  prefix to the daemon (for partial-match predictions) and renders the top
  suggestion as ghost text via `region_highlight`.
- **Auto-start**: on source, if the daemon socket doesn't exist, spawn
  `hunch daemon start` in the background. Bail silently if it fails (shell
  must not break).
- **Accept keybind**: bind Tab (or a configurable key) to accept the ghost
  suggestion.

### bash integration (`hunch.bash`)

- **Capture**: append to `PROMPT_COMMAND`. After each command, send to
  the daemon. (Alternative: `DEBUG` trap, but `PROMPT_COMMAND` is simpler
  and runs after the command has finished.)
- **Prediction**: readline redisplay. Bind to a key (default: Tab) to
  fetch and insert the prediction. (Real ghost text in bash is harder
  than zsh; the pragmatic v1 is "press Tab to accept suggestion" rather
  than inline ghost text.)
- **Auto-start**: same as zsh.

### fish integration (`hunch.fish`)

- **Capture**: `function fish_prompt; ...; _hunch_record; end` (or use
  the `fish_postexec` event).
- **Prediction**: bind a function to a key (default: Tab) that calls
  `commandline` to get the current input, queries the daemon, and
  replaces the buffer.
- **Auto-start**: same as zsh.

### Design notes

- **Failure isolation**: every IPC call has a short timeout (50ms). If the
  daemon is slow or dead, the shell must not hang. Failures are silent.
- **No shell-specific logic in the Go code**: `hunch-client` is shell-agnostic.
  All the shell quirks (zle widgets, readline redisplay, fish events) live
  in the shell scripts.
- **No persistence in the shell**: the shell holds zero state. It captures,
  sends, and renders. The daemon owns all data.
- **Configurability**: minimal. A few env vars
  (`HUNCH_SOCKET`, `HUNCH_DAEMON_BIN`, `HUNCH_ACCEPT_KEY`) to override
  defaults. No config file in v1.

### Test plan

- `hunch-client` round-trips with the daemon: record, predict, verify.
- `hunch-client` exits non-zero and prints to stderr on daemon unreachable.
- Shell scripts: manual testing is the primary path. Lint with
  `shellcheck` for the bash script. The zsh and fish scripts can be
  syntax-checked with `zsh -n` and `fish -n`.

---

## End-to-end flow

1. User types `git add .` in zsh.
2. ZLE calls `_hunch_predict` on every keystroke (or on keypress events).
   The widget asks the daemon for predictions given the current buffer
   state.
3. The daemon returns the top suggestion (e.g., the previous
   `git commit -m "..."` the user typically types after `git add`).
4. ZLE renders the suggestion as ghost text.
5. User accepts with Tab (or ignores it and types their own command).
6. User presses Enter; the command runs.
7. `precmd` fires; the integration sends `record` with the executed
   command and its exit code.
8. The daemon updates the in-memory graph; SQLite is flushed within
   5 seconds.
9. Repeat.

---

## Build order checklist

- [ ] **Phase 1**: `core/graph`
  - [ ] `graph.go` with New/Record/Transitions/All/Size/Merge/Decay
  - [ ] `seed.go` with Seed type (for export/import)
  - [ ] `graph_test.go` with race detector
  - [ ] No new dependencies

- [ ] **Phase 2**: `core/predict`
  - [ ] `predict.go` with New/Predict
  - [ ] `score.go` with the smoothed formula
  - [ ] `predict_test.go` with decay + ranking cases
  - [ ] No new dependencies

- [ ] **Phase 3**: `daemon`
  - [ ] `daemon.go` with Run/lifecycle
  - [ ] `ipc.go` with protocol types and socket handling
  - [ ] `store.go` with SQLite open/migrate/save/load
  - [ ] `daemon_test.go` with IPC roundtrip + persistence
  - [ ] New dep: `modernc.org/sqlite`

- [ ] **Phase 4**: `cli`
  - [ ] `main.go` dispatches to subcommands
  - [ ] All 7 subcommands implemented
  - [ ] `cli_test.go` per subcommand
  - [ ] No new dependencies

- [ ] **Phase 5**: `integrations`
  - [ ] `cmd/hunch-client/main.go` -- IPC helper
  - [ ] `integrations/zsh/hunch.zsh`
  - [ ] `integrations/bash/hunch.bash`
  - [ ] `integrations/fish/hunch.fish`
  - [ ] Manual test in each shell

---

## Open questions for later

These are known v1 limitations or decisions deferred to a future phase.
Documented here so they don't get forgotten.

- **CWD-aware scoring**: `types.State.CWD` is captured but unused in
  scoring. The graph stores CWD-less transitions, so CWD can't
  differentiate. Future: store CWD with transitions and add a CWD-match
  boost in predict.
- **State fallback**: if no transitions exist for `[A, B]`, try `[B]`
  alone. Deferred from the original design discussion; add if cold-start
  data shows it's needed.
- **Compaction**: `graph.Decay` is currently a no-op. Storage grows with
  the number of distinct transitions ever observed. For a single user,
  this is bounded (~100k). If it ever becomes a problem, implement
  periodic compaction in the daemon background loop.
- **Multi-window**: currently hardcoded to last 2. Make configurable per
  user if evidence shows different windows work better for different
  workflows.
- **Seed discovery**: community "workflow packs" in JSON. No registry or
  download mechanism in v1. User manages seeds manually.
- **Daemon binary location**: how does the shell find `hunch` and
  `hunch-client`? `PATH` is the assumption. Document install steps.
- **Platform quirks**: Windows is not a v1 target (Unix sockets, shell
  hooks are POSIX). Document the platforms supported.
- **Conflict with shell history**: hunch is independent of shell history
  files (`.zsh_history`, etc.). If we ever want to bootstrap from
  existing history, that's a separate feature.

---

## End

When all five phases are done, hunch is a working shell companion: install
the binary, source the shell init, and the daemon starts learning from
the first command. Predictions appear as ghost text within seconds of
having enough history.
