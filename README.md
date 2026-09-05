<p align="center">
  <a href="https://www.portless.run">
    <img src="brand/logo/portless-logo-dark-1200x300.png" alt="Portless" width="600">
  </a>
</p>

[Portless](https://www.portless.run) is a local application-environment
control plane. It discovers an application across one or more repositories,
runs each service from a local checkout, managed container, remote endpoint, or
deterministic mock, and gives the complete environment stable names without
fixed host ports.

The browser control plane and CLI let you inspect services, follow traffic,
record a reproduction, inject scoped failures, and replace one HTTP dependency
without changing its callers. There is no required `portless.yaml`, Docker
Compose project, account, or hosted control plane.

Portless is a runnable vertical implementation in active development. See the
[implementation boundary](docs/implementation-status.md) for what is complete
and what remains before a hardened public release.

## Install

Portless currently targets macOS and systemd-based Linux on AMD64 and ARM64.
Docker Engine or Podman is needed only for environments with managed container
resources.

There is not yet a stable public release. Build the current implementation
from source with Go 1.26 or newer, Node.js 22.12 or newer, and npm. Prerelease
artifacts, when present, are intended for release-pipeline validation and early
evaluation:

```bash
git clone https://github.com/runportless/portless.git
cd portless
make install
portless setup
```

`make install` writes `portless` to `GOBIN`, or to the default Go bin
directory when `GOBIN` is unset; ensure that directory is on `PATH`. On Linux,
clean endpoint setup requires systemd with `systemd-resolved` and fails closed
without changing the machine when that boundary is unavailable.

Static macOS/Linux archives and the `runportless/tap/portless` Homebrew formula
are configured for the first stable release. The tap-qualified name is
required because Homebrew Core contains a
[different project named `portless`](https://formulae.brew.sh/formula/portless).
See the [release guide](docs/releasing.md) for the publication boundary.

## Quick start

Run setup once per machine, enter a service checkout, and start the
environment:

```text
portless setup
cd billing
portless up

# Portless discovers billing/local, starts it, then opens:
http://portless.localhost/environments/billing/local
```

`portless setup` may request administrator approval to install the fixed
loopback HTTP and DNS relay. The application-aware daemon and all project
runtimes remain unprivileged.

On macOS the same setup installs a scoped resolver for descendant `.localhost`
names, so native API clients and language runtimes can resolve the advertised
HTTP URLs even when they do not implement `.localhost` handling themselves.
Linux `systemd-resolved` provides that mapping natively.

`portless up` statically discovers the checkout. From a registered service
directory it enables a discovered Node inspector or JVM debug endpoint for
that service while starting its peers normally. From a project root it
preserves existing debug modes and starts missing services in managed mode.

Current built-in discovery covers Spring Boot, NestJS, Express, Fastify,
Next.js, Go HTTP/RPC services, and FastAPI. Managed-resource plugins cover
PostgreSQL, Valkey, MySQL, and NATS. The
[implementation boundary](docs/implementation-status.md) is the maintained
inventory of supported behavior and deferred release work.

Discovery also selects an HTTP readiness endpoint when bounded static evidence
proves one: NestJS decorators, Express/Fastify literal GET registrations,
Next.js route files, raw Node URL comparisons, FastAPI decorators, Go HTTP
registrations, and Spring Boot Actuator configuration are recognized. Dynamic
or equally strong conflicting routes keep the conservative TCP readiness check;
Portless never executes application code or probes candidate routes during
discovery.

The compact [Store example](examples/store/README.md) discovers one mixed-stack
checkout with Spring Boot and Node services, persists inventory reservations
and orders in two independently managed PostgreSQL instances, caches order
reads in managed Valkey, and exposes the decoded SQL and Redis operations in
Traffic.

For a larger walkthrough, the [Dispatch example](examples/dispatch/README.md)
materializes one logical courier application as three independent Git
checkouts. It exercises mixed frameworks, MySQL and NATS, environment-specific
worktrees, source-aware faults and recordings, mocks, and a read-only remote
provider without a Portless declaration.

## How Portless models an application

| Concept | Meaning |
| --- | --- |
| Project | One logical application, which may span several repositories. |
| Source | A repository or checkout that contributes services to the project. |
| Environment | A reusable instance of the project topology with its own provider choices and runtime state. |
| Service | A readable application or managed-resource identity. |
| Provider | The selected local, container, remote, or mock implementation of a service. |
| Connection | A directed `source:target` dependency edge that preserves caller identity. |

HTTP ingress is stable and readable:

```text
http://checkout.local.billing.localhost
http://orders.local.billing.localhost
```

The hostname selects the application. Paths such as `/api/orders` and
`/auth/login` reach that application, including paths that match Portless
control routes. Portless's own `/api/v1/` API and `/auth/claim/` browser login
live on `portless.localhost`.

Applications also control their own browser security policies. Portless
preserves those response headers and applies its own browser restrictions
only to its control UI and API.

TCP services retain conventional ports while receiving distinct loopback
identities:

```text
orders-postgres.local.billing.portless.test:5432
orders-redis.local.billing.portless.test:6379
```

Processes and containers still use private dynamic ports. A caller receives a
source-aware dependency name such as
`orders-postgres.via-orders.local.billing.portless.test`, so traffic,
recordings, and faults retain the exact `orders:orders-postgres` edge. Generic
configuration hosts such as `postgres`, `mysql`, `redis`, and `nats` are
consumer-scoped during discovery; `postgresql://postgres` in orders therefore
becomes `orders-postgres`. Reusing a specific logical host such as
`shared-postgres` in multiple services explicitly declares one shared
resource.

The public naming model and private ownership keys are explained in
[ADR 0002](docs/architecture/decisions/0002-names-public-keys-private.md);
source-aware routing is explained in
[ADR 0003](docs/architecture/decisions/0003-edge-proxy.md).

## Core workflows

### Projects spanning repositories

Create a project by naming each source checkout. Portless discovers every
source, merges their services, and resolves cross-repository dependencies:

```bash
portless project create billing \
  --source checkout=../checkout-service \
  --source payments=../payments-service \
  --source notifications=../notifications-service

portless env select billing/local
portless up
```

Checkout paths belong to an environment, while the logical source belongs to
the project. Adding or deleting a source is therefore a stopped,
project-wide topology change; configuring a worktree affects only the selected
environment. See
[projects and environments](portless-cli/COMMANDS.md#projects-and-environments)
for the complete workflow and safety constraints.

### Environment-specific providers

Clone an environment when you need a different composition, then change only
the services that should come from elsewhere:

```bash
portless env clone qa-assisted --from local
portless --env billing/qa-assisted env bind payments \
  --remote https://payments.qa.example.com \
  --classification qa \
  --write-policy read-only \
  --health-path /health
portless --env billing/qa-assisted up
```

The clone records its direct source environment and copies its provider
bindings, mock scenarios, routes, and scenario restoration records. The resulting configuration is independent;
later changes do not propagate between the source and clone.

Start the clone normally, from the UI or with `portless up`. When another
environment is using the same Git checkout, Portless automatically prepares and
binds an independent worktree before starting local services. It copies the
current files, including uncommitted changes and installed local dependencies,
and preserves nested source directories. Sources in the same repository share
one worktree per environment. Subsequent starts reuse it and keep any edits
made there; later edits in the original checkout do not propagate to the copy.
An explicitly selected CLI environment remains selected from the original path.

Automatic preparation requires Git and a repository with a commit. Non-Git
sources and nested repositories still require a separate checkout or stopping
the environment using them. Prepared checkouts live under
`$PORTLESS_HOME/worktrees` (normally `~/.portless/worktrees`) and remain after
stop, forget, or reset. Full uninstall asks you to move or remove them first so
local edits are not erased. The paths remain visible in **Bindings → Checkouts**
and `portless env checkout list`.

HTTP remote providers still pass through the source-aware proxy, so traffic,
recordings, and faults keep working. A read-only binding blocks mutating HTTP
methods locally before the request leaves the machine. Active provider changes
handoff only the selected service and roll back if the replacement cannot
become ready.

Local providers can also be bound explicitly to a chosen Git worktree; container providers use
Portless-managed resources, and mock providers return fixed local HTTP
responses. See the decisions for
[live provider handoffs](docs/architecture/decisions/0006-live-provider-handoffs.md)
and [deterministic mocks](docs/architecture/decisions/0007-deterministic-http-mock-provider.md).

Mock scenarios can replace several HTTP services together. A scenario has a
name and description; each route selects its own target service:

```bash
portless mock create checkout-failure
portless mock route set checkout-failure inventory-lookup \
  --service inventory --path '/inventory/{sku}' --status 200 \
  --body '{"available":false}'
portless mock route set checkout-failure payment-attempt \
  --service payments --method POST --path /payments --status 503
portless mock enable checkout-failure
portless mock disable checkout-failure
```

Enabling saves each target's exact provider configuration and switches all
target services to the scenario. Disabling restores those saved providers.
Scenarios may be active together only when they target different services.
While active, route responses and enabled flags are editable, but changing the
set of target services requires disabling the scenario first. An unmatched
request returns `501`; it never falls through to the real service.

Daemon restarts recover active mock and remote providers without requiring
outgoing dependency proxies for those services. Incoming connections from
local callers retain their verified proxy endpoints. Scenarios remain editable
and can be disabled after recovery to restore their original providers.

In the browser, selecting a mock scenario opens a split workspace: a sortable,
ten-per-page route list on the left and the selected route's configuration on
the right. Adding and saving routes stays in that workspace. Unsaved drafts
are retained while switching between routes in the scenario; Save applies a
draft, and Discard restores its saved values. The panes scroll independently,
adapt to focus mode, and the route editor can also be maximized.

Topology service cards show a compact `MOCK` badge when their endpoint is bound
to a mock scenario. Hovering or focusing a card identifies the scenario; the
service's endpoint details link directly to its route workspace. Readiness and
traffic indicators retain their independent meanings.

### Observe and experiment

The embedded control plane and CLI share the same authenticated daemon API.
Both can inspect services and effective connections, tail structured logs,
follow raw exchanges and correlated traces, retain bounded recordings, apply
edge-scoped faults, and configure deterministic mocks.

Each browser tab stays focused on one project. The sidebar shows only that
project's environments, while the project switcher keeps running and recently
opened projects close at hand and remembers the last environment used in each.
The Projects page is the durable searchable registry, including projects hidden
from the recent list. Its project action menu opens configuration for creating
environments and managing sources, hides the project from the recent list, or
safely forgets it. Forgetting is available only after all of the project's
environments are stopped and never deletes source checkouts from disk.
The sidebar selects Overview, Topology, Traffic, Mocks, Recordings, Faults,
Bindings, and Timeline. Its badges count active mock scenarios and faults.
Recordings shows `1` only while a recording is running; all badges update live
and hide when inactive.
A compact persistent header shows the project,
environment, current view, health and ready-service count, an Open button,
and minimalist colored activity icons: red for recordings, amber for faults,
and purple for mocks. Tooltips show Recording or the active fault and mock
counts; clicking opens the corresponding list of recordings, faults, or mock
scenarios.
The health link opens Overview; Open uses the primary service's public HTTP URL.
When stopped, Open is replaced by a Start button that starts the entire
environment. Both share a fixed width, including disabled Starting… progress,
so the action does not resize during startup. Stop environment is available
through Search (`Command-K` or `Control-K`). The header has no environment action menu, and
Search keeps its visible label beside the keyboard shortcut at every screen size.
Overview's Services header offers Start All when the environment is stopped or
Stop All when every service is ready; mixed and empty states show neither action.
Lifecycle controls and command-palette actions share pending operation state
until its outcome is confirmed, with live header health and accessible progress
announcements.
Overview starts with the environment name, its current health indicator, and the
clone-origin chip. Clone provenance stays in Overview and is not repeated in the
persistent header.
Recording, fault, and mock shortcuts stay in the persistent header rather than
being repeated in Overview, and remain available in focus mode.
Routine start, recovery, and stop progress stays in the header instead of
inserting a page banner, so mock activation and restoration do not shift the
scenario table or route workspace. Failures and configuration issues use the
same red, structured error notice throughout the control plane, including
daemon diagnostics, mock scenarios, and captured traffic errors. Matching
operation errors and saved environment failure reasons appear only once.
Dismissing that notice hides both copies until the failure clears; a fresh page
load still shows an unresolved saved failure.
Focus mode, toggled with `Command-Shift-F`, `Control-Shift-F`, or the command
palette, keeps the header while hiding the fixed sidebar and its header navigation
button. Hover over the left edge to preview navigation, or activate the edge
button with a click, Enter, or Space to keep the overlay open. On narrow screens
outside focus mode, Open navigation in the header opens the same sidebar overlay.
The browser remembers focus mode and the desktop sidebar's collapse preference
across reloads.
The sidebar's Environments heading also exposes a persistent create action. It
clones the current environment by default and opens the new stopped environment.

The control plane's Portless System drawer groups daemon identity and build
provenance, control-plane health, recovery, managed runtime inventory, local
networking, handoff safety, and retained storage into focused Status, Runtime,
and Storage tabs. Storage inspection is loaded only when requested. A separate
Logs tab exposes a live, bounded tail of the fixed private daemon log; older
output is explicitly marked when omitted, and known installation
authentication and ownership secrets are redacted again at the inspection
boundary.

Normal CLI and browser daemon restarts share a five-second end-to-end readiness
deadline and ordinarily complete in under two seconds. The accepted restart
receipt identifies the coalesced handoff, target build, and deadline; the
Portless System drawer reports the last measured duration and whether it met
the SLA. If a handoff audit blocks restart, the CLI and System drawer identify
the responsible environments, show the exact `portless down --env
project/environment` commands, and direct the user to retry the restart.
After a fresh audit specifically reports a blocked handoff, the System drawer
also exposes a separately confirmed force restart. Its confirmation names every
active environment and warns that bypassing handoff safety may interrupt
services or require `portless up` afterward. Legacy CLI recovery remains
outside the normal restart SLA because it may have to signal an unresponsive
process.

HTTP exchanges retain bounded inspectable headers and bodies. Declared
PostgreSQL, Redis/Valkey, MySQL, and NATS connections are decoded into bounded
logical operations while their TCP connections remain open, so queries,
commands, results, subjects, and message payloads can be inspected from the
same traffic drawer. Decoded TCP spans use Command and Result views parallel to
HTTP's Request and Response views, with a side-by-side Compare view when the
drawer is maximized. Decoded PostgreSQL and MySQL result sets render as compact
database rows with captured column names instead of their JSON representation.
Result tables show at most ten rows per page, while Copy exports every captured
row as CSV. Mutation outcomes and undecodable payloads remain concise text.
Redis commands render in their familiar command-line form instead of their
decoded wire array, and JSON stored in Redis renders as highlighted JSON rather
than an escaped string.
PostgreSQL transactions render as one aggregate waterfall span whose duration
and outcome cover the complete transaction. The traffic drawer shows the
application SQL breakdown while folding protocol boundaries such as `BEGIN`,
`COMMIT`, and `ROLLBACK` into that aggregate. Unknown, encrypted, oversized, or
malformed TCP traffic remains byte-count-only and never interrupts forwarding.

Successful driver and connection-pool housekeeping is marked as background:
for example, PostgreSQL session setup and validation queries or Redis handshakes
and client metadata. Raw Exchanges and recordings still retain those operations,
while successful standalone housekeeping traces, housekeeping spans inside
foreground traces, and topology animation are omitted from the trace-oriented
views. Failed or faulted housekeeping always remains visible. Decoded TCP
dependencies use one consistent summary-row treatment in the trace waterfall.
Explicit database transactions remain individually inspectable in Exchanges
and are grouped into one aggregate waterfall span, while standalone commands
use the same presentation and open directly. The transaction's command/result
breakdown is available in its traffic drawer instead of competing with the
trace timeline.

The trace drawer's previous and next controls follow the waterfall's visible
span model: a transaction is always one command/result summary, and successful
housekeeping spans are excluded. Its overview keeps environment, target binding,
start time, and duration visible in both the standard and maximized layouts.
Drawers opened from the raw Exchanges table instead provide only previous and
next exchange controls, following the table's active filters across pages.

The trace list is HTTP-rooted while retaining decoded TCP dependency spans
inside those requests. Standalone TCP operations remain available through the
raw Exchanges view and its TCP protocol filter.

The [command reference](portless-cli/COMMANDS.md) contains complete CLI usage.
Traffic payloads can contain application data; see
[Local data and safety](#local-data-and-safety) before enabling durable payload
capture.

## Command reference

The [Portless command reference](portless-cli/COMMANDS.md) documents the
complete public hierarchy, global and command-specific options, defaults,
selection and output behavior, destructive safeguards, shell completion, and
common workflows.

Every installed build also provides generated help:

```bash
portless --help
portless env bind --help
```

Contributor guidance for the command tree lives in the
[CLI README](portless-cli/README.md).

## MCP clients

Open the control plane with `portless ui`, then choose **Settings → MCP** to
generate a client configuration. The MCP server is launched by the client over
stdio; the daemon does not expose an MCP network endpoint.

The default tool set is read-only and workspace-scoped. Environment-wide
visibility, lifecycle mutations, bounded traffic control, and sensitive
traffic detail are separate immutable capabilities selected when the client
process starts. See the
[MCP README](portless-mcp/README.md) for configuration examples, the tool
inventory, permission boundaries, limits, and troubleshooting.

## Local data and safety

- One lazily started daemon runs per user data directory. State defaults to
  `~/.portless`; set `PORTLESS_HOME` to isolate development and tests.
- Private directories, authentication records, and sessions are
  ownership-checked and protected before use.
- The machine-wide relay binds only the documented loopback HTTP and DNS
  listeners, verifies that its runtime user and private sockets exactly match
  its ownership receipt, and drops to the installing user before serving
  traffic. Privileged install, repair, restart, and removal are serialized by
  a fixed root-owned lifecycle lock; activation commits only after HTTP, DNS,
  and system-resolver readiness succeeds. The receipt binds the exact installed
  helper hash for integrity, while a relay-owned semantic helper version
  determines compatibility. Rebuilding unrelated Portless code does not
  require reinstalling the helper.
- Discovery is static, bounded, read-only, and confined to the supplied
  checkout. It does not run project code or fetch network resources.
- Locally launched project code is trusted at the developer's user-account
  level. Portless does not sandbox those processes or isolate them from the
  developer environment because they must behave like directly launched code
  for debugging.
- Secret-bearing provider values remain separate from redacted configuration
  inspection data. Authorization, cookie, common token headers, and
  unambiguously identified protocol-authentication fields are redacted before
  retention. Captured HTTP and decoded TCP application payloads remain local.
- Runtime adoption, forced lifecycle actions, reset, and uninstall fail closed
  when process or container ownership cannot be proven.

`portless reset` and `portless uninstall` are preview-first; `--force` is a
guarded recovery path, not permission to kill an unverified process or remove
an unowned resource. Review their exact behavior in
[Reset and uninstall](portless-cli/COMMANDS.md#reset-and-uninstall).

The daemon's ownership, relay, reconciliation, and API boundaries are covered
in the [daemon README](portless-daemon/README.md) and the
[single-daemon decision](docs/architecture/decisions/0001-single-local-daemon.md).

## Repository structure

Portless is one Go module and one distributed executable, with six product
roots:

| Product | Responsibility | Documentation |
| --- | --- | --- |
| `portless-cli` | Commands, selection, output, confirmation, completion, and browser launching. | [CLI README](portless-cli/README.md), [command reference](portless-cli/COMMANDS.md) |
| `portless-daemon` | API, control plane, discovery, state, runtimes, traffic, and embedded UI serving. | [Daemon README](portless-daemon/README.md), [OpenAPI](portless-daemon/api/openapi.yaml), [events](portless-daemon/api/events.md) |
| `portless-relay` | Narrow machine-wide HTTP and DNS relay, installation, health, restart, and removal. | [Relay README](portless-relay/README.md) |
| `portless-web` | React control plane embedded in the executable. | [Embedded assets](portless-web/embedded-assets.md) |
| `portless-site` | Static marketing site published separately at [www.portless.run](https://www.portless.run). | [Website README](portless-site/README.md) |
| `portless-mcp` | Local stdio MCP runtime, scope and capability policy, redaction, and result limits. | [MCP README](portless-mcp/README.md) |

Package ownership and dependency direction are defined in the
[product structure](docs/plans/package-structure-refactor.md) and enforced by
`tests/architecture`.

## Build and test

Building Portless requires Go 1.26 or newer, Node.js 22.12 or newer, and npm.
From the repository root:

```bash
make
make lint
make test
make coverage
```

`make` installs locked frontend dependencies when needed, builds the embedded
React control plane, and writes `bin/portless`. `make test` type-checks and
tests both web projects, builds their production assets, and runs all Go tests.
`make lint` checks Go formatting and vet diagnostics, runs pinned Staticcheck
and actionlint versions, lints the React/TypeScript sources with Oxlint and
React Hooks rules, and checks repository shell scripts with ShellCheck.
ShellCheck must be installed locally; the remaining lint tools are installed
from their locked project or Makefile versions.
`make coverage` runs the same non-destructive validation while writing a
summary, raw profiles, and browsable HTML reports under `coverage/`. CI adds
the summary to the workflow run and retains the complete report as an artifact.
Use `make test-go`, `make test-web`, or `make test-site` for a narrower suite.

The ordinary CLI and browser E2E suites use compiled binaries and isolated
Portless homes:

```bash
make install-e2e-browser
make test-e2e
```

CI runs the CLI and Chromium suites as separate required jobs so a failure is
attributed to the owning boundary without delaying the unit and release lanes.

Read the [E2E testing guide](docs/e2e-testing.md) before changing or running
those suites. Machine-level relay suites are intentionally excluded from this
quick path and require separate explicit authorization.

Additional contributor references:

- [Implementation status and release gates](docs/implementation-status.md)
- [Architecture decisions](docs/architecture/decisions/)
- [Public website development](portless-site/README.md)
- [Release process](docs/releasing.md)
- [API contract](portless-daemon/api/openapi.yaml)
- [Live event contract](portless-daemon/api/events.md)

## License

Portless is licensed under the
[Apache License 2.0](LICENSE.md).
