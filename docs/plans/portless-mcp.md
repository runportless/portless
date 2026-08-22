# Portless MCP integration plan

Status: initial tools implementation completed on 2026-08-17; optional MCP
resources, subscriptions, and Tasks remain deferred as described below.

## Executive decision

Add an MCP server to the existing `portless` executable as:

```text
portless mcp serve
```

The implementation belongs to a new top-level `portless-mcp` product. It is the
fifth product boundary in the repository and is consumed by `portless-cli`;
the CLI owns Cobra command construction and injects its daemon connector,
workspace context, streams, and build version into the MCP product. MCP remains
an adapter over `portless-daemon/api/client`, not a new daemon API or a second
control plane. The first release uses stdio only, defaults to read-only access
scoped to the current workspace, and does not expose reset, uninstall, relay
administration, volume deletion, arbitrary filesystem access, arbitrary URLs,
or command execution.

Use the official Go MCP SDK. As of this plan, `v1.7.0` supports the current MCP
protocol revision (`2026-07-28`) and older client revisions while requiring Go
1.25; that is compatible with Portless's Go 1.26 baseline. Recheck the latest
stable SDK release immediately before implementation and pin the selected
version in `go.mod` and `go.sum`.

This shape preserves the important trust boundary:

```text
LLM host
  -> launches `portless mcp serve`
  -> MCP over stdin/stdout
  -> portless-cli/administration command adapter
  -> portless-mcp
  -> authenticated portless-daemon/api/client
  -> loopback daemon HTTP API
  -> control plane
```

The MCP host never receives the daemon bearer token. The child `portless`
process reads and uses that mode-0600 installation secret through the existing
daemon control and typed-client boundary.

## Goals

- Let an LLM inspect an existing Portless project, environment, topology,
  services, effective configuration, connections, logs, traffic, recordings,
  faults, operations, and timeline using typed schemas.
- Support the core diagnostic loop: identify the current environment, inspect
  unhealthy services and their dependencies, correlate logs and traffic, and
  report concrete remediation.
- Add explicitly enabled operational tools for environment/service lifecycle,
  bounded recordings, and bounded fault injection.
- Preserve Portless public identities. MCP inputs use readable
  `project/environment`, service, connection, recording, and fault names; they
  never expose or accept private ownership keys.
- Preserve daemon-side policy and ownership checks. MCP must not reimplement or
  bypass remote write policy, process/container ownership verification,
  operation conflict handling, or secret redaction.
- Return machine-usable structured results and concise text fallbacks so both
  modern and older MCP clients work well.
- Keep reads and tool requests bounded and cancellable, and keep accepted
  mutations durable, attributable, and testable.

## Non-goals for the first release

- A standalone `portless-mcp` binary or another release artifact.
- A Streamable HTTP MCP endpoint, OAuth server, network listener, hosted control
  plane, or remote Portless access.
- Project creation, discovery, source-path changes, environment cloning,
  provider binding changes, project/environment forgetting, runtime selection,
  daemon restart/stop, relay operations, reset, uninstall, or volume removal.
- Arbitrary shell execution, arbitrary HTTP requests, SQL execution, container
  exec, process signaling, or reading files from source checkouts.
- Exporting complete recordings through MCP.
- MCP prompts, sampling, roots, protocol logging, or MCP Apps. Roots, sampling,
  and logging are deprecated in the current MCP revision and are unnecessary
  for the Portless server.
- Depending on the experimental MCP Tasks extension in the first release.
  Portless operation numbers provide durable polling until Tasks and its Go SDK
  support are stable enough to justify another dependency boundary.
- Treating an MCP client's confirmation UI as a security boundary. Server-side
  scope and capability checks remain authoritative.

## Product and package ownership

### Product layout

Create the following top-level product:

```text
portless-mcp/
  mcp.go                    package facade and exported Serve contract
  config.go                 startup scope and capability policy
  connector.go              injected typed daemon connection boundary
  server.go                 MCP SDK setup and server lifecycle
  registry.go               deterministic tool registration
  gateway.go                typed daemon connection and retry policy
  scope.go                  workspace/pinned/all environment authorization
  policy.go                 tool capability and sensitive-data checks
  limits.go                 concurrency, rate, input, and result limits
  results.go                structured result and error envelopes
  inspect_tools.go          environment/service/connection inspection
  observe_tools.go          logs, timeline, and operations
  traffic_tools.go          traffic, recordings, and fault inspection
  lifecycle_tools.go        explicitly enabled lifecycle mutations
  experiment_tools.go       explicitly enabled recording/fault mutations
  *_test.go

portless-cli/administration/
  mcp.go                    `mcp` and `mcp serve` Cobra adapter
  mcp_test.go               command, flags, stream, and composition tests
```

Use package name `portlessmcp` so callers import a product facade and imports of
the external SDK can use the clear alias `mcpsdk`.

