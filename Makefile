SHELL := /bin/bash
.DEFAULT_GOAL := build

GO ?= go
NPM ?= npm
GORELEASER ?= goreleaser
BINARY ?= bin/portless
E2E_BINARY ?= bin/portless-e2e
RELAY_E2E_BINARY ?= bin/portless-relay-e2e
RESOURCE_E2E_RUNTIME ?= auto
DISPATCH_EXAMPLE := examples/dispatch
STORE_EXAMPLE := examples/store
WEB_PROJECT := portless-web
SITE_PROJECT := portless-site
CLI_PACKAGE := ./portless-cli/cmd/portless
VERSION ?= dev
DISTRIBUTION ?= source
COMMIT ?= $(shell git rev-parse --verify HEAD 2>/dev/null || printf unknown)
PORTLESS_LDFLAGS := -X github.com/runportless/portless/portless-cli.Version=$(VERSION) -X github.com/runportless/portless/portless-cli.Distribution=$(DISTRIBUTION) -X github.com/runportless/portless/portless-cli.Commit=$(COMMIT)
WEB_DEPENDENCIES := $(WEB_PROJECT)/node_modules/.package-lock.json
WEB_MANIFESTS := $(WEB_PROJECT)/package.json $(WEB_PROJECT)/package-lock.json
SITE_DEPENDENCIES := $(SITE_PROJECT)/node_modules/.package-lock.json
SITE_MANIFESTS := $(SITE_PROJECT)/package.json $(SITE_PROJECT)/package-lock.json
SITE_NPM_CACHE := $(abspath $(SITE_PROJECT)/.npm-cache)

# Declare command-style targets phony so matching files never suppress their recipes.
.PHONY: build web site site-dev test test-go test-web test-site example-store-dependencies test-example-store example-dispatch-bootstrap example-dispatch-bootstrap-install test-example-dispatch e2e-binary relay-e2e-binary test-e2e test-e2e-cli test-e2e-ui test-e2e-resources test-e2e-store test-e2e-dispatch test-e2e-relay-destructive test-e2e-relay-destructive-resources install-e2e-browser install release-check release-snapshot clean reinstall-web-dependencies reinstall-site-dependencies

# Build the web control plane and the Portless executable.
build: web
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -ldflags "$(PORTLESS_LDFLAGS)" -o "$(BINARY)" $(CLI_PACKAGE)

# Install locked web development dependencies when their manifests change.
$(WEB_DEPENDENCIES): $(WEB_MANIFESTS)
	$(NPM) --prefix $(WEB_PROJECT) ci --include=dev

# Build the production web control-plane assets embedded in Portless.
web: $(WEB_DEPENDENCIES)
	$(NPM) --prefix $(WEB_PROJECT) run build

# Install locked marketing-site dependencies when their manifests change.
$(SITE_DEPENDENCIES): $(SITE_MANIFESTS)
	NPM_CONFIG_CACHE="$(SITE_NPM_CACHE)" $(NPM) --prefix $(SITE_PROJECT) ci --include=dev

# Build the production static marketing site.
site: $(SITE_DEPENDENCIES)
	$(NPM) --prefix $(SITE_PROJECT) run build

# Start the marketing-site development server on the loopback interface.
site-dev: $(SITE_DEPENDENCIES)
	$(NPM) --prefix $(SITE_PROJECT) run dev -- --host 127.0.0.1

# Validate the web projects and run the complete Go test suite.
test: test-web test-site
	$(GO) test ./...

# Run the complete Go test suite without validating the web projects.
test-go:
	$(GO) test ./...

# Type-check, unit-test, and production-build the web control plane.
test-web: $(WEB_DEPENDENCIES)
	$(NPM) --prefix $(WEB_PROJECT) run typecheck
	$(NPM) --prefix $(WEB_PROJECT) test
	$(NPM) --prefix $(WEB_PROJECT) run build

# Check, unit-test, and production-build the marketing site.
test-site: $(SITE_DEPENDENCIES)
	$(NPM) --prefix $(SITE_PROJECT) run check
	$(NPM) --prefix $(SITE_PROJECT) test
	$(NPM) --prefix $(SITE_PROJECT) run build

# Materialize the Dispatch example as three independent Git checkouts.
example-dispatch-bootstrap:
	$(MAKE) -C $(DISPATCH_EXAMPLE) bootstrap

# Install the Store example's locked Node dependencies.
example-store-dependencies:
	$(MAKE) -C $(STORE_EXAMPLE) dependencies

# Validate the Store example applications.
test-example-store:
	$(MAKE) -C $(STORE_EXAMPLE) test

# Materialize the Dispatch example and install its locked application dependencies.
example-dispatch-bootstrap-install:
	$(MAKE) -C $(DISPATCH_EXAMPLE) bootstrap-install

# Validate every application template in the Dispatch example.
test-example-dispatch:
	$(MAKE) -C $(DISPATCH_EXAMPLE) test

