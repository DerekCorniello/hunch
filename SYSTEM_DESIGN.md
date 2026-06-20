# System Design

Hunch is a lightweight statistical system that learns shell workflows from user behavior. It builds a Markov model over normalized command transitions, using exponential decay and additive smoothing to rank suggestions.

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Shell Process                       │
│  zsh / bash / fish / powershell                          │
└───────────────────────────────┬─────────────────────────┘
                                │
                    ┌───────────▼───────────┐
                    │    Integration Layer    │
                    │  (thin adapter / shim)  │
                    │                         │
                    │  • captures commands    │
                    │  • renders ghost text   │
                    │  • sends IPC to daemon  │
                    └───────────┬───────────┘
                                │  unix socket
                    ┌───────────▼───────────┐
                    │        Daemon           │
                    │  (background service)   │
                    │                         │
                    │  • owns SQLite (WAL)    │
                    │  • accepts IPC          │
                    │  • flush loop (5s)      │
                    │  • stale lock recovery  │
                    └───────────┬───────────┘
                                │
              ┌─────────────────┼─────────────────┐
              │                 │                 │
    ┌─────────▼─────┐ ┌────────▼──────┐ ┌───────▼────────┐
    │   normalize    │ │    graph      │ │    predict      │
    │  (pure logic)  │ │ (pure logic)  │ │  (pure logic)   │
    └───────────────┘ └───────────────┘ └────────────────┘
```

**Core principle:** Core packages are deterministic and stateless (given inputs). The daemon owns all IO and persistence. Shell integrations contain no learning logic.

---

## Component Design

### core/normalize — Command Template Generation

Converts raw shell commands into normalized templates suitable for use as graph keys. This is the foundation of Hunch's ability to generalize across syntactic variations of the same command.

#### Two-Phase Pipeline

**Phase 1: Unwrap Wrappers**

Strips leading wrapper commands and recurses into the inner command. This collapses `sudo apt update` and `apt update` into the same template.

Supported wrappers and their parsing strategies:

| Wrapper | Behavior |
|---------|----------|
| `sudo` | Skip flags, env assignments, honor `--` |
| `doas` | Same as sudo |
| `env` | Skip env assignments, honor `--` |
| `chroot` | Skip flags, skip NEWROOT positional arg |
| `time` | Skip flags, honor `--` |
| `nice` / `ionice` | Skip flags (short flag takes value), honor `--` |
| `exec` / `nohup` / `fakeroot` / `setsid` | No flags — inner command always at index 1 |
| `flock` | Special: handles `-c CMD` and `lock-file CMD` forms |
| `watch` / `systemd-run` / `taskset` / `numactl` / `unshare` / `stdbuf` / `prlimit` | Various flag-skip strategies |

**Phase 2: Token Classification**

Splits the command into tokens and classifies each by shape:

| Token Shape | Classification | Example |
|-------------|----------------|---------|
| Starts with `-` or `--` | `FLAG` | `--verbose`, `-o` |
| Contains `/`, starts with `.` or `~` | `PATH` | `./main.go`, `~/docs` |
| Matches URL or git remote pattern | `REPO` | `https://github.com/...`, `git@host:repo` |
| Hex string 6-40 chars | `HASH` | `a1b2c3d` |
| Pure number (optional decimal) | `NUM` | `8080`, `3.14` |
| Was quoted in original command | `STR` | `"hello world"` |
| Standalone `--` | `KWARGS` separator | Everything after becomes `KWARGS` |
| Known parent command (position 1) | Kept verbatim | `git push` → `git push` |

**Collapse:** Consecutive tokens of the same type are merged into a single representative token.

#### Examples

| Raw Command | Normalized Template |
|-------------|---------------------|
| `mkdir foo` | `mkdir PATH` |
| `git commit -m "init"` | `git commit FLAG STR` |
| `cargo build -- --target x86_64` | `cargo build KWARGS` |
| `sudo apt install nginx` | `apt install STR` |
| `time ls -la /tmp` | `ls FLAG PATH` |
| `git add . && git commit -m "fix"` | `git add PATH && git commit FLAG STR` |

#### Known Parent Commands

Tools whose first non-flag argument is a verb (subcommand) that distinguishes workflows are kept verbatim. This preserves semantic meaning: `git push` and `git pull` produce different templates.

Over 200 tools are recognized, including: git, cargo, npm, docker, kubectl, aws, systemctl, tmux, gh, and many more. See `core/normalize/classify.go:DefaultParents` for the full list.

---

### core/graph — Transition Tracking

Stores observed `state → next` transitions with counts and timestamps.

#### Data Model

