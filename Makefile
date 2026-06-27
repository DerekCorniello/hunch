GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=$(VERSION)"

BIN := hunch

.PHONY: all build test test-race test-zsh test-e2e vet lint lint-shell clean install hooks check

all: hooks build

build:
	$(GO) build $(LDFLAGS) -o $(BIN) .

test:
	$(GO) test ./...

test-race:
	$(GO) test -race -count=1 ./...

# Functional tests for the zsh integration's display-decision logic.
test-zsh:
	@which zsh >/dev/null 2>&1 && zsh integrations/zsh/hunch_test.zsh || echo "zsh not found, skipping"

# End-to-end CLI/daemon/IPC smoke test.
test-e2e:
	bash scripts/e2e-test.sh

vet:
	$(GO) vet ./...

lint:
	$(GO) vet ./...
	which staticcheck 2>/dev/null && staticcheck ./...

lint-shell:
	@echo "--- bash ---"
	which shellcheck 2>/dev/null && shellcheck integrations/bash/hunch.bash || echo "shellcheck not found, skipping"
	@echo "--- zsh ---"
	which zsh 2>/dev/null && zsh -n integrations/zsh/hunch.zsh || echo "zsh not found, skipping"
	@echo "--- fish ---"
	which fish 2>/dev/null && fish -n integrations/fish/hunch.fish || echo "fish not found, skipping"
	@echo "--- powershell ---"
	which pwsh 2>/dev/null && pwsh -NoLogo -NoProfile -Command "[ScriptBlock]::Create((Get-Content -Raw 'integrations/powershell/hunch.ps1')) | Out-Null" || echo "pwsh not found, skipping"

hooks:
	@if [ "$(shell git config core.hooksPath)" != ".githooks" ]; then \
		git config core.hooksPath .githooks; \
		echo "configured git hooks (.githooks/)"; \
	fi

check: test test-race vet lint lint-shell test-zsh test-e2e
	@echo "all checks passed"

clean:
	rm -f $(BIN) $(BIN).exe
	$(GO) clean

install:
	$(GO) install $(LDFLAGS) .
