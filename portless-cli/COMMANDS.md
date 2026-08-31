# Portless Command Reference

This document describes the complete public command tree of the `portless`
executable. For CLI ownership, architecture, and contribution guidance, see
the [Portless CLI README](README.md). For installation and the broader product
workflow, see the [repository README](../README.md).

The executable also contains private process modes whose names begin with
`__`. They are implementation protocols for the daemon, relay, and supervised
runtimes, not public commands, and are intentionally excluded here.

## Syntax and shared behavior

```text
portless [global flags] <command> [subcommand] [arguments] [local flags]
```

Notation used below:

- `<value>` is required and `[value]` is optional.
- `project/environment` is a public environment selector such as
  `billing/local`.
- `source:target` is a directed dependency edge such as `checkout:orders`.
- Durations use Go duration syntax, such as `500ms`, `10s`, `2m`, or `1h30m`.
- An option marked repeatable may be supplied more than once.

Global options are inherited by every public command and may appear before or
after the subcommand:

| Option | Meaning |
| --- | --- |
| `--env <project/environment>` | Use one environment for this invocation without changing the checkout's saved selection. |
| `--json` | Emit structured JSON; streaming commands emit JSON Lines. Not valid with `mcp serve`, whose stdout is MCP JSON-RPC. |
| `--no-color` | Disable ANSI color for this invocation. |
| `-h`, `--help` | Display help for the selected command without starting the daemon. |
| `-v`, `--version` | Display the root executable version. With `--json`, emit a version object. |

Environment-scoped commands use `--env` first, then a selection saved by
`portless env select`, then an unambiguous checkout match. Use
`portless env current` to inspect that decision. `--env` never persists a
selection.

Commands that start lifecycle operations wait by default. Where documented,
`--no-wait` returns after the operation is accepted. Destructive leaf commands
require `--yes`; machine-wide reset and uninstall first produce a preview when
`--yes` is absent. An incomplete command group, or a leaf missing required
positional arguments, displays help and exits successfully. Invalid syntax
exits `2`; runtime failures exit `1`.

Bounded `--limit` values are between `1` and `1000`, inclusive, except for
`logs`, whose maximum is `10000`.

## Command tree

```text
portless
├── up
├── down
├── status
├── open [service]
├── url [service]
├── ui
├── logs [service]
├── timeline
├── service
│   ├── list (ls)
│   ├── show <service>
│   ├── config <service>
│   ├── start <service>
│   ├── stop <service>
│   ├── restart <service>
│   ├── debug <service>
│   └── manage <service>
├── connection
│   ├── list (ls)
│   └── show <source:target>
├── traffic
│   ├── list (ls)
│   ├── show <sequence>
│   ├── traces
│   └── trace <number>
├── project
│   ├── list (ls)
│   ├── show [project]
│   ├── create <name>
│   ├── source
│   │   ├── add <name>
│   │   └── delete <name>
│   ├── export
│   ├── rename <new-name>
│   └── forget
├── env (environment)
│   ├── select <project/environment>
│   ├── current
│   ├── clear
│   ├── list [project]
│   ├── clone <name>
│   ├── bind <service>
│   ├── checkout
│   │   ├── list
│   │   ├── set <source>
│   │   └── remove <source>
│   ├── rescan
│   └── forget
├── record
│   ├── list
│   ├── start <name>
│   ├── stop [name]
│   ├── show <name>
│   ├── export <name>
│   └── delete <name>
├── fault
│   ├── list
│   ├── add <name> <source:target>
│   ├── show <name>
│   ├── enable <name>
│   ├── disable <name>
│   ├── delete <name>
│   └── clear
├── mock
│   ├── list (ls)
│   ├── show <profile>
│   ├── create <profile>
│   ├── delete <profile>
│   ├── route
│   │   ├── set <profile> <route>
│   │   └── delete <profile> <route>
│   └── preview <profile>
├── config
│   ├── color [auto|always|never]
│   └── reset
├── setup
├── relay
│   ├── install
│   ├── status
│   ├── restart
│   └── uninstall (remove)
├── daemon
│   ├── status
│   ├── stop
│   └── restart
├── runtime
│   ├── status
│   ├── start
│   └── use <auto|docker|podman>
├── doctor [daemon|relay|runtime]
├── mcp
│   └── serve
├── reset
├── uninstall
├── completion
│   ├── bash
│   ├── fish
│   ├── powershell
│   └── zsh
└── help [command]
```