Keep the CLI-facing surface deliberately small. The concrete names can be
adjusted during implementation, but the boundary should be equivalent to:

```go
type Connector interface {
	Connect(context.Context) (*client.Client, DaemonIdentity, error)
}

type DaemonIdentity struct {
	InstanceID string
}

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func Serve(context.Context, Config, Connector, Streams) error
```

`Config`, `DaemonIdentity`, and `Streams` are MCP-owned values. The connector
returns only the typed daemon client and the minimum stable identity needed to
detect a handoff; it must not expose the daemon token, private control record,
CLI context, or daemon controller. This keeps `portless-mcp` usable by the CLI
without making the MCP product depend on CLI or daemon-control internals.

`portless-cli/administration` constructs the `mcp` command, resolves the CLI's
startup workspace root and global `--env` selection, adapts
`command.DaemonController` to the MCP connector interface, and calls
`portlessmcp.Serve`. `portless-cli/commands.go` places the returned command in
the existing Administration group through the normal administration feature
composition. No MCP protocol, registry, tool, policy, DTO, or daemon-gateway
behavior belongs to the CLI product.

`portless-mcp` has an independent long-lived runtime—the client-launched stdio
server—and a clear lifecycle from `Serve` until client EOF/cancellation. That
runtime boundary is why it is a top-level product even though the repository
continues to distribute one `portless` executable rather than a separate
`portless-mcp` binary.

### Dependency rules

- `portless-cli/administration` may import the `portless-mcp` product facade and
  adapt shared CLI dependencies into its exported interfaces. No other CLI
  feature package imports `portless-mcp`.
- `portless-mcp` may import only `portless-daemon/api/client` and
  `portless-daemon/api/contract` from Portless production code. It receives
  daemon startup/reconnection and workspace-root behavior through injected
  interfaces and configuration rather than importing `portless-cli/command` or
  `portless-daemon/control`.
- `portless-mcp` must not import the CLI, daemon API server, control-plane
  implementation, database, model implementation, runtime implementations,
  relay, web product, or daemon composition root.
- It must not construct `/api/v1` paths or use the client's raw HTTP transport.
- The official MCP SDK should be imported only by the production
  `portless-mcp` product (and protocol/E2E tests); MCP wire types must not
  spread into the CLI, daemon domain packages, or daemon HTTP contract.
- Tool handlers should use small MCP-owned input/output DTOs. They may contain
  daemon contract values, but must not return raw internal implementation
  values or private keys.

Extend `tests/architecture` so `portless-mcp` is a required fifth product root,
all of its exported declarations have GoDoc, its only permitted Portless
dependency is the typed daemon API client/contract, daemon/relay/web products
cannot import it, and it is the only production package allowed to import
`github.com/modelcontextprotocol/go-sdk`. Preserve the existing CLI sibling
feature rule; the only CLI-to-MCP dependency is the administration adapter.

The enforced production dependency direction is:

```text
portless-cli/administration -> portless-mcp
portless-mcp -> portless-daemon/api/client
portless-mcp -> portless-daemon/api/contract
```

## Command contract

The public command is:

```text
portless mcp
portless mcp serve [flags]
```

Bare `portless mcp` displays help without starting or contacting the daemon.
Bare `portless mcp serve` starts the stdio server and writes no banner to
stdout. The server contacts or lazily starts the daemon only on the first tool
call that needs control-plane data.

Supported startup policy flags:

| Flag | Default | Effect |
| --- | --- | --- |
| global `--env project/environment` | unset | Pins every tool to exactly one named environment. |
| `--all-environments` | false | Explicitly permits access to every environment owned by this Portless installation. Mutually exclusive with `--env`. |
| `--allow-lifecycle` | false | Adds environment and service lifecycle mutation tools. |
| `--allow-traffic-control` | false | Adds bounded recording and fault mutation tools. |
| `--allow-sensitive-traffic` | false | Adds traffic-detail access that may contain headers, query values, or bodies. It does not enable mutations. |

When neither `--env` nor `--all-environments` is present, access is scoped to
the source root containing the server process's startup working directory. The
root is resolved once, using the existing bounded project-root discovery. The
set of environments that uses that root is refreshed through the typed daemon
API on every authorization decision.

Reject `--json` for `mcp serve`; MCP owns stdout and already uses JSON-RPC.
`--no-color` may be accepted but the MCP process always keeps protocol output
free of ANSI sequences. Syntax failures occur before the protocol starts and
go only to stderr.

Exit behavior:

- exit 0 after clean client EOF or context cancellation;
- exit 1 after server setup, transport, or unrecoverable protocol failure;
- retain the root CLI's exit 2 behavior for invalid command syntax.

## Protocol and SDK behavior

- Build the server with the official `github.com/modelcontextprotocol/go-sdk/mcp`
  package and `StdioTransport`.
- Advertise implementation name `portless`, title `Portless Local Control
  Plane`, and the Portless executable version.
