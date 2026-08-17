SHELL := /bin/bash
.DEFAULT_GOAL := build

GO ?= go
NPM ?= npm
BINARY ?= bin/portless
E2E_BINARY ?= bin/portless-e2e
RELAY_E2E_BINARY ?= bin/portless-relay-e2e
RESOURCE_E2E_RUNTIME ?= auto
WEB_PROJECT := portless-web
CLI_PACKAGE := ./portless-cli/cmd/portless
WEB_DEPENDENCIES := $(WEB_PROJECT)/node_modules/.package-lock.json
WEB_MANIFESTS := $(WEB_PROJECT)/package.json $(WEB_PROJECT)/package-lock.json

.PHONY: build web test test-go test-web e2e-binary relay-e2e-binary test-e2e test-e2e-cli test-e2e-ui test-e2e-resources test-e2e-relay-destructive test-e2e-relay-destructive-resources install-e2e-browser install clean reinstall-web-dependencies

build: web
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -o "$(BINARY)" $(CLI_PACKAGE)

$(WEB_DEPENDENCIES): $(WEB_MANIFESTS)
	$(NPM) --prefix $(WEB_PROJECT) ci --include=dev

web: $(WEB_DEPENDENCIES)
	$(NPM) --prefix $(WEB_PROJECT) run build

test: test-web
	$(GO) test ./...

test-go:
	$(GO) test ./...

test-web: $(WEB_DEPENDENCIES)
	$(NPM) --prefix $(WEB_PROJECT) run typecheck
	$(NPM) --prefix $(WEB_PROJECT) test
	$(NPM) --prefix $(WEB_PROJECT) run build

e2e-binary: web
	@mkdir -p "$(dir $(E2E_BINARY))"
	$(GO) build -tags=e2e -trimpath -o "$(E2E_BINARY)" $(CLI_PACKAGE)

relay-e2e-binary: web
	@mkdir -p "$(dir $(RELAY_E2E_BINARY))"
	$(GO) build -trimpath -o "$(RELAY_E2E_BINARY)" $(CLI_PACKAGE)

test-e2e: test-e2e-cli test-e2e-ui

test-e2e-cli: e2e-binary
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" $(GO) test -count=1 -tags=e2e ./tests/e2e

test-e2e-ui: e2e-binary
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" $(NPM) --prefix $(WEB_PROJECT) run test:e2e

test-e2e-resources: e2e-binary
	PORTLESS_MANAGED_RESOURCE_E2E=1 \
	PORTLESS_MANAGED_RESOURCE_RUNTIME="$(RESOURCE_E2E_RUNTIME)" \
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" \
	$(GO) test -count=1 -tags=e2e ./tests/e2e -run '^TestCLIManagedResource' -v

test-e2e-relay-destructive: relay-e2e-binary
	@PORTLESS_DESTRUCTIVE_RELAY_E2E=1 \
	PORTLESS_RELAY_E2E_BINARY="$(abspath $(RELAY_E2E_BINARY))" \
	$(GO) test -count=1 -tags=relay_e2e ./tests/relay_e2e -v

test-e2e-relay-destructive-resources: relay-e2e-binary
	@PORTLESS_DESTRUCTIVE_RELAY_E2E=1 \
	PORTLESS_DESTRUCTIVE_RELAY_RESOURCE_E2E=1 \
	PORTLESS_MANAGED_RESOURCE_RUNTIME="$(RESOURCE_E2E_RUNTIME)" \
	PORTLESS_RELAY_E2E_BINARY="$(abspath $(RELAY_E2E_BINARY))" \
	$(GO) test -count=1 -tags=relay_e2e ./tests/relay_e2e -v

install-e2e-browser: $(WEB_DEPENDENCIES)
	$(NPM) --prefix $(WEB_PROJECT) exec -- playwright install chromium

install: build
	@install_directory="$${GOBIN:-$$($(GO) env GOPATH)/bin}"; \
	mkdir -p "$$install_directory"; \
	install -m 0755 "$(BINARY)" "$$install_directory/portless"; \
	echo "Installed $$install_directory/portless"

reinstall-web-dependencies:
	$(NPM) --prefix $(WEB_PROJECT) ci --include=dev

clean:
	rm -rf bin $(WEB_PROJECT)/coverage
