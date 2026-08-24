# Portless MCP Server

`portless mcp serve` exposes the machine-local Portless control plane to an MCP
host over stdin/stdout. It is a client adapter over the same authenticated,
typed daemon API used by the CLI. It does not open a network listener, expose
the daemon token, or add an MCP route to the daemon.

`portless-mcp` is a library consumed only by `portless-cli/administration`;
it is not installed as another executable. The public entry point remains
`portless mcp serve` in the distributed `portless` binary.

The safe default is deliberate: the server is read-only and limited to
environments associated with the workspace in which the MCP process starts.
Capability flags and scope are fixed at startup, so an MCP tool call cannot
grant itself more access.

## Client configuration

The easiest setup path is `portless ui`, followed by **Settings → MCP**. The
control plane generates generic MCP client JSON or an equivalent shell command
for one of three scopes:

- a named `project/environment`;
- a source checkout working directory; or
- every environment in the current Portless installation.

Lifecycle, traffic-control, and sensitive-traffic access are opt-in. The screen
shows the resulting access level, tool count, and a warning for each elevated
choice. Those choices are not persisted in browser storage and reset whenever
the screen is reloaded.

This is configuration, not a process manager. Copying the result does not start
anything in Portless. The MCP host owns the child process and launches
`portless mcp serve` over stdio when it needs the server. If a desktop host does
not inherit the same `PATH` as the terminal, enter the absolute path to the
`portless` executable in the generator.

Use this configuration for read-only access to the current workspace:

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

The host must launch Portless with the project checkout as its working
directory. If the host cannot set a working directory, pin one environment:

```json
{
  "mcpServers": {
    "portless-billing": {
      "command": "portless",
      "args": ["--env", "billing/local", "mcp", "serve"]
    }
  }
}
```

Use installation-wide visibility only when the host genuinely needs to inspect
unrelated local applications:

```json
{
  "mcpServers": {
    "portless-all": {
      "command": "portless",
      "args": ["mcp", "serve", "--all-environments"]
    }
  }
}
```

`--env` and `--all-environments` are mutually exclusive. `--json` is rejected
because stdout is reserved exclusively for MCP JSON-RPC. Diagnostics go to
stderr.

## Scope

The server supports three immutable scope modes:

| Mode | Startup configuration | Visible environments |
| --- | --- | --- |
| Workspace (default) | no scope flag | Environments associated with the startup checkout path |
| Pinned | `--env project/environment` | Exactly that named environment |
| Installation | `--all-environments` | Every environment in this Portless installation |

Every tool call revalidates its environment against the configured scope. A
request outside it returns `SCOPE_DENIED`, even if a caller cached an earlier
tool result. Environment selectors always use the public
`project/environment` form.

## Capability flags

The default inventory contains 15 read-only tools. Additional tools appear
only when their startup flag is present:

| Flag | Added tools | Consequence |
| --- | --- | --- |
| none | 15 inspection tools | Read-only summaries, safe configuration, logs, operation history, recordings, and faults |
| `--allow-sensitive-traffic` | 1 | Exact target, bounded headers, and captured request/response body prefixes for one exchange |
| `--allow-lifecycle` | 3 | Start/stop environments and change one service's state |
| `--allow-traffic-control` | 5 | Start/stop bounded recordings and apply/disable bounded faults |

Flags can be combined. A fully enabled server has 24 tools:

```json
{
  "mcpServers": {
    "portless-operator": {
      "command": "portless",
      "args": [
        "--env", "billing/local",
        "mcp", "serve",
        "--allow-lifecycle",
        "--allow-traffic-control",
        "--allow-sensitive-traffic"
      ]
    }
  }
}
```

Prefer separate read-only and operator configurations. MCP hosts should still
ask for user confirmation before lifecycle or traffic-control calls.

## Tool inventory

All tools use structured JSON inputs and outputs. Tools that address an
environment require `environment` in `project/environment` form.

### Default inspection tools

| Tool | Purpose |
| --- | --- |
| `portless_list_environments` | List environments in scope and report enabled capability categories |
| `portless_get_environment` | Inspect effective sources, providers, topology, issues, service state, and public endpoints |
| `portless_get_service` | Inspect one service plus exact incoming and outgoing edges |
| `portless_get_service_configuration` | Read safe effective configuration without secret-bearing runtime values |
| `portless_list_connections` | List directed source-to-target connections |
| `portless_get_connection` | Inspect one exact directed connection |
| `portless_read_logs` | Read bounded chronological logs, optionally by service and time |
| `portless_query_traffic` | Query bounded traffic summaries by protocol, service, edge, or sequence cursor |
| `portless_list_recordings` | List bounded recording metadata without exporting captured events |
| `portless_get_recording` | Inspect one recording's bounds, state, and retained count |
| `portless_list_faults` | List fault metadata, effects, expiry, and match counts |
| `portless_get_fault` | Inspect one named fault |
| `portless_list_operations` | List recent durable operations and their state |
| `portless_get_operation` | Read one durable operation and its ordered events |
| `portless_get_timeline` | Read bounded newest-first environment history |

### Sensitive traffic tool