Invoking `service`, `connection`, `traffic`, `project`, `project source`,
`env`, `env checkout`, `record`, `fault`, `mock`, `mock route`, `config`,
`relay`, `daemon`, `runtime`, `mcp`, or `completion` without a required child
displays that group's help.

## Environment lifecycle

### `portless up`

Discover or load the current environment, prepare clean ingress, and start it
for development. In an unregistered checkout this preserves the
zero-configuration workflow and creates a discovered project. From a
registered service directory, the service is started in debug mode when a
supported debugger is discovered; from a project directory, existing launch
modes are preserved.

| Option | Meaning |
| --- | --- |
| `--name <name>` | Name a newly discovered project. |
| `--timeout <duration>` | Startup deadline; default `10m`. |
| `--open` | Open the environment dashboard after readiness; enabled by default. |
| `--no-open` | Do not launch a browser. Mutually exclusive with `--open`. |
| `--no-wait` | Return after the start operation is accepted. |
| `--debug <service>` | Start one service with its discovered debugger enabled. |
| `--managed` | Start every service in normal managed mode. Mutually exclusive with `--debug`. |

Examples:

```bash
portless up
portless up --debug checkout --no-open
portless --env billing/qa up --managed
```

### Other environment commands

| Command | Usage |
| --- | --- |
| `portless down` | Stop the selected environment. `--all` targets every active environment and cannot be combined with `--env`. `--no-wait` returns after acceptance. `--timeout <duration>` defaults to `3m`. `--volumes` also removes managed data volumes and requires `--yes`; with `--all`, stopped environments are included so their volumes can be removed. |
| `portless status` | Show the selected environment and service status. With no resolvable checkout context, list all environments instead. |
| `portless open [service]` | Open a service's HTTP endpoint, defaulting to the primary service. If the environment has no primary service and none is requested, open its control-plane page. |
| `portless url [service]` | Print a service's primary public endpoint, defaulting to the environment's primary service. |
| `portless ui` | Open the browser control plane, preferring the selected environment page and otherwise the project list. |

`down` preserves volumes, traffic, recordings, logs, history, and project
metadata unless the separate volume-deletion flags are supplied.

## Observability and service control

### Logs and timeline

| Command | Usage |
| --- | --- |
| `portless logs [service]` | Read logs for every service or one service. `--limit <n>` defaults to `500`; `--since <duration>` filters by age; `--timestamps` adds timestamps to human output; `-t, --tail` keeps streaming and uses JSON Lines with `--json`. |
| `portless timeline` | Show durable environment history. `--limit <n>` defaults to `50`. |

### Services

`portless service` displays service-command help.

| Command | Usage |
| --- | --- |
| `portless service list` | List services. `--limit <n>` defaults to `250`. Alias: `service ls`. |
| `portless service show <service>` | Show service identity, provider, runtime, health, debugger, and public endpoint details. |
| `portless service config <service>` | Show the effective discovered and provider configuration for one service. |
| `portless service start <service>` | Start one service. |
| `portless service stop <service>` | Stop one service. |
| `portless service restart <service>` | Restart one service while preserving its configured launch mode. |
| `portless service debug <service>` | Restart one local process service with its discovered debugger enabled. |
| `portless service manage <service>` | Restart one service in normal managed mode. |

Every service lifecycle leaf accepts `--no-wait` and
`--timeout <duration>`; the default timeout is `2m`.

### Effective connections

`portless connection` displays connection-command help. Edges are always
directed and retain caller identity.

