# End-to-end testing

Portless has two default end-to-end suites and four explicit opt-in integration
boundaries. All of them exercise the compiled product rather than replacing
the daemon or API with test doubles:

- The CLI suite starts the real `portless` executable, daemon, process
  supervisors, edge proxies, and fixture applications.
- The UI suite starts the same stack and drives the embedded production UI in
  Chromium with Playwright.
- The managed-resource suite additionally provisions real PostgreSQL, Valkey,
  MySQL, and NATS containers with Docker or Podman.
- The Store example suite runs the real stateful single-checkout example with
  Spring Boot, Node.js, two PostgreSQL instances, and Valkey.
- The Dispatch example suite runs the real three-source example, including its
  Next.js, FastAPI, Fastify, and Go services plus MySQL and NATS.
- The destructive relay suite installs the production machine relay and uses
  the real port-80 and DNS integration.

The default, managed-resource, Store, and Dispatch tests receive a temporary
`PORTLESS_HOME`. The first two also receive temporary source checkouts. Store
and Dispatch run tracked example sources and write only ignored dependency and
build output. Teardown performs a forced Portless reset, stops the isolated
daemon, and removes the temporary directory. Those suites do not read or
change the developer's normal `~/.portless` installation. The relay suite is
the explicit machine-level exception described below.

## Run the suites

Install the Playwright Chromium build once:

```bash
make install-e2e-browser
```

Then run both suites with one command:

```bash
make test-e2e
```

The suites can also run independently:

```bash
make test-e2e-cli
make test-e2e-ui
```

Real managed-resource lifecycle coverage is opt-in because it requires a
ready Docker or Podman engine and may pull images:

```bash
make test-e2e-resources

# Force one engine when testing runtime-specific behavior:
make test-e2e-resources RESOURCE_E2E_RUNTIME=podman
```

The multi-checkout Dispatch application has its own opt-in target. It installs
the example's locked dependencies, uses the selected container engine, and may
pull MySQL or NATS images:

```bash
make test-e2e-dispatch
make test-e2e-dispatch RESOURCE_E2E_RUNTIME=docker
```

The stateful Store application has a corresponding opt-in target for two
managed PostgreSQL instances and Valkey:

```bash
make test-e2e-store
make test-e2e-store RESOURCE_E2E_RUNTIME=podman
```

`make test` remains the fast unit, component, and build-validation suite. It
does not install a browser or run E2E tests.

## What is covered

The CLI E2E suite protects these product contracts:

- zero-configuration discovery and a complete `up`, request, inspect, logs,
  `down` lifecycle;
- framework-plugin discovery for Spring Boot with Gradle and Maven, NestJS,
  Express, Fastify, Next.js, Go, and FastAPI, including commands, port
  contracts, statically proven HTTP readiness paths with TCP fallback, evidence,
  debugger metadata, precedence, deterministic rescan, and fail-closed malformed
  manifests;
- resource-plugin discovery for PostgreSQL, Valkey, MySQL, and NATS, including
  versions, ports, generated environment bindings, and dependency edges;
- context-aware startup from a nested service directory, Portless-owned Node
  inspectors, additive debug modes, independent return to normal mode, and
  clean environment-wide reset with `up --managed`;
- human-readable default output, valid `--json` output, grouped help, and
  useful help for incomplete commands;
- exact traffic targets, repeated header capture, credential-header redaction,
  bounded Redis, PostgreSQL, MySQL, and NATS operation decoding, and explicit
  session fallbacks for encrypted or incomplete protocol traffic;
- bounded recordings and persistent fault creation, matching, disable,
  re-enable, export, and deletion;
- authenticated daemon restart within the fixed five-second readiness deadline,
  with adoption of the original service processes and proxy routes while live
  browser event streams reconnect;
- hard daemon crashes and executable replacement with exact process adoption,
  plus service crashes, degraded state, retained logs, and recovery;
- reboot-shaped loss where durable supervisor files still say `ready` but the
  authenticated supervisor PIDs and application process groups are gone,
  including automatic restart by one `portless up` and direct forced reset;
- `down --all` from an ambiguous checkout and across multiple simultaneously
  active worktrees;
