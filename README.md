<p align="center">
  <a href="https://www.portless.run">
    <img src="brand/logo/portless-logo-dark-1200x300.png" alt="Portless" width="600">
  </a>
</p>

<p align="left">
  <a href="https://github.com/runportless/portless/actions/workflows/ci.yml">
    <img src="https://github.com/runportless/portless/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI status">
  </a>
</p>

[Portless](https://www.portless.run) runs your development environment with
stable URLs and a single place to see what your services are doing. Start with
`portless up` from a checkout. Portless discovers your application, starts its
services and dependencies, and opens a browser dashboard.

An application can span one repository or several. Each environment can mix
services running from local code, managed containers, remote HTTP endpoints,
and mocks. No account is required.

- **Keep the same URLs** as services restart or switch providers.
- **See requests across services**, alongside logs, database queries, and cache
  commands.
- **Reproduce problems** with traffic recordings and HTTP request replay.
- **Test failure cases** with scoped delays, errors, and mock responses.

Portless is in active development and does not yet have a stable public release.
See [current support and limitations](docs/implementation-status.md).

## Install

### Homebrew (macOS)

Install the latest alpha release from the project tap:

```bash
brew install runportless/tap/portless
```

Use the full `runportless/tap/portless` name to select this project's formula.
You can also [build from source](#build-from-source).

### Build from source

Portless targets macOS and systemd-based Linux on AMD64 and ARM64. Linux setup
requires `systemd-resolved`.

Build from source with Go 1.26 or newer, Node.js 22.12 or newer, npm, and Make:

```bash
git clone https://github.com/runportless/portless.git
cd portless
make install
```

The executable is installed into `GOBIN`, or `$(go env GOPATH)/bin` when
`GOBIN` is unset. Ensure that directory is on your `PATH`.

### One-time setup

After installing, configure local networking once per machine:

```bash
portless setup
```

Setup may request administrator approval to configure the local HTTP and DNS
relay that provides clean service addresses. Your application services run under
your user account.

Docker Engine or Podman is needed only when your environment uses managed
container resources.

## Quick start

Install your application's dependencies as you normally would, then run
Portless from its checkout:

```bash
cd /path/to/billing
portless up
```

For a checkout named `billing`, Portless creates the `billing/local`
environment, starts the discovered services, and opens its dashboard. Use the
dashboard to open a service, inspect its dependencies, or follow traffic.

Services get readable addresses that stay the same across restarts:

```text
http://checkout.local.billing.localhost
http://orders.local.billing.localhost
orders-postgres.local.billing.portless.test:5432
```

A **project** is your application. An **environment**, such as `billing/local`,
is an instance of that application with its own choice of providers and runtime
state. Portless manages the ports and dependency connections for you.

Built-in discovery supports Spring Boot, NestJS, Express, Fastify, Next.js,
Go HTTP/RPC services, and FastAPI. Managed resources include PostgreSQL, Valkey,
MySQL, and NATS. Discovery reads your project files without executing code.

To try Portless with a ready-made application, start with the
[Chat](examples/chat/README.md) or [Store](examples/store/README.md) example.

## Guides

[Run your first application](guides/run-application.md) walks through starting
Store, creating an order, and stopping and resuming the environment. The guides
use screenshots from the running application to show each workflow.

[![Portless showing Store's service readiness](guides/images/run-application/overview.jpg)](guides/README.md)

- [Debug checkout with VS Code](guides/debug-checkout-vscode.md): start debug
  mode in Portless, attach to the process, and inspect a live request.
- [Investigate a failed request](guides/investigate-failed-request.md): follow
  a rejected checkout through its dependency trace.
- [Mock a dependency](guides/mock-dependency.md): preview a response and test
  it through the real checkout page.

[Browse all guides](guides/README.md) for service management, database and cache
inspection, replay, faults, recordings, and multiple environments.

## Everyday commands

Run these from a checkout belonging to your project:

| Command | What it does |
| --- | --- |
| `portless up` | Start the environment and open its dashboard. |
| `portless status` | Show environment and service status. |
| `portless open [service]` | Open a service in the browser. |
| `portless url [service]` | Print a service's public address. |
| `portless ui` | Open the Portless dashboard. |
| `portless logs [service] --tail` | Follow logs for one service or the whole environment. |
| `portless down` | Stop the environment, preserving managed data volumes. |

Use `--env billing/local` to target a particular environment and `--json`
for structured output. `portless --help` and the
[command reference](portless-cli/COMMANDS.md) cover all commands and options.

For debugging, use `portless up --debug checkout`. Running `portless up` from
a registered service directory also enables its discovered Node or JVM debugger.

## Work with multiple repositories

Register related checkouts as sources of one project. For example, from your
`checkout-service` directory:

```bash
portless project create billing \
  --source checkout=. \
  --source orders=../orders-service \
  --source payments=../payments-service

portless env select billing/local
portless up
```

Portless discovers services across those sources and connects their dependencies.
See [projects and environments](portless-cli/COMMANDS.md#projects-and-environments)
for adding sources and choosing checkouts.

## Try a different environment

Clone an environment to change how selected services run. For example, use a
remote QA payment service while keeping the rest of the application local:

```bash
portless env clone qa-assisted --from local
portless --env billing/qa-assisted env bind payments \
  --remote https://payments.qa.example.com \
  --classification qa \
  --write-policy read-only \
  --health-path /health
portless --env billing/qa-assisted up
```

The clone's configuration is independent. When another environment is already
using the same Git checkout, Portless prepares a separate worktree automatically,
including current uncommitted files and installed dependencies. This requires
Git and an existing commit. Later edits stay in their respective checkouts;
`portless --env billing/qa-assisted env checkout list` shows the clone's paths.

Remote requests still appear in traffic inspection. A `read-only` binding
blocks mutating HTTP methods locally before they reach the remote service.

## Inspect traffic and reproduce problems

Open **Traffic** in the dashboard to follow requests across services, inspect
HTTP headers and bodies, and view decoded PostgreSQL, Redis/Valkey, MySQL, and
NATS operations. Select an HTTP exchange and choose **Replay** to edit a request,
send it to a ready environment in the same project, and compare responses.

Use **Recordings** to retain a session for later inspection, or **Faults** to
simulate latency and failures on a specific caller-to-service connection. For
example, delay calls from checkout to payments for one minute:

```bash
portless fault add slow-payments checkout:payments --latency 500 --duration 1m
```

WebSockets work through the same HTTP service addresses using `ws://`.
Traffic inspection captures the opening handshake; WebSocket messages are not
captured or replayed.

See the [traffic reference](portless-cli/COMMANDS.md#captured-traffic-and-traces)
and [recording and fault commands](portless-cli/COMMANDS.md#recordings-faults-and-mocks)
for filters, capture options, and replay limits.

## Mock a dependency

Use **Mocks** in the dashboard to define fixed HTTP responses. A partial mock
overrides matching requests while other requests reach the real service. For
example, make `POST /payments` return an error:

```bash
portless mock create payment-error --unmatched-requests forward
portless mock route set payment-error reject-payment \
  --service payments --method POST --path /payments --status 503
portless mock enable payment-error
```

Run `portless mock disable payment-error` to restore normal behavior.
For a full replacement, create a scenario without `--unmatched-requests forward`;
unmatched requests then return `501`. The dashboard's **Preview** lets you
check route matching without sending application traffic.

See [mock commands](portless-cli/COMMANDS.md#deterministic-mocks) for route
matching, scenarios spanning several services, and importing recordings.

## Examples

| Example | What you can try |
| --- | --- |
| [Chat](examples/chat/README.md) | Two Node.js services with live WebSockets. No database or container engine required. |
| [Store](examples/store/README.md) | A single checkout with Node.js, Spring Boot, PostgreSQL, and Valkey. |
| [Dispatch](examples/dispatch/README.md) | An application across three repositories, with mixed frameworks, MySQL, NATS, mocks, and a remote provider. |

Each example includes setup instructions and a walkthrough.

## MCP clients

Connect an MCP-compatible assistant through **Settings → MCP** in the
dashboard (`portless ui`). Choose the scope and permissions, then copy the
generated client configuration.

Inspection is enabled by default. Starting services, changing configuration,
accessing sensitive traffic, and replaying requests require explicit capability
selection. See the [MCP guide](portless-mcp/README.md) for configuration examples
and permissions.

## Local data and safety

Portless stores its state in `~/.portless` by default. Discovery only reads
files inside the supplied checkout; starting services runs your application
code with your user permissions.

Traffic capture stays local. Common credential headers are redacted, but
application payloads can still contain sensitive data. Recording payloads is
opt-in; review recordings before sharing them.

`portless down` keeps your managed data volumes. For removing stored state or
Portless itself, review the preview-first
[reset and uninstall commands](portless-cli/COMMANDS.md#reset-and-uninstall).

## More documentation

- [Command reference](portless-cli/COMMANDS.md)
- [MCP configuration](portless-mcp/README.md)
- [Current support and limitations](docs/implementation-status.md)
- [Contributing, builds, and repository structure](CONTRIBUTING.md)

## License

[Apache License 2.0](LICENSE.md).
