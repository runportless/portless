# Product structure refactor

Status: implemented on 2026-08-17.

Portless is organized around the five things a contributor can identify in the
running product: the CLI, daemon, privileged relay, web control plane, and
client-launched MCP runtime. The repository no longer puts the implementation
below a generic `internal` tree,
and it does not pretend that the daemon's HTTP API is a separate product.

## Decisions

- Use five explicit top-level product roots: `portless-cli`,
  `portless-daemon`, `portless-relay`, `portless-web`, and `portless-mcp`.
- Keep one Go module and one distributable `portless` executable. These are
  source ownership boundaries, not separate repositories or release units.
- Put the HTTP API contract, client, and server in `portless-daemon/api`. The
  API is the daemon's public boundary; it has no useful lifecycle by itself.
- Let the CLI import the daemon API client and contract. Ordinary commands do
  not import daemon feature implementations.
- Keep lifecycle control separate from feature control. The CLI may start,
  inspect, replace, stop, or uninstall the daemon through
  `portless-daemon/control`, even when the daemon API is unavailable.
- Keep relay installation and relay runtime code together in
  `portless-relay`. They are one small privileged product with a single public
  facade.
- Keep generated frontend assets in `portless-web/dist` and embed them from
  the `portless-web` Go package.
- Keep the client-launched stdio MCP runtime in `portless-mcp`. The CLI owns
  command construction and streams; MCP uses only the typed daemon API and is
  not independently distributed.
- Do not retain compatibility packages or forwarding imports. Portless is
  greenfield, so all callers move to the new contracts at once.

## Final layout

```text
portless-cli/
  cmd/portless/             executable entry point
  command/                  shared execution context, selection, output, and UX primitives
  environment/              up, down, status, open, URL, and UI commands
  projects/                 projects, sources, environments, and bindings
  observe/                  logs, services, connections, and timeline
  traffic/                  traffic inspection, recordings, and faults
  administration/           daemon, relay, runtime, doctor, config, MCP, reset, and uninstall
  doctor/                   read-only installation diagnostics engine
  app.go                    dependency composition
  commands.go               Cobra root and global execution policy

portless-daemon/
  api/
    contract/               wire types and API version
    client/                 authenticated typed HTTP client
    server/                 authenticated handlers and UI fallback
    openapi.yaml
    events.md
  control/                  out-of-process daemon lifecycle manager
  controlplane/             application orchestration and use cases
  database/                 SQLite state and persistence
  identity/                 private daemon discovery record
  lifecycle/                identity, shutdown, and replacement protocol
  projects/                 discovery and project compilation
  providers/                managed dependency provider registry
  runtime/                  process, debugger, container, and log runtimes
  traffic/                  application proxies and traffic behavior
  system/installation/      installation layout and guarded state removal
  auth/ dns/ events/ model/ networking/
  daemon.go                 daemon composition root
  command.go                daemon and supervised-runner process facade

portless-relay/
  *.go                      relay runtime, DNS, health, install, and removal

portless-web/
  src/                      React control plane
  e2e/                      Playwright tests
  public/                   static source assets
  dist/                     generated embedded assets
  assets.go                 Go embed facade consumed by the daemon

portless-mcp/
  mcp.go                    injected serving facade and stdio transport
  server.go                 protocol server, limits, and tool registry
  scope.go                  immutable workspace, pinned, or installation scope
  *_tools.go                typed inspection and explicitly gated mutation tools
  results.go                MCP-owned safe result mapping and size caps

tests/
  architecture/             import and source-layout guardrails
  e2e/                      isolated CLI end-to-end suite
  relay_e2e/                explicitly destructive machine relay suite
```

The repository also keeps `docs`, `examples`, and `tests` at the top level.
Those are cross-product support areas rather than running products.

## Ownership by product

### `portless-cli`

Owns command discovery, Cobra construction, current-directory and selected
environment resolution, human and JSON rendering, color preferences, browser
launching, and user confirmations. The root package only composes the CLI.
Shared command mechanics live in `command`; user-facing behavior is split into
`environment`, `projects`, `observe`, `traffic`, and `administration`. Feature
packages do not import one another. They share the execution context and call
the typed daemon API.

Tests follow the same ownership boundaries. Feature command and rendering tests
live beside `environment`, `projects`, `observe`, `traffic`, `administration`,
or `command`. The CLI root retains only composition, global execution-policy,
and complete command-tree contract tests; it has no compatibility test facade.

The executable entry point imports only the three Go product facades:
`portless-cli`, `portless-daemon`, and `portless-relay`. Hidden daemon, relay,
and supervised-service process modes are dispatched there but implemented by
the product that owns them.

### `portless-daemon`

Owns the environment model and every unprivileged long-lived capability:
discovery, project and environment state, process/container execution,
debugging, topology, application proxies, traffic capture, recordings, faults,
logs, and the UI/API server.

`portless-daemon/api` is intentionally nested here:

- `contract` defines stable wire values and error envelopes;
- `client` is the only place that performs daemon HTTP transport for the CLI;
- `server` adapts HTTP requests to the control plane;
- the OpenAPI and event documents live beside the implementation they specify.