- several source repositories compiled into one project, environment cloning,
  adding a source after cloning, explicit remediation of the other
  environment, and project-wide source deletion;
- a mixed environment with local services and a remote QA dependency,
  including traffic attribution, local enforcement of its read-only write
  policy, and active local/remote provider handoffs that preserve unrelated
  service PIDs and generations;
- deterministic multi-service scenario and route creation, service-specific
  matcher preview, whole-scenario activation, mock traffic attribution,
  dependency short circuiting, peer-process preservation, and restoration of
  every target provider;
- forced reset when ordinary lifecycle state is from an incompatible model.

The Playwright suite protects these browser journeys:

- browser authentication and one-use claim consumption;
- focused per-browser-tab project and environment navigation, the running/recent
  project switcher with remembered environments, the searchable project registry
  with direct configuration, hide, and safe forget workflows, the persistent
  collapsible icon rail, Settings return, and breadcrumbs;
- a persistent environment header across all eight views, health and public
  Open App links, shared lifecycle state with the command palette, and a single
  activity subscription that discards responses from a previous environment;
- compact recording, fault, and mock icons in that header, with themed colors,
  descriptive tooltips, keyboard navigation, and live activation state;
- an Overview heading with environment identity and clone provenance, live
  recording/fault/mock links, and readable wrapping in focus mode and on narrow
  screens;
- keyboard- and command-palette-driven focus mode, desktop hover navigation,
  explicit overlay navigation on narrow screens, nested dialog dismissal,
  focus restoration, and viewport-sized topology;
- environment creation from the persistent sidebar through the modal without
  duplicating project sources,
  including visible clone provenance that does not displace status messaging,
  plus stopped-only forgetting from the current environment header;
- browser theme persistence;
- services, copyable endpoints, hover- and focus-driven topology service
  previews with connected-edge emphasis, service details, and default-on live
  logs with a plain-text raw tab and pause/resume controls;
- live mock-binding badges on topology cards, scenario identification on hover
  and keyboard focus, and service-endpoint links into the scenario workspace,
  with stable card geometry in both themes and indicator removal on restoration;
- starting a real Portless-owned Node debugger from the service drawer,
  displaying its attach endpoint, preserving healthy environment semantics,
  and returning the service to normal mode;
- captured request and response inspection with repeated headers, redacted
  credentials, and Portless-injected trace carriers kept out of header views;
- recording, mock-provider, and fault workflows, including scenario-table-first
  navigation, empty service-independent scenario creation, URL-addressable
  scenario split workspaces with URL-addressable route selection, a
  service-selecting right-hand editor that respects focus mode, retained drafts
  while switching routes, save/discard and selected-route deletion, clickable
  and sortable routes paginated at ten rows, whole-scenario activation, stable
  peer service PIDs, stationary tables and route panes throughout activation
  and restoration, and traffic attribution;
- environment stop/start controls, project-page source add/delete, and
  Bindings-page checkout configure/edit/remove workflows using the native
  directory picker;
- active service-scoped provider handoff with unrelated runtime preservation,
  plus stopped-environment remote binding persistence and restore;
- durable timeline rendering and pagination;
- trace-first traffic expansion, raw exchange filtering, buffered pause/resume,
  and HTTP/TCP switching;
- keyboard topology inspection, command-palette navigation, runtime status,
  not-found routes, and automatic recovery from a failed control-plane poll;
- daemon details, restart timing, and logs; full-screen drawer behavior;
  blocked-handoff stop guidance and force-restart confirmation; five-second
  restart failure messaging, reconnect, and runtime adoption.

## Test-only ingress

Production `portless up` requires the installed machine-level relay because
the public product contract uses clean port-80 and TCP DNS endpoints. Normal
CI jobs must not install privileged services or claim machine ports.

The E2E binary is therefore compiled with the `e2e` Go build tag. That tag uses
the isolated daemon's private Unix ingress socket instead of the machine relay.
HTTP application requests still cross the real daemon ingress router and all
real per-edge proxies. TCP dependency edges use ephemeral `127.0.0.1` proxy
ports, and the isolated daemon does not publish public TCP listeners from the
machine-wide `127.77.0.0/24` pool. This lets the suites run beside a developer's
active Portless installation without competing for its clean endpoints. The
destructive relay suite uses a normal production build and separately verifies
stable clean TCP endpoints and system DNS. A normal `make` build cannot
activate the private path because its composition root hard-codes it off.

