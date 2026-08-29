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

HTTP remote providers still pass through the source-aware proxy, so traffic,
recordings, and faults keep working. A read-only binding blocks mutating HTTP
methods locally before the request leaves the machine. Active provider changes
handoff only the selected service and roll back if the replacement cannot
become ready.

Local providers can point at a different Git worktree, container providers use
Portless-managed resources, and mock providers return fixed local HTTP
responses. See the decisions for
[live provider handoffs](docs/architecture/decisions/0006-live-provider-handoffs.md)
and [deterministic mocks](docs/architecture/decisions/0007-deterministic-http-mock-provider.md).

### Observe and experiment

The embedded control plane and CLI share the same authenticated daemon API.
Both can inspect services and effective connections, tail structured logs,
follow raw exchanges and correlated traces, retain bounded recordings, apply
edge-scoped faults, and configure deterministic mocks.

The control plane's daemon drawer groups identity, build provenance,
control-plane health, recovery, managed runtime inventory, local networking,
handoff safety, and retained storage into focused Status, Runtime, and Storage
tabs. Storage inspection is loaded only when requested. A separate Logs tab
exposes a live, bounded tail of the fixed private daemon log; older output is
explicitly marked when omitted, and known installation authentication and
ownership secrets are redacted again at the inspection boundary.

Normal CLI and browser daemon restarts share a five-second end-to-end readiness
deadline and ordinarily complete in under two seconds. The accepted restart
receipt identifies the coalesced handoff, target build, and deadline; the
daemon drawer reports the last measured duration and whether it met the SLA.
Forced or legacy recovery is deliberately outside that SLA because it may have
to signal an unresponsive process and can interrupt active environments.

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
housekeeping spans are excluded.
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
| `portless-relay` | Narrow machine-wide HTTP and DNS relay, installation, health, restart, and removal. | [Source](portless-relay/) |
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