```
Graph
├── state_key_1 (null-joined state string)
│   ├── next_template_A → {count: 5, last_seen: 2026-01-15T10:30:00Z}
│   └── next_template_B → {count: 2, last_seen: 2026-01-15T09:00:00Z}
├── state_key_2
│   └── ...
```

- **State key:** Null-joined concatenation of the previous N command templates (window size = 2 by default).
- **Entry:** Count of observations + most recent timestamp.
- **Concurrency:** Single `sync.RWMutex` protects all access.

#### Operations

| Operation | Description |
|-----------|-------------|
| `Record(state, next, at)` | Increment count for (state, next) pair |
| `Transitions(state)` | Return all next-commands for a given state |
| `Merge(seed)` | Additive merge of seed transitions (count += seed.Count) |
| `Decay(at, halfLife)` | Prune transitions where `count * exp(-age/halfLife) < epsilon` |
| `All()` | Return all transitions sorted by (state, next) for stable export |
| `Size()` | Count of distinct (state, next) pairs |

#### Exponential Decay

Transitions lose weight over time. The effective weight at time `at` is:

```
weight = count × exp(-age / halfLife)
```

where `age = at - lastSeen`. Transitions below `epsilon = 0.001` are pruned during `Decay()`. The default half-life is 720 hours (30 days).

---

### core/predict — Scoring and Ranking

Generates ranked suggestions from the transition graph for a given state.

#### Scoring Formula

Additive-smoothed decay scoring:

```
score(t) = (effCount(t) + α) / (Σ effCount + α × N)
```

Where:
- `effCount(t) = count(t) × exp(-age(t) / halfLife)`
- `α` = additive smoothing constant (default 0.5)
- `N` = number of candidate transitions for this state

**Properties:**
- All scores are in `(0, 1]`
- Additive smoothing prevents cold-start collapse (a single observation gets a non-trivial score)
- Scores are fully deterministic given the same inputs

#### Ranking

Suggestions are sorted by:
1. Descending score (primary)
2. Descending count (tie-breaker)
3. Ascending template string (determinism)

---

### daemon — Background Service

The daemon is a long-running process that owns all persistence and handles IPC from shell integrations.

#### Lifecycle

1. **Start:** Acquire lock (with stale recovery), open SQLite, load graph, start flush loop, start IPC listener
2. **Run:** Accept connections, handle record/predict/reset/export operations
3. **Stop:** Close listener, flush graph to SQLite, remove socket and PID files, release lock

#### Lock Management

- **File:** `<data_dir>/hunch.lock`
- **PID file:** `<data_dir>/hunch.pid`
- **Acquisition:** Non-blocking `flock(2)` on Unix, `LockFileEx` on Windows
- **Stale recovery:** If lock is held, check if the PID is alive. If not, remove stale lock and retry.

#### IPC Protocol

- **Transport:** Unix domain socket (`~/.cache/hunch.sock`)
- **Framing:** One JSON message per connection, newline-terminated
- **Timeouts:** 10s for initial request read, 30s for processing

**Operations:**

| Op | Request Fields | Response |
|----|----------------|----------|
| `record` | `state[]`, `next`, `at` | `{ok: true}` |
| `predict` | `state[]`, `prefix`, `limit`, `at` | `{suggestions: [...]}` |
| `reset` | — | `{ok: true}` |
| `export` | — | `{transitions: [...]}` |
| `normalize` | `next` (or last state entry) | `{raw, template}` |
| `stats` | — | `{size, half_life, alpha, db_path}` |
| `config` | — | `{accept_keys, extra_parents, half_life, alpha}` |
| `import` | `next` (file path) | `{ok: true}` |
| `record_raws` | `next` (JSON array of examples) | `{ok: true}` |

#### Persistence

**Database:** SQLite with WAL mode

**Schema:**

```sql
CREATE TABLE transitions (
    state     TEXT NOT NULL,  -- JSON array of templates
    next      TEXT NOT NULL,  -- normalized template
    count     INTEGER NOT NULL,
    last_seen INTEGER NOT NULL,  -- Unix timestamp
    PRIMARY KEY (state, next)
);

CREATE TABLE raw_examples (
    template TEXT NOT NULL,  -- normalized template
    raw      TEXT NOT NULL,  -- most common raw command for this template
    count    INTEGER NOT NULL,
    PRIMARY KEY (template, raw)
);
```

#### Flush Loop

- **Interval:** Every 5 seconds (if dirty)
- **Threshold:** Immediate flush when dirty count ≥ 50
- **Behavior:** Saves graph + raw examples in a single transaction. Only clears dirty flag on successful save. If records arrive during save, a re-flush is scheduled.