The shared fixture at `tests/fixtures/store-lite` intentionally has no Portless
declaration and no external dependencies. It is discovered as:

```text
client -> checkout -> inventory
                   -> orders
```

The multi-source test materializes those applications as separate temporary Go
modules so it exercises project compilation rather than a monorepo shortcut.
The `tests/fixtures/debug-node` workspace provides two small NestJS-shaped Node
services with safe direct-node launch commands. It verifies real inspector
listeners and process ownership without installing application dependencies.

## Managed-resource integration

`make test-e2e-resources` enables only the container-backed scenarios with
`PORTLESS_MANAGED_RESOURCE_E2E=1`. The target accepts
`RESOURCE_E2E_RUNTIME=auto|docker|podman`; `auto` uses the normal Portless
runtime selection. It verifies:

- real readiness and protocol probes for PostgreSQL, Valkey, MySQL, and NATS;
- generated connection values delivered to the consuming service;
- exact container adoption across daemon restart;
- reboot-shaped recovery of dead process supervisors and an externally stopped
  fully owned Valkey container, including recreation at new generations and
  preservation of data in the managed volume;
- ordinary `down`/`up` behavior and Valkey volume persistence;
- explicit `down --volumes --yes` data removal; and
- endpoint, upstream, and data isolation between two active environments,
  followed by `down --all`.

Each scenario uses a temporary Portless home and cleans up its containers and
volumes. The suite is safe for normal application state, but it intentionally
exercises the selected local container engine and may download several images.

## Store example integration

`make test-e2e-store` enables only `TestStoreExampleEndToEnd` with
`PORTLESS_STORE_EXAMPLE_E2E=1`. It runs the tracked Store source against a
temporary Portless home and private E2E ingress. It verifies:

- discovery and readiness of checkout, orders, inventory, consumer-scoped
  `inventory-postgres`, `orders-postgres`, and `orders-redis` resources;
- an atomic inventory reservation followed by a checkout persisted with a
  PostgreSQL-generated order ID;
- cache-aside reads that miss through PostgreSQL and then hit Valkey;
- decoded inventory PostgreSQL `UPDATE` plus order PostgreSQL `INSERT` and
  `SELECT` exchanges with captured SQL;
- decoded Redis `GET` and `SET` exchanges with the expected cache key;
- stock and order persistence across their owning process restarts; and
- both PostgreSQL volumes persisting across ordinary environment down/up; and
- the checkout page's inventory reset restoring the seed stock through the
  checkout-to-inventory dependency edge.

The normal Go suite separately asserts the statically discovered Store
topology and application-protocol classifications. `make test-example-store`
runs the Node unit tests and the Spring Boot inventory tests. See the
[Store walkthrough](../examples/store/README.md) for the interactive traffic,
fault, lifecycle, and debugger workflow.

## Dispatch example integration

`make test-e2e-dispatch` enables only `TestDispatchExampleEndToEnd` with
`PORTLESS_DISPATCH_EXAMPLE_E2E=1`. It runs the tracked application templates as
three source roots against a temporary Portless home and private E2E ingress.
It verifies:

- compilation of `console`, `operations`, and `maps` into one seven-service
  project, including consumer-scoped `api-mysql` and explicitly shared
  `dispatch-nats`;
- readiness of five local processes and both managed resources;
- location lookup and a route estimate across the source-aware HTTP graph;
- a delivery persisted to MySQL with a readable public ID;
- publication and consumption of the corresponding NATS event; and
- captured `console:api`, `console:notifier`, `api:geocoder`, `api:routing`,
  and `routing:geocoder` traffic.

The normal Go suite separately bootstraps temporary independent Git
repositories, verifies that the bootstrap refuses to overwrite them, applies
the scenic-routing worktree patch with `git apply --check`, validates the four
OpenAPI documents, and asserts the statically discovered topology. See the
[Dispatch walkthrough](../examples/dispatch/README.md) for the interactive
worktree, fault, recording, mock, and remote-provider scenarios.

