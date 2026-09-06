SHELL := /bin/bash
NPM ?= npm
NODE_DEPENDENCIES := node_modules/.package-lock.json
NODE_MANIFESTS := package.json package-lock.json apps/chat/package.json apps/rooms/package.json
NPM_CACHE := $(abspath .npm-cache)

.PHONY: dependencies test install-browser e2e

$(NODE_DEPENDENCIES): $(NODE_MANIFESTS)
	NPM_CONFIG_CACHE="$(NPM_CACHE)" $(NPM) ci --include=dev

dependencies: $(NODE_DEPENDENCIES)

test: dependencies
	$(NPM) run check
	$(NPM) test

install-browser: dependencies
	$(NPM) run install-browser

e2e: dependencies
	$(NPM) run test:e2e