#### Raw Examples

Maps normalized templates back to the most common raw command observed for that template. This allows `predict` to return human-readable suggestions (e.g., `git commit -m "msg"` instead of `git commit FLAG STR`).

---

### Shell Integrations

Each integration is a thin adapter that:
1. Ensures the daemon is running on shell startup
2. Records executed commands asynchronously (non-blocking)
3. Queries predictions and renders ghost text
4. Handles accept/reject via keybindings

#### zsh Integration

- **Hook:** `zle-line-pre-redraw` for predictions, `precmd` for recording
- **Rendering:** Uses `POSTDISPLAY` for ghost text with `region_highlight` for styling
- **Accept:** Right arrow or End key
- **Compatibility:** Works alongside `zsh-autosuggestions` (uses memo-tagged highlights)
- **History:** Reads from `$HISTCMD` for accurate command capture

#### bash Integration

- **Hook:** `PROMPT_COMMAND` for recording, `bind -x` for Tab accept
- **Rendering:** Tab inserts suggestion into `READLINE_LINE`
- **Accept:** Tab key

#### fish Integration

- **Hook:** `fish_postexec` for recording, `commandline` manipulation for predictions
- **Rendering:** Uses `commandline` to append ghost text
- **Accept:** Right arrow or End key

#### PowerShell Integration

- **Requirements:** PowerShell 7.4+, PSReadLine 2.3+
- **Hook:** Custom `Invoke-HunchRecord` via key binding
- **Rendering:** PSReadLine `Replace` API (native prediction disabled)
- **Accept:** Right arrow or End key

---

## Data Flow

### Recording a Command

```
User runs: git add .
                │
                ▼
┌─────────────────────────────┐
│ Integration captures command │
│ Records: state=["", "git add ."] │
│         next=<next command>  │
└──────────────┬──────────────┘
               │  IPC: record op
               ▼
┌─────────────────────────────┐
│ Daemon receives request      │
│ 1. Normalize state & next    │
│ 2. graph.Record()            │
│ 3. Update rawMap             │
│ 4. Increment dirty counter   │
└──────────────┬──────────────┘
               │  on flush
               ▼
┌─────────────────────────────┐
│ SQLite persistence           │
│ UPSERT transitions table     │
│ UPSERT raw_examples table    │
└─────────────────────────────┘
```

### Generating a Prediction

```
User types: git pus
                │
                ▼
┌─────────────────────────────┐
│ Integration queries daemon   │
│ predict(state=["", "git add ."], │
│         prefix="git pus",   │
│         limit=1)            │
└──────────────┬──────────────┘
               │  IPC: predict op
               ▼
┌─────────────────────────────┐
│ Daemon:                      │
│ 1. Normalize state           │
│ 2. predictor.Predict()       │
│ 3. filterByPrefix("git pus") │
│ 4. Look up raw examples      │
│ 5. Return top suggestion     │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ Integration renders          │
│ Ghost text: "h" (completes   │
│ "git push origin main")      │
└─────────────────────────────┘
```

---

## Configuration

### Precedence (lowest → highest)

1. Built-in defaults
2. Config file (`~/.config/hunch/config.toml`)
3. Environment variables
4. CLI flags

### Defaults

| Setting | Default | Description |
|---------|---------|-------------|
| `half_life_hours` | 720 | Decay half-life (30 days) |
| `alpha` | 0.5 | Additive smoothing constant |
| `socket` | `~/.cache/hunch.sock` | IPC socket path |
| `db_path` | `~/.local/share/hunch.db` | SQLite database path |
| `accept_keys` | `["right", "end"]` | Keys that accept ghost text |
| `log_level` | `"info"` | Log verbosity |

---

## Platform Support

| Platform | Socket | Locking | Data Paths |
|----------|--------|---------|------------|
| Linux | Unix domain | `flock(2)` | XDG directories |
| macOS | Unix domain | `flock(2)` | XDG directories |
| Windows | Unix domain (1803+) | `LockFileEx` | `%LocalAppData%` |
| FreeBSD | Unix domain | `flock(2)` | XDG directories |

Windows note: Exclude `%LocalAppData%\hunch\` from Windows Defender real-time scanning to avoid SQLite lock contention.

---

## Testing Strategy

- **Unit tests:** Core logic (normalize, graph, predict) — deterministic, no IO
- **Integration tests:** Daemon IPC roundtrip, persistence, stale lock recovery
- **Race detection:** All tests run with `-race` flag
- **Cross-compile check:** Linux, macOS, Windows in pre-commit hooks

```bash
make check   # test + test-race + vet + lint + lint-shell
```