## Destructive relay integration

The default E2E suites deliberately do not mutate machine-level networking.
Relay installation and removal have a separate, deliberately destructive test:

```bash
make test-e2e-relay-destructive
```

To include a real Valkey container and verify its clean `*.portless.test` TCP
endpoint through system DNS, use the more explicit target:

```bash
make test-e2e-relay-destructive-resources
```

Both targets build a dedicated binary with the normal production behavior, so
they do not replace the executable watched by a running development daemon.
They then run the read-only safety preflight, ask `sudo` to cache administrator
approval, and run serially against the real fixed Portless service, port 80,
DNS listener, resolver configuration, and loopback address pool. Neither is
included by `make test` or `make test-e2e`.

The harness performs these safety checks before changing the machine:

- it must run as the normal non-root user through the deliberately named
  destructive Make target;
- a machine-wide lock prevents concurrent destructive relay suites;
- an existing relay must have a valid receipt owned by the current user;
- its HTTP and DNS sockets must identify one recognizable Portless home; and
- the daemon behind an existing relay must be reachable and report no active
  environments; a stopped daemon is rejected because it cannot prove that no
  supervised runtime survived it.

If an owned relay exists, the harness records its socket targets and daemon
state, removes it, installs the test relay against a temporary
`PORTLESS_HOME`, and restores the original relay during `TestMain` teardown.
Restoration also runs after a failed assertion or panic. A forced cleanup is
allowed only after the harness has removed the verified original installation
and taken exclusive ownership of the machine relay slot.

The scenario verifies:

- install and idempotent repair through the public CLI;
- receipt ownership, target sockets, launch service, helper, resolver, address
  pool, HTTP, direct DNS, and system-resolver health;
- a production `portless up`, clean control and application URLs through
  `127.0.0.1:80`, a real one-use browser claim and authenticated session, and
  rejection of unknown hosts;
- relay restart without losing application routing;
- the relay's controlled `503` response while the isolated daemon is stopped,
  followed by daemon recovery and runtime adoption; and
- full uninstall preview and confirmation, removal of processes, application
  data, every reported system artifact and listener, resolver removal,
  preservation of a source-tree executable, and idempotent repeated uninstall.

The resource-enabled variant also verifies system resolution of a clean
Valkey hostname and a real TCP `PING` through the production relay.

The test is suitable for a Portless developer machine when no environment is
running, but it temporarily interrupts the existing relay. A process kill,
machine restart, or terminal loss can prevent teardown, so a disposable macOS
or systemd Linux runner remains the safest place to automate it.

Orphan-container cleanup after an externally interrupted engine operation and
automation on disposable macOS and Linux hosts remain separate future
coverage.

## Failures and artifacts

The Playwright suite is split into focused access/navigation, settings,
projects, environment, experiments, topology-mocks, traffic-list, traffic-inspection,
traffic-waterfall, and daemon journey specs. The specs still run with one
worker and stop after the first failure because they share one real isolated
Portless stack. To run one journey while developing:

```bash
make e2e-binary
npm --prefix portless-web run test:e2e -- traffic-waterfall.spec.ts
```

On failure Playwright retains a screenshot, video, trace, and error context
under `portless-web/test-results/`. Open a trace with:

```bash
npm --prefix portless-web exec -- playwright show-trace portless-web/test-results/<test>/trace.zip
```

CLI failures print the isolated daemon log in the failing assertion. To target
one CLI scenario while developing:

```bash
make e2e-binary
PORTLESS_E2E_BINARY="$PWD/bin/portless-e2e" \
  go test -count=1 -tags=e2e ./tests/e2e -run TestCLIZeroConfigurationLifecycle -v
```

The CI workflow runs the CLI and Playwright suites in independent jobs with
bounded step and job timeouts. Failed runs retain the complete command log and
isolated daemon log for seven days; browser failures also retain the Playwright
HTML report, trace, screenshot, video, and error context. Set
`PORTLESS_E2E_ARTIFACT_DIR` locally to preserve the same daemon-log diagnostics
outside the temporary test installation.