- Let the SDK negotiate MCP protocol revisions. Do not hand-maintain an
  `initialize` compatibility adapter or JSON-RPC framing.
- Register tools before serving and keep their order deterministic. The tool
  list is fixed by startup flags and does not change as environment state
  changes.
- Use inferred JSON Schema 2020-12 input and output schemas from concrete Go
  structs, with explicit descriptions and `additionalProperties: false`.
- Return `structuredContent` conforming to each output schema and also return a
  concise JSON or summary text content block for clients that do not consume
  structured content.
- Put operational diagnostics on stderr only. Never write logs, startup text,
  or Cobra output to stdout after the stdio transport starts.
- Honor request cancellation. Cancellation stops waiting or reading, but it
  does not claim to cancel a Portless operation that the daemon has already
  accepted.
- Set standardized read-only, destructive, and idempotency annotations when
  supported by the negotiated client revision, but do not rely on annotations
  for authorization.
- Do not advertise resource, prompt, subscription, or Tasks capabilities in
  the first read-only release.

## Scope and authorization model

### Environment scopes

Every environment-scoped tool takes a required `environment` string in the
canonical `project/environment` form. There is no implicit "current" target for
mutations. This keeps host confirmation displays unambiguous and avoids a
working-directory change selecting another environment.

The server authorizes that selector on every call:

| Startup mode | Authorized selectors |
| --- | --- |
| default workspace | Environments whose source bindings contain the captured source root. |
| `--env p/e` | Only `p/e`, after loading it through the typed client. |
| `--all-environments` | Any environment returned by the current installation's bounded environment list. |

Names are parsed with the existing model/contract validation. A valid but
out-of-scope selector returns a structured `SCOPE_DENIED` tool error without
revealing whether some other Portless installation or user owns it.

`portless_list_environments` is the discovery entry point. Its result includes
the active scope mode, enabled capability categories, and only the environment
summaries visible in that scope. When a default workspace belongs to no
environment, it returns an empty list plus remediation to run `portless up` or
configure the MCP server with `--env`; it does not discover or create a project
itself.

### Capability categories

Capabilities are immutable for the lifetime of the stdio process:

| Category | Enabled by | Contents |
| --- | --- | --- |
| inspection | always | Bounded environment, service, connection, log, traffic-summary, recording, fault, operation, and timeline reads. |
| sensitive traffic | `--allow-sensitive-traffic` | Exact traffic detail that may include headers, query values, and captured bodies. |
| lifecycle | `--allow-lifecycle` | Environment up/down-without-volumes and service start/stop/restart/debug/manage. |
| traffic control | `--allow-traffic-control` | Start/stop bounded recordings and create/disable bounded faults. |

Disabled tools are omitted from `tools/list`; handlers also recheck the policy
so a crafted `tools/call` cannot bypass registration. Enabling a mutation
category does not widen the environment scope, and `--all-environments` does
not enable mutations.

## Tool contract

### Common conventions

- Tool names are lowercase ASCII with underscores and a `portless_` prefix to
  reduce collisions in clients that aggregate multiple servers.
- All names are readable public names. No input accepts a private database key,
  container name, supervisor socket, daemon token, or PID.
- List inputs use `limit` with conservative MCP defaults below the daemon API
  maxima. Invalid or excessive limits fail instead of silently widening.
- Array fields are always non-null in results.
- Timestamps are RFC3339 values inherited from the daemon contract.
- Environment-scoped results repeat `project` and `environment` so a model can
  verify the target after every call.
- Diagnostic text from logs, traffic, timelines, and application errors is
  labeled as untrusted application data in tool descriptions and result
  metadata. Handlers never interpret that text as instructions.

### Inspection tools

These tools are present in the default read-only server:

| Tool | Inputs | Result and behavior |
| --- | --- | --- |
| `portless_list_environments` | `limit` (default 100, max 500) | Active scope, enabled categories, visible environment summaries, and total visible count. |
| `portless_get_environment` | `environment` | Effective sources, provider bindings, topology, issues, service states, public endpoints, and revision. |
| `portless_get_service` | `environment`, `service` | One runtime service snapshot plus its incoming and outgoing effective connection summaries. |
| `portless_get_service_configuration` | `environment`, `service` | Effective command, working directory, health check, and only the API's masked or safe configuration values. |
| `portless_list_connections` | `environment`, `limit` (default 100, max 500) | Directed source-to-target connections with provider, state, public/proxy endpoint, and safe injected values. |
| `portless_get_connection` | `environment`, `source`, `target` | One exact directed edge. The source identity is never collapsed into a target-only lookup. |
| `portless_read_logs` | `environment`, optional `service`, optional `since`, `limit` (default 200, max 1,000) | Chronological structured entries. Each returned message is capped at 16 KiB with explicit MCP truncation metadata. |
| `portless_query_traffic` | `environment`, optional `protocol`, `service`, `edge`, `after`, `limit` (default 100, max 500) | Traffic summaries only. It excludes headers, bodies, exact request targets, and raw query strings, returning only the normalized path. |
| `portless_list_recordings` | `environment`, `limit` (default 100, max 500) | Recording metadata only; never exported events. |
| `portless_get_recording` | `environment`, `recording` | One recording's bounds, scope, state, and event count. |
| `portless_list_faults` | `environment`, `limit` (default 100, max 500) | Fault metadata, enabled state, expiry, effect, and match count. |
| `portless_get_fault` | `environment`, `fault` | One named fault rule. |
| `portless_list_operations` | `environment`, `limit` (default 50, max 100) | Recent durable operations and terminal/running state. |
| `portless_get_operation` | `environment`, `number` | One operation and ordered progress events. This is the polling primitive for long-running actions. |
| `portless_get_timeline` | `environment`, `limit` (default 100, max 500) | Newest-first durable, user-visible environment history. |