| Command | Usage |
| --- | --- |
| `portless connection list` | List effective source-to-target connections. `--limit <n>` defaults to `250`. Alias: `connection ls`. |
| `portless connection show <source:target>` | Explain one effective edge, including how the selected target provider is reached. |

### Captured traffic and traces

`portless traffic` displays traffic-command help.

| Command | Usage |
| --- | --- |
| `portless traffic list` | List captured exchanges. `--protocol <all|http|tcp>` defaults to `all`; `--limit <n>` defaults to `250`; `--service <name>` matches either endpoint; `--edge <source:target>` matches one directed edge; `-t, --tail` streams live exchanges. `--service` and `--edge` are mutually exclusive. Alias: `traffic ls`. |
| `portless traffic show <sequence>` | Show one captured exchange by its sequence number. |
| `portless traffic traces` | List correlated traces. `--limit <n>` defaults to `100`; filter with mutually exclusive `--service <name>` or `--edge <source:target>`; `--include-background` includes browser subresources and successful connection housekeeping. |
| `portless traffic trace <number>` | Show one correlated trace by trace number. |

Examples:

```bash
portless logs checkout --since 10m --timestamps
portless logs --tail --json
portless traffic list --edge checkout:orders --protocol http
portless traffic traces --service checkout
portless traffic show 42 --json
```

## Projects and environments

### Projects and sources

`portless project` displays project-command help, and
`portless project source` displays source-command help.

| Command | Usage |
| --- | --- |
| `portless project list` | List logical projects. `--limit <n>` defaults to `100`. Alias: `project ls`. |
| `portless project show [project]` | Show sources, environments, services, and connections for the named project, or resolve the current project when omitted. An explicit project cannot be combined with `--env`. |
| `portless project create <name>` | Create a logical project from one or more checkouts. Requires repeatable `--source <name=path>`. |
| `portless project source add <name>` | Discover and add a source to the current project. Requires `--path <checkout>`. Every project environment must be stopped; only the selected environment receives this initial checkout path. |
| `portless project source delete <name>` | Delete a logical source and its owned topology from every project environment. Requires `--yes`, and every project environment must be stopped. |
| `portless project export` | Export the current project declaration. `-o, --output <path>` defaults to `portless.project.json`; use `-` for stdout. |
| `portless project rename <new-name>` | Rename the current project. |
| `portless project forget` | Remove the current project, all of its environments, and their metadata. Requires `--yes`. |

Example multi-source project:

```bash
portless project create billing \
  --source checkout=../checkout \
  --source orders=../orders
portless env select billing/local
portless up
```

### Environment selection and configuration

`portless env` manages environments; `portless environment` is an alias.
`portless env checkout` displays checkout-command help.

| Command | Usage |
| --- | --- |
| `portless env select <project/environment>` | Save an environment selection for the current checkout. Pass the selector positionally; this command rejects `--env`. |
| `portless env current` | Show the effective environment, current source path, and whether selection came from a flag, saved selection, or inference. |
| `portless env clear` | Clear only the saved selection for the current checkout. This command rejects `--env`. |
| `portless env list [project]` | List environments, optionally for one project. `--limit <n>` defaults to `100`. |
| `portless env clone <name>` | Clone environment configuration and record its direct source as provenance. `--from <environment>` selects the source environment; otherwise the selected environment is used. Provider bindings, mock profiles, and routes are copied independently. |
| `portless env bind <service>` | Choose exactly one provider with `--local <source>`, `--container`, `--remote <url>`, or `--mock <profile>`. See provider options below. |
| `portless env checkout list` | List source checkout paths configured for the selected environment. |
| `portless env checkout set <source>` | Discover `--path <checkout>` and configure it for the selected environment. The environment must be stopped. |
| `portless env checkout remove <source>` | Remove only the selected environment's checkout path. Requires `--yes`; the environment must be stopped and no local provider may use the checkout. |
| `portless env rescan` | Rediscover every configured source and recompile the selected environment. The environment must be stopped. |
| `portless env forget` | Remove the selected environment and its metadata. Requires `--yes`. |

Provider binding options:

