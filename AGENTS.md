# Hunch

## Goal

Hunch is a shell companion that learns from your command history and suggests the most likely next command based on:

- previous commands (sequence context)
- command outcome (success/failure types)
- working directory context
- normalized command patterns (not raw strings)

It is not an AI assistant. It is a lightweight statistical system that learns workflows from user behavior.

Primary UX:
- After a command runs, Hunch suggests the next likely command inline or via shell integration.
- Suggestions are learned from the user’s own history.

---

## Repository Structure

### `core/`

Pure logic only. No IO, no database, no shell integration.

Contains:

- `graph/`  
  Transition tracking logic  
  - state → next-command counts  
  - weight updates  
  - history aggregation

- `normalize/`  
  Converts raw shell commands into templates via a two-phase pipeline:

  **Phase 1 — Unwrap wrappers**: strip `sudo`, `time`, `nohup`, etc., then recurse on the inner command.

  **Phase 2 — Token-type classification**: split the command into tokens, classify each by shape:
  
  | Pattern | Type |
  |---|---|
  | Starts with `-` or `--` | `FLAG` |
  | Contains `/`, starts with `.`/`~` | `PATH` |
  | Looks like a URL or git remote | `REPO` |
  | Hex string of git-hash length | `HASH` |
  | Looks like a number | `NUM` |
  | Was quoted in the original | `STR` |
  | Standalone `--` token | separator — everything after becomes `KWARGS` |
  | Known parent command (`git`, `cargo`, `npm`, etc.) | keeps verb/subcommand as-is |
  
  After classification, collapse consecutive same-type tokens into one.
  
  Examples: `mkdir foo → mkdir PATH`, `git commit -m "init" → git commit FLAG STR`, `cargo build -- --target x86_64 → cargo build KWARGS`

- `predict/`  
  Generates ranked suggestions from state  
  - scoring
  - ranking
  - simple heuristics

- `types/`  
  Shared domain models  
  - Command
  - State
  - Outcome
  - Suggestion

---

### `daemon/`

Long-running background service.

Responsibilities:

- Owns SQLite database
- Receives events from shell integrations
- Calls `core` to update graph + generate predictions
- Caches recent state for fast access
- Exposes local IPC (unix socket)
- Supports `--seed <path>` to merge a pre-built transition graph on first run (e.g. community "workflow packs" or your own exported data)

---

### `cli/`

Developer/admin interface.

Responsibilities:

- Inspect learned behavior
- Debug normalization and predictions
- Reset or export data
- Show stats and top transitions
- Export normalized transition graphs for use as seed data

No runtime dependency on shell.

---

### `integrations/`

Shell-specific adapters.

Responsibilities:

- Capture executed commands
- Send events to daemon
- Request predictions
- Render suggestions (ghost text or post-command hints)

Subdirectories:

- `zsh/` — ZLE integration
- `bash/` — readline hooks
- `fish/` — fish event hooks

Must remain minimal and contain no learning logic.

---

## Key Design Rules

- Core is deterministic and stateless (except inputs)
- Daemon owns persistence and caching
- Shell integrations are thin UI/adapters only
- No AI/LLM dependency
- No terminal-specific logic outside integrations
- Normalize early, predict cheaply

---

## Non-goals (for now)

- No distributed system
- No cloud sync
- No multi-user graph merging
- No complex grammar parsing of shells
- No LLM-based prediction

---

## Mental Model

Think of Hunch as:

> a learned Markov model over shell workflows with template-based normalization
