# Changelog

## Unreleased

### Fixed
- Importing history no longer inflates counts. `Merge` added seed counts to
  existing ones, but a seed states how many times a transition was observed
  rather than supplying that many new observations. A shell history file
  records the same commands the daemon already saw live, so the first import
  counted them twice and each re-import doubled them again, which turned
  one-off commands into apparent habits. Counts now combine by maximum, making
  import idempotent.
- `hunch import-history` records generalized contexts, so an imported graph
  supports the same fallbacks as a learned one. It previously recorded only the
  exact two-command context, which left almost every imported transition at a
  count of 1. Combined with the new evidence threshold that meant a freshly
  imported history produced very few suggestions. Raw examples are expanded the
  same way, since a generalized hit with no concrete command behind it is
  suppressed rather than shown. Re-run `hunch import-history <shell>` to
  backfill an existing graph.
- Commands run only once are no longer suggested. A transition observed a
  single time in a context that is never revisited is the only candidate for
  that state, so additive smoothing scores it 1.0 and it was offered as
  confidently as a daily habit. `min_count` (default `2`) gates every
  suggestion on evidence, which `min_confidence` cannot do because the problem
  is maximum probability on minimum evidence. Measured on a 10k-command
  history, this beats the previous behavior on both axes at once: precision
  when a suggestion is shown rises from 30.4% to 37.5%, above even the
  pre-generalization 34.4%, while top-1 stays at 21.9%, well above the
  pre-generalization 18.9%.
- Imported seeds no longer vanish on the next daemon restart. `graph.Transition`
  carried no JSON tags, so a seed file's `last_seen` never unmarshaled and every
  imported transition got a zero timestamp. Import reported success and
  predictions worked, then the decay pass at the next startup read those
  transitions as two thousand years old and pruned all of them. The field names
  are now tagged and pinned by a test.
- `hunch client export` emits a seed envelope instead of a bare array, so its
  output can be fed back to `client import` or `daemon run --seed`. The
  documented export-to-seed workflow previously failed outright.
- A seed whose timestamps are missing or unparseable is now rejected at import
  with an error naming the offending transition, rather than accepted and
  silently deleted later.
- zsh hooks are registered through `add-zle-hook-widget` instead of `zle -N`.
  Binding a hook directly replaces whatever was bound before, so hunch could
  silently disable another plugin's `zle-line-pre-redraw`, and a plugin loaded
  after hunch could just as silently disable hunch's ghost text. `zle-line-finish`
  was replaced outright with no chaining at all. Load order no longer matters
  for correctness. zsh older than 5.3 keeps the previous single-predecessor
  chaining.
- Predictions now generalize. State keys compare by exact join, so a query for
  a shorter context never matched a longer recorded one, and a query without a
  directory never matched a recording made with one. The documented fallback
  levels could therefore almost never fire. Observations are now also recorded
  under their generalized contexts, which is what makes those fallbacks
  reachable.
- Suggestions reached through a generalization now carry a concrete command.
  Raw examples were keyed only by the exact context, so a generalized hit would
  have rendered as a bare template like `git commit FLAG STR`.
- A template containing placeholders is never displayed as a suggestion. Both
  the hint path and the zsh coprocess previously fell back to the template when
  no concrete command was known, which could put unrunnable text on screen.

### Added
- `min_confidence` (`HUNCH_MIN_CONFIDENCE`, default `0.20`) sets the score a
  generalized match must reach before it is shown. Exact-context matches are
  always shown. Set it to `1` to only ever show exact matches.

On a 10k-command history this moves top-1 from 18.5% to 23.1% and top-3 from
24.0% to 30.8%, against a 9.0% baseline. Suggestions are offered 78.7% of the
time rather than 55.8%. The graph grows about 1.6x.

## v0.1.2 - 2026-07-18

### Added
- `hunch eval <shell>` measures prediction accuracy by replaying your own
  history, reporting top-1/3/5 hit rates against a most-frequent-command
  baseline. Prediction quality was previously unmeasurable.
- Releases now publish a `SHA256SUMS` manifest, and `hunch update` verifies the
  downloaded binary against it before installing. Verification fails closed: a
  missing manifest or mismatched digest aborts the update.