| Option | Meaning |
| --- | --- |
| `--local <source>` | Run the service from the named checkout source. |
| `--container` | Use a Portless-managed container. |
| `--remote <HTTP(S)-URL>` | Route the service to a remote HTTP endpoint. |
| `--mock <profile>` | Serve the service from a deterministic mock profile. |
| `--classification <development|qa|staging|unknown>` | Classify a remote provider; default `unknown`. Valid only with `--remote`. |
| `--write-policy <read-only|read-write>` | Enforce a remote write policy; default `read-only`. Valid only with `--remote`. |
| `--health-path <path>` | Probe this readiness path before switching to a remote provider. Valid only with `--remote`. |

Provider changes may occur while an environment is active and preserve the
source-aware proxy edge. Checkout changes and rescans are stopped-only because
they can recompile several services.

Examples:

```bash
portless env select billing/local
portless --env billing/qa status
portless env bind inventory --local inventory
portless env bind payments \
  --remote https://payments.qa.example.com \
  --classification qa \
  --write-policy read-only \
  --health-path /health
portless env checkout set inventory --path ../inventory-worktree
```

## Recordings, faults, and mocks

### Recordings

`portless record` displays recording-command help. Only one unnamed active
recording is implied by `record stop` when a name is omitted.

| Command | Usage |
| --- | --- |
| `portless record list` | List retained recordings. `--limit <n>` defaults to `100`. |
| `portless record start <name>` | Start a bounded recording. `--edge <source:target>` scopes it; `--duration <duration>` defaults to `15m` and must be greater than zero and at most `1h`; `--max-events <n>` defaults to `10000` and accepts `1..100000`; `--capture-payloads` retains HTTP bodies and decoded TCP application content; `--max-payload-bytes <n>` defaults to `65536` and accepts `1..1048576` bytes per direction. |
| `portless record stop [name]` | Stop the named recording, or the active recording when omitted. |
| `portless record show <name>` | Show recording metadata and retained-event summary. |
| `portless record export <name>` | Export recording JSON. `-o, --output <path>` defaults to `-` for stdout; `--force` overwrites an existing file. |
| `portless record delete <name>` | Permanently delete a recording. Requires `--yes`. |

Body capture is opt-in and bounded because traffic may contain sensitive
application data.

### Fault rules

`portless fault` displays fault-command help. A new rule must define at least
one effect: latency, jitter, synthetic status, or connection abort.

| Command | Usage |
| --- | --- |
| `portless fault list` | List fault rules. `--limit <n>` defaults to `100`. |
| `portless fault add <name> <source:target>` | Add and enable a scoped rule. Effects: `--latency <milliseconds>`, `--jitter <milliseconds>`, `--status <400..599>`, and `--abort`; latency plus jitter may not exceed `60000ms`. Filters: `--probability <value>` (`0 < value <= 1`, default `1`), `--method <method>`, and `--path <glob>`. `--duration <duration>` automatically disables it; default `0` leaves it enabled. |
| `portless fault show <name>` | Show one rule and its current state. |
| `portless fault enable <name>` | Enable a saved rule. |
| `portless fault disable <name>` | Disable a rule without deleting it. |
| `portless fault delete <name>` | Permanently delete a rule. Requires `--yes`. |
| `portless fault clear` | Disable every active fault rule without deleting saved definitions. |

### Deterministic mocks

`portless mock` displays mock-command help, and `portless mock route` displays
route-command help.