When `--allow-sensitive-traffic` is present, also register:

| Tool | Inputs | Result and behavior |
| --- | --- | --- |
| `portless_get_traffic_detail` | `environment`, `sequence` | One detailed exchange. Preserve daemon capture/truncation flags, cap each body at 64 KiB for the MCP response, cap aggregate headers, and add explicit MCP truncation fields. |

The traffic DTOs must be adapted to the typed API contract that is current when
this work begins. If the trace-first work in `docs/plans/traffic-tracing.md`
lands first, use its exchange and trace names directly through the typed client;
do not add an MCP-only compatibility facade for retired `TrafficEvent` shapes.

### Lifecycle tools

Register these only with `--allow-lifecycle`:

| Tool | Inputs | Result and constraints |
| --- | --- | --- |
| `portless_start_environment` | `environment`, optional `debugServices`, `managed`, `waitSeconds`, `idempotencyKey` | Starts an existing environment. It never performs bare-`up` discovery. Debug service names must be explicit. |
| `portless_stop_environment` | `environment`, `waitSeconds`, `idempotencyKey` | Stops exactly one environment with `removeVolumes=false`. No input can change that value. |
| `portless_change_service_state` | `environment`, `service`, `action`, `waitSeconds`, `idempotencyKey` | `action` is one of `start`, `stop`, `restart`, `debug`, or `manage`; the daemon remains authoritative for provider and debugger eligibility. |

`waitSeconds` defaults to 30, accepts 0 to return immediately, and is capped at
120. The result always contains the durable Portless operation. If waiting
expires while the operation is still running, return a successful structured
result with `timedOutWaiting: true` and tell the caller to use
`portless_get_operation`; do not report that the operation failed.

### Traffic-control tools

Register these only with `--allow-traffic-control`:

| Tool | Inputs | Result and constraints |
| --- | --- | --- |
| `portless_start_recording` | `environment`, `recording`, optional `source`, `target`, `durationSeconds`, `maxEvents` | Requires a finite duration from 1 second through 1 hour. Apply the daemon's event bounds. Body capture remains disabled in the first release. |
| `portless_stop_recording` | `environment`, `recording` | Stops capture but retains the named recording. |
| `portless_apply_fault` | `environment`, `fault`, `source`, `target`, effect fields, `durationSeconds`, optional method/path/probability | Requires a finite duration no longer than 1 hour and at least one effect. Reuse the CLI/API validation for probability, latency+jitter, and HTTP status ranges. |
| `portless_disable_fault` | `environment`, `fault` | Disables one existing rule. Enabling an existing indefinite rule is deliberately unavailable. |
| `portless_disable_all_faults` | `environment` | Atomically disables all active rules without deleting audit history. |

Do not expose recording deletion, fault deletion, indefinite MCP-created faults,
or recording export in the first release. These omissions make accidental data
loss and persistent test effects impossible through the MCP surface.

## Structured results and errors

Each successful tool has a concrete MCP-owned output struct. Let the official
SDK infer and validate its output schema. Return the value as structured
content and include one compact text block that either summarizes the outcome
or contains the same value as JSON for older clients.

Use a common execution-error shape:

```json
{
  "error": {
    "code": "SCOPE_DENIED",
    "message": "environment billing/prod is outside this MCP server's scope",
    "status": 403,
    "subject": {"environment": "billing/prod"},
    "remediation": []
  }
}
```

Error rules:

- Invalid JSON-RPC, unknown tools, and schema-invalid arguments are protocol
  errors handled by the SDK.
- Valid tool calls that fail validation, policy, or daemon business rules
  return `isError: true` with the structured error and an actionable text
  summary.
- Preserve `client.ClientError` code, status, subject, details, and remediation
  after filtering values that are not safe for inspection.
- Map context cancellation and deadlines to stable `CANCELLED` and `TIMEOUT`
  codes.
