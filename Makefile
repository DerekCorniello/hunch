GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=$(VERSION)"

BIN := hunch

.PHONY: all build test test-race vet lint lint-shell clean install

all: hooks build

build:
	$(GO) build $(LDFLAGS) -o $(BIN) .

test:
	$(GO) test ./...

test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint:
	$(GO) vet ./...
	which staticcheck 2>/dev/null && staticcheck ./...

lint-shell:
	@echo "--- bash ---"
	shellcheck integrations/bash/hunch.bash
	@echo "--- zsh ---"
	zsh -n integrations/zsh/hunch.zsh
	@echo "--- fish ---"
	fish -n integrations/fish/hunch.fish
	@echo "--- powershell ---"
	pwsh -NoLogo -NoProfile -Command "Get-Command -Syntax . 'integrations/powershell/hunch.ps1'"

hooks:
	@if [ "$(shell git config core.hooksPath)" != ".githooks" ]; then \
		git config core.hooksPath .githooks; \
		echo "configured git hooks (.githooks/)"; \
	fi

check: test test-race vet lint lint-shell
	@echo "all checks passed"

clean:
	rm -f $(BIN) $(BIN).exe
	$(GO) clean

install:
	$(GO) install $(LDFLAGS) .