| Command | Usage |
| --- | --- |
| `portless mock list` | List mock profiles. Alias: `mock ls`. |
| `portless mock show <profile>` | Show a profile and all of its routes. |
| `portless mock create <profile>` | Create a profile for required `--service <service>`. `--description <text>` adds context. Import routes with either `--from-recording <name>` or `--from-openapi <path>`; those options are mutually exclusive. OpenAPI 3.0/3.1 input is read from a local file and is limited to `1048576` bytes. |
| `portless mock delete <profile>` | Delete an unbound profile. Requires `--yes`. |
| `portless mock route set <profile> <route>` | Create or replace a route. Match with `--method <method>` (default `GET`), `--path <pattern>` (default `/`), and repeatable `--query <name=value>`. Respond with `--status <code>` (default `200`), repeatable `--header <name=value>`, either `--body <text>` or `--body-file <path>`, and `--delay <milliseconds>`. `--disabled` saves the route without matching it. |
| `portless mock route delete <profile> <route>` | Permanently delete a route. Requires `--yes`. |
| `portless mock preview <profile>` | Resolve a request without sending traffic or changing state. Request options are `--method <method>` (default `GET`), `--path <path>` (default `/`), repeatable `--query <name=value>` and `--header <name=value>`, and either `--body <text>` or `--body-file <path>`. |

Examples:

```bash
portless record start checkout-debug \
  --edge checkout:orders \
  --capture-payloads
portless fault add slow-orders checkout:orders \
  --latency 1500 \
  --probability 0.5 \
  --duration 10m
portless mock create sold-out --service inventory
portless mock route set sold-out lookup \
  --path '/inventory/{sku}' \
  --status 404 \
  --body '{"error":"sold out"}'
portless mock preview sold-out --path /inventory/coffee-mug
portless env bind inventory --mock sold-out
```

## Administration

### CLI preferences

`portless config` displays preference-command help.

| Command | Usage |
| --- | --- |
| `portless config color [auto|always|never]` | Show the active color decision when no value is supplied, or save a preference. `auto` uses color only for an interactive terminal. `--no-color`, `NO_COLOR`, JSON, and completion output take precedence. |
| `portless config reset` | Remove saved CLI preferences and restore built-in defaults. This remains usable even if the preference file is malformed. |

### Relay and clean endpoint setup

`portless setup` is the first-run form of relay installation. It and
`portless relay install` may request administrator approval to install or
repair the narrow machine-wide HTTP and DNS relay.

`portless relay` displays relay-command help.

| Command | Usage |
| --- | --- |
| `portless setup` | Configure system-resolvable clean HTTP URLs and `portless.test` TCP endpoint DNS. Idempotent. |
| `portless relay install` | Explicitly install or repair the HTTP and DNS relay. Idempotent. |
| `portless relay status` | Show relay installation, ownership, helper integrity and compatibility, HTTP, and DNS health, with an explicit repair command when the receipt-bound helper, helper version, or system configuration requires repair. |
| `portless relay restart` | Restart an installed relay whose ownership matches the current installation. There is deliberately no force option. |
| `portless relay uninstall` | Remove only the privileged relay and scoped resolver integration. Alias: `relay remove`. `--force` permits intentional cleanup of fixed artifacts when the owner is another user or unknown, but it never removes macOS loopback aliases without a valid receipt. Residual aliases are reported for explicit verification. |

Relay removal does not remove projects, environments, application runtimes,
recordings, or the Portless data directory.

If a missing or invalid receipt prevents Portless from proving ownership of
reserved macOS loopback aliases, forced removal deletes only the fixed service,
helper, and resolver artifacts and reports the remaining endpoint pool. Review
`ifconfig lo0` and remove only aliases independently verified as Portless before
reinstalling; Portless does not infer alias ownership from their address alone.

### Daemon and container runtime

`portless daemon` and `portless runtime` display their respective command
help.

| Command | Usage |
| --- | --- |
| `portless daemon status` | Inspect and authenticate the local daemon without starting a replacement, including a fresh runtime-handoff safety audit. |
| `portless daemon stop` | Gracefully stop the authenticated daemon. `--timeout <duration>` defaults to `15s`; `--force` permits a guarded stop with active environments or a legacy fallback. |
| `portless daemon restart` | Replace the authenticated daemon with the current executable build and wait for ready state. Normal restart has one fixed five-second end-to-end deadline; `--force` uses the guarded legacy stop/start path outside that SLA. |
| `portless runtime status` | Show configured and detected Docker or Podman runtime status. |
| `portless runtime start` | Start the configured container runtime when supported. |
| `portless runtime use <auto|docker|podman>` | Save the container-runtime preference. `auto` chooses an available supported runtime. |

