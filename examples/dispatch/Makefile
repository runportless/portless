SHELL := /bin/bash

GO ?= go
NPM ?= npm
UV ?= uv
WORKSPACE ?= $(CURDIR)/workspace
CONSOLE_DEPENDENCIES := templates/console/node_modules/.package-lock.json
OPERATIONS_DEPENDENCIES := templates/operations/node_modules/.package-lock.json
API_DEPENDENCIES := templates/operations/api/.venv/.dispatch-lock

.PHONY: bootstrap bootstrap-install prepare dependencies test test-go test-node test-python build-console qa-api scenic-worktree

bootstrap:
	./bootstrap.sh --workspace "$(WORKSPACE)"

bootstrap-install:
	./bootstrap.sh --workspace "$(WORKSPACE)" --install

prepare:
	@test -f "$(WORKSPACE)/.dispatch-example" || { echo "Run make bootstrap first" >&2; exit 1; }
	$(NPM) --prefix "$(WORKSPACE)/checkouts/console" ci
	$(NPM) --prefix "$(WORKSPACE)/checkouts/operations" ci
	$(UV) sync --project "$(WORKSPACE)/checkouts/operations/api" --frozen

$(CONSOLE_DEPENDENCIES): templates/console/package.json templates/console/package-lock.json
	$(NPM) --prefix templates/console ci

$(OPERATIONS_DEPENDENCIES): templates/operations/package.json templates/operations/package-lock.json templates/operations/notifier/package.json
	$(NPM) --prefix templates/operations ci

$(API_DEPENDENCIES): templates/operations/api/pyproject.toml templates/operations/api/uv.lock
	$(UV) sync --project templates/operations/api --frozen
	@touch "$@"

dependencies: $(CONSOLE_DEPENDENCIES) $(OPERATIONS_DEPENDENCIES) $(API_DEPENDENCIES)

test: dependencies test-go test-node test-python build-console

test-go:
	$(GO) -C templates/maps test ./...
	$(GO) -C tools/qa-api test ./...

test-node: $(CONSOLE_DEPENDENCIES) $(OPERATIONS_DEPENDENCIES)
	$(NPM) --prefix templates/console test
	$(NPM) --prefix templates/operations test

test-python: $(API_DEPENDENCIES)
	$(UV) run --project templates/operations/api --frozen pytest templates/operations/api/tests

build-console: $(CONSOLE_DEPENDENCIES)
	$(NPM) --prefix templates/console run build

qa-api:
	$(GO) -C tools/qa-api run .

scenic-worktree:
	@test -f "$(WORKSPACE)/.dispatch-example" || { echo "Run make bootstrap first" >&2; exit 1; }
	@test ! -e "$(WORKSPACE)/worktrees/maps-scenic" || { echo "Scenic worktree already exists" >&2; exit 1; }
	git -C "$(WORKSPACE)/checkouts/maps" worktree add "$(WORKSPACE)/worktrees/maps-scenic" -b scenic-routing
	git -C "$(WORKSPACE)/worktrees/maps-scenic" apply "$(CURDIR)/scenarios/scenic-routing.patch"
	@echo "Scenic maps worktree created at $(WORKSPACE)/worktrees/maps-scenic"