- Unexpected implementation errors return a generic `INTERNAL` result. Their
  detailed Go error goes to stderr, never to the model if it could contain a
  local path, token, request body, or process detail.
- Never byte-truncate encoded JSON. Apply documented per-field/per-list limits
  first. If a valid result would still exceed the 1 MiB MCP response budget,
  return `RESULT_TOO_LARGE` with instructions to reduce the limit or add a
  filter.

## Daemon connection, handoff, and retries

Expose an MCP-owned `Connector` interface that returns the typed API client and
only the daemon instance ID. The CLI administration adapter implements that
interface around its existing `command.DaemonController`; `portless-mcp` never
imports the CLI command context, `portless-daemon/control`, or the private
daemon identity record. Inside the MCP product, a `Gateway` wraps the connector
and typed client so tool tests remain independent of processes and HTTP while
the production path retains normal daemon lifecycle behavior.

- Capture startup scope without starting the daemon.
- Have the CLI adapter connect lazily using `Daemon.Connect` when the MCP
  product first requests a connection, preserving Portless's normal
  compatible-daemon startup/replacement behavior.
- Resolve and authorize the environment before invoking its operation.
- Use a per-call context with a 10-second default for snapshot reads and a
  bounded context for operation waiting.
- On a transport failure, reconnect and retry a read once only if daemon
  identity changed or the connection was demonstrably lost.
- Never blindly retry a mutation. Mutation retries use explicit idempotency as
  described below.
- A daemon handoff does not invalidate the MCP process. The next call reconnects
  through the injected connector and receives an updated typed client and
  instance ID.

## Mutation idempotency and attribution

Agent clients retry, users repeat prompts, and stdio responses can be lost. All
MCP-exposed asynchronous mutations therefore need a durable idempotency path.

Before adding mutation tools, make this coherent API change in contract-first
order:

1. Add an optional authenticated client-kind header contract with the fixed
   values `cli` and `mcp`. The typed client can return a shallow clone marked as
   the MCP caller without exposing its transport or token.
2. When a valid CLI bearer request carries the fixed MCP client kind, the API
   server maps its actor to exactly `MCP`. Do not accept arbitrary actor text or
   MCP client-supplied names in the durable timeline.
3. Document the header in OpenAPI and increment the daemon API minor version
   from whatever version is current when implementation begins.
4. Add/document `Idempotency-Key` on environment down and every asynchronous
   service action and plumb it through server capabilities and control-plane
   methods.
5. Extend operation persistence with a canonical request fingerprint covering
   operation type, target service, and behavior-changing inputs. The same
   environment/key/fingerprint returns the existing operation; the same key
   with a different fingerprint returns conflict. This closes the existing
   ambiguity where a key identifies an operation without proving that the
   retried request is equivalent.
6. Namespace MCP keys by tool/action before persistence and use the existing
   unique environment/idempotency index together with the new fingerprint.
   Handle concurrent unique-index races by loading and comparing the winning
   operation rather than surfacing an internal SQLite error.
7. Update the CLI callers and their tests at the same time; do not retain old
   service method signatures or compatibility wrappers.

An MCP mutation accepts an optional `idempotencyKey` of at most 120 visible
ASCII characters. The adapter prefixes it with the tool and target. If absent,
the adapter generates a cryptographically random key and returns the effective
caller key in the result so a model can reuse it. This guarantees retries after
a received response and internal reconnects. Full fetch-later recovery after a
lost first response is a later candidate for the MCP Tasks extension.

Named recording/fault mutations use the public artifact name as their natural
idempotency identity. If a create call finds an existing artifact, return it as
a replay only when its requested scope and bounds match; otherwise return a
conflict. Stop and disable actions should remain naturally idempotent.

MCP-originated operations and timeline changes must show actor `MCP`. Do not
append timeline events for every read; that would pollute the developer's
application history. Tool invocation diagnostics remain on the MCP process's
stderr, without tokens or returned application content.

## Data safety and prompt-injection controls

- The daemon API remains the source of truth for secret redaction. MCP never
  reads the database or provider secret-bearing runtime values directly.
- Service configuration exposes only values returned by the safe inspection
  API. Add regression tests containing generated credentials and discovered
  secret-looking values.
- Traffic summaries omit headers and bodies. Exact traffic detail is absent
  unless `--allow-sensitive-traffic` is set because traffic can contain
  credentials, cookies, tokens, personal data, and prompt-injection text.
- Recording export is not exposed. Recording metadata is safe to inspect; any
  later resource containing recorded exchanges must use the same sensitive
  traffic gate.
- Logs are available because they are essential to diagnosis, but their tool
  description and output metadata identify messages as untrusted application
  data. Bound each message and the overall response.
- Tool descriptions explicitly say that log, traffic, timeline, and remote
  error text is data, not instructions. Mutation handlers consume only their
  typed arguments and never derive actions by parsing a previous result.
