# End-to-end testing

Portless has two default end-to-end suites and two explicit opt-in integration
boundaries. All of them exercise the compiled product rather than replacing
the daemon or API with test doubles:

- The CLI suite starts the real `portless` executable, daemon, process
  supervisors, edge proxies, and fixture applications.
- The UI suite starts the same stack and drives the embedded production UI in
  Chromium with Playwright.
- The managed-resource suite additionally provisions real PostgreSQL, Valkey,
  MySQL, and NATS containers with Docker or Podman.
- The destructive relay suite installs the production machine relay and uses
  the real port-80 and DNS integration.

The default and managed-resource tests receive a temporary `PORTLESS_HOME` and
temporary source checkouts. Teardown performs a forced Portless reset, stops
the isolated daemon, and removes the temporary directory. Those suites do not
read or change the developer's normal `~/.portless` installation. The relay
suite is the explicit machine-level exception described below.

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

`make test` remains the fast unit, component, and build-validation suite. It
does not install a browser or run E2E tests.

## What is covered

The CLI E2E suite protects these product contracts:

- zero-configuration discovery and a complete `up`, request, inspect, logs,
  `down` lifecycle;
- framework-plugin discovery for Spring Boot with Gradle and Maven, NestJS,
  Express, Fastify, Next.js, Go, and FastAPI, including commands, port
  contracts, health checks, evidence, debugger metadata, precedence, rescan,
  and fail-closed malformed manifests;
- resource-plugin discovery for PostgreSQL, Valkey, MySQL, and NATS, including
  versions, ports, generated environment bindings, and dependency edges;
- context-aware startup from a nested service directory, Portless-owned Node
  inspectors, additive debug modes, independent return to normal mode, and
  clean environment-wide reset with `up --managed`;
- human-readable default output, valid `--json` output, grouped help, and
  useful help for incomplete commands;
- lossless traffic detail and header capture;
- bounded recordings and persistent fault creation, matching, disable,
  re-enable, export, and deletion;
- authenticated daemon restart with adoption of the original service
  processes and proxy routes;
- hard daemon crashes and executable replacement with exact process adoption,
  plus service crashes, degraded state, retained logs, and recovery;
- `down --all` from an ambiguous checkout and across multiple simultaneously
  active worktrees;
- several source repositories compiled into one project, environment cloning,
  adding a source after cloning, and explicit remediation of the other
  environment;
- a mixed environment with local services and a remote QA dependency,
  including traffic attribution and local enforcement of its read-only write
  policy;
- forced reset when ordinary lifecycle state is from an incompatible model.

The Playwright suite protects these browser journeys:

- browser authentication and one-use claim consumption;
- project, environment, sidebar, and breadcrumb navigation;
- environment creation through the modal without duplicating project sources;
- browser theme persistence;
- services, copyable endpoints, topology edges, service details, and logs;
- starting a real Portless-owned Node debugger from the service drawer,
  displaying its attach endpoint, preserving healthy environment semantics,
  and returning the service to normal mode;
- captured request and response inspection with complete headers;
- recording and fault workflows;
- environment stop/start controls;
- stopped-only provider editing with remote binding persistence and restore;
- durable timeline rendering and pagination;
- traffic filtering, pause/resume snapshots, and HTTP/TCP switching;
- keyboard topology inspection, command-palette navigation, runtime status,
  not-found routes, and automatic recovery from a failed control-plane poll;
- daemon details, full-screen drawer behavior, restart, reconnect, and runtime
  adoption.

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
- ordinary `down`/`up` behavior and Valkey volume persistence;
- explicit `down --volumes --yes` data removal; and
- endpoint, upstream, and data isolation between two active environments,
  followed by `down --all`.

Each scenario uses a temporary Portless home and cleans up its containers and
volumes. The suite is safe for normal application state, but it intentionally
exercises the selected local container engine and may download several images.

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

Playwright runs serially because the scenarios intentionally share one real
environment. On failure it retains a screenshot, video, trace, and error
context under `web/test-results/`. Open a trace with:

```bash
npm --prefix web exec -- playwright show-trace web/test-results/<test>/trace.zip
```

CLI failures print the isolated daemon log in the failing assertion. To target
one CLI scenario while developing:

```bash
make e2e-binary
PORTLESS_E2E_BINARY="$PWD/bin/portless-e2e" \
  go test -count=1 -tags=e2e ./tests/e2e -run TestCLIZeroConfigurationLifecycle -v
```