`portless-daemon/control` is different from the API client. It operates when a
daemon may not exist or may be stale, so it owns authenticated instance
inspection, serialized startup, safe replacement, shutdown, reset, and
uninstall coordination.

### `portless-relay`

Owns the narrow machine-wide process that accepts clean HTTP traffic and serves
the scoped TCP endpoint DNS zone. The same product owns its root-service
installation, ownership receipt, platform configuration, health probes,
restart, and removal. There is no nested generic `install` package or separate
relay command implementation.

### `portless-web`

Owns the React/TypeScript control plane, frontend unit tests, Playwright suite,
and built assets. `npm --prefix portless-web run build` writes to
`portless-web/dist`; `assets.go` embeds that directory into the Go executable.
The browser uses the same daemon HTTP API as the CLI.

### `portless-mcp`

Owns the long-lived stdio MCP runtime launched by `portless mcp serve`, its
immutable startup scope and capability gates, tool schemas, concurrency/rate
limits, redaction, and MCP result mapping. Only
`portless-cli/administration` imports its facade. It calls
`portless-daemon/api/client` and uses `portless-daemon/api/contract`; it does
not import the CLI, daemon server, control plane, database, runtimes, or relay.

## Dependency rules

```text
portless-cli/cmd/portless
  -> portless-cli
  -> portless-daemon       hidden daemon and service-runner modes
  -> portless-relay        hidden privileged relay modes

portless-cli
  -> portless-daemon/api/client + contract
  -> portless-daemon/control
  -> portless-daemon identity + model
  -> portless-daemon project discovery      current-checkout resolution
  -> portless-daemon container probes       read-only doctor checks
  -> portless-daemon/system/installation
  -> portless-relay
  -> portless-mcp                           administration adapter only

portless-mcp
  -> portless-daemon/api/client + contract
  -> official Go MCP SDK

portless-daemon
  -> portless-daemon/api/server
  -> portless-daemon/controlplane
  -> daemon feature packages
  -> portless-web

portless-daemon/api/server
  -> contract + control-plane interfaces and API adapters

portless-daemon/api/client
  -> contract only

portless-relay
  -> low-level daemon DNS/network value packages only
```

Forbidden directions are enforced in `tests/architecture`:

- no Portless import may point at a generic `internal` tree;
- the CLI cannot import the API server, control-plane implementation, or
  database;
- the CLI composition root contains no feature implementation, and CLI feature
  packages cannot import one another;
- daemon features cannot import CLI or process-adapter products;
- the API client cannot import anything except the API contract;
- the API server receives lifecycle and relay control through interfaces;
- the relay cannot import CLI or daemon control-plane implementations;
- only CLI administration may import `portless-mcp`, and `portless-mcp` may
  import only the daemon API client/contract plus the official MCP SDK;
- the official MCP SDK cannot leak into another product;
- installation safety primitives use only the Go standard library;
- the executable entry point imports product facades, not implementation
  subpackages.

## Migration completed

| Previous location | Product location |
| --- | --- |
| `cmd/portless` | `portless-cli/cmd/portless` |
| `internal/cli` | `portless-cli` |
| `internal/diagnostics` | `portless-cli/doctor` |
| `internal/api` and root `api` documents | `portless-daemon/api` |
| `internal/daemon` | `portless-daemon` |
| `internal/application` | `portless-daemon/controlplane` |
| `internal/store` | `portless-daemon/database` |
| `internal/project` | `portless-daemon/projects` |
| `internal/resource` | `portless-daemon/providers` |
| `internal/proxy` | `portless-daemon/traffic/proxy` |
| daemon support packages under `internal` | matching `portless-daemon` areas |
| `internal/relay` and its install package | `portless-relay` |
| `web` and `webui` | `portless-web` |

Package declarations were renamed with their ownership. Callers now use
`controlplane`, `database`, `providers`, `identity`, and `doctor`; the refactor
does not preserve misleading aliases such as `application`, `store`,
`resource`, `instance`, or `diagnostics`.

## Build and test integration

The root `Makefile` remains the single build entry point:

```bash
make
```

It installs locked frontend dependencies when needed, builds
`portless-web/dist`, and compiles `portless-cli/cmd/portless` to
`bin/portless`. Unit, CLI E2E, UI E2E, and relay E2E targets all use the new
paths. The relay E2E suite remains separate because it intentionally modifies
the machine-wide privileged installation.

The refactor is complete when all of the following remain true:

```bash
make test
make test-e2e-cli
make test-e2e-ui
```

The destructive relay suite can be run separately on a prepared development
machine:

```bash
make test-e2e-relay-destructive
```

## Placement rule for future code

Start with the product that owns the behavior, then choose a domain name inside
that product. A Cobra concern belongs in `portless-cli`; an environment or
runtime concern belongs in `portless-daemon`; machine-wide clean-ingress work
belongs in `portless-relay`; browser presentation belongs in `portless-web`;
MCP protocol, capability, and result-adapter behavior belongs in
`portless-mcp`. Create a new top-level product only if it has an independent
runtime and a clear user-facing lifecycle. Do not recreate a generic
`internal`, `pkg`, `common`, or standalone `api` dumping ground.
