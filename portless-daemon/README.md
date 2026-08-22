# Portless daemon

`portless-daemon` owns the unprivileged, per-user control plane behind
Portless. It discovers application topology, persists project and environment
state, coordinates local processes and managed containers, publishes private
ingress, captures traffic, and serves the HTTP API and embedded web control
plane.

The daemon is part of the repository's single Go module and the distributed
`portless` executable. It is not a separately installed binary or a hosted
service. Normal CLI and browser workflows start or reconnect to it through the
daemon lifecycle controller.

## Product boundary

The daemon owns:

- project discovery, compilation, and environment state;
- process, container, resource, debugger, and supervisor coordination;
- service ingress and source-aware dependency proxies;
- traffic, traces, recordings, faults, deterministic mocks, logs, and live
  events;
- durable state, runtime ownership, reconciliation, and safe handoff; and
- the authenticated daemon API and embedded browser control plane.

Adjacent products keep separate responsibilities:

- `portless-cli` owns commands, output, confirmation, and current-checkout
  selection.
- `portless-relay` owns the narrow machine-wide HTTP and DNS privilege
  boundary. It forwards traffic to the daemon's private sockets but does not
  own discovery, orchestration, or state.
- `portless-web` owns the React application whose built assets the daemon
  embeds.
- `portless-mcp` is a local stdio adapter over the typed daemon API; it is not
  another daemon protocol.

## Package map

| Path | Responsibility |
| --- | --- |
| `daemon.go`, `command.go` | Compose the daemon and expose its private process modes through the shared executable. |
| `api/contract` | Stable wire types, error envelopes, and the semantic API version. |
| `api/client` | Typed authenticated HTTP client used by native consumers. |
| `api/server` | HTTP routing and adapters from the wire contract to injected daemon capabilities. |
| `control` | Out-of-process inspection, serialized startup, replacement, shutdown, reset, and uninstall coordination. |
| `controlplane` | Application behavior for projects, environments, lifecycle operations, provider changes, observability, and reconciliation. |
| `database` | SQLite persistence for topology, operations, ownership, runtime state, traffic artifacts, mocks, and faults. |
| `projects` | Project compilation and bounded, root-confined static discovery. |
| `providers` | Managed-resource plugin contracts, bounded discovery, declarative container plans, and safe bindings. |
| `runtime` | Process supervisors, containers, debugging, health checks, and log storage. |
| `networking`, `dns` | Stable endpoint allocation and authoritative `portless.test` DNS data. |
| `traffic`, `mocks` | Source-aware proxies, traffic and trace capture, and deterministic HTTP responses. |
| `events` | Bounded, nonblocking environment event publication. |
| `auth`, `identity`, `lifecycle` | Local authentication, private daemon identity, and guarded replacement or shutdown. |
| `system` | Standard-library installation layout and operating-system integration helpers. |

Dependency direction is enforced by `tests/architecture`. In particular,
feature packages do not import the CLI, relay implementation, API server, or
daemon composition root. The API client depends only on the API contract, and
the server receives control-plane and lifecycle behavior through injected
capabilities.

## Runtime model

```text
CLI / browser / MCP
        |
        v
authenticated daemon API
        |
        v
control plane -> database + runtimes + source-aware proxies
        |
        v
private HTTP/DNS sockets
        |
        v
privileged relay -> clean HTTP URLs and portless.test endpoints
```

The lifecycle controller starts one daemon per user data directory behind a
startup lock. On startup, the daemon protects its private state, authenticates
its identity, opens the database, and reconciles every recorded runtime before
publishing ingress. Application processes run below authenticated supervisors,
and managed containers carry installation and environment ownership labels.

A daemon replacement adopts only runtimes, listener ports, and proxies whose
ownership can be proven. Ambiguous state becomes `unknown`, and Portless
refuses to launch a duplicate or signal an unverified process. A normal
restart can therefore keep an environment running; a forced replacement is an
explicit recovery action and is not routine development workflow.

The privileged relay remains deliberately narrow. It binds the documented
machine-wide loopback listeners, verifies its ownership receipt, drops to the
installing user, and forwards bytes to the daemon. All application-aware
behavior remains here in the unprivileged daemon.

## API and event contracts

The daemon's public wire boundary lives under `api`:

- [`api/openapi.yaml`](api/openapi.yaml) specifies the authenticated HTTP API.
- [`api/events.md`](api/events.md) specifies the Server-Sent Events boundary.
- `api/contract` owns the Go wire types.
- `api/client` owns native HTTP transport.
- `api/server` adapts requests to daemon capabilities.

For a contract change, update the contract first, then the typed client,
server adapters and behavior, CLI and web consumers, OpenAPI or event
documentation, and the relevant tests. Increment the semantic API version
deliberately when the compatibility contract changes.

Public API values use readable project, environment, service, source,
recording, fault, and mock names. Runtime ownership keys, authentication
material, private ports, process identifiers, and container identities remain
implementation details. Inspection responses must never expose secrets;
providers keep secret-bearing runtime values separate from redacted safe
values.

## Safety invariants

- Discovery is static, bounded, read-only, and confined to its supplied
  workspace root. It does not execute project code or inspect arbitrary host
  paths.
- Source-to-target dependency edges are preserved. Routing, traffic,
  recordings, and faults depend on caller identity.
- Remote providers require explicit classification and write policy. Read-only
  policy is enforced locally before a request leaves the machine.
- Runtime adoption and destructive cleanup fail closed unless exact ownership
  is proven.
- Request handlers keep contexts and timeouts on I/O, process, network, and
  persistence boundaries; they do not start unbounded background work.
- Secrets are redacted before they reach API responses, logs, retained traffic,
  errors, or tests.

## Development

Run supported workflows from the repository root:

```bash
make                         # build the web assets and bin/portless
make test-go                 # run the complete Go suite
make test                    # validate web projects and run all Go tests
go test ./portless-daemon/...
go test ./portless-daemon/controlplane
go test ./portless-daemon/api/server
go test ./tests/architecture
```

Use the complete executable for manual daemon checks. Do not invoke the private
`__daemon` or supervised-runner modes directly.

```bash
make
./bin/portless daemon status
./bin/portless doctor daemon
./bin/portless daemon restart
```

When testing a source change against an already running daemon, rebuild the
complete executable and restart with that exact checkout. Inspect daemon status
before any forced replacement; `daemon restart --force` can interrupt active
environments and is not a routine fallback.

Ordinary end-to-end suites use isolated Portless homes and compiled binaries.
Read [`docs/e2e-testing.md`](../docs/e2e-testing.md) before changing or running
them. The relay-destructive suites alter machine-level networking and require
separate explicit authorization.

## Further reading

- [Repository overview](../README.md)
- [Single local daemon decision](../docs/architecture/decisions/0001-single-local-daemon.md)
- [Public names and private ownership keys](../docs/architecture/decisions/0002-names-public-keys-private.md)
- [Source-aware edge proxy decision](../docs/architecture/decisions/0003-edge-proxy.md)
- [Package ownership and dependency direction](../docs/plans/package-structure-refactor.md)
- [MCP boundary](../docs/mcp.md)