- No tool input accepts a host filesystem path, executable, environment
  variable, raw daemon URL, arbitrary destination URL, PID, container ID, or
  shell fragment.
- Do not pass MCP client identity, model names, prompts, or free-form reasons
  into operation actors, timeline summaries, logs, or idempotency keys.
- Continue enforcing remote-provider classification and read-only policy in the
  daemon before traffic leaves the machine.

## Resource protection

The MCP SDK may dispatch tool calls concurrently, so add explicit per-process
limits:

- at most 8 concurrent tool handlers;
- token-bucket rate limit of 20 calls/second with a burst of 40;
- no more than 2 mutation submissions executing concurrently;
- documented list limits from the tool table;
- 1 MiB maximum encoded tool result;
- 16 KiB maximum returned log message;
- 64 KiB maximum returned request body and response body in sensitive traffic
  detail, with explicit truncation metadata;
- bounded aggregate header size in sensitive traffic detail;
- 10-second snapshot call timeout and at most 120 seconds of operation waiting.

Rate or concurrency rejection returns a retryable structured tool error. The
daemon remains authoritative for per-environment operation conflicts. Do not
create unbounded goroutines, queues, subscriptions, or caches in an MCP
handler.

## Optional resource phase

After the tool-based server is stable across supported hosts, add snapshot MCP
resources backed by the same handlers and policy checks:

```text
portless:///environments/{project}/{environment}
portless:///environments/{project}/{environment}/services/{service}
portless:///environments/{project}/{environment}/connections/{source}/{target}
portless:///environments/{project}/{environment}/recordings/{recording}
portless:///environments/{project}/{environment}/faults/{fault}
portless:///environments/{project}/{environment}/operations/{number}
```

- Percent-encode every public path segment and reject traversal or malformed
  encodings.
- Return `application/json`, mark cache scope private, and use environment
  revision/timestamps for freshness hints where available.
- Paginate `resources/list` and expose only resources inside the startup scope.
- Keep filtered logs and traffic as tools; they are queries, not stable named
  resources.
- Do not implement resource subscriptions in the first resource slice. A later
  slice may bridge the daemon's bounded environment SSE stream into current MCP
  `subscriptions/listen`, with snapshot-after-reconnect semantics matching
  `portless-daemon/api/events.md`.
- Do not add prompts merely to advertise a diagnostic workflow. First evaluate
  whether precise tool descriptions and normal host prompting are insufficient.

Resources and tools must call shared typed handlers so their authorization,
redaction, and size behavior cannot drift.

## Repository changes by boundary

### MCP product

- Add the top-level `portless-mcp` product with the files and responsibilities
  above.
- Export only its configuration, stream, connector, daemon-identity, and
  serving facade. Keep the SDK, registry, handlers, policy, limits, and result
  mapping private to the product.
- Add package documentation that defines it as a local stdio control-plane
  adapter consumed by the CLI, not an independently distributed executable or
  a daemon API.

### CLI product

- Add the thin adapter in `portless-cli/administration/mcp.go`. It owns Cobra
  flags and help, captures the startup working directory through the shared
  CLI dependency, adapts `command.DaemonController.Connect`, and calls
  `portlessmcp.Serve` with the CLI's streams and build version.
- Return the `mcp` command from `administration.Commands.RootCommands`; the CLI
  composition root continues only to mount the Administration group and must
  not gain MCP protocol or tool behavior.
- Add `portless mcp` and `portless mcp serve` to the audited bare-command
  behavior and full command-tree tests.
- Ensure completion/help never starts the daemon and `mcp serve` never uses
  normal human/JSON renderers after transport startup.

### Daemon API contract and client

- Add the fixed authenticated client-kind contract and MCP attribution.
- Add coherent idempotency headers for MCP-exposed asynchronous mutations.
- Update `contract` first, then typed client, server adapters, control plane,
  CLI callers, OpenAPI, and tests in the required order.
- Increment the semantic API version deliberately. No old method overloads,
  aliases, or alternate wire formats remain.
- Add any missing typed list-operation client method needed by
  `portless_list_operations`; MCP must not build its route.

### Daemon behavior

- Reuse the existing operation store, actor fields, timeline, safe service
  configuration, traffic summaries/details, and policy enforcement.
- Plumb idempotency into service lifecycle operations without moving MCP types
  into the control plane.
- Do not add an MCP route to `portless-daemon/api/server` and do not teach the
  daemon to speak JSON-RPC.

### Documentation

- Add a user guide at `portless-mcp/README.md` covering client configuration,
  default workspace scope, permission flags, tool inventory, data sensitivity,
  troubleshooting, and example diagnostic flows.
- Update `README.md` with the implemented capability and a generic MCP client
  configuration:

  ```json
  {
    "mcpServers": {
      "portless": {
        "command": "portless",
        "args": ["mcp", "serve"]
      }
    }
  }
  ```

- Document mutation-enabled configurations separately and make the read-only
  default visually obvious.
