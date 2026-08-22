# Portless CLI

`portless-cli` owns the user-facing command surface of Portless. It assembles
the Cobra command tree, resolves the current checkout, renders human and
machine-readable output, confirms destructive actions, generates shell
completion, and opens browser destinations.

The CLI is part of the repository's single Go module and the distributed
`portless` executable. It is not a second daemon or a client for a hosted
service. For the complete public command hierarchy, options, and examples, see
the [command reference](COMMANDS.md).

## Product boundary

The CLI owns:

- public command names, arguments, flags, aliases, help, and completion;
- current-checkout and one-invocation environment selection;
- concise human output, structured JSON, JSON Lines for streams, and exit
  codes;
- semantic color, confirmations, progress, and browser launching; and
- composition of the local daemon, relay, runtime, doctor, and MCP entry
  points exposed through the shared executable.

Feature state and behavior remain behind narrower boundaries:

- `portless-daemon/api/client` is the typed boundary for projects,
  environments, lifecycle, observability, traffic, and mocks. CLI packages do
  not construct daemon routes or import server and control-plane packages.
- `portless-daemon/control` owns out-of-process daemon inspection, startup,
  replacement, shutdown, reset, and uninstall coordination.
- `portless-relay` owns the machine-wide privileged HTTP and DNS boundary.
- `portless-mcp` owns the local stdio MCP runtime. Only `administration`
  consumes it.

`cmd/portless` is deliberately a thin executable entry point. Its private
`__daemon`, `__relay`, `__runner`, and relay-installation modes are internal
process protocols, not public commands. Contributors and users should invoke
the normal command tree instead.

## Package map

| Path | Responsibility |
| --- | --- |
| `cmd/portless` | Start the shared executable and dispatch private process modes. |
| `app.go`, `commands.go` | Compose dependencies, assemble the root tree, apply global policy, and map errors to exit codes. |
| `command` | Shared CLI mechanics: context, selection, output, color, completion, operation waiting, browser launching, and host seams. |
| `environment` | `up`, `down`, `status`, `open`, `url`, and `ui`. |
| `projects` | Project, source, environment, provider-binding, and checkout commands. |
| `observe` | Logs, timeline, service inspection and lifecycle, and effective connections. |
| `traffic` | Traffic exchanges and traces, recordings, and fault rules. |
| `mocks` | Deterministic mock profiles, routes, imports, and previews. |
| `administration` | Preferences, daemon, relay, runtime, MCP, reset, uninstall, and setup commands. |
| `doctor` | Installation diagnostics and report types used by `administration`. |

The feature packages are siblings: they do not import one another. Shared
invocation state and host dependencies flow through `command.Context`, while
daemon-backed behavior flows through the typed API client. These dependency
rules are guarded by `tests/architecture`.

## Execution model

```text
argv
  |
  v
CLI.Run -> root Cobra command -> owning feature package
  |                                |
  |                                +-> typed daemon API client
  |                                +-> daemon lifecycle controller
  |                                +-> narrow local host dependency
  v
human output / JSON / JSON Lines + process exit code
```

`CLI.Run` is reusable and returns an exit code instead of terminating the
process. It loads saved presentation preferences, configures the root command,
executes one invocation, and applies the common error policy. The executable
entry point is the only layer that turns that return value into process exit.

Normal daemon-backed actions use the lifecycle controller to connect to the
per-user daemon, starting it when the command contract permits. Help and shell
completion do not start a daemon. Dynamic completion consults only an existing
daemon with a short timeout and quietly returns no candidates when local state
is unavailable.

## Selection contract

Most environment-scoped commands resolve context in this order:

1. the global `--env project/environment` override for this invocation;
2. a selection saved for the current checkout by `portless env select`; or
3. an environment inferred unambiguously from the current checkout.

An ambiguous checkout fails with the candidate selectors and explicit next
steps. `portless env current` explains the effective resolution,
`portless env clear` removes only the saved checkout selection, and `--env`
never changes saved state. Commands with an intentional inventory fallback,
such as `portless status`, document that exception in the command reference.

