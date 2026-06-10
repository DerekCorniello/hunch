GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X github.com/DerekCorniello/hunch/cli.Version=$(VERSION)"

BIN := hunch

.PHONY: all build test test-race vet lint lint-shell clean install

all: build

build:
	$(GO) build $(LDFLAGS) -o $(BIN) .

test:
	$(GO) test ./...

test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint:
	which staticcheck 2>/dev/null && staticcheck ./... || true

lint-shell:
	@echo "--- bash ---"
	shellcheck integrations/bash/hunch.bash 2>&1 || true
	@echo "--- zsh ---"
	zsh -n integrations/zsh/hunch.zsh 2>&1 || true
	@echo "--- fish ---"
	fish -n integrations/fish/hunch.fish 2>&1 || true
	@echo "--- powershell ---"
	pwsh -NoLogo -NoProfile -Command "Get-Command -Syntax . 'integrations/powershell/hunch.ps1'" 2>&1 || true

check: test test-race vet lint lint-shell
	@echo "all checks passed"

clean:
	rm -f $(BIN) $(BIN).exe
	$(GO) clean

install:
	$(GO) install $(LDFLAGS) .