- Update `docs/implementation-status.md`, the package-structure plan, and
  `AGENTS.md` from four to five top-level products, documenting that
  `portless-mcp` is consumed by the CLI and depends only on the typed daemon API
  plus the official MCP SDK.
- Document the optional authenticated client-kind and idempotency headers in
  `portless-daemon/api/openapi.yaml`.

## Testing strategy

### MCP product unit tests

- Exact, deterministic tool inventory for every capability-flag combination.
- Generated input/output schemas reject extra fields, invalid enums, invalid
  selectors, partial edges, negative sequences, excessive limits, invalid
  durations, invalid fault effects, and overlong idempotency keys.
- Workspace, pinned, and all-environment scopes expose exactly the permitted
  selectors and revalidate after rename/forget/source-binding changes.
- A direct call to a disabled or out-of-scope handler fails even if it bypasses
  `tools/list`.
- Default startup and all inspection tools cannot stop/restart/reset the daemon
  or mutate environment, service, recording, or fault state. A first data call
  may lazily start or compatibly replace the per-user daemon through the normal
  `Daemon.Connect` contract.
- Sensitive traffic detail is absent by default and strips/caps sensitive
  fields exactly when enabled.
- Safe service configuration never reveals fixture secrets.
- Log/body/header and whole-result limits are explicit and preserve valid JSON.
- Rate limiting, concurrency bounds, context cancellation, and clean EOF do not
  leak goroutines.
- Read reconnect retries happen once; mutations are not retried without an
  idempotency key.
- Structured API errors retain safe codes/remediation; unexpected errors do not
  leak local paths or secrets.
- No startup, help, error, or log bytes contaminate MCP stdout.

### MCP protocol tests

Use the official SDK's in-memory or command transport as the test client:

- current `server/discover` negotiation and a legacy initialize negotiation;
- `tools/list` schemas and deterministic ordering;
- successful structured `tools/call` plus text fallback;
- invalid arguments become protocol errors and domain failures become tool
  execution errors;
- cancellation closes a waiting call while its accepted Portless operation
  remains pollable;
- server exits cleanly when the client closes stdin.

Do not snapshot raw JSON-RPC bytes produced by the SDK beyond Portless-owned
schema and result fields; that would couple tests to an SDK serialization
detail rather than the MCP contract.

### API and control-plane tests

- Bearer requests default to actor `CLI`; only the fixed authenticated client
  kind maps to actor `MCP`; browser sessions remain actor `UI`.
- Unknown client kinds and browser attempts to spoof MCP attribution are
  rejected or ignored according to the documented contract.
- Up, down, and every service action replay the same operation for the same
  namespaced idempotency key.
- Reusing a key for another operation type or target returns conflict.
- MCP-originated successful and failed mutations retain actor `MCP` in
  operations and durable timeline events.
- Existing CLI and browser behavior remains unchanged.

### CLI end-to-end test

Add an isolated `PORTLESS_HOME` MCP E2E test that launches the built executable
through the official SDK `CommandTransport`:

1. Start the MCP server in the store fixture checkout with no mutation flags.
2. Confirm its tool inventory is read-only and scoped to the fixture workspace.
3. Call environment, service, connection, configuration, log, traffic-summary,
   operation, and timeline tools and validate structured targets.
4. Attempt an out-of-scope selector and a disabled mutation.
5. Restart/replace the isolated daemon and confirm the same MCP process
   reconnects for the next read.
6. Launch a second server with `--allow-lifecycle`, perform one service restart
   with an idempotency key, replay it, wait for completion, and verify one
   operation with actor `MCP`.
7. Close the client and verify the MCP process exits without stopping the
   daemon or application services.

Keep this in the ordinary isolated CLI E2E suite. It requires no relay changes,
administrator approval, external network, Docker/Podman, or destructive relay
suite.

## Delivery sequence

### Slice 1: protocol and read-only foundation

1. Pin the official Go SDK and record its license/transitive dependency impact.
2. Add the top-level `portless-mcp` product, architecture guards, the thin CLI
   administration adapter, and the `mcp serve` stdio lifecycle.
3. Implement immutable startup policy, workspace/pinned/all scope resolution,
   common DTOs, limits, error mapping, and lazy typed-client gateway.
4. Register `portless_list_environments`, `portless_get_environment`, service,
   configuration, connection, operation, and timeline inspection tools.
5. Add package, command-tree, protocol, and focused E2E tests.

Ship criterion: a default MCP server is useful for topology/status diagnosis,
cannot mutate state, never leaks the bearer token, and emits only JSON-RPC on
stdout.

### Slice 2: bounded diagnostic data

1. Add bounded log and traffic-summary tools.
2. Add recording/fault inspection.
3. Add the separately gated traffic-detail tool with explicit sensitive-data
   and truncation behavior.
4. Reconcile its DTOs with the current trace/exchange API contract rather than
   preserving a retired shape.
