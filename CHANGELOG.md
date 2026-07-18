# Changelog

## v0.1.0 - 2026-07-18

First tagged release. Pre-built binaries for Linux, macOS, and Windows
(amd64 and arm64) are attached to the GitHub release.

### Added
- CI pipeline (GitHub Actions) — test on Linux, macOS, Windows with race detection
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
- Daemon: world-readable lock and PID files (0644 → 0600)
- Daemon: world-readable Unix socket (now 0700)
- Windows: lock file `OVERLAPPED` struct size (too small on 64-bit)
- Log file descriptor leak in parent process after `hunch daemon start`
- Removed unimplemented `--outcome` and `--cwd` flags from `hunch client record`
- CLI: `hunch init` now warns when binary not on $PATH
- Path traversal in `handleImport` (validate file is a regular file)

### Changed
- zsh integration rewritten to use `zle-line-pre-redraw` hook (compatible with zsh-autosuggestions)
- `hunch daemon run` now handles SIGQUIT in addition to SIGTERM/SIGINT
