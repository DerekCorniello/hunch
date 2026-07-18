# Contributing

## Development setup

```bash
# One-time: install git hooks
make hooks

# Build from source
go build -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=$(git describe --tags --always --dirty)" -o hunch .

# Run tests
make check
```

## Code style

- Follow standard Go conventions (`gofmt`, `go vet`, `golangci-lint`)
- Core packages are pure logic - no IO, no database, no shell dependencies
- Daemon is the only package that owns persistence and caching
- Shell integrations are thin UI shims - no learning logic

## Testing

- All tests must pass with `-race` (data race detector)
- Cross-platform: ensure Linux, macOS, and Windows build
- Core + IPC tests should be fast (<30s total)
- Daemon tests start real daemon instances with temp dirs

```bash
make test           # unit tests
make test-race      # with race detector
make vet            # go vet
make lint           # go vet + staticcheck
make lint-shell     # shellcheck on integration scripts
```

## Pull requests

1. Ensure `make check` passes locally
2. Add tests for new functionality
3. Update README and CHANGELOG for user-facing changes
4. Keep PRs focused - one logical change per PR

## Release process

1. Tag a new version: `git tag v0.1.0 && git push origin v0.1.0`
2. CI builds and attaches cross-platform binaries to the release
3. Update CHANGELOG.md with the release notes