5. Add adversarial secret, prompt-injection, oversized-data, and result-budget
   tests.

Ship criterion: the MCP server can explain common failures from topology,
configuration, logs, and traffic without silently returning unbounded or
sensitive detail.

### Slice 3: attribution and lifecycle operations

1. Add the contract-first authenticated client-kind change and API version
   increment.
2. Complete idempotency support for down and service actions across API client,
   server, control plane, store behavior, CLI callers, OpenAPI, and tests.
3. Register the three lifecycle tools only behind `--allow-lifecycle`.
4. Implement bounded wait/poll behavior and MCP actor attribution.
5. Extend the isolated E2E journey through daemon handoff and idempotent service
   restart.

Ship criterion: every MCP lifecycle mutation is explicitly enabled, precisely
scoped, durable, replay-safe, attributable as `MCP`, and incapable of removing
volumes.

### Slice 4: bounded traffic control

1. Register recording and fault mutation tools only behind
   `--allow-traffic-control`.
2. Enforce finite durations and natural-name idempotency/reconciliation.
3. Test expiry, replay, conflicts, disable-all, and daemon restart behavior.

Ship criterion: an agent can create a temporary reproduction or fault
experiment, but cannot create an indefinite effect or delete retained data.

### Slice 5: optional resources and live updates

1. Add the custom snapshot resource URIs using shared handlers.
2. Add private cache/freshness metadata and pagination.
3. Validate host support and usefulness before adding bounded resource update
   subscriptions over the existing daemon SSE boundary.
4. Evaluate the MCP Tasks extension for direct mapping to durable Portless
   operations only after the extension and official Go SDK API are stable.

This slice is not required to call the initial Portless MCP integration
complete.

## Validation before handoff

During implementation, run focused checks for each owning package, then the
complete non-destructive suite:

```bash
gofmt -w <changed Go files>
go test ./portless-mcp
go test ./portless-cli/administration
go test ./portless-daemon/api/...
go test ./portless-daemon/controlplane
go test ./tests/architecture
make test
make test-e2e-cli
git diff --check
```

The MCP work does not justify running relay installation/removal, `portless
reset`, `portless uninstall`, or either machine-destructive relay E2E suite.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Prompt injection in logs or traffic triggers an operation. | Read-only default, immutable mutation flags, host confirmation guidance, untrusted-data labels, and typed mutation inputs. |
| MCP becomes a second API that drifts from the CLI/UI. | Adapter uses only the typed daemon client; API remains authoritative; no daemon MCP route. |
| An agent reaches unrelated projects on the machine. | Workspace scope by default, explicit pin/all modes, and authorization on every call. |
| A retry repeats a restart or shutdown. | Namespaced durable idempotency for every asynchronous mutation and natural-name reconciliation for artifacts. |
| Application data overwhelms model context or memory. | Conservative list limits, field caps, 1 MiB result budget, concurrency/rate limits, and actionable size errors. |
| Traffic exposes credentials or personal data. | Summary-only default, separate sensitive-detail startup flag, no recording export, explicit documentation and tests. |
| Daemon replacement breaks a long-lived MCP process. | Lazy/reconnecting gateway, read-only retry policy, durable operation polling, and E2E handoff coverage. |
| MCP SDK evolution leaks across Portless. | Pin the official SDK and confine it to the `portless-mcp` product behind Portless-owned handlers/DTOs. |
| Tool proliferation makes model selection unreliable. | Stable names, precise descriptions, deterministic ordering, category gating, and no one-endpoint/one-tool expansion beyond user-relevant workflows. |

## Definition of done

The initial Portless MCP integration is done after slices 1 through 4 when:

- `portless mcp serve` works over stdio with current and supported legacy MCP
  clients using the official Go SDK;
- default access is workspace-scoped and read-only;
- every returned value comes through the typed daemon API and respects its
  public-name, ownership, policy, and secret boundaries;
- sensitive traffic and both mutation categories require separate explicit
  startup flags;
- no MCP tool can remove volumes/data, administer the relay/daemon, execute a
  command, read arbitrary files, or contact arbitrary destinations;
- asynchronous mutations are idempotent, durable, pollable, and attributed as
  `MCP`;
- protocol, package, API, architecture, and isolated E2E tests cover the
  security and lifecycle behavior;
- README, MCP guide, implementation status, OpenAPI, package documentation, and
  contributor instructions describe the shipped contract; and
- `make test`, `make test-e2e-cli`, and `git diff --check` pass without running
  machine-destructive validation.

## Protocol references

- [MCP specification, revision 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28)
- [MCP stdio and transport rules](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports)
- [MCP tool contract and security considerations](https://modelcontextprotocol.io/specification/2026-07-28/server/tools)
- [MCP resource contract](https://modelcontextprotocol.io/specification/2026-07-28/server/resources)
- [Official Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- [Official Go SDK v1.7.0 protocol support](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