`--allow-sensitive-traffic` adds `portless_get_traffic_detail`. It returns one
exchange by environment-local sequence, including exact target, bounded
headers, and captured body prefixes. Use `portless_query_traffic` first to find
the sequence.

### Lifecycle tools

`--allow-lifecycle` adds:

- `portless_start_environment`, with optional explicit debug services or
  managed-mode restoration;
- `portless_stop_environment`, which always preserves volumes and retained
  data; and
- `portless_change_service_state`, whose action is `start`, `stop`, `restart`,
  `debug`, or `manage`.

Lifecycle tools wait up to 30 seconds by default. `waitSeconds` may be 0 through
120; zero returns the accepted durable operation immediately. Results include
the operation number so `portless_get_operation` can poll it.

Every lifecycle call accepts an optional `idempotencyKey`. Portless generates
one if omitted, returns the caller-visible key, namespaces it for MCP, and
persists a request fingerprint. Retrying the same request with the same key
returns the original operation. Reusing it for different arguments returns
`IDEMPOTENCY_CONFLICT`. Durable operation actors are recorded as `MCP`.

### Traffic-control tools

`--allow-traffic-control` adds:

- `portless_start_recording` and `portless_stop_recording`;
- `portless_apply_fault` and `portless_disable_fault`; and
- `portless_disable_all_faults`.

MCP-created recordings are metadata-only and must expire in 1 through 3600
seconds. They default to 10,000 events and cannot exceed 100,000. MCP-created
faults also require a 1-through-3600-second duration, an exact source/target
edge, and at least one latency, jitter, HTTP status, or abort effect. Reusing a
recording or fault name is accepted only when its existing scope and bounds
match.

## Data and safety boundaries

Portless treats log messages, traffic, timeline details, and application errors
as untrusted application data. They are marked as such in results and must not
be interpreted as instructions by the MCP host.

The default traffic tool excludes exact targets, headers, query values, and
bodies. Sensitive detail remains local but may contain credentials, personal
data, or business data even after the daemon redacts common authorization,
cookie, and token headers. Header material is capped, each request/response body
prefix is capped at 64 KiB, individual log messages are capped at 16 KiB, and
every complete MCP result is capped at 1 MiB.

MCP tools never expose private ownership keys or the daemon bearer token. The
server has no tools for reset, uninstall, daemon or relay administration,
volume deletion, arbitrary command execution, arbitrary filesystem reads, or
arbitrary URLs. Remote-provider write policy is still enforced locally by the
daemon before traffic leaves the machine.

The runtime accepts at most eight concurrent calls, at most two concurrent
mutations, and a burst of 40 calls with a sustained limit of 20 calls per
second. Ordinary reads time out after 10 seconds.

## Example diagnostic flows

For a failing local request:

1. Call `portless_list_environments` and choose an in-scope environment.
2. Call `portless_get_environment` to inspect service and provider state.
3. Call `portless_get_service` for the failing service and its exact edges.
4. Call `portless_read_logs` with a small limit.
5. Call `portless_query_traffic` for the service or `source:target` edge.
6. If explicitly enabled and necessary, call
   `portless_get_traffic_detail` for one sequence.

For an asynchronous restart with lifecycle permission:

1. Call `portless_change_service_state` with action `restart` and a stable
   `idempotencyKey`.
2. If `timedOutWaiting` is true or the operation is still running, call
   `portless_get_operation` with its number until terminal.
3. Recheck `portless_get_service`, logs, and traffic.

## Troubleshooting

- `SCOPE_DENIED`: the requested environment is outside the startup scope.
  Restart with the correct working directory, `--env`, or—only when
  appropriate—`--all-environments`.
- `RESOURCE_NOT_FOUND`: refresh `portless_list_environments` or the relevant
  list tool; the named object may have been renamed or removed.
- `IDEMPOTENCY_CONFLICT`: generate a new key for changed lifecycle arguments,
  or reuse the original arguments.
- `RATE_LIMITED`: reduce parallel calls and retry after a short delay.
- `RESULT_TOO_LARGE`: reduce `limit`, add a service/edge filter, or request a
  narrower time window.
- No tools appear: inspect the MCP host's stderr and verify that it launches
  `portless mcp serve`, not bare `portless mcp`; also verify the executable is
  on the host's `PATH`.
- The daemon is unavailable: run `portless daemon status` in a terminal. The
  MCP adapter uses normal Portless daemon startup and compatibility checks on
  its first data call.

Closing stdin cleanly stops the MCP server. Portless writes no human output to
stdout while serving because that stream belongs exclusively to the protocol.

## Development

Run focused checks from the repository root:

```bash
go test ./portless-mcp
go test ./portless-cli/administration
go test ./tests/architecture
```

The package depends on the official MCP Go SDK and only the daemon API client
and contract. It must not import CLI or daemon implementation packages. Update
the tool inventory, scope and capability documentation, result bounds, and
nearby tests together when the MCP surface changes.

## Further reading

- [Repository overview](../README.md)
- [CLI command reference](../portless-cli/COMMANDS.md#mcp-server)
- [Daemon boundary](../portless-daemon/README.md)
- [MCP implementation plan](../docs/plans/portless-mcp.md)
