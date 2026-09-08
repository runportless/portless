# Portless CLI

Use `portless` to start your application's services, give them stable local
addresses, and inspect logs and traffic from your terminal. You can start
from a checkout without an account or a `portless.yaml` file.

This guide covers everyday use. See the [command reference](COMMANDS.md) for
all commands, flags, and advanced options.

## Get started

Follow the [installation guide](../README.md#install), then configure local
networking once:

```bash
portless setup
```

Setup may request administrator approval to configure local HTTP and DNS
routing. Docker or Podman is needed only when your environment uses containers.

Install your application's dependencies as usual, then start Portless from
its checkout:

```bash
cd /path/to/billing
portless up
```

On first use, Portless discovers supported services and creates a project with
a `local` environment. It starts the environment and opens its dashboard when
ready. Use `portless up --no-open` to start without opening a browser.

For a ready-made application, try the [Chat](../examples/chat/README.md) or
[Store](../examples/store/README.md) example.

## Everyday commands

Run these from a checkout belonging to your project. Replace `checkout` with
a service name from `portless service list`.

| Command | What it does |
| --- | --- |
| `portless up` | Start the environment and open its dashboard. |
| `portless status` | Show environment and service status. |
| `portless service list` | List the environment's services. |
| `portless open checkout` | Open a service in the browser. |
| `portless url checkout` | Print a service's public endpoint. |
| `portless ui` | Open the Portless dashboard. |
| `portless logs --tail` | Follow logs from all services. |
| `portless service restart checkout` | Restart one service. |
| `portless down` | Stop the environment and keep its managed data volumes. |

You can omit the service name from `open` or `url` to use the environment's
primary service. Public endpoints stay the same across service restarts.

Start, stop, and restart commands wait and show progress by default. Where
supported, `--no-wait` returns as soon as the operation is accepted.

## Choose an environment

A project is your application, which may span several repositories. An
environment is an instance of that application. For example, `billing/local`
means the `local` environment in the `billing` project.

List environments, select one for your current checkout, and check the
selection:

```bash
portless env list
portless env select billing/local
portless env current
```

Most commands use `--env` first, then your saved checkout selection, then an
unambiguous match for the current checkout. If several environments match,
choose one explicitly. `portless env clear` removes the saved selection.

To target an environment for just one command, use `--env`. This also works
outside a project checkout and does not change your saved selection:

```bash
portless --env billing/local status
```

To try a separate configuration, clone an environment and start the clone:

```bash
portless --env billing/local env clone qa
portless --env billing/qa up
```

If the original environment is already using the checkout, Portless prepares
an independent Git worktree for the clone automatically. This requires Git
and an existing commit. See [environment configuration](COMMANDS.md#environment-selection-and-configuration)
for checkout paths and local, container, or remote providers.

## Inspect a service or request

For a service named `checkout`, inspect its status and configuration or follow
its recent logs:

```bash
portless service show checkout
portless service config checkout
portless logs checkout --since 10m --tail
```

For requests from `checkout` to `orders`, inspect the connection and follow
its traffic:

```bash
portless connection show checkout:orders
portless traffic list --edge checkout:orders --tail
```

The `source:target` notation identifies the caller and the service it calls.
Use `portless traffic traces --service checkout` to list correlated traces,
or open **Traffic** in the dashboard to inspect requests and responses.

For a local service with a supported debugger, `portless service debug checkout`
restarts it with debugging enabled. Run `portless service manage checkout`
to restart it in normal mode. See [service commands](COMMANDS.md#services)
for the full set of controls.

## Use Portless in scripts

Add `--json` for structured output:

```bash
portless status --json
portless --env billing/local service list --json
portless logs checkout --tail --json
```

Streaming commands emit JSON Lines: one JSON object per line. Errors go to
stderr; JSON invocations include a structured `error` object. `mcp serve`
uses its own protocol and does not accept `--json`.

| Exit code | Meaning |
| --- | --- |
| `0` | Success, including displaying help. |
| `1` | The requested action failed. |
| `2` | The command or its arguments were invalid. |

JSON output has no color codes. For plain terminal output, use `--no-color`
or set `NO_COLOR`. Save a preference with `portless config color auto`,
`portless config color always`, or `portless config color never`.

## Help and shell completion

Append `--help` to any command to see its arguments, options, and examples:

```bash
portless --help
portless up --help
portless env bind --help
```

Completion is available for Bash, Zsh, Fish, and PowerShell. For example,
load it into a Zsh session with completion already enabled:

```zsh
source <(portless completion zsh)
```

Run `portless completion zsh --help` for persistent installation instructions,
or replace `zsh` with your shell's name: `bash`, `fish`, or `powershell`.

## Troubleshooting

Start with `portless doctor` to check the local installation. Use a narrower
check when you know where the problem is:

| Problem | Command |
| --- | --- |
| Unsure which environment a command will use | `portless env current` |
| A service fails to start | `portless logs checkout --since 10m` |
| Local service addresses do not resolve or open | `portless doctor relay` |
| Container dependencies cannot start | `portless doctor runtime` |
| Commands cannot connect to Portless | `portless doctor daemon` |

Use `portless down` for a normal stop. To remove environment data or uninstall
Portless, review the [cleanup commands and their previews](COMMANDS.md#reset-and-uninstall).

## More workflows

- [Combine several repositories into one project](COMMANDS.md#projects-and-sources)
- [Record traffic for later inspection](COMMANDS.md#recordings)
- [Replay an HTTP request and compare responses](COMMANDS.md#captured-traffic-and-traces)
- [Simulate delays and failures](COMMANDS.md#fault-rules)
- [Mock a dependency's HTTP responses](COMMANDS.md#deterministic-mocks)
- [Connect an MCP client](../portless-mcp/README.md)

For development of Portless itself, see [Contributing](../CONTRIBUTING.md).