# Build the Portless executable used by the ordinary end-to-end suites.
e2e-binary: web
	@mkdir -p "$(dir $(E2E_BINARY))"
	$(GO) build -tags=e2e -trimpath -ldflags "$(PORTLESS_LDFLAGS)" -o "$(E2E_BINARY)" $(CLI_PACKAGE)

# Build the Portless executable used by destructive relay end-to-end suites.
relay-e2e-binary: web
	@mkdir -p "$(dir $(RELAY_E2E_BINARY))"
	$(GO) build -trimpath -ldflags "$(PORTLESS_LDFLAGS)" -o "$(RELAY_E2E_BINARY)" $(CLI_PACKAGE)

# Run the ordinary CLI and browser end-to-end suites.
test-e2e: test-e2e-cli test-e2e-ui

# Run the CLI end-to-end suite against an isolated Portless executable.
test-e2e-cli: e2e-binary
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" $(GO) test -count=1 -tags=e2e ./tests/e2e

# Run the Playwright end-to-end suite against an isolated Portless executable.
test-e2e-ui: e2e-binary
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" $(NPM) --prefix $(WEB_PROJECT) run test:e2e

# Run managed-resource end-to-end tests with the selected container runtime.
test-e2e-resources: e2e-binary
	PORTLESS_MANAGED_RESOURCE_E2E=1 \
	PORTLESS_MANAGED_RESOURCE_RUNTIME="$(RESOURCE_E2E_RUNTIME)" \
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" \
	$(GO) test -count=1 -tags=e2e ./tests/e2e -run '^TestCLIManagedResource' -v

# Run the real multi-checkout Dispatch application with managed MySQL and NATS.
test-e2e-dispatch: e2e-binary
	$(MAKE) -C $(DISPATCH_EXAMPLE) dependencies
	PORTLESS_DISPATCH_EXAMPLE_E2E=1 \
	PORTLESS_MANAGED_RESOURCE_RUNTIME="$(RESOURCE_E2E_RUNTIME)" \
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" \
	$(GO) test -count=1 -tags=e2e ./tests/e2e -run '^TestDispatchExampleEndToEnd$$' -v

# Run the real Store application with two managed PostgreSQL instances and Valkey.
test-e2e-store: e2e-binary
	$(MAKE) -C $(STORE_EXAMPLE) dependencies
	PORTLESS_STORE_EXAMPLE_E2E=1 \
	PORTLESS_MANAGED_RESOURCE_RUNTIME="$(RESOURCE_E2E_RUNTIME)" \
	PORTLESS_E2E_BINARY="$(abspath $(E2E_BINARY))" \
	$(GO) test -count=1 -tags=e2e ./tests/e2e -run '^TestStoreExampleEndToEnd$$' -v

# Run the machine-destructive relay end-to-end suite.
test-e2e-relay-destructive: relay-e2e-binary
	@PORTLESS_DESTRUCTIVE_RELAY_E2E=1 \
	PORTLESS_RELAY_E2E_BINARY="$(abspath $(RELAY_E2E_BINARY))" \
	$(GO) test -count=1 -tags=relay_e2e ./tests/relay_e2e -v

# Run destructive relay tests that also exercise managed resources.
test-e2e-relay-destructive-resources: relay-e2e-binary
	@PORTLESS_DESTRUCTIVE_RELAY_E2E=1 \
	PORTLESS_DESTRUCTIVE_RELAY_RESOURCE_E2E=1 \
	PORTLESS_MANAGED_RESOURCE_RUNTIME="$(RESOURCE_E2E_RUNTIME)" \
	PORTLESS_RELAY_E2E_BINARY="$(abspath $(RELAY_E2E_BINARY))" \
	$(GO) test -count=1 -tags=relay_e2e ./tests/relay_e2e -v

# Install the Chromium browser required by Playwright end-to-end tests.
install-e2e-browser: $(WEB_DEPENDENCIES)
	$(NPM) --prefix $(WEB_PROJECT) exec -- playwright install chromium

# Build Portless and install it into GOBIN or the default GOPATH bin directory.
install: build
	@install_directory="$${GOBIN:-$$($(GO) env GOPATH)/bin}"; \
	mkdir -p "$$install_directory"; \
	install -m 0755 "$(BINARY)" "$$install_directory/portless"; \
	echo "Installed $$install_directory/portless"

# Validate the GoReleaser configuration without creating artifacts.
release-check:
	$(GORELEASER) check

# Build local macOS and Linux release archives without publishing or requiring Syft.
release-snapshot: web release-check
	$(GORELEASER) release --snapshot --clean --skip=sbom

# Reinstall the locked web dependencies even when the dependency stamp is current.
reinstall-web-dependencies:
	$(NPM) --prefix $(WEB_PROJECT) ci --include=dev

# Reinstall the locked marketing-site dependencies even when the stamp is current.
reinstall-site-dependencies:
	NPM_CONFIG_CACHE="$(SITE_NPM_CACHE)" $(NPM) --prefix $(SITE_PROJECT) ci --include=dev

# Remove generated binaries, web coverage, and marketing-site build output.
clean:
	rm -rf bin $(WEB_PROJECT)/coverage $(SITE_PROJECT)/dist
