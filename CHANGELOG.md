# Changelog

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
