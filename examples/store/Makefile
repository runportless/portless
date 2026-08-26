SHELL := /bin/bash

NPM ?= npm
NODE_DEPENDENCIES := node_modules/.package-lock.json
NODE_MANIFESTS := package.json package-lock.json apps/checkout/package.json apps/orders/package.json
NPM_CACHE := $(abspath .npm-cache)

.PHONY: dependencies test test-node test-java reinstall-dependencies

$(NODE_DEPENDENCIES): $(NODE_MANIFESTS)
	NPM_CONFIG_CACHE="$(NPM_CACHE)" $(NPM) ci --include=dev

dependencies: $(NODE_DEPENDENCIES)

test: test-node test-java

test-node: $(NODE_DEPENDENCIES)
	$(NPM) test

test-java:
	./gradlew test

reinstall-dependencies:
	NPM_CONFIG_CACHE="$(NPM_CACHE)" $(NPM) ci --include=dev