`portless up` additionally preserves the zero-configuration entry path: when
the checkout is not registered, it asks the daemon to discover and create the
project without requiring a `portless.yaml` or account.

## Output and error contract

Human-readable output is the default. Data-bearing commands also honor the
global `--json` flag; streaming commands emit one compact JSON document per
line. Global flags work before or after subcommands. `portless mcp serve` is
the intentional exception because stdout carries MCP JSON-RPC.

Errors go to stderr. JSON invocations use a stable `error` envelope, including
typed daemon error details and remediation when available. Exit codes are:

| Code | Meaning |
| --- | --- |
| `0` | The action succeeded, or an incomplete parent/leaf invocation displayed useful help. |
| `1` | The requested action failed at runtime. |
| `2` | The invocation was invalid, such as an unknown command, invalid value, conflicting flags, or missing required flag. |

Color is semantic and never part of machine output. Resolution order is JSON
or completion output, `--no-color`, `NO_COLOR`, the saved
`auto|always|never` preference, then terminal detection. Incomplete parent
commands display help without making an API request; missing required
positional arguments do the same, while malformed arguments are usage errors.

## Lifecycle and destructive actions

Commands that start asynchronous work wait and render progress by default.
Their explicit `--no-wait` form returns after the operation is accepted.
Callers should not add a second fire-and-forget path or permit duplicate work
while an operation is already running.

Destructive commands are preview-first where a useful inventory can be shown,
or require an explicit `--yes` confirmation at the leaf. `--force` is a
guarded recovery mechanism, not general permission to kill an unverified
process or remove an unowned runtime resource. Keep confirmation, preview,
ownership checks, and the final result consistent between human and JSON
output.

## Changing the command tree

When adding or changing a command:

1. Put it in the package that owns the user-facing behavior. Keep
   `cmd/portless`, `app.go`, and `commands.go` limited to composition and
   global execution policy.
2. Use the typed daemon client for feature requests. Change the wire contract,
   client, server, consumers, and API documentation together when the
   operation does not already exist.
3. Give incomplete parent commands useful help. Wrap positional validation
   with `command.UsageArgs`, validate bounded options before I/O, and register
   completion without starting the daemon.
4. Implement both concise human output and the appropriate JSON contract.
   Use JSON Lines only for actual streams.
5. Add focused tests in the owning package. Update
   `command_contract_test.go` whenever the public tree or bare-command
   behavior changes.
6. Update the [command reference](COMMANDS.md) in the same change.

Every exported declaration under `portless-cli` needs meaningful GoDoc that
begins with its exact identifier.

## Development

Run supported workflows from the repository root:

```bash
make                              # build web assets and bin/portless
go test ./portless-cli/...        # focused CLI suite
go test ./tests/architecture      # package and import boundaries
make test-go                      # complete Go suite
make test                         # complete non-destructive repository suite
git diff --check
```

Use the complete executable for manual command-tree checks:

```bash
make
./bin/portless --help
./bin/portless env bind --help
./bin/portless --json daemon status
```

Ordinary end-to-end suites compile a product binary and isolate the Portless
home. Read [the E2E testing guide](../docs/e2e-testing.md) before changing or
running them. Do not use reset, uninstall, relay installation/removal, forced
daemon replacement, or machine-destructive relay suites as incidental CLI
validation.

## Further reading

- [Complete CLI command reference](COMMANDS.md)
- [Repository overview and product workflow](../README.md)
- [Daemon ownership and runtime model](../portless-daemon/README.md)
- [Daemon OpenAPI contract](../portless-daemon/api/openapi.yaml)
- [Daemon event contract](../portless-daemon/api/events.md)
- [Package ownership and dependency direction](../docs/plans/package-structure-refactor.md)
- [MCP boundary and client configuration](../portless-mcp/README.md)