A normal daemon restart is designed to adopt owned runtimes, target completion
under two seconds, and fail with the daemon-log path if a different ready
instance is not observed within five seconds. Concurrent CLI, browser, MCP,
and executable-change requests coalesce into one replacement. Forced
replacement can interrupt active environments and is not a routine refresh
mechanism or part of the normal restart SLA.

The browser's routine connection and status polling use shallow daemon identity
and active-environment state. Opening the daemon drawer or requesting a restart
performs the deeper supervisor, container, proxy-listener, and ownership audit;
restart always repeats that audit immediately before replacement and fails
closed if current ownership cannot be proven. The restart receipt includes its
identifier, trigger, target build, acceptance time, and shared ready deadline.

### Diagnostics

| Command | Usage |
| --- | --- |
| `portless doctor` | Diagnose daemon, relay, and runtime installation state. |
| `portless doctor daemon` | Run only daemon checks. |
| `portless doctor relay` | Run only relay and clean-endpoint checks. |
| `portless doctor runtime` | Run only container-runtime checks. |

### MCP server

`portless mcp` displays MCP-command help.

| Command | Usage |
| --- | --- |
| `portless mcp serve` | Serve Portless tools over stdin/stdout. By default, scope access to the current workspace. `--env <project/environment>` pins one environment; `--all-environments` grants installation-wide inspection and cannot be combined with `--env`. `--allow-lifecycle` enables environment and service lifecycle tools. `--allow-traffic-control` enables bounded recording and fault tools. `--allow-sensitive-traffic` enables detailed traffic access that may include application data. `--json` is rejected because stdout is reserved for MCP JSON-RPC. |

See the [MCP README](../portless-mcp/README.md) for client configuration and
the exact tool capability model.

### Reset and uninstall

| Command | Usage |
| --- | --- |
| `portless reset` | Preview a reset to empty application state. `--yes` confirms removal of projects, environments, traffic, recordings, faults, logs, generated credentials, and owned runtime resources while preserving CLI preferences, runtime selection, installation identity, authentication, and relay installation. `--force --yes` is the guarded recovery form for active or unknown environments. |
| `portless uninstall` | Preview complete removal of Portless state, owned runtimes, relay integration, and a verified CLI launcher. `--yes` confirms when every environment is stopped; `--force --yes` is the guarded recovery form for active or unavailable inventory. Package-manager-owned launchers remain for their package manager to remove. |

Neither `--force` form authorizes killing an unverified process or deleting an
unowned container, network, volume, relay, or launcher. Run the preview first
and read its exact inventory and remediation.

## Help and shell completion

| Command | Usage |
| --- | --- |
| `portless help [command]` | Display help for the root or a selected command path. `portless <path> --help` is equivalent. |
| `portless completion bash` | Generate a Bash completion script. |
| `portless completion fish` | Generate a Fish completion script. |
| `portless completion powershell` | Generate a PowerShell completion script. |
| `portless completion zsh` | Generate a Zsh completion script. |

For example, load completion into the current Zsh session with:

```zsh
source <(portless completion zsh)
```

Completion covers static values and public project, environment, service,
connection, source, checkout, recording, mock, fault, traffic-sequence, and
trace identifiers. Dynamic completion queries only an already running daemon,
uses a short timeout, and returns no candidates instead of starting Portless or
printing an error when state is unavailable.

## Common workflows

Start from a checkout, inspect it, and use its clean endpoint:

```bash
portless up
portless status
portless url
```

Select an environment persistently for one checkout, or override it once:

```bash
portless env select billing/local
portless env current
portless --env billing/qa status --json
```

Inspect a service and its directed dependency traffic:

```bash
portless service show checkout
portless connection show checkout:orders
portless traffic list --edge checkout:orders --tail
```

For the authoritative help generated by the installed build, append `--help`
to any path:

```bash
portless env bind --help
portless fault add --help
portless daemon restart --help
```
