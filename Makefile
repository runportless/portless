SHELL := /bin/bash
.DEFAULT_GOAL := build

GO ?= go
NPM ?= npm
BINARY ?= bin/portless
E2E_BINARY ?= bin/portless-e2e
RELAY_E2E_BINARY ?= bin/portless-relay-e2e
WEB_DEPENDENCIES := web/node_modules/.package-lock.json
WEB_MANIFESTS := web/package.json web/package-lock.json

.PHONY: build web test test-go test-web e2e-binary relay-e2e-binary test-e2e test-e2e-cli test-e2e-ui test-e2e-relay-destructive install-e2e-browser install clean reinstall-web-dependencies

build: web
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -o "$(BINARY)" ./cmd/portless

$(WEB_DEPENDENCIES): $(WEB_MANIFESTS)
	$(NPM) --prefix web ci --include=dev

web: $(WEB_DEPENDENCIES)
	$(NPM) --prefix web run build

test: test-web
	$(GO) test ./...

test-go:
	$(GO) test ./...

test-web: $(WEB_DEPENDENCIES)
	$(NPM) --prefix web run typecheck
	$(NPM) --prefix web test
	$(NPM) --prefix web run build

e2e-binary: web
	@mkdir -p "$(dir $(E2E_BINARY))"
	$(GO) build -tags=e2e -trimpath -o "$(E2E_BINARY)" ./cmd/portless

relay-e2e-binary: web
	@mkdir -p "$(dir $(RELAY_E2E_BINARY))"
	$(GO) build -trimpath -o "$(RELAY_E2E_BINARY)" ./cmd/portless

test-e2e: test-e2e-cli test-e2e-ui

test-e2e-cli: e2e-binary
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" $(GO) test -count=1 -tags=e2e ./tests/e2e

test-e2e-ui: e2e-binary
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" $(NPM) --prefix web run test:e2e

test-e2e-relay-destructive: relay-e2e-binary
	@PORTLESS_DESTRUCTIVE_RELAY_E2E=1 \
	PORTLESS_RELAY_E2E_BINARY="$(abspath $(RELAY_E2E_BINARY))" \
	$(GO) test -count=1 -tags=relay_e2e ./tests/relay_e2e -v

install-e2e-browser: $(WEB_DEPENDENCIES)
	$(NPM) --prefix web exec -- playwright install chromium

install: build
	@install_directory="$${GOBIN:-$$($(GO) env GOPATH)/bin}"; \
	mkdir -p "$$install_directory"; \
	install -m 0755 "$(BINARY)" "$$install_directory/portless"; \
	echo "Installed $$install_directory/portless"

reinstall-web-dependencies:
	$(NPM) --prefix web ci --include=dev

clean:
	rm -rf bin web/coverage