- Tests for the raw-example store, doctor's diagnostics, and the checksum path.
  Coverage: cli 61% to 67%, daemon 76% to 80%, total 72% to 75%.

### Changed
- Split `daemon.go` (957 lines, 30 functions on one struct) into `daemon.go`
  (lifecycle, 409 lines), `handlers.go` (IPC dispatch and handlers), and
  `rawstore.go` (the template-to-command mapping, now an encapsulated type).
  No behavior change.
- `handlePredict`'s four-level fallback is now `predictWithFallback`; the
  stats, config, and normalize handlers share one `respondJSON` helper.
- Failed response writes are logged instead of discarded via `_ =`.
- `cmdDoctor` separates diagnosis from rendering, so check logic is testable
  and output is column-aligned.
- README documents how hunch relates to zsh-autosuggestions, atuin, fzf, and
  thefuck, including what it cannot do.

## v0.1.1 - 2026-07-18

### Fixed
- `hunch update` shelled out to `go install`, so it failed for anyone who
  installed a pre-built binary. It now downloads the release asset for the
  running platform and replaces the executable in place, with no Go toolchain
  required. Unwritable install directories and platforms with no published
  binary now report actionable errors instead of failing opaquely.
- bash and fish printed the post-command hint with a non-ASCII marker while
  PowerShell used `>`. All three now use `hunch > `.

### Added
- `hunch version` as an alias for `--version`/`-v`.
- gofmt enforcement in the pre-commit hook and CI, plus a `make fmt` target.
  Formatting had never been checked, and three files were unformatted.
- Tests for the update path, `appendToRc`, the PowerShell history parser, and
  command dispatch. cli coverage 57% to 61%.

### Changed
- GitHub Actions bumped to current majors (checkout v5, setup-go v6,
  upload/download-artifact v5, action-gh-release v3), clearing the Node 20
  deprecation warnings.
- Replaced non-ASCII characters in prose, comments, and program output with
  ASCII equivalents. Architecture diagrams keep their box-drawing characters.

## v0.1.0 - 2026-07-18

First tagged release. Pre-built binaries for Linux, macOS, and Windows
(amd64 and arm64) are attached to the GitHub release.

### Added
- CI pipeline (GitHub Actions) - test on Linux, macOS, Windows with race detection
- `hunch daemon run --seed <path>` flag for seeding the graph on first start
- Pre-commit hooks (go vet, cross-compile check, core unit tests)
- Integration tests for history parsers (zsh, bash, fish, markdown)
- lock contention, daemon stats/config/import, process existence tests
- PATH warning in `hunch init` when binary directory is not on $PATH

### Fixed
- zsh integration: PID spam on every command (`&!` vs `&` + `disown`)
- zsh integration: infinite recursion with zsh-autosuggestions (`zle-line-pre-redraw` instead of widget wrappers)
- Daemon: crash after stale socket file left on disk (remove before listen)
- Daemon: data loss on seed import (transitions not flushed to SQLite)
- Daemon: data loss on concurrent `handleReset` + `handleRecord` (dirty counter race)
- Daemon: data loss on flush failure (dirty counter reset despite failed save)
- Daemon: busy-spin on accept errors (added exponential backoff)
- Daemon: missing read/write deadlines on IPC connections (slow-loris / goroutine leak)
- Daemon: missing panic recovery in connection goroutines (single bad request crashes daemon)
- Daemon: no SQLite connection pool limits (SetMaxOpenConns=1)
- Daemon: world-readable lock and PID files (0644 -> 0600)
- Daemon: world-readable Unix socket (now 0700)
- Windows: lock file `OVERLAPPED` struct size (too small on 64-bit)
- Log file descriptor leak in parent process after `hunch daemon start`
- Removed unimplemented `--outcome` and `--cwd` flags from `hunch client record`
- CLI: `hunch init` now warns when binary not on $PATH
- Path traversal in `handleImport` (validate file is a regular file)

### Changed
- zsh integration rewritten to use `zle-line-pre-redraw` hook (compatible with zsh-autosuggestions)
- `hunch daemon run` now handles SIGQUIT in addition to SIGTERM/SIGINT
